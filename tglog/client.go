package tglog

import (
	"context"
	"errors"
	"git.woa.com/tglog/v3/sdk-go/bufferpool"
	"github.com/prometheus/client_golang/prometheus"
	"net"
	"sync/atomic"
	"time"

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
	ErrInvalidHost = errors.New("invalid host")
	ErrInvalidPort = errors.New("invalid port")
)

// Callback is the callback func that will be called when Client finish sending the logger
type Callback func(log string, err error)

// Client is the interface of a TGLog netClient
type Client interface {
	// Send sends the log synchronously
	Send(ctx context.Context, log string) error
	// SendAsync sends the log asynchronously, if cb is not nil, it will be called after the log is sent.
	SendAsync(ctx context.Context, log string, cb Callback)
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
	bufferPool               bufferpool.BufferPool //
	bytePool                 bufferpool.BytePool   //
	log                      logger.Logger         // 日志
	metrics                  *metrics              //
}

// NewClient news a TGLog client
func NewClient(opts ...Option) (Client, error) {
	// default options
	options := &Options{
		Network:                 "udp",
		Codec:                   CodecV1,
		BatchingMaxMessages:     10,
		BatchingMaxPublishDelay: 10 * time.Millisecond,
		BatchingMaxSize:         4096,
		MaxPendingMessages:      40960,
		WorkerNum:               4,
		Logger:                  logger.Std(),
	}

	for _, o := range opts {
		o(options)
	}

	if options.Host == "" {
		return nil, ErrInvalidHost
	}
	if options.Port == 0 {
		return nil, ErrInvalidPort
	}
	if options.WorkerNum <= 0 {
		options.WorkerNum = 4
	}
	if options.MetricsName == "" {
		options.MetricsName = "tglog-go"
	}
	if options.MetricsRegistry == nil {
		options.MetricsRegistry = prometheus.DefaultRegisterer
	}
	if options.Codec == CodecV1 {
		if options.BatchingMaxSize > maxUDPReqSizeV1 && isUDP(options.Network) {
			options.BatchingMaxSize = maxUDPReqSizeV1
		}
		if options.BatchingMaxSize > maxTCPReqSizeV1 && isTCP(options.Network) {
			options.BatchingMaxSize = maxTCPReqSizeV1
		}
	}
	if options.Codec == CodecV3 {
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
		connPool:   connpool.NewConnPool(256), // 256 is enough
		log:        options.Logger,
		workers:    make([]*worker, 0, options.WorkerNum),
		bufferPool: options.BufferPool,
		bytePool:   options.BytePool,
		metrics:    metrics,
	}

	// net client handle IO
	netClient, err := gnet.NewClient(cli, gnet.WithLogger(options.Logger))
	if err != nil {
		return nil, err
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

	// save net client
	cli.netClient = netClient
	// listen on discoverer events
	cli.discoverer.AddEventHandler(cli)

	return cli, nil
}

func (c *client) Dial() (net.Conn, error) {
	ep, err := c.discoverer.GetEndpoint()
	if err != nil {
		return nil, err
	}

	return c.netClient.Dial(c.options.Network, ep.Addr)
}

func (c *client) Send(ctx context.Context, log string) error {
	worker := c.getWorker()
	return worker.send(ctx, []byte(log))
}

func (c *client) SendAsync(ctx context.Context, log string, cb Callback) {
	worker := c.getWorker()
	worker.sendAsync(ctx, []byte(log), cb)
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
	c.log.Debug("data received")
	// todo 处理响应
	return
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
