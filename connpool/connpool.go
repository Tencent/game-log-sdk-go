// Package connpool provides a connection pool.
package connpool

import (
	"context"
	"errors"
	"math"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/panjf2000/gnet/v2"

	"git.woa.com/tglog/v3/sdk-go/logger"
	"git.woa.com/tglog/v3/sdk-go/util"
)

// error variables
var (
	ErrInitEndpointEmpty   = errors.New("init endpoints is empty")
	ErrDialerIsNil         = errors.New("dialer is nil")
	ErrLoggerIsNil         = errors.New("logger is nil")
	ErrNoAvailableEndpoint = errors.New("no available server endpoint")
)

// Dialer is the interface of a dialer that return a NetConn
type Dialer interface {
	Dial(addr string) (gnet.Conn, error)
}

// EndpointRestrictedConnPool is the interface of a simple endpoint restricted connection connPool
// the connection's remote address must be in an endpoint list, if not, it will be closed and can
// not be used anymore, it is useful for holding the connections to a service whose endpoints can
// be changed at runtime.
type EndpointRestrictedConnPool interface {
	// Get gets a connection
	Get() (gnet.Conn, error)
	// Put puts a connection back to the pool, if err is not nil, the connection will be closed by the pool
	Put(conn gnet.Conn, err error)
	// UpdateEndpoints updates the endpoints the pool to dial to
	UpdateEndpoints(all, add, del []string)
	// NumPooled returns the connection number in the pool, not the number of all the connection that the pool created
	NumPooled() int
	// OnConnClosed used to notify that a connection is closed, the connection will be removed from the pool, if err is not nil, the remote endpoint will mark as unavailable
	OnConnClosed(conn gnet.Conn, err error)
	// Close closes the pool
	Close()
}

// NewConnPool news a EndpointRestrictedConnPool
func NewConnPool(initEndpoints []string, connsPerEndpoint, size int,
	dialer Dialer, log logger.Logger) (EndpointRestrictedConnPool, error) {
	if len(initEndpoints) == 0 {
		return nil, ErrInitEndpointEmpty
	}

	if connsPerEndpoint <= 0 {
		connsPerEndpoint = 1
	}

	if dialer == nil {
		return nil, ErrDialerIsNil
	}

	if log == nil {
		return nil, ErrLoggerIsNil
	}

	requiredConnNum := len(initEndpoints) * connsPerEndpoint
	if size <= 0 {
		size = int(math.Max(1024, float64(requiredConnNum)))
	}

	// copy endpoints
	endpoints := make([]string, 0, len(initEndpoints))
	endpoints = append(endpoints, initEndpoints...)

	pool := &connPool{
		connChan:         make(chan gnet.Conn, size),
		connsPerEndpoint: connsPerEndpoint,
		requiredConnNum:  requiredConnNum,
		dialer:           dialer,
		log:              log,
		backoff: util.ExponentialBackoff{
			InitialInterval: 10 * time.Second,
			MaxInterval:     1 * time.Minute,
			Multiplier:      2,
			Randomization:   0.5,
		},
		closeCh: make(chan struct{}),
	}

	// store endpoints
	pool.endpoints.Store(endpoints)

	// store endpoints to map
	for _, e := range endpoints {
		pool.endpointMap.Store(e, struct{}{})
	}

	err := pool.initConns(requiredConnNum)
	if err != nil {
		return nil, err
	}

	// 启动后台任务，定期检查并尝试恢复不可用的节点
	go pool.recoverAndRebalance()

	return pool, nil
}

type connPool struct {
	connChan           chan gnet.Conn
	index              atomic.Uint64
	endpoints          atomic.Value
	endpointMap        sync.Map
	connsPerEndpoint   int
	requiredConnNum    int
	dialer             Dialer
	log                logger.Logger
	unavailable        sync.Map
	retryCounts        sync.Map
	backoff            util.ExponentialBackoff
	closeCh            chan struct{}
	closeOnce          sync.Once
	endpointConnCounts sync.Map // 记录每个节点的连接数
}

func (p *connPool) Get() (gnet.Conn, error) {
	p.log.Debug("Get()")
	select {
	case conn := <-p.connChan:
		return conn, nil
	default:
		conn, err := p.newConn()
		if err != nil {
			return nil, err
		}
		addr := conn.RemoteAddr()
		if addr == nil {
			return nil, err
		}
		p.incEndpointConnCount(addr.String())
		return conn, nil
	}
}

