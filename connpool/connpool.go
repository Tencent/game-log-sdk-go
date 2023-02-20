package connpool

import (
	"context"
	"errors"
	"net"
	"time"
)

// Dialer is the interface of a dialer that return a NetConn
type Dialer interface {
	Dial() (net.Conn, error)
}

// ConnPool is the interface of a simple connection pool that just hold the pool only
type ConnPool interface {
	Get(dialer Dialer) (net.Conn, error)
	Put(conn net.Conn)
	NumPooled() int
}

// NewConnPool news a ConnPool
func NewConnPool(size int) ConnPool {
	return &connPool{make(chan net.Conn, size)}
}

type connPool struct {
	pool chan net.Conn
}

func (p *connPool) Get(dialer Dialer) (net.Conn, error) {
	select {
	case conn := <-p.pool:
		return conn, nil
	default:
		if dialer == nil {
			return nil, errors.New("dialer is nil")
		}
		return dialer.Dial()
	}
}

func (p *connPool) Put(conn net.Conn) {
	select {
	case p.pool <- conn:
	default:
		// pool is full, close the connection after 2m
		CloseConn(conn, 2*time.Minute)
	}
}

func (p *connPool) NumPooled() int {
	return len(p.pool)
}

// CloseConn closes a connection after a duration of time
func CloseConn(conn net.Conn, after time.Duration) {
	if after <= 0 {
		conn.Close()
		return
	}

	ctx := context.Background()
	go func() {
		select {
		case <-time.After(after):
			conn.Close()
			return
		case <-ctx.Done():
			conn.Close()
			return
		}
	}()
}
