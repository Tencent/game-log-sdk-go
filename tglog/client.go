package tglog

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"time"

	v3 "git.woa.com/tglog/v3/proto/pbgo"

	"git.woa.com/tglog/v3/sdk-go/bufferpool"
	"github.com/prometheus/client_golang/prometheus"

	"git.woa.com/tglog/v3/sdk-go/connpool"
	"github.com/panjf2000/gnet/v2"

	"git.woa.com/tglog/v3/sdk-go/discoverer"
	"git.woa.com/tglog/v3/sdk-go/logger"
)

const (
	maxUDPReqSizeV1 = 65507
	maxTCPReqSizeV1 = 512 * 1024
	maxUDPReqSizeV3 = 64512
	maxTCPReqSizeV3 = 512 * 1024
)

// variables
var (
	ErrInvalidHost        = errors.New("invalid host")
	ErrInvalidPort        = errors.New("invalid port")
	ErrInvalidNetwork     = errors.New("invalid network")
	ErrV3TCPNoFrameHeader = errors.New("NoFrameHeader it must be false when codec is V3 and network is TCP")
	ErrV3CENoFrameHeader  = errors.New("NoFrameHeader it must be false when codec is V3 and compress or encrypt is true")
	ErrInvalidEncryptKey  = errors.New("invalid encrypt key")
)

// Callback is the callback func that will be called when Client finish sending the message
type Callback func(msg *Message, err error)

// Client is the interface of a TGLog netClient
type Client interface {
	// Send sends the msg synchronously
	Send(ctx context.Context, msg *Message) error
	// SendAsync sends the log asynchronously, if cb is not nil, it will be called after the log is sent.
	SendAsync(ctx context.Context, msg *Message, cb Callback)
	// Close closes the netClient
	Close()
}

type client struct {
	*gnet.BuiltinEventEngine                       // 继承网络事件处理器
	options                  *Options              // 配置
	discoverer               discoverer.Discoverer // 服务发现
	connPool                 connpool.ConnPool     // 连接池
	netClient                *gnet.Client          // 多路复用管理器
	workers                  []*worker             // 工作者
	curWorkerIndex           atomic.Uint64         // 当前工作者
	log                      logger.Logger         // 日志
	metrics                  *metrics              // 指标
	framer                   framer                // TCP分帧器，V1协议不回包，V3协议TCP传输才有用
}