func (p *connPool) getEndpoint() (string, error) {
	p.log.Debug("getEndpoint()")
	epValue := p.endpoints.Load()
	endpoints, ok := epValue.([]string)
	if !ok || len(endpoints) == 0 {
		return "", ErrNoAvailableEndpoint
	}

	for i := 0; i < len(endpoints); i++ {
		index := p.index.Load()
		p.index.Add(1)
		ep := endpoints[index%uint64(len(endpoints))]

		// 在不可用节点列表里，跳过
		_, unavailable := p.unavailable.Load(ep)
		if unavailable {
			continue
		}

		return ep, nil
	}

	return "", ErrNoAvailableEndpoint
}

func (p *connPool) newConn() (gnet.Conn, error) {
	p.log.Debug("newConn()")
	ep, err := p.getEndpoint()
	if err != nil {
		return nil, err
	}

	return p.dialNewConn(ep)
}

func (p *connPool) dialNewConn(ep string) (gnet.Conn, error) {
	p.log.Debug("dialNewConn()")
	conn, err := p.dialer.Dial(ep)
	if err != nil {
		p.markUnavailable(ep)
		return nil, err
	}
	return conn, nil
}

func (p *connPool) initConns(count int) error {
	// create some conns and then put them back to the pool
	conns := make([]gnet.Conn, 0)
	for i := 0; i < count; i++ {
		conn, err := p.newConn()
		if err != nil {
			return err
		}

		conns = append(conns, conn)
	}

	for _, conn := range conns {
		p.put(conn, nil, true)
	}

	return nil
}

func (p *connPool) Put(conn gnet.Conn, err error) {
	p.put(conn, err, false)
}

func (p *connPool) put(conn gnet.Conn, err error, isNewConn bool) {
	if conn == nil {
		return
	}

	remoteAddr := conn.RemoteAddr()
	if remoteAddr == nil {
		p.log.Error("remote address is nil, it is closed, stop putting")
		return
	}

	addr := remoteAddr.String()
	_, ok := p.endpointMap.Load(addr)
	if !ok {
		p.log.Info("endpoint deleted, close its connection, addr:", addr)
		CloseConn(conn, 2*time.Minute)
		return
	}

	// 如果出错了，先关闭该连接
	if ok && err != nil {
		p.log.Warn("connection error, close it, addr:", addr, ", err:", err)
		CloseConn(conn, 2*time.Minute)
		return
	}

	select {
	case p.connChan <- conn:
		// 更新连接数
		if isNewConn {
			p.incEndpointConnCount(addr)
		}
	default:
		// connChan is full, close the connection after 2m
		CloseConn(conn, 2*time.Minute)
	}
}

func (p *connPool) incEndpointConnCount(addr string) {
	count, _ := p.endpointConnCounts.LoadOrStore(addr, 0)
	p.endpointConnCounts.Store(addr, count.(int)+1)
}

func (p *connPool) decEndpointConnCount(addr string) {
	count, ok := p.endpointConnCounts.Load(addr)
	if !ok {
		return
	}

	if count.(int) > 0 {
		if count.(int) == 1 {
			p.endpointConnCounts.Delete(addr)
			return
		}

		p.endpointConnCounts.Store(addr, count.(int)-1)
	}
}

