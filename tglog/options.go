package tglog

import (
	"math"
	"time"

	"git.woa.com/tglog/v3/sdk-go/bufferpool"

	"git.woa.com/tglog/v3/sdk-go/logger"

	"github.com/prometheus/client_golang/prometheus"
)

// Options is the TGLog go client config options
type Options struct {
	Network                 string                // 网络，默认：udp
	Host                    string                // 服务器主机，可以是IP也可以是域名
	Port                    int                   // 服务器端口
	WorkerNum               int                   // 工作线程，默认：8
	BatchingMaxPublishDelay time.Duration         // 间隔多少时间发一次，默认：10ms
	BatchingMaxMessages     int                   // 每个批次的最大消息条数，默认：20
	BatchingMaxSize         int                   // 每个批次的最大字节数，默认：10K
	MaxPendingMessages      int                   // 每个工作线程待处理的消息队列长度，默认：102400
	BlockIfQueueIsFull      bool                  // 队列满则阻塞，默认：false
	ConnTimeout             time.Duration         // 连接超时，TCP有效，默认：3000ms
	WriteBufferSize         int                   // 网络层写缓冲大小，默认：16M
	ReadBufferSize          int                   // 网络层读缓冲大小，默认：16M
	SocketSendBufferSize    int                   // socket发送缓冲大小，默认：16M
	SocketRecvBufferSize    int                   // socket接收缓冲大小，默认：16M
	BufferPoolSize          int                   // 发送请求时编码用的缓冲池大小，默认：204800
	BytePoolSize            int                   // 接收响应时用的缓冲池大小，默认：204800
	BytePoolWidth           int                   // 接收响应或者压缩请求时用的缓冲内存块大小，默认：与BatchingMaxSize相同
	BufferPool              bufferpool.BufferPool // 打解包用的缓冲池，为空的话内部初始化一个
	BytePool                bufferpool.BytePool   // 打解包用的内存池，为空的话内部初始化一个
	Logger                  logger.Logger         // 调试日志，默认：控制台
	MetricsName             string                // metrics唯一名称，用于隔离指标，默认：tglog-go，如果一个进程创建了多个client对象需要配置不同的名字，否则指标名冲突会导致进程异常
	MetricsRegistry         prometheus.Registerer // 指标存储器，默认：prometheus.DefaultRegisterer
	isV1                    bool                  // 是否V1协议，内部使用
	isV3                    bool                  // 是否V3协议，内部使用
	isUDP                   bool                  // 是否UDP，内部使用
	isTCP                   bool                  // 是否TCP，内部使用
	AppID                   string                // 业务ID，暂时没什么用途，V3协议有效，默认：空
	AppName                 string                // 业务名称，暂时没什么用途，V3协议有效，默认：空
	AppVer                  string                // 业务版本号，暂时没什么用途，V3协议有效，默认：空
	SendTimeout             time.Duration         // 发送超时，V3协议有效，默认：30000ms
	MaxRetries              int                   // 重试次数，V3协议有效，默认2，
	Compress                bool                  // 是否压缩，V3协议有效，默认：false
	Encrypt                 bool                  // 是否加密，V3协议有效，默认：false
	EncryptKey              string                // 加密密钥，V3协议有效，默认：无
	NoFrameHeader           bool                  // 不带协议帧头，V3协议有效，TCP传输时，会强制设置为false，UDP传输时， 不带帧头就无法支持加密和压缩
	LittleEndian            bool                  // 是否小端字节序，V3协议有效，默认：false
	MaxFrameLen             int                   // 最大帧长，单位字节，V3协议有效，默认：64K
	LenFieldOffset          int                   // 长度字段在一个帧中的位移，V3协议有效，默认：2
	LenFieldLength          int                   // 长度字段占用字节数，V3协议有效，默认：4
	LenAdjustment           int                   // 计算帧长度时的修正值，可以正可以负V3协议有效，默认：-6
	FrameBytesToStrip       int                   // 分帧时需要截掉的字节数，只在解码的时候有用，获取帧长度时没用，V3协议有效，默认：0
	PayloadBytesToTrip      int                   // 从一帧数据获取有效载荷时需要截掉的字节数，V3协议有效，默认：10，截掉V3协议的10个字节帧头
}