// NewClient news a TGLog client
func NewClient(opts ...Option) (Client, error) {
	// default options
	options := &Options{
		Network:                 "udp",
		Codec:                   CodecV1,
		BatchingMaxMessages:     10,
		BatchingMaxPublishDelay: 10 * time.Millisecond,
		SendTimeout:             10 * time.Second,
		BatchingMaxSize:         4096,
		MaxPendingMessages:      40960,
		WorkerNum:               4,
		Logger:                  logger.Std(),
		FieldOffset:             2,
		FieldLength:             4,
		Adjustment:              -6,
	}

	for _, o := range opts {
		o(options)
	}

	if options.Host == "" {
		// 未指定服务器域名
		return nil, ErrInvalidHost
	}
	if options.Port == 0 {
		// 未指定服务器端口
		return nil, ErrInvalidPort
	}
	if options.WorkerNum <= 0 {
		options.WorkerNum = 4
	}
	if options.MetricsName == "" {
		// 指标名，如果一个进程初始化多个client，又用这个默认的指标名，会导致prometheus查不到数据
		options.MetricsName = "tglog-go"
	}
	if options.MetricsRegistry == nil {
		// 指标存储器，如果没有指定，用默认的
		options.MetricsRegistry = prometheus.DefaultRegisterer
	}
	if options.Codec == CodecV1 {
		options.isV1 = true
		if options.BatchingMaxSize > maxUDPReqSizeV1 && isUDP(options.Network) {
			options.BatchingMaxSize = maxUDPReqSizeV1
		}
		if options.BatchingMaxSize > maxTCPReqSizeV1 && isTCP(options.Network) {
			options.BatchingMaxSize = maxTCPReqSizeV1
		}
	}
	if options.Codec == CodecV3 {
		options.isV3 = true
		if options.BatchingMaxSize > maxUDPReqSizeV3 && isUDP(options.Network) {
			options.BatchingMaxSize = maxUDPReqSizeV3
		}
		if options.BatchingMaxSize > maxTCPReqSizeV3 && isTCP(options.Network) {
			options.BatchingMaxSize = maxTCPReqSizeV3
		}
	}
	if options.BufferPoolSize <= 0 {
		options.BufferPoolSize = 4096
	}
	if options.BytePoolSize <= 0 {
		options.BytePoolSize = 4096
	}
	if options.BytePoolWidth <= 0 {
		options.BytePoolWidth = options.BatchingMaxSize
	}
	if options.BufferPool == nil {
		options.BufferPool = bufferpool.NewBuffer(options.BufferPoolSize)
	}
	if options.BytePool == nil {
		options.BytePool = bufferpool.NewBytePool(options.BytePoolSize, options.BytePoolWidth)
	}

	options.isUDP = isUDP(options.Network)
	options.isTCP = isTCP(options.Network)
	if !options.isUDP && !options.isTCP {
		return nil, ErrInvalidNetwork
	}
	if !options.isV1 && !options.isV3 {
		return nil, ErrInvalidNetwork
	}
	if options.NoFrameHeader && options.isV3 {
		// V3协议用TCP传输必须有帧头
		if options.isTCP {
			return nil, ErrV3TCPNoFrameHeader
		}
		// V3协议启用了加密或者压缩必须有协议头
		if options.Encrypt || options.Compress {
			return nil, ErrV3CENoFrameHeader
		}
	}
	if options.Encrypt && options.EncryptKey == "" {
		return nil, ErrInvalidEncryptKey
	}

	// create discoverer
	discoverer, err := discoverer.NewDNS(options.Host, options.Port, 30*time.Second, options.Logger)
	if err != nil {
		return nil, err
	}

	metrics, err := newMetrics(options.MetricsName, options.MetricsRegistry)
	if err != nil {
		return nil, err
	}

	// the client struct
	cli := &client{
		options:    options,
		discoverer: discoverer,
		connPool:   connpool.NewConnPool(256), // as a client, 256 is enough
		log:        options.Logger,
		workers:    make([]*worker, 0, options.WorkerNum),
		metrics:    metrics,
	}

	if options.isTCP && options.isV3 {
		framer, err := newLengthField(lengthFieldCfg{
			maxFrameLen:  options.MaxFrameLen,
			fieldOffset:  options.FieldOffset,
			fieldLength:  options.FieldLength,
			adjustment:   options.Adjustment,
			bytesToStrip: options.BytesToStrip,
		})
		if err != nil {
			cli.discoverer.Close()
			return nil, err
		}
		cli.framer = framer
	}

	// listen on discoverer events
	cli.discoverer.AddEventHandler(cli)

	// net client handle IO
	netClient, err := gnet.NewClient(cli, gnet.WithLogger(options.Logger))
	if err != nil {
		return nil, err
	}

	// save net client
	cli.netClient = netClient

	// init connections
	initConns := make([]net.Conn, 0)
	for i := 0; i < cli.options.WorkerNum+4; i++ {
		conn, err := cli.getConn()
		if err != nil {
			_ = cli.netClient.Stop()
			cli.discoverer.Close()
			return nil, err
		}

		initConns = append(initConns, conn)
	}
	for _, conn := range initConns {
		cli.putConn(conn)
	}

	// create workers
	for i := 0; i < options.WorkerNum; i++ {
		w, err := cli.createWorker(i)
		if err != nil {
			_ = cli.netClient.Stop()
			cli.discoverer.Close()
			return nil, err
		}
		cli.workers = append(cli.workers, w)
	}

	return cli, nil
}

func (c *client) Dial() (net.Conn, error) {
	ep, err := c.discoverer.GetEndpoint()
	if err != nil {
		return nil, err
	}

	return c.netClient.Dial(c.options.Network, ep.Addr)
}

func (c *client) Send(ctx context.Context, msg *Message) error {
	worker := c.getWorker()
	return worker.send(ctx, msg)
}

func (c *client) SendAsync(ctx context.Context, msg *Message, cb Callback) {
	worker := c.getWorker()
	worker.sendAsync(ctx, msg, cb)
}

func (c *client) getWorker() *worker {
	index := c.curWorkerIndex.Load()
	w := c.workers[index%uint64(len(c.workers))]
	c.curWorkerIndex.Add(1)
	return w
}

func (c *client) Close() {
	if c.discoverer != nil {
		c.discoverer.Close()
	}

	for _, w := range c.workers {
		w.close()
	}

	if c.netClient != nil {
		_ = c.netClient.Stop()
	}
}