func (p *connPool) UpdateEndpoints(all, add, del []string) {
	defer func() {
		if rec := recover(); rec != nil {
			p.log.Error("panic when update endpoints:", rec)
			p.log.Error(string(debug.Stack()))
		}
	}()

	if len(all) == 0 {
		return
	}
	p.log.Debug("UpdateEndpoints")
	p.log.Debug("all:", all)
	p.log.Debug("add:", add)
	p.log.Debug("del:", del)
	endpoints := make([]string, 0, len(all))
	endpoints = append(endpoints, all...)
	p.endpoints.Store(endpoints)

	// store new endpoints to map
	p.log.Info("add new connections...")
	for _, ep := range add {
		p.endpointMap.Store(ep, struct{}{})

		for i := 0; i < p.connsPerEndpoint; i++ {
			conn, err := p.dialNewConn(ep)
			if err != nil {
				p.log.Error("new connection failed, addr:", ep, ", err:", err)
				continue
			}

			p.put(conn, nil, true)
		}
	}

	//
	delEndpoints := make(map[string]struct{})
	for _, ep := range del {
		p.endpointMap.Delete(ep)
		delEndpoints[ep] = struct{}{}
	}

	if len(delEndpoints) == 0 {
		return
	}

	// 从 unavailable 列表中移除删除的节点
	for ep := range delEndpoints {
		p.unavailable.Delete(ep)
		p.retryCounts.Delete(ep)
	}

	// delete connections for deleted endpoints
	p.log.Info("delete old connections...")
	for i := 0; i < cap(p.connChan); i++ {
		select {
		case conn := <-p.connChan:
			// fix: when conn is closed by peer, remote addr may be nil
			remoteAddr := conn.RemoteAddr()
			if remoteAddr == nil {
				CloseConn(conn, 0)
				continue
			}

			addr := remoteAddr.String()
			if _, ok := delEndpoints[addr]; ok {
				p.log.Info("endpoint deleted, close its connection, addr:", addr)
				CloseConn(conn, 2*time.Minute)
			} else {
				select {
				case p.connChan <- conn:
				default:
					CloseConn(conn, 2*time.Minute)
				}
			}
		default:
			// 没有更多的连接了，退出循环
			return
		}
	}
}

func (p *connPool) NumPooled() int {
	return len(p.connChan)
}

// CloseConn closes a connection after a duration of time
func CloseConn(conn gnet.Conn, after time.Duration) {
	if after <= 0 {
		_ = conn.Close()
		return
	}

	ctx := context.Background()
	go func() {
		select {
		case <-time.After(after):
			_ = conn.Close()
			return
		case <-ctx.Done():
			_ = conn.Close()
			return
		}
	}()
}

// OnConnClosed handles conn closed event, call it when conn is closed actively by the server
func (p *connPool) OnConnClosed(conn gnet.Conn, err error) {
	remoteAddr := conn.RemoteAddr()
	if remoteAddr != nil {
		addr := remoteAddr.String()
		if err != nil {
			p.markUnavailable(addr)
		}
		p.decEndpointConnCount(addr)
	}

	// 使用临时切片存储从 connChan 中取出的连接
	tempConns := make([]gnet.Conn, 0, cap(p.connChan))

	// 遍历 connChan，找到并删除关闭的连接
loop:
	for i := 0; i < cap(p.connChan); i++ {
		select {
		case chConn := <-p.connChan:
			if chConn != conn && chConn.RemoteAddr() != nil {
				// 如果不是要删除的连接，则存储到临时切片
				tempConns = append(tempConns, chConn)
			} else {
				if remoteAddr != nil {
					p.log.Debug("remove conn from pool, addr:", remoteAddr.String())
				}
			}
		default:
			// 没有更多的连接了，退出循环
			break loop
		}
	}

	// 将非目标连接重新放回 connChan
	for _, chConn := range tempConns {
		select {
		case p.connChan <- chConn:
		default:
			// 如果 connChan 已满，停止放回
			return
		}
	}
}

func (p *connPool) markUnavailable(ep string) {
	p.log.Info("endpoint cannot be connected, marking as unavailable, addr: ", ep)
	p.unavailable.Store(ep, time.Now())
	p.retryCounts.Store(ep, 0)
}

// recoverAndRebalance 定期检查并尝试恢复不可用的节点
func (p *connPool) recoverAndRebalance() {
	recoverTicker := time.NewTicker(10 * time.Second)
	defer recoverTicker.Stop()
	dumpTicker := time.NewTicker(10 * time.Second)
	defer dumpTicker.Stop()
	reBalanceTicker := time.NewTicker(1 * time.Minute)
	defer reBalanceTicker.Stop()

	for {
		select {
		case <-recoverTicker.C:
			// 重新均衡
			recovered := p.recover()
			if recovered {
				p.rebalance()
			}
		case <-dumpTicker.C:
			p.dump()
		case <-reBalanceTicker.C:
			p.rebalance()
		case <-p.closeCh:
			return
		}
	}
}

