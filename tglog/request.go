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
	pool         *sync.Pool
	batchID      string
	options      *Options
	dataReqs     []*sendDataReq
	dataSize     int
	batchTime    time.Time
	lastSendTime time.Time
	retries      int
	bufferPool   bufferpool.BufferPool
	buffer       *bytes.Buffer
	bytePool     bufferpool.BytePool
	callback     batchCallback
	metrics      *metrics
}

// append appends data request to a batch request
func (b *batchReq) append(r *sendDataReq) {
	b.dataReqs = append(b.dataReqs, r)
	b.dataSize += len(r.msg.Payload)
}

// done done batch request
func (b *batchReq) done(err error) {
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
			false)
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
	batch *batchReq
	retry bool
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
