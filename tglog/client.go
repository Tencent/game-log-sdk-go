package tglog

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	v3 "git.woa.com/tglog/v3/proto/pbgo"

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
	ErrInvalidProtoVer    = errors.New("invalid protocol version")
	ErrV3TCPNoFrameHeader = errors.New("NoFrameHeader it must be false when protocol is V3 and network is TCP")
	ErrV3CENoFrameHeader  = errors.New("NoFrameHeader it must be false when protocol is V3 and compress or encrypt is true")
	ErrInvalidEncryptKey  = errors.New("invalid encrypt key")
)

// Callback is the callback func that will be called when Client finish sending the message
type Callback func(msg Message, err error)

// Client is the interface of a TGLog netClient
type Client interface {
	// Send sends the msg synchronously
	Send(ctx context.Context, msg Message) error
	// SendAsync sends the log asynchronously, if cb is not nil, it will be called after the log is sent.
	SendAsync(ctx context.Context, msg Message, cb Callback)
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

// NewV1Client news a TGLog client that use UDP network and V1 proto
func NewV1Client(opts ...Option) (Client, error) {
	// default v1 options
	options := &Options{
		Network:                 "udp",
		WorkerNum:               4,
		BatchingMaxPublishDelay: 10 * time.Millisecond,
		BatchingMaxMessages:     10,
		BatchingMaxSize:         4096,
		MaxPendingMessages:      40960,
		ConnTimeout:             3 * time.Second,
		BufferPoolSize:          40960,
		BytePoolSize:            40960,
		BytePoolWidth:           4096,
		Logger:                  logger.Std(),
		MetricsName:             "tglog-go",
		MetricsRegistry:         prometheus.DefaultRegisterer,
		isV1:                    true,
		isUDP:                   true,
	}

	for _, o := range opts {
		o(options)
	}

	err := options.ValidateAndSetDefault()
	if err != nil {
		return nil, err
	}

	// the client struct
	cli := &client{
		options: options,
		log:     options.Logger,
	}

	err = cli.initAll()
	if err != nil {
		cli.Close()
		return nil, err
	}

	err = cli.netClient.Start()
	if err != nil {
		cli.Close()
		return nil, err
	}

	return cli, nil
}

// NewV3Client news a TGLog client that use UDP network and V3 proto
func NewV3Client(opts ...Option) (Client, error) {
	// default options
	options := &Options{
		Network:                 "udp",
		WorkerNum:               4,
		BatchingMaxPublishDelay: 10 * time.Millisecond,
		BatchingMaxMessages:     10,
		BatchingMaxSize:         4096,
		MaxPendingMessages:      40960,
		ConnTimeout:             3 * time.Second,
		BufferPoolSize:          40960,
		BytePoolSize:            40960,
		BytePoolWidth:           4096,
		Logger:                  logger.Std(),
		MetricsName:             "tglog-go",
		MetricsRegistry:         prometheus.DefaultRegisterer,
		isV3:                    true,
		isUDP:                   true,
		SendTimeout:             10 * time.Second,
		MaxRetries:              2,
		Compress:                true,
		MaxFrameLen:             64 * 1024,
		LenFieldOffset:          2,
		LenFieldLength:          4,
		LenAdjustment:           -6,
		FrameBytesToStrip:       0,
		PayloadBytesToTrip:      10,
	}

	for _, o := range opts {
		o(options)
	}

	err := options.ValidateAndSetDefault()
	if err != nil {
		return nil, err
	}

	// the client struct
	cli := &client{
		options: options,
		log:     options.Logger,
	}

	err = cli.initAll()
	if err != nil {
		cli.Close()
		return nil, err
	}

	err = cli.netClient.Start()
	if err != nil {
		cli.Close()
		return nil, err
	}

	return cli, nil
}

func (c *client) initAll() error {
	// 以下初始化的顺序不能乱
	err := c.initDiscoverer()
	if err != nil {
		return err
	}

	err = c.initNetClient()
	if err != nil {
		return err
	}

	err = c.initConns()
	if err != nil {
		return err
	}

	err = c.initFramer()
	if err != nil {
		return err
	}

	err = c.initMetrics()
	if err != nil {
		return err
	}

	err = c.initWorkers()
	if err != nil {
		return err
	}

	return nil
}

func (c *client) initDiscoverer() error {
	dis, err := discoverer.NewDNS(c.options.Host, c.options.Port, 30*time.Second, c.options.Logger)
	if err != nil {
		return err
	}

	c.discoverer = dis
	dis.AddEventHandler(c)
	return nil
}

func (c *client) initNetClient() error {
	netClient, err := gnet.NewClient(
		c,
		gnet.WithLogger(c.options.Logger),
		gnet.WithWriteBufferCap(c.options.WriteBufferSize),
		gnet.WithReadBufferCap(c.options.ReadBufferSize),
		gnet.WithSocketSendBuffer(c.options.SocketSendBufferSize),
		gnet.WithSocketRecvBuffer(c.options.SocketRecvBufferSize))
	if err != nil {
		return err
	}

	// save net client
	c.netClient = netClient
	return nil
}

func (c *client) initConns() error {
	// as a client, 256 is enough
	c.connPool = connpool.NewConnPool(256)

	// create some conns and then put them back to the pool
	initConns := make([]net.Conn, 0)
	for i := 0; i < c.options.WorkerNum+4; i++ {
		conn, err := c.getConn()
		if err != nil {
			return err
		}

		initConns = append(initConns, conn)
	}

	for _, conn := range initConns {
		c.putConn(conn)
	}

	return nil
}

func (c *client) initFramer() error {
	if !c.options.isV3 || !c.options.isTCP {
		return nil
	}

	framer, err := newLengthField(lengthFieldCfg{
		maxFrameLen:  c.options.MaxFrameLen,
		fieldOffset:  c.options.LenFieldOffset,
		fieldLength:  c.options.LenFieldLength,
		adjustment:   c.options.LenAdjustment,
		bytesToStrip: c.options.FrameBytesToStrip,
	})
	if err != nil {
		return err
	}

	c.framer = framer
	return nil
}

func (c *client) initWorkers() error {
	c.workers = make([]*worker, 0, c.options.WorkerNum)
	for i := 0; i < c.options.WorkerNum; i++ {
		w, err := c.createWorker(i)
		if err != nil {
			return err
		}
		c.workers = append(c.workers, w)
	}

	return nil
}

func (c *client) initMetrics() error {
	m, err := newMetrics(c.options.MetricsName, c.options.MetricsRegistry)
	if err != nil {
		return err
	}

	c.metrics = m
	return nil
}

func (c *client) Dial() (net.Conn, error) {
	ep, err := c.discoverer.GetEndpoint()
	if err != nil {
		return nil, err
	}

	return c.netClient.Dial(c.options.Network, ep.Addr)
}

func (c *client) Send(ctx context.Context, msg Message) error {
	worker := c.getWorker()
	return worker.send(ctx, msg)
}

func (c *client) SendAsync(ctx context.Context, msg Message, cb Callback) {
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
	// c.log.Debug("response received")
	if !c.options.isV3 {
		return
	}

	if c.options.isUDP {
		frame, err := conn.Next(-1)
		if err != nil {
			c.log.Error("read UDP response failed, err:", err)
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
	rsp := v3RspPool.Get().(*v3.Rsp)
	defer v3RspPool.Put(rsp)
	
	rsp, err := DecodeV3Rsp(
		frame,
		c.options.NoFrameHeader,
		false,
		c.options.PayloadBytesToTrip,
		c.options.EncryptKey,
		c.options.BytePool,
		rsp)
	if err != nil {
		c.log.Error("decode UDP response failed, err:", err)
		return
	}

	// c.log.Debug(rsp.String())
	switch r := rsp.Rsp.(type) {
	case *v3.Rsp_HeartbeatRsp:
		_ = r
		// c.log.Debug("heartbeat response")
		return
	case *v3.Rsp_LogRsp:
		_ = r
		batchID := rsp.Header.ReqID
		index := getWorkerIndex(batchID)
		// c.log.Debugf("log response, index=%d, batchID=%s", index, batchID)
		if index < 0 || index >= len(c.workers) {
			c.log.Debugf("invalid worker index from response, index=%d", index)
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