func (p *connPool) dump() {
	p.log.Info("all endpoints:")
	eps := p.endpoints.Load()
	endpoints, ok := eps.([]string)
	if ok {
		for _, ep := range endpoints {
			p.log.Info(ep)
		}
	}

	dump := false
	p.unavailable.Range(func(key, value any) bool {
		if !dump {
			p.log.Info("unavailable endpoints:")
		}
		p.log.Info(key)
		return true
	})

	p.log.Info("opened connections:")
	p.endpointConnCounts.Range(func(key, value any) bool {
		p.log.Info("endpoint: ", key, ", conns: ", value.(int))
		return true
	})
}

func (p *connPool) recover() bool {
	recovered := false
	p.unavailable.Range(func(key, value any) bool {
		lastUnavailable := value.(time.Time)
		retries := 0
		retry, ok := p.retryCounts.Load(key)
		if ok {
			retries = retry.(int)
		}
		if time.Since(lastUnavailable) > p.backoff.Next(retries) {
			// 尝试创建新连接
			conn, err := p.dialer.Dial(key.(string))
			if err == nil {
				p.log.Info("endpoint recovered, addr: ", key)
				p.put(conn, nil, true)
				p.unavailable.Delete(key)
				p.retryCounts.Delete(key)
				recovered = true
			} else {
				p.log.Info("failed to recover endpoint, addr: ", key, ", err: ", err)
				// 更新重试次数
				retries++
				p.retryCounts.Store(key, retries)
			}
		}
		return true
	})
	if recovered {
		p.log.Info("recover triggered")
	}
	return recovered
}

func (p *connPool) rebalance() {
	// 计算当前已创建的连接数
	totalConnCount := 0
	p.endpointConnCounts.Range(func(key, value any) bool {
		totalConnCount += value.(int)
		return true
	})

	// 使用实际的连接数和 p.requiredConnNum 取最大值
	totalConnCount = int(math.Max(float64(totalConnCount), float64(p.requiredConnNum)))
	if totalConnCount == 0 {
		return
	}

	unavailableEndpointNum := 0
	p.unavailable.Range(func(key, value any) bool {
		unavailableEndpointNum++
		return true
	})

	epValue := p.endpoints.Load()
	endpoints, ok := epValue.([]string)
	if !ok {
		return
	}

	availableEndpointNum := len(endpoints) - unavailableEndpointNum
	if availableEndpointNum <= 0 {
		return
	}

	expectedConnsPerEndpoint := int(math.Ceil(float64(totalConnCount) / float64(availableEndpointNum)))

	rebalanced := false
	p.endpointConnCounts.Range(func(key, value any) bool {
		addr := key.(string)
		currentCount := value.(int)
		if currentCount < expectedConnsPerEndpoint {
			rebalanced = true
			// 增加连接数
			for i := currentCount; i < expectedConnsPerEndpoint; i++ {
				conn, err := p.dialer.Dial(addr)
				if err == nil {
					p.log.Info("adding connection for addr: ", addr)
					p.put(conn, nil, true)
				} else {
					break
				}
			}
		} else if currentCount > expectedConnsPerEndpoint {
			rebalanced = true
			// 减少连接数
			for i := currentCount; i > expectedConnsPerEndpoint; i-- {
				p.removeEndpointConn(addr)
			}
		}
		return true
	})

	if rebalanced {
		p.log.Info("rebalance triggered")
	}
}

func (p *connPool) removeEndpointConn(addr string) {
	for i := 0; i < cap(p.connChan); i++ {
		select {
		case conn := <-p.connChan:
			remoteAddr := conn.RemoteAddr()
			if remoteAddr == nil {
				continue
			}

			if remoteAddr.String() == addr {
				p.log.Info("reducing connection for addr: ", addr)
				CloseConn(conn, 2*time.Minute)
				return
			}

			// 不是目标连接，放回去
			p.connChan <- conn
		default:
			// 没有更多的连接了，退出循环
			return
		}
	}
}

// Close 关闭连接池，释放资源
func (p *connPool) Close() {
	p.closeOnce.Do(func() {
		close(p.closeCh)

		// 关闭所有连接
		for {
			select {
			case conn := <-p.connChan:
				CloseConn(conn, 0)
			default:
				return
			}
		}
	})
}
