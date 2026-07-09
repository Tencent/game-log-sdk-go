package tglog

import (
	"bytes"
	"context"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	v3 "github.com/tencent/game-log-sdk-proto/pbgo"

	"github.com/tencent/game-log-sdk-go/syncx"

	"github.com/tencent/game-log-sdk-go/bufferpool"
)

var (
	reqPool   *sync.Pool
	batchPool *sync.Pool
)

func init() {
	reqPool = &sync.Pool{
		New: func() interface{} {
			return &sendDataReq{}
		},
	}
	batchPool = &sync.Pool{
		New: func() interface{} {
			return &batchReq{
				dataReqs: make([]*sendDataReq, 0, 50),
			}
		},
	}
}

// sendDataReq is send data request
type sendDataReq struct {
	pool             *sync.Pool
	ctx              context.Context
	msg              Message
	callback         Callback
	flushImmediately bool
	publishTime      time.Time
	semaphore        syncx.Semaphore
	metrics          *metrics
	workerID         string
}

// done callback
func (r *sendDataReq) done(err error, errCode string) {
	if r.semaphore != nil {
		r.semaphore.Release()
		if r.metrics != nil {
			r.metrics.decPending(r.workerID)
		}
		r.semaphore = nil
	}

	if r.callback != nil {
		r.callback(r.msg, err)
		r.callback = nil
	}

	if r.metrics != nil {
		if errCode == "" {
			errCode = getErrorCode(err)
		}
		r.metrics.incMessage(errCode)
		r.metrics = nil
	}
	if r.pool != nil {
		pool := r.pool
		r.pool = nil
		pool.Put(r)
	}
}

// closeReq is close request
type closeReq struct {
	doneCh chan struct{}
}

// batchReq is batch callback function
type batchCallback func()

// batchReq is batch request
type batchReq struct {
	pool *sync.Pool
	// finished 是幂等标志，防止同一个 batch 被 done 多次。
	// 在超时重传与迟到响应竞态下，同一个 *batchReq 可能被重复 done，
	// 导致 buffer 双重归还、对已被 pool 复用的对象回调、semaphore 多次 Release。
	// 所有 done() 调用都发生在 worker 单一事件循环主协程内（串行），故用普通 bool 即可，无需原子。
	// 从 batchPool 取出时随 *batch = batchReq{...} 自动复位为 false。
	finished           bool
	batchID            string
	options            *Options
	dataReqs           []*sendDataReq
	dataSize           int
	batchTime          time.Time
	lastSendTime       time.Time
	lastSendServerAddr string
	retries            int
	bufferPool         bufferpool.BufferPool
	buffer             *bytes.Buffer
	bytePool           bufferpool.BytePool
	callback           batchCallback
	metrics            *metrics
}

// append appends data request to a batch request
func (b *batchReq) append(r *sendDataReq) {
	b.dataReqs = append(b.dataReqs, r)
	b.dataSize += len(r.msg.Payload)
}

// done done batch request
func (b *batchReq) done(err error) {
	// 幂等保护：已经 done 过的 batch 直接返回，避免重复释放资源 / 重复回调。
	if b.finished {
		return
	}
	b.finished = true

	errorCode := getErrorCode(err)
	for i, req := range b.dataReqs {
		req.done(err, errorCode)
		b.dataReqs[i] = nil
	}
	b.dataReqs = b.dataReqs[:0]

	if b.callback != nil {
		b.callback()
		b.callback = nil
	}
	if b.buffer != nil && b.bufferPool != nil {
		b.bufferPool.Put(b.buffer)
		b.buffer = nil
	}
	if b.metrics != nil {
		if errorCode != errOK.strCode {
			b.metrics.incError(errorCode)
		}
		b.metrics.observeTime(errorCode, time.Since(b.batchTime).Milliseconds())
		b.metrics.observeSize(errorCode, b.dataSize)
		b.metrics = nil
	}
	if b.pool != nil {
		pool := b.pool
		b.pool = nil
		pool.Put(b)
	}
}

// encode encodes batch request
func (b *batchReq) encode() ([]byte, error) {
	if b.bufferPool == nil {
		panic("batch req buffer pool is not set")
	}
	if b.bytePool == nil {
		panic("batch req byte pool is not set")
	}
	if b.buffer != nil {
		return b.buffer.Bytes(), nil
	}

	b.buffer = b.bufferPool.Get()
	b.buffer.Grow(b.dataSize)

	messages := make([]Message, len(b.dataReqs))
	for i := 0; i < len(b.dataReqs); i++ {
		messages[i] = b.dataReqs[i].msg
	}

	if b.options.isV1 {
		return EncodeV1(messages, b.buffer)
	}

	if b.options.isV3 {
		var header *v3.ReqHeader
		if b.options.Auth || b.options.Sign {
			header = V3HeaderPool.Get().(*v3.ReqHeader)
			defer V3HeaderPool.Put(header)
		}

		body := V3ReqPool.Get().(*v3.Req)
		defer V3ReqPool.Put(body)

		header, body, err := BuildV3LogReq(
			b.options.AppID,
			b.options.AppName,
			b.options.AppVer,
			b.options.Network,
			b.batchID,
			b.options.Token,
			b.options.TokenType,
			messages,
			nil,
			nil,
			nil,
			header,
			body)
		if err != nil {
			return nil, err
		}

		// fmt.Println(body.String())

		return EncodeV3Req(
			header,
			body,
			b.options.NoFrameHeader,
			b.options.Compress,
			b.options.Encrypt,
			b.options.Auth,
			b.options.Sign,
			b.options.EncryptKey,
			b.buffer,
			b.options.LittleEndian)
	}

	return b.buffer.Bytes(), nil
}

// batchRsp is the response of batch request
type batchRsp struct {
	batchID string
	code    int
	msg     string
	seqs    []uint64
}

// sendFailedBatchReq is the request of retry batch request
type sendFailedBatchReq struct {
	batchID string
	batch   *batchReq
	retry   bool
}

type retryingBatch struct {
	batch  *batchReq
	cancel context.CancelFunc
}

type retryBatchReq struct {
	batchID string
	batch   *batchReq
}

type doneBatchReq struct {
	batchID string
	batch   *batchReq
	err     error
}

// buildBatchID builds batch id
func buildBatchID(index string) string {
	u, err := uuid.NewRandom()
	if err != nil {
		return index + ":" + strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	return index + ":" + u.String()
}

// getWorkerIndex gets worker index
func getWorkerIndex(batchID string) int {
	i := strings.Index(batchID, ":")
	if i > 0 {
		idx := batchID[0:i]
		index, err := strconv.Atoi(idx)
		if err != nil {
			return -1
		}
		return index
	}
	return -1
}
