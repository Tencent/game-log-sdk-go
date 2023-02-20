package tglog

import (
	"strings"
	"time"

	"git.woa.com/tglog/v3/sdk-go/bufferpool"

	"git.woa.com/tglog/v3/sdk-go/logger"

	"github.com/prometheus/client_golang/prometheus"
)

// const values
const (
	CodecV1 = "v1" // TGLog v1 options: logName|v1|v2|v3\n
	CodecV3 = "v3" // TGLog v3 options, see: https://git.woa.com/tglog/v3/proto/blob/master/tglog_v3.proto
)

// Options is the TGLog netClient config options
type Options struct {
	AppID                   string                // 业务ID，暂时没什么用途，默认：空
	AppName                 string                // 业务名称，暂时没什么用途，默认：空
	AppVer                  string                // 业务版本号，暂时没什么用途，默认：空
	Network                 string                // 网络，默认：udp
	Host                    string                // 服务器主机，可以是IP也可以是域名
	Port                    int                   // 服务器端口
	Codec                   string                // 格式，默认：v1
	ConnTimeout             time.Duration         // 连接超时，TCP有效，默认：3000ms
	BatchingMaxPublishDelay time.Duration         // 间隔多少时间发一次，默认：10ms
	BatchingMaxMessages     int                   // 每个批次的最大消息条数，默认：10
	BatchingMaxSize         int                   // 每个批次的最大字节数，默认：4K
	MaxPendingMessages      int                   // 每个工作线程待处理的消息队列长度，默认：40960
	BlockIfQueueIsFull      bool                  // 队列满则阻塞，默认：false
	SendTimeout             time.Duration         // 发送超时，V3协议有效，默认：10000ms
	MaxRetries              int                   // 重试次数，V3协议有效，默认2，
	Compress                bool                  // 是否压缩，V3协议有效，默认：false
	Encrypt                 bool                  // 是否加密，V3协议有效，默认：false
	EncryptKey              string                // 加密密钥，V3协议有效，默认：无
	WorkerNum               int                   // 工作线程，默认：1
	Logger                  logger.Logger         // 调试日志，默认：控制台
	WriteBufferSize         int                   // 网络层写缓冲大小，默认：64K
	ReadBufferSize          int                   // 网络层读缓冲大小，默认：64K
	SocketSendBufferSize    int                   // socket发送缓冲大小，默认：系统内核配置
	SocketRecvBufferSize    int                   // socket接收缓冲大小，默认：系统内核配置
	MetricsName             string                // metrics唯一名称，用于隔离指标，默认：tglog-go，如果一个进程创建了多个client对象需要配置不同的名字，否则指标名冲突会导致进程异常
	MetricsRegistry         prometheus.Registerer // 指标存储器，默认：prometheus.DefaultRegisterer
	BufferPool              bufferpool.BufferPool // 打解包用的缓冲池，为空的话内部初始化一个
	BytePool                bufferpool.BytePool   // 打解包用的内存池，为空的话内部初始化一个
	BufferPoolSize          int                   // 发送请求时编码用的缓冲池大小，默认：4096
	BytePoolSize            int                   // 接收响应时用的缓冲池大小，默认：128
	BytePoolWidth           int                   // 接收响应或者压缩请求时用的缓冲内存块大小，默认：与BatchingMaxSize相同
	NoFrameHeader           bool                  // 不带协议帧头，V3协议有效，TCP传输时，会强制设置为false，UDP传输时， 不带帧头就无法支持加密和压缩
	isV1                    bool                  // 是否V1协议，内部使用
	isV3                    bool                  // 是否V3协议，内部使用
	isUDP                   bool                  // 是否UDP，内部使用
	isTCP                   bool                  // 是否TCP，内部使用
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

// WithEncryptKey sets EncryptKey
func WithEncryptKey(k string) Option {
	return func(o *Options) {
		if k == "" {
			return
		}
		o.EncryptKey = k
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

// WithMetricsName sets Logger
func WithMetricsName(name string) Option {
	return func(o *Options) {
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

// WithNoFrameHeader sets NoFrameHeader
func WithNoFrameHeader(n bool) Option {
	return func(o *Options) {
		o.NoFrameHeader = n
	}
}

// WithAppID sets AppID
func WithAppID(id string) Option {
	return func(o *Options) {
		if id == "" {
			return
		}
		o.AppID = id
	}
}

// WithAppName sets AppName
func WithAppName(n string) Option {
	return func(o *Options) {
		if n == "" {
			return
		}
		o.AppName = n
	}
}

// WithAppVer sets AppVer
func WithAppVer(v string) Option {
	return func(o *Options) {
		if v == "" {
			return
		}
		o.AppVer = v
	}
}