// ValidateAndSetDefault validates an options and set up the default values
func (options *Options) ValidateAndSetDefault() error {
	if options.Network == "" {
		options.Network = "udp"
	}

	if options.Host == "" {
		// 未指定服务器域名
		return ErrInvalidHost
	}

	if options.Port <= 0 || options.Port > 65535 {
		// 未指定服务器端口
		return ErrInvalidPort
	}

	if options.WorkerNum <= 0 {
		options.WorkerNum = 8
	}

	options.isUDP = isUDP(options.Network)
	options.isTCP = isTCP(options.Network)
	if (options.isUDP && options.isTCP) || (!options.isUDP && !options.isTCP) {
		return ErrInvalidNetwork
	}

	if (options.isV1 && options.isV3) || (!options.isV1 && !options.isV3) {
		return ErrInvalidProtoVer
	}

	if options.BatchingMaxPublishDelay == 0 {
		options.BatchingMaxPublishDelay = 10 * time.Millisecond
	}

	if options.BatchingMaxMessages <= 0 {
		options.BatchingMaxMessages = 10
	}

	if options.BatchingMaxSize <= 0 {
		options.BatchingMaxSize = 4096
	}

	if options.BatchingMaxSize > maxUDPReqSizeV1 && options.isUDP {
		options.BatchingMaxSize = maxUDPReqSizeV1
	}
	if options.BatchingMaxSize > maxTCPReqSizeV1 && options.isTCP {
		options.BatchingMaxSize = maxTCPReqSizeV1
	}

	if options.MaxPendingMessages <= 0 {
		options.MaxPendingMessages = 102400
	}

	if options.ConnTimeout <= 0 {
		options.ConnTimeout = 3 * time.Second
	}

	if options.WriteBufferSize <= 0 {
		options.WriteBufferSize = 16 * 1024 * 1024
	}

	if options.ReadBufferSize <= 0 {
		options.ReadBufferSize = 16 * 1024 * 1024
	}

	if options.SocketSendBufferSize <= 0 {
		options.SocketSendBufferSize = 16 * 1024 * 1024
	}

	if options.SocketRecvBufferSize <= 0 {
		options.SocketRecvBufferSize = 16 * 1024 * 1024
	}

	if options.BufferPoolSize <= 0 {
		options.BufferPoolSize = 204800
	}

	if options.BytePoolSize <= 0 {
		options.BytePoolSize = 204800
	}

	if options.BytePoolWidth <= 0 {
		options.BytePoolWidth = int(math.Max(4096, float64(options.BatchingMaxSize)))
	}

	if options.BufferPool == nil {
		options.BufferPool = bufferpool.NewBuffer(options.BufferPoolSize)
	}
	if options.BytePool == nil {
		options.BytePool = bufferpool.NewBytePool(options.BytePoolSize, options.BytePoolWidth)
	}

	if options.Logger == nil {
		options.Logger = logger.Std()
	}

	if options.MetricsName == "" {
		options.MetricsName = "tglog-go"
	}

	if options.MetricsRegistry == nil {
		options.MetricsRegistry = prometheus.DefaultRegisterer
	}

	if options.SendTimeout == 0 {
		options.SendTimeout = 30 * time.Second
	}

	if options.MaxRetries <= 0 {
		options.MaxRetries = 2
	}

	if options.NoFrameHeader && options.isV3 {
		// V3协议用TCP传输必须有帧头
		if options.isTCP {
			return ErrV3TCPNoFrameHeader
		}
		// V3协议启用了加密或者压缩必须有协议头
		if options.Encrypt || options.Compress {
			return ErrV3CENoFrameHeader
		}
	}

	if options.Encrypt && options.EncryptKey == "" {
		return ErrInvalidEncryptKey
	}

	if options.MaxFrameLen <= 0 {
		options.MaxFrameLen = 64 * 1024
	}

	return nil
}
