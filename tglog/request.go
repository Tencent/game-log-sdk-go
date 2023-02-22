package tglog

import (
	"bytes"
	"context"
	"strconv"
	"strings"
	"time"

	"git.woa.com/tglog/v3/sdk-go/syncx"

	"git.woa.com/tglog/v3/sdk-go/bufferpool"
	"git.woa.com/tglog/v3/sdk-go/util"
)

type updateConnReq struct {
	deletedEndpoints map[string]struct{}
	doneCh           chan struct{}
}

type sendDataReq struct {
	ctx              context.Context
	msg              *Message
	callback         Callback
	flushImmediately bool
	publishTime      time.Time
	semaphore        syncx.Semaphore
	metrics          *metrics
}

func (r *sendDataReq) done(err error) {
	if r.semaphore != nil {
		r.semaphore.Release()
	}

	if r.callback != nil {
		r.callback(r.msg, err)
	}

	if r.metrics != nil {
		if r.semaphore != nil {
			r.metrics.decPending()
		}
		code := getErrorCode(err)
		r.metrics.incMessage(code)
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
	for _, req := range b.dataReqs {
		req.done(err)
	}
	if b.callback != nil {
		b.callback()
	}
	if b.buffer != nil && b.bufferPool != nil {
		b.bufferPool.Put(b.buffer)
		b.buffer = nil
	}
	if b.metrics != nil {
		code := getErrorCode(err)
		b.metrics.observeTime(code, time.Since(b.batchTime).Milliseconds())
		b.metrics.observeSize(code, b.dataSize)
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

	messages := make([]*Message, len(b.dataReqs))
	for i := 0; i < len(b.dataReqs); i++ {
		messages[i] = b.dataReqs[i].msg
	}

	if b.options.isV1 {
		return EncodeV1(messages, b.buffer)
	}

	if b.options.isV3 {
		req, err := BuildV3LogReq(
			b.options.AppID,
			b.options.AppName,
			b.options.AppVer,
			b.options.Network,
			b.batchID,
			messages,
			nil,
			nil)
		if err != nil {
			return nil, err
		}

		// fmt.Println(req.String())

		return EncodeV3Req(
			req,
			b.options.NoFrameHeader,
			b.options.Compress,
			b.options.Encrypt,
			b.options.EncryptKey,
			b.buffer,
			false)
	}

	return b.buffer.Bytes(), nil
}

type batchRsp struct {
	batchID string
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
