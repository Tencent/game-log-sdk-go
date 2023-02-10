package tglog

import (
	"context"
	"math"
	"math/rand"
	"time"

	"git.woa.com/tglog/v3/sdk-go/discoverer"
	"git.woa.com/tglog/v3/sdk-go/logger"
	"github.com/panjf2000/gnet/v2"
)

type connMgr struct {
	*gnet.BuiltinEventEngine
	network         string                // 网络
	discoverer      discoverer.Discoverer // 服务发现
	netClient       *gnet.Client          // 多路复用管理器
	connIndex       int                   // 连接索引，负载均衡用
	activeConns     []*netConn            // 活跃连接，每个IP:Port只创建一个连接
	inactiveConns   []*netConn            // 非活跃连接
	log             logger.Logger         // 日志
	reqChan         chan interface{}      // 命令管道
	dataChan        chan *reqSendData     // 数据管道
	closeConnTicker *time.Ticker          // 关闭非活跃连接定时器
}

func newConnMgr(network string, discoverer discoverer.Discoverer, log logger.Logger) (*connMgr, error) {
	cm := &connMgr{
		network:         network,
		discoverer:      discoverer,
		inactiveConns:   make([]*netConn, 0),
		log:             log,
		reqChan:         make(chan interface{}),
		dataChan:        make(chan *reqSendData, 4096),
		closeConnTicker: time.NewTicker(1 * time.Minute),
		connIndex:       rand.New(rand.NewSource(time.Now().UnixNano())).Int(),
	}

	endpoints := discoverer.GetEndpoints()
	if len(endpoints) == 0 {
		return nil, ErrDomainLookup
	}

	// 多路复用，用于管理连接的事件
	netClient, err := gnet.NewClient(cm, gnet.WithLogger(log))
	if err != nil {
		return nil, err
	}

	addrs := endpoints.Addresses()
	// 创建初始连接，并加入netClient集中管理
	netConns := createConns(netClient, network, addrs, log)
	if len(netConns) == 0 {
		return nil, ErrNoConns
	}

	// 保存多路复用管理器
	cm.netClient = netClient
	// 保存活跃连接
	cm.activeConns = netConns
	// 监听服务发现事件，当节点上、下线时得到通知
	cm.discoverer.AddEventHandler(cm)
	go cm.start()

	return cm, nil
}

func createConns(cli *gnet.Client, network string, addrs []string, log logger.Logger) []*netConn {
	conns := make([]*netConn, 0)

	for _, addr := range addrs {
		conn, err := cli.Dial(network, addr)
		if err != nil {
			log.Error("dail addr err:", err)
			continue
		}

		conns = append(conns, &netConn{
			remoteAddr: addr,
			conn:       conn,
		})
	}

	return conns
}

func (c *connMgr) send(ctx context.Context, data []byte) error {
	// 构造请求，放入队列
	req := &reqSendData{
		data: data,
	}

	select {
	case c.dataChan <- req:
	case <-ctx.Done():
		return ctx.Err()
	}

	return nil
}

func (c *connMgr) handleSend(req *reqSendData) {
	// 获取一个连接
	conn := c.getConn()
	if conn == nil {
		c.log.Error("no available connections")
		return
	}

	// 发送数据
	_, err := conn.send(req.data)
	if err != nil {
		c.log.Error("write err:", err)
		// 写失败，删除连接？
		c.delConns([]string{conn.remoteAddr})
	}
}

func (c *connMgr) getConn() *netConn {
	connNum := len(c.activeConns)
	if connNum == 0 {
		// 没有连接了，重建
		endpoints := c.discoverer.GetEndpoints()
		addrs := endpoints.Addresses()
		conns := createConns(c.netClient, c.network, addrs, c.log)
		if len(conns) == 0 {
			return nil
		}

		c.activeConns = append(c.activeConns, conns...)
		connNum = len(c.activeConns)
	}

	if connNum == 1 {
		return c.activeConns[0]
	}

	if c.connIndex >= math.MaxInt {
		c.connIndex = 0
	}

	conn := c.activeConns[c.connIndex%connNum]
	c.connIndex++

	return conn
}

func (c *connMgr) start() error {
	err := c.netClient.Start()
	if err != nil {
		return err
	}

	for {
		select {
		case <-c.closeConnTicker.C:
			c.handleCloseConns()
		case req, ok := <-c.reqChan:
			if !ok {
				continue
			}
			switch r := req.(type) {
			case *reqAddEndpoint:
				c.handleAddEndpoints(r)
			case *reqDelEndpoint:
				c.handleDelEndpoints(r)
			case *reqStop:
				c.handleStop(r)
			}

		case data, ok := <-c.dataChan:
			if !ok {
				continue
			}
			c.handleSend(data)
		}
	}
}

