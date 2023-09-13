// Package bufferpool provides buffer pool
package bufferpool

import (
	"bytes"

	"github.com/oxtoacart/bpool"
)

// BufferPool buffer pool interface
type BufferPool interface {
	Get() *bytes.Buffer
	Put(buffer *bytes.Buffer)
}

// BytePool byte pool interface
type BytePool interface {
	Get() []byte
	Put(b []byte)
}

// NewSizedBuffer news a buffer pool with specific buffer size
func NewSizedBuffer(poolSize, bufferSize int) BufferPool {
	return bpool.NewSizedBufferPool(poolSize, bufferSize)
}

// NewBuffer news a buffer pool
func NewBuffer(poolSize int) BufferPool {
	return bpool.NewBufferPool(poolSize)
}

// NewBytePool news a byte pool
func NewBytePool(poolSize, length int) BytePool {
	return bpool.NewBytePool(poolSize, length)
}
