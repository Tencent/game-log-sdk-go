// Package tglog implements the go version of TGLog client
package tglog

import (
	"context"
	"errors"
	"math"
	"net"
	"sync"
	"sync/atomic"
	"time"

	v3 "github.com/tencent/game-log-sdk-proto/pbgo"

	"github.com/panjf2000/gnet/v2"

	"github.com/tencent/game-log-sdk-go/connpool"
	"github.com/tencent/game-log-sdk-go/discoverer"
	"github.com/tencent/game-log-sdk-go/framer"
	"github.com/tencent/game-log-sdk-go/logger"
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
	ErrNoEndpoint         = errors.New("service has no endpoints")
	ErrInvalidProtoVer    = errors.New("invalid protocol version")
	ErrV3TCPNoFrameHeader = errors.New("NoFrameHeader must be false when proto is V3 and network is TCP")
	ErrV3NoFrameHeader    = errors.New("NoFrameHeader must be false when proto is V3 and compress/encrypt is true")
	ErrInvalidEncryptKey  = errors.New("invalid encrypt key")
	ErrNoAvailableWorker  = errors.New("no available worker")
)

// Callback is the callback func that will be called when Client finish sending the message
type Callback func(msg Message, err error)

// Client is the interface of a TGLog netClient
type Client interface {
	// Send sends the msg synchronously. If you create a V3 client, call SendAsync instead of Send, 'cause
	// Send in V3 will block to wait for the responses from the server, the delay is unacceptable.
	Send(ctx context.Context, msg Message) error
	// SendAsync sends the log asynchronously, if cb is not nil, it will be called after the log is sent.
	SendAsync(ctx context.Context, msg Message, cb Callback)
	// Close closes the netClient
	Close()
}

type client struct {
	*gnet.BuiltinEventEngine                                     // 继承网络事件处理器
	options                  *Options                            // 配置
	discoverer               discoverer.Discoverer               // 服务发现
	connPool                 connpool.EndpointRestrictedConnPool // 连接池
	netClient                *gnet.Client                        // 多路复用管理器
	workers                  []*worker                           // 工作者
	curWorkerIndex           atomic.Uint64                       // 当前工作者
	log                      logger.Logger                       // 日志
	metrics                  *metrics                            // 指标
	framer                   framer.Framer                       // TCP分帧器，V1协议不回包，V3协议TCP传输才有用
	closeOnce                sync.Once
}