func (c *connMgr) stop() {
	req := &reqStop{
		doneCh: make(chan struct{}),
	}

	c.reqChan <- req
	// wait
	<-req.doneCh
}

func (c *connMgr) handleStop(r *reqStop) {
	defer close(r.doneCh)

	c.discoverer.DelEventHandler(c)

	c.closeConnTicker.Stop()

	for _, conn := range c.inactiveConns {
		conn.close()
	}

	for _, conn := range c.activeConns {
		conn.close()
	}

	close(c.reqChan)
	close(c.dataChan)

	c.netClient.Stop()
}

func (c *connMgr) OnBoot(e gnet.Engine) gnet.Action {
	c.log.Info("client boot")
	return gnet.None
}

func (c *connMgr) OnShutdown(e gnet.Engine) {
	c.log.Info("client shutdown")
}

func (c *connMgr) OnOpen(conn gnet.Conn) ([]byte, gnet.Action) {
	c.log.Debug("connection opened")
	return nil, gnet.None
}

func (c *connMgr) OnClose(conn gnet.Conn, err error) gnet.Action {
	c.log.Debug("connection closed")
	return gnet.None
}

func (c *connMgr) OnTraffic(conn gnet.Conn) (action gnet.Action) {
	c.log.Debug("data received")
	// todo 处理响应
	return
}

func (c *connMgr) OnEndpointDel(endpoints discoverer.EndpointList) {
	cmd := &reqDelEndpoint{endpoints: endpoints}
	c.reqChan <- cmd
}

func (c *connMgr) handleDelEndpoints(r *reqDelEndpoint) {
	addrs := r.endpoints.Addresses()
	c.delConns(addrs)
}

func (c *connMgr) delConns(addresses []string) {
	// 有主机被删除，要把连接禁用，并从活跃列表中移除
	indexes := make(map[int]struct{})
	for _, addr := range addresses {
		for i, conn := range c.activeConns {
			if conn.remoteAddr == addr {
				// 保存被删除的主机的下标
				indexes[i] = struct{}{}
				// 禁用
				conn.deactivate()
				// 放入禁用列表
				c.inactiveConns = append(c.inactiveConns, conn)
			}
		}
	}

	// 收集剩余活跃连接
	leftConns := make([]*netConn, 0)
	for i, conn := range c.activeConns {
		if _, ok := indexes[i]; !ok {
			leftConns = append(leftConns, conn)
		}
	}

	// 更新活跃连接
	c.activeConns = leftConns
}

func (c *connMgr) OnEndpointAdd(endpoints discoverer.EndpointList) {
	cmd := &reqAddEndpoint{endpoints: endpoints}
	c.reqChan <- cmd
}

func (c *connMgr) handleAddEndpoints(r *reqAddEndpoint) {
	// 有新主机上线，创建连接，并放入活跃队列
	addrs := r.endpoints.Addresses()
	c.addConns(addrs)
}

func (c *connMgr) addConns(addresses []string) {
	conns := createConns(c.netClient, c.network, addresses, c.log)
	c.activeConns = append(c.activeConns, conns...)
}

func (c *connMgr) handleCloseConns() {
	closed := 0
	for _, conn := range c.inactiveConns {
		if conn.isClosable() {
			conn.close()
			closed++
		}
	}

	if closed > 0 {
		left := make([]*netConn, 0)
		for _, conn := range c.inactiveConns {
			if !conn.isClosed() {
				left = append(left, conn)
			}
		}

		c.inactiveConns = left
	}
}

type reqAddEndpoint struct {
	endpoints discoverer.EndpointList
}

type reqDelEndpoint struct {
	endpoints discoverer.EndpointList
}

type reqSendData struct {
	data []byte
}

type reqStop struct {
	doneCh chan struct{}
}

type netConn struct {
	remoteAddr    string
	deactivatedAt time.Time
	conn          gnet.Conn
	closed        bool
}

func (c *netConn) send(p []byte) (int, error) {
	return c.conn.Write(p)
}

func (c *netConn) deactivate() {
	if !c.deactivatedAt.IsZero() {
		return
	}

	c.deactivatedAt = time.Now()
}

func (c *netConn) isClosable() bool {
	if c.deactivatedAt.IsZero() {
		return false
	}

	// 失效超过2m表示超时，可以移除
	return time.Since(c.deactivatedAt) > 2*time.Minute
}

func (c *netConn) close() {
	c.conn.Close()
	c.closed = true
}

func (c *netConn) isClosed() bool {
	return c.closed
}
