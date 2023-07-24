package tglog

import (
	"bytes"
	"context"
	"strconv"
	"strings"
	"time"

	v3 "git.woa.com/tglog/v3/proto/pbgo"

	"git.woa.com/tglog/v3/sdk-go/syncx"

	"git.woa.com/tglog/v3/sdk-go/bufferpool"
	"git.woa.com/tglog/v3/sdk-go/util"
)

type sendDataReq struct {
	ctx              context.Context
	msg              Message
	callback         Callback
	flushImmediately bool
	publishTime      time.Time
	semaphore        syncx.Semaphore
	metrics          *metrics
	workerID         string
}

func (r *sendDataReq) done(err error, errCode string) {
	if r.semaphore != nil {
		r.semaphore.Release()
	}

	if r.callback != nil {
		r.callback(r.msg, err)
	}

	if r.metrics != nil {
		if r.semaphore != nil {
			r.metrics.decPending(r.workerID)
		}
		if errCode == "" {
			errCode = getErrorCode(err)
		}
		r.metrics.incMessage(errCode)
	}
}

type closeReq struct {
	doneCh chan struct{}
}

type batchCallback func()
type batchReq struct {
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

func (b *batchReq) append(r *sendDataReq) {
	b.dataReqs = append(b.dataReqs, r)
	b.dataSize += len(r.msg.Payload)
}

func (b *batchReq) done(err error) {
	errorCode := getErrorCode(err)
	for _, req := range b.dataReqs {
		req.done(err, errorCode)
	}
	if b.callback != nil {
		b.callback()
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
	}
}

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

type batchRsp struct {
	batchID string
	code    int
	msg     string
	seqs    []uint64
}

type sendFailedBatchReq struct {
	batch *batchReq
	retry bool
}

func buildBatchID(index string) string {
	return index + ":" + util.SnowFlakeID()
}

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
