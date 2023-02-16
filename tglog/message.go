package tglog

import (
	"bytes"
	"context"
	"git.woa.com/tglog/v3/sdk-go/bufferpool"
	"git.woa.com/tglog/v3/sdk-go/util"
	"strconv"
	"strings"
	"time"
)

type updateConnReq struct {
	deletedEndpoints map[string]struct{}
	doneCh           chan struct{}
}

type sendReq struct {
	ctx              context.Context
	data             []byte
	callback         Callback
	flushImmediately bool
	publishTime      time.Time
	semaphore        Semaphore
	metrics          *metrics
}

func (r *sendReq) done(err error) {
	if r.semaphore != nil {
		r.semaphore.Release()
	}

	if r.callback != nil {
		r.callback(BytesToString(r.data), err)
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
	codec        string
	sendReqs     []*sendReq
	sendSize     int
	batchTime    time.Time
	lastSendTime time.Time
	retries      int
	bufferPool   bufferpool.BufferPool
	bytePool     bufferpool.BytePool
	buffer       *bytes.Buffer
	callback     batchCallback
	metrics      *metrics
}

func (b *batchReq) append(r *sendReq) {
	b.sendReqs = append(b.sendReqs, r)
	b.sendSize += len(r.data)
}

func (b *batchReq) done(err error) {
	for _, req := range b.sendReqs {
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
		b.metrics.observeSize(code, b.sendSize)
	}
}

func (b *batchReq) encode() []byte {
	if b.bufferPool == nil {
		panic("batch req buffer pool is not set")
	}
	if b.bytePool == nil {
		panic("batch req byte pool is not set")
	}
	if b.buffer != nil {
		return b.buffer.Bytes()
	}

	b.buffer = b.bufferPool.Get()
	b.buffer.Grow(b.sendSize)
	if b.codec == CodecV1 {
		for i := 0; i < len(b.sendReqs); i++ {
			r := b.sendReqs[i]
			b.buffer.Write(r.data)
			if r.data[len(r.data)-1] != '\n' {
				b.buffer.Write([]byte{'\n'})
			}
		}
		return b.buffer.Bytes()
	}

	if b.codec == CodecV3 {
		// todo
		return b.buffer.Bytes()
	}
	return b.buffer.Bytes()
}

type batchRsp struct {
	batchID string
}

func buildBatchID(index int) string {
	return strconv.Itoa(index) + ":" + util.SnowFlakeID()
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
