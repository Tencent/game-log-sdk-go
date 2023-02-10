package tglog

import (
	"strings"
	"time"

	"git.woa.com/tglog/v3/sdk-go/logger"
)

// const values
const (
	CodecV1 = "v1" // TGLog v1 codec: logName|v1|v2|v3\n
	CodecV3 = "v3" // TGLog v3 codec, see: https://git.woa.com/tglog/v3/proto/blob/master/tglog_v3.proto
)

// Options is the TGLog netClient config options
type Options struct {
	Network                 string        // 网络，默认：udp
	Host                    string        // 服务器主机，可以是IP也可以是域名
	Port                    int           // 服务器端口
	Codec                   string        // 格式，默认：v1
	ConnTimeout             time.Duration // 连接超时，TCP有效，默认：3000ms
	BatchingMaxPublishDelay time.Duration // 间隔多少秒发一次，默认：10ms
	BatchingMaxMessages     int           // 每一批次的最大消息条数，默认：10
	BatchingMaxSize         int           // 每一批次的最大字节数，默认：4K
	MaxPendingMessages      int           // 每个工作线程待处理的消息队列长度，默认：40960
	BlockIfQueueIsFull      bool          // 队列满则阻塞，默认：false
	SendTimeout             time.Duration // 发送超时，V3协议有效，默认：10000ms
	MaxRetries              int           // 重试次数，V3协议有效，默认2，
	BufferPoolSize          int           // 发送请求时编码用的缓冲池大小，默认：4096
	BytePoolSize            int           // 接收响应时用的缓冲池大小，默认：128
	BytePoolWidth           int           // 接收响应或者压缩请求时用的缓冲内存块大小，默认：与BatchingMaxSize相同
	Compress                bool          // 是否压缩，V3协议有效，默认：false
	Encrypt                 bool          // 是否加密，V3协议有效，默认：false
	WorkerNum               int           // 工作线程，默认：1
	Logger                  logger.Logger // 调试日志，默认：控制台
	WriteBufferSize         int           // 网络层写缓冲大小，默认：64K
	ReadBufferSize          int           // 网络层读缓冲大小，默认：64K
	SocketSendBufferSize    int           // socket发送缓冲大小，默认：系统内核配置
	SocketRecvBufferSize    int           // socket接收缓冲大小，默认：系统内核配置
}

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

// WithCodec sets Codec
func WithCodec(c string) Option {
	return func(o *Options) {
		if c != CodecV1 && c != CodecV3 {
			return
		}
		o.Codec = c
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

// WithSendTimeout sets SendTimeout
func WithSendTimeout(t time.Duration) Option {
	return func(o *Options) {
		if t <= 0 {
			return
		}
		o.SendTimeout = t
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

// WithMaxRetries sets MaxRetries
func WithMaxRetries(n int) Option {
	return func(o *Options) {
		if n < 0 {
			return
		}
		o.MaxRetries = n
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

// WithCompress sets Compress
func WithCompress(c bool) Option {
	return func(o *Options) {
		o.Compress = c
	}
}

// WithEncrypt sets Encrypt
func WithEncrypt(e bool) Option {
	return func(o *Options) {
		o.Encrypt = e
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

// WithLogger sets Logger
func WithLogger(log logger.Logger) Option {
	return func(o *Options) {
		if log == nil {
			return
		}
		o.Logger = log
	}
}
