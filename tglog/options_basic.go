package tglog

import (
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/tencent/game-log-sdk-go/bufferpool"
	"github.com/tencent/game-log-sdk-go/logger"
)

// Option is the Options helper.
type Option func(*Options)

// WithNetwork sets Network
func WithNetwork(n string) Option {
	return func(o *Options) {
		n = strings.ToLower(n)
		if n != "udp" && n != "tcp" && n != "udp4" && n != "tcp4" && n != "udp6" && n != "tcp6" {
			return
		}
		o.Network = n
	}
}

// WithHost sets Host
func WithHost(h string) Option {
	return func(o *Options) {
		o.Host = h
	}
}

// WithPort sets Port
func WithPort(p int) Option {
	return func(o *Options) {
		if p < 0 || p > 65535 {
			return
		}
		o.Port = p
	}
}

// WithUpdateInterval sets UpdateInterval
func WithUpdateInterval(u time.Duration) Option {
	return func(o *Options) {
		o.UpdateInterval = u
	}
}

// WithWorkerNum sets WorkerNum
func WithWorkerNum(n int) Option {
	return func(o *Options) {
		if n <= 0 {
			return
		}
		o.WorkerNum = n
	}
}

// WithBatchingMaxPublishDelay sets BatchingMaxPublishDelay
func WithBatchingMaxPublishDelay(t time.Duration) Option {
	return func(o *Options) {
		if t <= 0 {
			return
		}
		o.BatchingMaxPublishDelay = t
	}
}

// WithBatchingMaxMessages sets BatchingMaxMessages
func WithBatchingMaxMessages(n int) Option {
	return func(o *Options) {
		if n <= 0 {
			return
		}
		o.BatchingMaxMessages = n
	}
}

// WithBatchingMaxSize sets BatchingMaxSize
func WithBatchingMaxSize(n int) Option {
	return func(o *Options) {
		if n <= 0 {
			return
		}
		o.BatchingMaxSize = n
	}
}

// WithMaxPendingMessages sets MaxPendingMessages
func WithMaxPendingMessages(n int) Option {
	return func(o *Options) {
		if n <= 0 {
			return
		}
		o.MaxPendingMessages = n
	}
}

// WithBlockIfQueueIsFull sets BlockIfQueueIsFull
func WithBlockIfQueueIsFull(b bool) Option {
	return func(o *Options) {
		o.BlockIfQueueIsFull = b
	}
}

// WithConnTimeout sets ConnTimeout
func WithConnTimeout(t time.Duration) Option {
	return func(o *Options) {
		if t <= 0 {
			return
		}
		o.ConnTimeout = t
	}
}

// WithWriteBufferSize sets WriteBufferSize
func WithWriteBufferSize(n int) Option {
	return func(o *Options) {
		if n <= 0 {
			return
		}
		o.WriteBufferSize = n
	}
}

// WithReadBufferSize sets ReadBufferSize
func WithReadBufferSize(n int) Option {
	return func(o *Options) {
		if n <= 0 {
			return
		}
		o.ReadBufferSize = n
	}
}

// WithSocketSendBufferSize sets SocketSendBufferSize
func WithSocketSendBufferSize(n int) Option {
	return func(o *Options) {
		if n <= 0 {
			return
		}
		o.SocketSendBufferSize = n
	}
}

// WithSocketRecvBufferSize sets SocketRecvBufferSize
func WithSocketRecvBufferSize(n int) Option {
	return func(o *Options) {
		if n <= 0 {
			return
		}
		o.SocketRecvBufferSize = n
	}
}

// WithBufferPool sets BufferPool
func WithBufferPool(bp bufferpool.BufferPool) Option {
	return func(o *Options) {
		o.BufferPool = bp
	}
}

// WithBytePool sets BytePool
func WithBytePool(bp bufferpool.BytePool) Option {
	return func(o *Options) {
		o.BytePool = bp
	}
}

// WithBufferPoolSize sets BufferPoolSize
func WithBufferPoolSize(n int) Option {
	return func(o *Options) {
		if n <= 0 {
			return
		}
		o.BufferPoolSize = n
	}
}

// WithBytePoolSize sets BytePoolSize
func WithBytePoolSize(n int) Option {
	return func(o *Options) {
		if n <= 0 {
			return
		}
		o.BytePoolSize = n
	}
}

// WithBytePoolWidth sets BytePoolWidth
func WithBytePoolWidth(n int) Option {
	return func(o *Options) {
		if n <= 0 {
			return
		}
		o.BytePoolWidth = n
	}
}

// WithLogger sets Logger
func WithLogger(log logger.Logger) Option {
	return func(o *Options) {
		if log == nil {
			return
		}
		o.Logger = log
	}
}

// WithMetricsName sets Logger
func WithMetricsName(name string) Option {
	return func(o *Options) {
		if name == "" {
			return
		}
		o.MetricsName = name
	}
}

// WithMetricsRegistry sets Logger
func WithMetricsRegistry(reg prometheus.Registerer) Option {
	return func(o *Options) {
		if reg == nil {
			return
		}
		o.MetricsRegistry = reg
	}
}

// WithMaxConnLifetime sets MaxConnLifetime
func WithMaxConnLifetime(lifetime time.Duration) Option {
	return func(o *Options) {
		o.MaxConnLifetime = lifetime
	}
}
