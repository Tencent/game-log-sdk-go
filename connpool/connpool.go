// Package connpool provides a connection pool.
package connpool

import (
	"context"
	"errors"
	"math"
	"runtime/debug"
	"sync"
	"time"

	"git.woa.com/tglog/v3/sdk-go/logger"
	"github.com/panjf2000/gnet/v2"
	"go.uber.org/atomic"
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
	Get() (gnet.Conn, error)
	Put(conn gnet.Conn, err error)
	UpdateEndpoints(all, add, del []string)
	NumPooled() int
}

// NewConnPool news a EndpointRestrictedConnPool
func NewConnPool(initEndpoints []string, connsPerEndpoint, size int,
	dialer Dialer, log logger.Logger) (EndpointRestrictedConnPool, error) {
	if len(initEndpoints) == 0 {
		return nil, errors.New("init endpoints is empty")
	}

	if connsPerEndpoint <= 0 {
		connsPerEndpoint = 1
	}

	if dialer == nil {
		return nil, errors.New("dialer is nil")
	}

	if log == nil {
		return nil, errors.New("logger is nil")
	}

	initConnNum := len(initEndpoints) * connsPerEndpoint
	if size <= 0 {
		size = int(math.Max(1024, float64(initConnNum)))
	}

	// copy endpoints
	endpoints := make([]string, 0, len(initEndpoints))
	endpoints = append(endpoints, initEndpoints...)

	pool := &connPool{
		connChan:         make(chan gnet.Conn, size),
		endpoints:        endpoints,
		connsPerEndpoint: connsPerEndpoint,
		dialer:           dialer,
		log:              log,
	}

	// store endpoints to map
	for _, e := range endpoints {
		pool.endpointMap.Store(e, struct{}{})
	}

	err := pool.initConns(initConnNum)
	if err != nil {
		return nil, err
	}

	return pool, nil
}

type connPool struct {
	sync.RWMutex
	connChan         chan gnet.Conn
	index            atomic.Uint64
	endpoints        []string
	endpointMap      sync.Map
	connsPerEndpoint int
	dialer           Dialer
	log              logger.Logger
}

func (p *connPool) Get() (gnet.Conn, error) {
	select {
	case conn := <-p.connChan:
		return conn, nil
	default:
		return p.newConn()
	}
}

func (p *connPool) getEndpoint() string {
	index := p.index.Load()
	p.index.Add(1)

	p.RLock()
	ep := p.endpoints[index%uint64(len(p.endpoints))]
	p.RUnlock()
	return ep
}

func (p *connPool) newConn() (gnet.Conn, error) {
	ep := p.getEndpoint()
	return p.dialer.Dial(ep)
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
		p.Put(conn, nil)
	}

	return nil
}

func (p *connPool) Put(conn gnet.Conn, err error) {
	if conn == nil {
		return
	}

	remoteAddr := conn.RemoteAddr()
	if remoteAddr == nil {
		p.log.Error("conn is not nil but remote address is nil")
		return
	}

	addr := remoteAddr.String()
	_, ok := p.endpointMap.Load(addr)
	if !ok {
		p.log.Info("endpoint deleted, close its connection, addr:", addr)
		CloseConn(conn, 2*time.Minute)
		return
	}

	// 如果出错了，先关闭该连接，再尝试补充一个新连接，避免连接数不均衡导致流量不均衡
	if ok && err != nil {
		p.log.Info("connection error, close it and try to open a new one, addr:", addr)
		CloseConn(conn, 2*time.Minute)
		newConn, err := p.dialer.Dial(addr)
		if err != nil {
			return
		}

		select {
		case p.connChan <- newConn:
			return
		case <-time.After(1 * time.Second):
			// connChan is full, close the new conn
			_ = newConn.Close()
			return
		}
	}

	select {
	case p.connChan <- conn:
	default:
		// connChan is full, close the connection after 2m
		CloseConn(conn, 2*time.Minute)
	}
}

func (p *connPool) UpdateEndpoints(all, add, del []string) {
	defer func() {
		if rec := recover(); rec != nil {
			p.log.Errorf("panic when update endpoints:", rec)
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
	p.Lock()
	p.endpoints = endpoints
	p.Unlock()

	// store new endpoints to map
	p.log.Info("add new connections...")
	for _, ep := range add {
		p.endpointMap.Store(ep, struct{}{})

		for i := 0; i < p.connsPerEndpoint; i++ {
			conn, err := p.dialer.Dial(ep)
			if err != nil {
				p.log.Error("new connection failed, addr:", ep, ", err:", err)
				continue
			}

			p.log.Info("endpoint added, open new connection, addr:", ep)
			select {
			case p.connChan <- conn:
				continue
			case <-time.After(1 * time.Second):
				// connChan is full, close the new conn
				_ = conn.Close()
				continue
			}
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

	// delete connections for deleted endpoints
	p.log.Info("delete old connections...")
	for i := 0; i < len(p.connChan); i++ {
		conn, ok := <-p.connChan
		if !ok {
			break
		}

		// fix: when conn is closed by peer, remote add may be nil
		remoteAddr := conn.RemoteAddr()
		if remoteAddr == nil {
			CloseConn(conn, 0)
			continue
		}

		addr := remoteAddr.String()
		_, ok = delEndpoints[addr]
		if ok {
			p.log.Info("endpoint deleted, close its connection, addr:", addr)
			CloseConn(conn, 2*time.Minute)
		} else {
			p.connChan <- conn
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