// NewV1Client news a TGLog client that use UDP network and V1 proto
func NewV1Client(opts ...Option) (Client, error) {
	// default v1 options
	options := &Options{
		Network: "udp",
		isV1:    true,
		isUDP:   true,
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

	return cli, nil
}

// NewV3Client news a TGLog client that use UDP network and V3 proto
func NewV3Client(opts ...Option) (Client, error) {
	// default options
	options := &Options{
		Network:            "udp",
		isV3:               true,
		isUDP:              true,
		MaxRetries:         2,
		Compress:           true,
		MaxFrameLen:        64 * 1024,
		LenFieldOffset:     2,
		LenFieldLength:     4,
		LenAdjustment:      -6,
		FrameBytesToStrip:  0,
		PayloadBytesToTrip: 10,
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

	return cli, nil
}

func (c *client) initAll() error {
	// 以下初始化的顺序不能乱
	err := c.initMetrics()
	if err != nil {
		return err
	}

	err = c.initDiscoverer()
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

	err = c.initWorkers()
	if err != nil {
		return err
	}

	return nil
}

func (c *client) initDiscoverer() error {
	dis, err := discoverer.NewDNS(c.options.Host, c.options.Port, c.options.UpdateInterval, c.options.Logger)
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
		gnet.WithSocketRecvBuffer(c.options.SocketRecvBufferSize),
		gnet.WithTCPKeepAlive(5*time.Minute))
	if err != nil {
		return err
	}

	err = netClient.Start()
	if err != nil {
		return err
	}

	// save net client
	c.netClient = netClient
	return nil
}

func (c *client) initConns() error {
	epList := c.discoverer.GetEndpoints()
	epLen := len(epList)
	if epLen == 0 {
		return ErrNoEndpoint
	}

	endpoints := make([]string, epLen)
	for i := 0; i < epLen; i++ {
		endpoints[i] = epList[i].Addr
	}

	// minimum connection number per endpoint is 1
	connsPerEndpoint := int(math.Ceil(float64(c.options.WorkerNum) * 1.2 / float64(epLen)))
	pool, err := connpool.NewConnPool(endpoints, connsPerEndpoint, 2048, c, c.log, c.options.MaxConnLifetime, c.SendHeartbeat)
	if err != nil {
		return err
	}

	c.connPool = pool
	return nil
}

func (c *client) initFramer() error {
	if !c.options.isV3 || !c.options.isTCP {
		return nil
	}

	fr, err := framer.NewLengthField(framer.LengthFieldCfg{
		LittleEndian: c.options.LittleEndian,
		MaxFrameLen:  c.options.MaxFrameLen,
		FieldOffset:  c.options.LenFieldOffset,
		FieldLength:  c.options.LenFieldLength,
		Adjustment:   c.options.LenAdjustment,
		BytesToStrip: c.options.FrameBytesToStrip,
	})
	if err != nil {
		return err
	}

	c.framer = fr
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

func (c *client) Dial(addr string, ctx any) (gnet.Conn, error) {
	if c.options.ConnTimeout > 0 {
		conn, err := net.DialTimeout(c.options.Network, addr, c.options.ConnTimeout)
		if err != nil {
			return nil, err
		}
		return c.netClient.EnrollContext(conn, ctx)
	}

	return c.netClient.DialContext(c.options.Network, addr, ctx)
}

func (c *client) Send(ctx context.Context, msg Message) error {
	worker, err := c.getWorker()
	if err != nil {
		return ErrNoAvailableWorker
	}
	return worker.send(ctx, msg)
}

func (c *client) SendAsync(ctx context.Context, msg Message, cb Callback) {
	worker, err := c.getWorker()
	if err != nil {
		if cb != nil {
			cb(msg, ErrNoAvailableWorker)
		}
		return
	}

	worker.sendAsync(ctx, msg, cb)
}

func (c *client) getWorker() (*worker, error) {
	// 用原子自增的返回值作为 index，保证并发调用各自拿到不同的序号，避免 Load+Add
	// 两步非原子组合导致多个调用命中同一 worker 加剧热点。Add(1) 返回自增后的值，减 1
	// 得到本次应使用的序号（fetch-and-increment 语义）。
	index := c.curWorkerIndex.Add(1) - 1
	w := c.workers[index%uint64(len(c.workers))]

	if w.available() {
		return w, nil
	}
	c.metrics.incError(workerBusy.strCode)
	return nil, workerBusy
}

func (c *client) Close() {
	c.closeOnce.Do(func() {
		if c.discoverer != nil {
			c.discoverer.Close()
		}

		for _, w := range c.workers {
			w.close()
		}

		// The connection pool must be closed before netClient. Once
		// netClient.Stop() returns, gnet's event-loops have all exited, so any
		// Dial started afterwards would block forever inside
		// gnet.Client.EnrollContext waiting for connection registration (there
		// is no event-loop left to process the registration task), leaking a
		// goroutine. connPool.Close() waits (with a bound) for in-flight dials
		// to finish first, to avoid dials entering EnrollContext after
		// netClient has stopped; for a Dialer that hangs forever, Close still
		// returns after the timeout.
		if c.connPool != nil {
			c.connPool.Close()
		}

		if c.netClient != nil {
			_ = c.netClient.Stop()
		}
	})
}

func (c *client) createWorker(index int) (*worker, error) {
	return newWorker(c, index, c.options)
}

func (c *client) getConn() (gnet.Conn, error) {
	return c.connPool.Get()
}

func (c *client) putConn(conn gnet.Conn, err error) {
	c.connPool.Put(conn, err)
}

func (c *client) OnBoot(e gnet.Engine) gnet.Action {
	c.log.Info("client boot")
	return gnet.None
}

func (c *client) OnShutdown(e gnet.Engine) {
	c.log.Info("client shutdown")
}

func (c *client) OnOpen(conn gnet.Conn) ([]byte, gnet.Action) {
	c.log.Debug("connection opened: ", conn.RemoteAddr())
	return nil, gnet.None
}

func (c *client) OnClose(conn gnet.Conn, err error) gnet.Action {
	if err != nil {
		c.log.Warn("connection closed: ", conn.RemoteAddr(), ", err:", err)
		c.metrics.incError(errConnClosedByPeer.strCode)
	}

	// delete this conn from conn pool
	if c.connPool != nil {
		c.connPool.OnConnClosed(conn, err)
	}

	if err != nil {
		for _, w := range c.workers {
			w.onConnClosed(conn, err)
		}
	}

	return gnet.Close
}

func (c *client) SendHeartbeat(conn gnet.Conn) {
	if c.options.isV1 {
		return
	}

	body := V3ReqPool.Get().(*v3.Req)
	defer V3ReqPool.Put(body)

	reqID := buildBatchID("0")
	header, body, err := BuildV3HeartbeatReq(
		c.options.AppID,
		c.options.AppName,
		c.options.AppVer,
		c.options.Network,
		reqID,
		c.options.Token,
		c.options.TokenType,
		nil,
		nil,
		body)
	if err != nil {
		return
	}

	bb := c.options.BufferPool.Get()
	bytes, err := EncodeV3Req(header, body, c.options.NoFrameHeader, false, false, false, false, "", bb, c.options.LittleEndian)
	if err != nil {
		return
	}

	onErr := func(conn gnet.Conn, e error, inCallback bool) {
		c.metrics.incError(errConnWriteFailed.getStrCode())
		c.log.Error("send heartbeat failed, err:", e, ", serverAddr:", connpool.GetRemoteAddr(conn))
	}

	// 非常重要：我们使用的是gnet异步框架，在不同的协程中发送数据，必须调用AsyncWrite，Write只能在gnet.OnTraffic()中调用
	err = conn.AsyncWrite(bytes, func(conn gnet.Conn, e error) error {
		// 如果是UDP，回调无意义，参数c和e都是nil，具体可以查看AsyncWrite()的代码实现
		if c.options.isUDP {
			return nil
		}
		if e != nil {
			onErr(conn, e, true)
		}
		// 发送完，回收
		c.options.BufferPool.Put(bb)
		return nil
	})

	if err != nil {
		onErr(conn, err, false)
		// 发送失败，回收
		c.options.BufferPool.Put(bb)
	} else if c.options.isUDP {
		// 如果是UDP，成功失败都要在conn.AsyncWrite()返回后处理，而不是在它的回调中处理
		// 发送成功，回收
		c.options.BufferPool.Put(bb)
	}
}

func (c *client) OnTraffic(conn gnet.Conn) (action gnet.Action) {
	// c.log.Debug("response received")
	if !c.options.isV3 {
		return
	}

	if c.options.isUDP {
		total := conn.InboundBuffered()
		if total <= 0 {
			return gnet.None
		}

		frame, err := conn.Peek(total)
		if err != nil {
			c.metrics.incError(errConnReadFailed.getStrCode())
			c.log.Error("read UDP response failed, close it, err:", err)
			return gnet.Close
		}

		c.onResponse(frame)

		_, err = conn.Discard(total)
		if err != nil {
			c.metrics.incError(errConnReadFailed.getStrCode())
			c.log.Error("discard UDP response failed, close it, err:", err)
			return gnet.Close
		}

		return gnet.None
	}

	for {
		total := conn.InboundBuffered()
		if total <= 0 {
			return gnet.None
		}

		buf, err := conn.Peek(total)
		if err != nil {
			c.metrics.incError(errConnReadFailed.getStrCode())
			c.log.Error("peek connection stream failed, close it, err:", err)
			// 读失败，关闭连接
			return gnet.Close
		}

		length, payloadOffset, payloadOffsetEnd, err := c.framer.ReadFrame(buf)
		if err != nil {
			if errors.Is(err, framer.ErrIncompleteFrame) {
				return gnet.None
			}

			c.metrics.incError(errConnReadFailed.getStrCode())
			c.log.Error("invalid packet from stream connection, close it, err:", err)
			// 读失败，关闭连接
			return gnet.Close
		}

		frame, err := conn.Peek(length)
		if err != nil {
			c.metrics.incError(errConnReadFailed.getStrCode())
			c.log.Error("peek connection stream failed, close it, err:", err)
			// 读失败，关闭连接
			return gnet.Close
		}

		// 处理数据
		c.onResponse(frame[payloadOffset:payloadOffsetEnd])

		_, err = conn.Discard(length)
		if err != nil {
			c.metrics.incError(errConnReadFailed.getStrCode())
			c.log.Error("discard connection stream failed, close it, err:", err)
			// 读失败，关闭连接
			return gnet.Close
		}
	}
}

func (c *client) onResponse(frame []byte) {
	rsp := V3RspPool.Get().(*v3.Rsp)
	defer V3RspPool.Put(rsp)

	rsp, err := DecodeV3Rsp(
		frame,
		c.options.NoFrameHeader,
		false,
		c.options.PayloadBytesToTrip,
		c.options.EncryptKey,
		c.options.BytePool,
		rsp)
	if err != nil {
		c.log.Error("decode response failed, err:", err)
		return
	}

	// c.log.Debug(rsp.String())
	switch r := rsp.Rsp.(type) {
	case *v3.Rsp_HeartbeatRsp:
		_ = r
		// c.log.Debug("heartbeat response")
		return
	case *v3.Rsp_LogRsp:
		batchID := rsp.Header.ReqID
		index := getWorkerIndex(batchID)
		// c.log.Debugf("log response, index=%d, batchID=%s", index, batchID)
		if index < 0 || index >= len(c.workers) {
			c.log.Debugf("invalid worker index from response, index=%d", index)
			return
		}
		c.workers[index].onRsp(&batchRsp{
			batchID: batchID,
			code:    int(rsp.Header.Code),
			msg:     rsp.Header.Msg,
			seqs:    r.LogRsp.Seqs})
	default:
		return
	}
}

func (c *client) OnEndpointUpdate(all, add, del discoverer.EndpointList) {
	c.connPool.UpdateEndpoints(all.Addresses(), add.Addresses(), del.Addresses())
}

func isUDP(network string) bool {
	return network == "udp" || network == "udp4" || network == "udp6"
}

func isTCP(network string) bool {
	return network == "tcp" || network == "tcp4" || network == "tcp6"
}