func (c *client) createWorker(index int) (*worker, error) {
	return newWorker(c, index, c.options)
}

func (c *client) getConn() (net.Conn, error) {
	return c.connPool.Get(c)
}

func (c *client) putConn(conn net.Conn) {
	c.connPool.Put(conn)
}

func (c *client) closeConn(conn net.Conn) {
	connpool.CloseConn(conn, 2*time.Minute)
}

func (c *client) OnBoot(e gnet.Engine) gnet.Action {
	c.log.Info("client boot")
	return gnet.None
}

func (c *client) OnShutdown(e gnet.Engine) {
	c.log.Info("client shutdown")
}

func (c *client) OnOpen(conn gnet.Conn) ([]byte, gnet.Action) {
	c.log.Debug("connection opened")
	return nil, gnet.None
}

func (c *client) OnClose(conn gnet.Conn, err error) gnet.Action {
	c.log.Debug("connection closed")
	return gnet.None
}

func (c *client) OnTraffic(conn gnet.Conn) (action gnet.Action) {
	c.log.Debug("msg received")
	// todo 处理响应
	if !c.options.isV3 {
		return
	}

	if c.options.isUDP {
		frame, err := conn.Next(-1)
		if err != nil {
			return gnet.Close
		}
		c.onResponse(frame)
		return gnet.None
	}

	for {
		total := conn.InboundBuffered()
		buf, _ := conn.Peek(total)

		length, payloadOffset, payloadOffsetEnd, err := c.framer.readFrame(buf)
		if err == errIncompleteFrame {
			break
		}

		if err != nil {
			c.metrics.incError(errCodeConnReadFailed)
			c.log.Error("invalid packet from stream connection, close it, err:", err)
			// 读失败，关闭连接
			return gnet.Close
		}

		frame, _ := conn.Peek(length)
		_, err = conn.Discard(length)
		if err != nil {
			c.metrics.incError(errCodeConnReadFailed)
			c.log.Error("discard connection stream failed, err", err)
			// 读失败，关闭连接
			return gnet.Close
		}

		// 处理数据
		c.onResponse(frame[payloadOffset:payloadOffsetEnd])

	}
	return gnet.None
}

func (c *client) onResponse(frame []byte) {
	rsp, err := DecodeV3Rsp(
		frame,
		c.options.NoFrameHeader,
		false,
		c.options.HandlerBytesToTrip,
		c.options.EncryptKey,
		c.options.BytePool)
	if err != nil {
		return
	}
	switch r := rsp.Rsp.(type) {
	case *v3.Rsp_HeartbeatRsp:
		_ = r
		return
	case *v3.Rsp_LogRsp:
		_ = r
		batchID := rsp.Header.ReqID
		index := getWorkerIndex(batchID)
		if index < 0 || index >= len(c.workers) {
			return
		}
		c.workers[index].onRsp(batchRsp{batchID: batchID})
	default:
		return
	}
}

func (c *client) OnEndpointDel(endpoints discoverer.EndpointList) {
	deletedEndpoints := make(map[string]struct{})
	for _, ep := range endpoints {
		deletedEndpoints[ep.Addr] = struct{}{}
	}

	// 通知工作者，如果有使用这些端点，需要切换一下
	for _, w := range c.workers {
		w.updateConn(deletedEndpoints)
	}

	// 删除连接池中对应的连接
	num := c.connPool.NumPooled()
	for i := 0; i < num; i++ {
		conn, err := c.connPool.Get(nil)
		if err != nil {
			continue
		}

		addr := conn.RemoteAddr().String()
		_, ok := deletedEndpoints[addr]
		if ok {
			c.closeConn(conn)
		} else {
			c.connPool.Put(conn)
		}
	}
}

func (c *client) OnEndpointAdd(endpoints discoverer.EndpointList) {
	// 创建新的连接放入池中
	for _, ep := range endpoints {
		conn, err := c.netClient.Dial(c.options.Network, ep.Addr)
		if err != nil {
			c.log.Error("dail error:", err)
			continue
		}

		c.putConn(conn)
	}
}

func isUDP(network string) bool {
	return network == "udp" || network == "udp4" || network == "udp6"
}

func isTCP(network string) bool {
	return network == "tcp" || network == "tcp4" || network == "tcp6"
}
