// Package bytecloser provides a ByteCloser interface
package bytecloser

import (
	"bytes"

	"github.com/tencent/game-log-sdk-go/bufferpool"
)

// ByteCloser is the interface of a byte Buffer that can be closed
type ByteCloser interface {
	// Bytes return the bytes
	Bytes() []byte
	// Close closes the bytes
	Close()
}

// Bytes is the raw byte implementations of ByteCloser
type Bytes []byte

// Bytes return the bytes
func (b Bytes) Bytes() []byte {
	return b
}

// Close closes the bytes
func (b Bytes) Close() {
}

// BufferBytes is the buffered byte implementations of ByteCloser
type BufferBytes struct {
	BufferPool bufferpool.BufferPool
	Buffer     *bytes.Buffer
}

// Bytes return the bytes
func (b *BufferBytes) Bytes() []byte {
	if b.Buffer == nil {
		return nil
	}

	return b.Buffer.Bytes()
}

// Close closes the bytes
func (b *BufferBytes) Close() {
	if b.Buffer == nil {
		return
	}

	if b.BufferPool == nil {
		b.Buffer.Reset()
		return
	}

	b.BufferPool.Put(b.Buffer)
	b.Buffer = nil
}
