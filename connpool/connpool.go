// Package connpool provides a connection pool.
package connpool

import (
	"errors"
	"math"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/panjf2000/gnet/v2"

	"github.com/tencent/game-log-sdk-go/logger"
	"github.com/tencent/game-log-sdk-go/util"
)

const (
	defaultConnCloseDelay = 2 * time.Minute
	defaultCmdChanSize    = 4096
	// defaultCloseDialWait bounds how long Close waits for the dials that are
	// already in flight. Dials normally return quickly because the Dialer
	// applies its own connect timeout, but a hung Dialer must not block Close
	// forever.
	defaultCloseDialWait = 5 * time.Second
)

// error variables
var (
	ErrInitEndpointEmpty   = errors.New("init endpoints is empty")
	ErrDialerIsNil         = errors.New("dialer is nil")
	ErrLoggerIsNil         = errors.New("logger is nil")
	ErrNoAvailableEndpoint = errors.New("no available server endpoint")
	ErrPoolClosed          = errors.New("connection pool is closed")
)

// Dialer is the interface of a dialer that return a NetConn
type Dialer interface {
	// Dial dials to the addr and bind ctx to the returned connection, which network(TCP/UDP) to use is determined by the Dialer
	// Dial should use gnet.Client.DialContext() to get a connection that can be driven by a gnet event engine.
	Dial(addr string, ctx any) (gnet.Conn, error)
}

// HeartbeatFunc is the callback func to send a heartbeat request
// Because we will call this func in connpool's goroutine, you should
// call gnet.Conn.AsyncWrite() or gnet.Conn.AsyncWritev() to send the heartbeat request.
type HeartbeatFunc func(conn gnet.Conn)

// ConnContext is the additional attributes to set to a gnet.Conn
type ConnContext struct {
	CreatedAt time.Time // the created time of the connection
	Endpoint  string    // the address of the remote endpoint
}

// EndpointRestrictedConnPool is the interface of a simple endpoint restricted connection connPool
// the connection's remote address must be in an endpoint list, if not, it will be closed and can
// not be used anymore, it is useful for holding the connections to a service whose endpoints can
// be changed at runtime.
// Best practice:
// gnet is a high-performance networking package, the best way to use this pool is:
//  1. call Get() to get a gnet.Conn;
//  2. use the conn to read/write for a duration, 1m, for example, and then put the conn back to the pool and get a new one for load balancing, avoid putting/getting frequently;
//  3. do not switch(put and get) to a new conn in the callback of gnet.Conn.AsyncWrite(buf []byte, callback AsyncCallback) or gnet.Conn.AsyncWritev(bs [][]byte, callback AsyncCallback), it may be blocked;
//  4. if you use TCP conn and can not update endpoints by service discovery directly, for example, your endpoints are behind at the back of a LB, it is better to set a max lifetime
//     for your pool, so that you can restart your endpoints(RS) without data lost by:
//     1). set the weight of your endpoint(RS) to 0, so that no new connection incoming;
//     2). wait for the existing connections to close by lifetime timeout;
//     3). restart your endpoint.
type EndpointRestrictedConnPool interface {
	// Get gets a connection, it's concurrency-safe, but you can not call it in the callback of gnet.Conn.AsyncWrite() or gnet.Conn.AsyncWritev().
	Get() (gnet.Conn, error)
	// Put puts a connection back to the pool, if err is not nil, the connection will be closed by the pool, it's concurrency-safe,
	// but you can not call it in the callback of gnet.Conn.AsyncWrite() or gnet.Conn.AsyncWritev().
	Put(conn gnet.Conn, err error)
	// UpdateEndpoints updates the endpoints the pool to dial to, it's concurrency-safe.
	UpdateEndpoints(all, add, del []string)
	// NumPooled returns the connection number in the pool, not the number of all the connection that the pool created, it's concurrency-safe.
	NumPooled() int
	// OnConnClosed used to notify that a connection is closed, the connection will be removed from the pool, if err is not nil, the remote endpoint will mark as unavailable, it's concurrency-safe.
	OnConnClosed(conn gnet.Conn, err error)
	// Close closes the pool
	Close()
}

type poolCmdKind int

const (
	poolCmdGet poolCmdKind = iota
	poolCmdPut
	poolCmdUpdateEndpoints
	poolCmdConnClosed
	poolCmdDialResult
	poolCmdNumPooled
	poolCmdClose
)

type poolCmd struct {
	kind poolCmdKind

	conn gnet.Conn
	err  error
	addr string

	all []string
	add []string
	del []string

	getReply chan getResult
	intReply chan int
	done     chan struct{}
}

type getResult struct {
	conn gnet.Conn
	err  error
}

// NewConnPool news a EndpointRestrictedConnPool
func NewConnPool(initEndpoints []string, connsPerEndpoint, size int,
	dialer Dialer, log logger.Logger, maxConnLifetime time.Duration,
	heartbeatFunc HeartbeatFunc) (EndpointRestrictedConnPool, error) {
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

	endpoints := make([]string, 0, len(initEndpoints))
	endpoints = append(endpoints, initEndpoints...)
	endpointMap := make(map[string]struct{}, len(endpoints))
	for _, e := range endpoints {
		endpointMap[e] = struct{}{}
	}

	pool := &connPool{
		cmdCh:            make(chan poolCmd, defaultCmdChanSize),
		closeDone:        make(chan struct{}),
		size:             size,
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
		maxConnLifetime:    maxConnLifetime,
		idleConns:          make([]gnet.Conn, 0, size),
		endpoints:          endpoints,
		endpointMap:        endpointMap,
		unavailable:        make(map[string]time.Time),
		retryCounts:        make(map[string]int),
		endpointConnCounts: make(map[string]int),
		pendingDials:       make(map[string]int),
		countedConns:       make(map[gnet.Conn]string),
		heartbeatFunc:      heartbeatFunc,
	}

	if err := pool.initConns(requiredConnNum); err != nil {
		pool.closeIdleConns()
		return nil, err
	}

	go pool.innerWork()
	return pool, nil
}

type connPool struct {
	cmdCh     chan poolCmd
	closeDone chan struct{}
	closeOnce sync.Once
	closed    atomic.Bool
	dialWG    sync.WaitGroup
	enqueueMu sync.Mutex
	enqueueWG sync.WaitGroup

	size               int
	index              uint64
	connsPerEndpoint   int
	requiredConnNum    int
	dialer             Dialer
	log                logger.Logger
	backoff            util.ExponentialBackoff
	maxConnLifetime    time.Duration
	idleConns          []gnet.Conn
	endpoints          []string
	endpointMap        map[string]struct{}
	unavailable        map[string]time.Time
	retryCounts        map[string]int
	endpointConnCounts map[string]int
	pendingDials       map[string]int
	countedConns       map[gnet.Conn]string
	heartbeatFunc      HeartbeatFunc
}

func (p *connPool) Get() (gnet.Conn, error) {
	p.log.Debug("Get()")
	if p.closed.Load() {
		return nil, ErrPoolClosed
	}

	reply := make(chan getResult, 1)
	cmd := poolCmd{kind: poolCmdGet, getReply: reply}
	select {
	case p.cmdCh <- cmd:
	case <-p.closeDone:
		return nil, ErrPoolClosed
	}

	select {
	case result := <-reply:
		return result.conn, result.err
	case <-p.closeDone:
		return nil, ErrPoolClosed
	}
}

func (p *connPool) Put(conn gnet.Conn, err error) {
	if conn == nil {
		return
	}

	cmd := poolCmd{kind: poolCmdPut, conn: conn, err: err}
	if p.closed.Load() {
		CloseConn(conn, 0)
		return
	}

	p.enqueue(cmd, func() { CloseConn(conn, defaultConnCloseDelay) })
}

func (p *connPool) UpdateEndpoints(all, add, del []string) {
	if len(all) == 0 || p.closed.Load() {
		return
	}

	cmd := poolCmd{
		kind: poolCmdUpdateEndpoints,
		all:  append([]string(nil), all...),
		add:  append([]string(nil), add...),
		del:  append([]string(nil), del...),
	}
	select {
	case p.cmdCh <- cmd:
	case <-p.closeDone:
	}
}

func (p *connPool) NumPooled() int {
	if p.closed.Load() {
		return 0
	}

	reply := make(chan int, 1)
	cmd := poolCmd{kind: poolCmdNumPooled, intReply: reply}
	select {
	case p.cmdCh <- cmd:
	case <-p.closeDone:
		return 0
	}

	select {
	case n := <-reply:
		return n
	case <-p.closeDone:
		return 0
	}
}

// OnConnClosed handles conn closed event, call it when conn is closed actively by the server
func (p *connPool) OnConnClosed(conn gnet.Conn, err error) {
	if conn == nil || p.closed.Load() {
		return
	}

	// Capture the remote address here, while still running synchronously inside
	// gnet's OnClose callback. As soon as OnClose returns, gnet's event loop
	// calls (*conn).release(), which nils out the conn's ctx/remoteAddr fields.
	// Reading them later on the pool goroutine would be a data race, so we pass
	// the address through the command instead of re-deriving it from the conn.
	cmd := poolCmd{kind: poolCmdConnClosed, conn: conn, err: err, addr: GetRemoteAddr(conn)}
	p.enqueue(cmd, nil)
}

// Close 关闭连接池，释放资源
func (p *connPool) Close() {
	p.closeOnce.Do(func() {
		// Stop admitting new enqueue operations, then wait for every operation
		// admitted before closing to finish. Holding the same mutex around the
		// closed transition and enqueueWG.Add prevents Add from racing with Wait.
		p.enqueueMu.Lock()
		p.closed.Store(true)
		p.enqueueMu.Unlock()
		p.enqueueWG.Wait()

		done := make(chan struct{})
		cmd := poolCmd{kind: poolCmdClose, done: done}
		select {
		case p.cmdCh <- cmd:
			// Wait for the close command to be processed (which drains
			// cmdCh and closes idle conns). closeDone is closed before
			// done, so do not race against it here.
			<-done
		case <-p.closeDone:
		}

		// Wait for the goroutines spawned by startDial to finish. This
		// matters because the caller may stop the underlying gnet client right
		// after Close returns: a dial still in flight would then block forever
		// inside gnet, waiting to be registered with an event-loop that no
		// longer exists. The wait is bounded so that a Dialer stuck in connect
		// cannot block Close indefinitely.
		if !waitWithTimeout(&p.dialWG, defaultCloseDialWait) {
			p.log.Warn("timed out waiting for in-flight dials during close")
		}
	})
}

// waitWithTimeout waits for wg, giving up after timeout.
func waitWithTimeout(wg *sync.WaitGroup, timeout time.Duration) bool {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}

func (p *connPool) enqueue(cmd poolCmd, onDrop func()) {
	p.enqueueMu.Lock()
	if p.closed.Load() {
		p.enqueueMu.Unlock()
		if onDrop != nil {
			onDrop()
		}
		return
	}
	p.enqueueWG.Add(1)
	p.enqueueMu.Unlock()

	select {
	case p.cmdCh <- cmd:
		p.enqueueWG.Done()
		return
	default:
	}

	go func() {
		defer p.enqueueWG.Done()

		select {
		case p.cmdCh <- cmd:
		case <-p.closeDone:
			if onDrop != nil {
				onDrop()
			}
		}
	}()
}

func (p *connPool) innerWork() {
	defer func() {
		if rec := recover(); rec != nil {
			p.log.Error("panic in connection pool:", rec)
			p.log.Error(string(debug.Stack()))
		}
		// Best-effort: ensure closeDone is closed even if we exit via panic.
		// The normal close path closes it before draining cmdCh.
		select {
		case <-p.closeDone:
		default:
			close(p.closeDone)
		}
	}()

	recoverTicker := time.NewTicker(10 * time.Second)
	defer recoverTicker.Stop()
	dumpTicker := time.NewTicker(10 * time.Second)
	defer dumpTicker.Stop()
	reBalanceTicker := time.NewTicker(defaultConnCloseDelay + 30*time.Second)
	defer reBalanceTicker.Stop()

	var heartbeatChan <-chan time.Time
	if p.heartbeatFunc != nil {
		heartbeatTicker := time.NewTicker(1 * time.Minute)
		defer heartbeatTicker.Stop()
		heartbeatChan = heartbeatTicker.C
	}

	var cleanExpiredConnChan <-chan time.Time
	if p.maxConnLifetime > 0 {
		cleanExpiredConnTicker := time.NewTicker(1 * time.Minute)
		defer cleanExpiredConnTicker.Stop()
		cleanExpiredConnChan = cleanExpiredConnTicker.C
	}

	for {
		select {
		case cmd := <-p.cmdCh:
			if p.handleCmd(cmd) {
				return
			}
		case <-recoverTicker.C:
			if p.recover() {
				p.rebalance()
			}
		case <-dumpTicker.C:
			p.dump()
		case <-reBalanceTicker.C:
			p.rebalance()
		case <-heartbeatChan:
			p.sendHeartbeat()
		case <-cleanExpiredConnChan:
			p.cleanExpiredConns()
		}
	}
}

func (p *connPool) handleCmd(cmd poolCmd) bool {
	switch cmd.kind {
	case poolCmdGet:
		p.handleGet(cmd.getReply)
	case poolCmdPut:
		p.putConn(cmd.conn, cmd.err, false)
	case poolCmdUpdateEndpoints:
		p.handleUpdateEndpoints(cmd.all, cmd.add, cmd.del)
	case poolCmdConnClosed:
		p.handleConnClosed(cmd.conn, cmd.addr, cmd.err)
	case poolCmdDialResult:
		p.handleDialResult(cmd.addr, cmd.conn, cmd.err, cmd.getReply)
	case poolCmdNumPooled:
		cmd.intReply <- len(p.idleConns)
	case poolCmdClose:
		// Close closeDone first so any in-flight dial goroutines hit the
		// closeDone branch instead of pushing new dialResult cmds into cmdCh.
		// Then drain whatever already made it into cmdCh to avoid leaking
		// conns or hanging callers.
		close(p.closeDone)
		p.closeIdleConns()
		p.drainCmdChOnClose()
		close(cmd.done)
		return true
	}
	return false
}

// drainCmdChOnClose drains commands left in cmdCh after Close is being
// processed. closeDone has already been closed by the caller, so producers
// will not enqueue new dial results past this point.
//
// It must close any conn carried by Put/DialResult to avoid leaks, and reply
// to outstanding Get/NumPooled callers with ErrPoolClosed.
func (p *connPool) drainCmdChOnClose() {
	for {
		select {
		case cmd := <-p.cmdCh:
			switch cmd.kind {
			case poolCmdGet:
				if cmd.getReply != nil {
					cmd.getReply <- getResult{err: ErrPoolClosed}
				}
			case poolCmdPut:
				if cmd.conn != nil {
					CloseConn(cmd.conn, 0)
				}
			case poolCmdConnClosed:
				// nothing to release; connection is already closed by gnet.
			case poolCmdDialResult:
				if cmd.conn != nil {
					CloseConn(cmd.conn, 0)
				}
				if cmd.getReply != nil {
					cmd.getReply <- getResult{err: ErrPoolClosed}
				}
			case poolCmdNumPooled:
				if cmd.intReply != nil {
					cmd.intReply <- 0
				}
			case poolCmdUpdateEndpoints:
				// drop silently; pool is shutting down.
			case poolCmdClose:
				if cmd.done != nil {
					close(cmd.done)
				}
			}
		default:
			return
		}
	}
}

func (p *connPool) handleGet(reply chan getResult) {
	if p.closed.Load() {
		reply <- getResult{err: ErrPoolClosed}
		return
	}

	for len(p.idleConns) > 0 {
		conn := p.popIdleConn()
		addr := GetRemoteAddr(conn)
		if p.isConnUsable(conn, addr) {
			reply <- getResult{conn: conn}
			return
		}
		p.closeCountedConn(conn, addr, defaultConnCloseDelay)
	}

	addr, err := p.getEndpoint()
	if err != nil {
		reply <- getResult{err: err}
		return
	}
	p.startDial(addr, reply)
}

func (p *connPool) handleDialResult(addr string, conn gnet.Conn, err error, reply chan getResult) {
	if p.pendingDials[addr] > 1 {
		p.pendingDials[addr]--
	} else {
		delete(p.pendingDials, addr)
	}

	if p.closed.Load() {
		if conn != nil {
			CloseConn(conn, 0)
		}
		if reply != nil {
			reply <- getResult{err: ErrPoolClosed}
		}
		return
	}

	if err != nil {
		p.markUnavailable(addr)
		p.retryCounts[addr]++
		if reply != nil {
			reply <- getResult{err: err}
		}
		return
	}

	if conn == nil {
		if reply != nil {
			reply <- getResult{err: errors.New("dial returned nil connection")}
		}
		return
	}

	connAddr := GetRemoteAddr(conn)
	if connAddr == "" {
		connAddr = addr
	}
	if _, ok := p.endpointMap[connAddr]; !ok {
		CloseConn(conn, 0)
		if reply != nil {
			p.retryGetFromCurrentEndpoints(reply)
		}
		return
	}

	delete(p.unavailable, connAddr)
	delete(p.retryCounts, connAddr)
	p.incEndpointConnCount(conn, connAddr)
	if reply != nil {
		reply <- getResult{conn: conn}
		return
	}
	p.storeIdleConn(conn, connAddr)
}

// handleConnClosed removes a closed conn from the pool. addr is captured in
// OnConnClosed before gnet releases the conn; it must not be re-derived from the
// conn here (see OnConnClosed). decEndpointConnCount falls back to the dial-time
// address keyed by the conn pointer when addr is empty.
func (p *connPool) handleConnClosed(conn gnet.Conn, addr string, err error) {
	if addr != "" && err != nil {
		p.markUnavailable(addr)
	}
	p.decEndpointConnCount(conn, addr)
	p.removeIdleConn(conn)
}

func (p *connPool) handleUpdateEndpoints(all, add, del []string) {
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

	p.endpoints = append(p.endpoints[:0], all...)
	newEndpointMap := make(map[string]struct{}, len(all))
	for _, ep := range all {
		newEndpointMap[ep] = struct{}{}
	}
	p.endpointMap = newEndpointMap

	for ep := range p.unavailable {
		if _, ok := p.endpointMap[ep]; !ok {
			delete(p.unavailable, ep)
			delete(p.retryCounts, ep)
		}
	}

	if len(del) > 0 {
		p.log.Debug("delete old connections...")
	}
	p.removeDeletedEndpointConns()

	if len(add) > 0 || len(del) > 0 {
		p.rebalance()
	}
}

func (p *connPool) initConns(count int) error {
	// Partial success is acceptable: as long as at least one connection is
	// established, the pool starts. Endpoints that failed to dial are marked
	// unavailable inside dialNewConn and will be retried by the background
	// recover()/rebalance() loops. Returning an error only when every dial
	// fails avoids spurious startup failures from transient network issues
	// (DNS jitter, a single bad backend) while still surfacing a fully broken
	// upstream to the caller.
	conns := make([]gnet.Conn, 0, count)
	var lastErr error
	for i := 0; i < count; i++ {
		conn, err := p.newConn()
		if err != nil {
			lastErr = err
			continue
		}
		conns = append(conns, conn)
	}

	if len(conns) == 0 {
		return lastErr
	}

	if len(conns) < count {
		p.log.Warn("initConns partial success, required: ", count,
			", succeeded: ", len(conns), ", lastErr: ", lastErr)
	}

	for _, conn := range conns {
		p.putConn(conn, nil, true)
	}
	return nil
}

func (p *connPool) getEndpoint() (string, error) {
	p.log.Debug("getEndpoint()")
	if len(p.endpoints) == 0 {
		return "", ErrNoAvailableEndpoint
	}

	for i := 0; i < len(p.endpoints); i++ {
		index := p.index
		p.index++
		ep := p.endpoints[index%uint64(len(p.endpoints))]
		if _, unavailable := p.unavailable[ep]; unavailable {
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
	conn, err := p.dialer.Dial(ep, ConnContext{CreatedAt: time.Now(), Endpoint: ep})
	if err != nil {
		p.markUnavailable(ep)
		return nil, err
	}
	return conn, nil
}

func (p *connPool) startDial(addr string, reply chan getResult) {
	if addr == "" {
		if reply != nil {
			reply <- getResult{err: errors.New("addr is empty")}
		}
		return
	}
	if p.closed.Load() {
		if reply != nil {
			reply <- getResult{err: ErrPoolClosed}
		}
		return
	}
	p.pendingDials[addr]++
	p.dialWG.Add(1)
	go func() {
		defer p.dialWG.Done()

		if p.closed.Load() {
			if reply != nil {
				reply <- getResult{err: ErrPoolClosed}
			}
			return
		}

		conn, err := p.dialer.Dial(addr, ConnContext{CreatedAt: time.Now(), Endpoint: addr})
		if p.closed.Load() {
			if conn != nil {
				CloseConn(conn, 0)
			}
			if reply != nil {
				reply <- getResult{err: ErrPoolClosed}
			}
			return
		}

		cmd := poolCmd{kind: poolCmdDialResult, addr: addr, conn: conn, err: err, getReply: reply}
		select {
		case p.cmdCh <- cmd:
		case <-p.closeDone:
			if conn != nil {
				CloseConn(conn, 0)
			}
			if reply != nil {
				reply <- getResult{err: ErrPoolClosed}
			}
		}
	}()
}

func (p *connPool) retryGetFromCurrentEndpoints(reply chan getResult) {
	if len(p.endpoints) == 0 || p.getAvailableEndpointCount() <= 0 {
		reply <- getResult{err: ErrNoAvailableEndpoint}
		return
	}
	p.handleGet(reply)
}

func (p *connPool) putConn(conn gnet.Conn, err error, isNewConn bool) {
	if conn == nil {
		return
	}

	addr := GetRemoteAddr(conn)
	if addr == "" {
		p.log.Error("remote address is nil, it is closed, stop putting")
		CloseConn(conn, defaultConnCloseDelay)
		return
	}

	if _, ok := p.endpointMap[addr]; !ok {
		p.log.Warn("endpoint deleted, close its connection, addr:", addr)
		p.closeCountedConn(conn, addr, defaultConnCloseDelay)
		return
	}

	if err != nil {
		p.log.Warn("connection error, close it, addr:", addr, ", err:", err)
		p.markUnavailable(addr)
		p.closeCountedConn(conn, addr, defaultConnCloseDelay)
		return
	}

	if p.expired(conn) {
		p.log.Debug("connection expired, close it, addr:", addr, ", err:", err)
		p.closeCountedConn(conn, addr, defaultConnCloseDelay)
		p.startDial(addr, nil)
		return
	}

	if isNewConn {
		p.incEndpointConnCount(conn, addr)
	}
	p.storeIdleConn(conn, addr)
}

func (p *connPool) storeIdleConn(conn gnet.Conn, addr string) {
	if len(p.idleConns) >= p.size {
		p.log.Warn("connection pool is full, closing connection, addr: ", addr)
		p.closeCountedConn(conn, addr, defaultConnCloseDelay)
		return
	}
	p.idleConns = append(p.idleConns, conn)
}

func (p *connPool) isConnUsable(conn gnet.Conn, addr string) bool {
	if conn == nil || addr == "" {
		return false
	}
	if _, ok := p.endpointMap[addr]; !ok {
		return false
	}
	return !p.expired(conn)
}

func (p *connPool) expired(conn gnet.Conn) bool {
	if conn == nil || p.maxConnLifetime <= 0 {
		return false
	}

	ctx := conn.SafeContext()
	if ctx == nil {
		return false
	}

	connCtx, ok := ctx.(ConnContext)
	if !ok {
		return false
	}
	return connCtx.CreatedAt.Add(p.maxConnLifetime).Before(time.Now())
}

func (p *connPool) incEndpointConnCount(conn gnet.Conn, addr string) {
	if conn == nil || addr == "" {
		return
	}
	if _, ok := p.countedConns[conn]; ok {
		return
	}
	p.countedConns[conn] = addr
	p.endpointConnCounts[addr]++
}

func (p *connPool) decEndpointConnCount(conn gnet.Conn, addr string) {
	if conn == nil {
		return
	}
	countedAddr, ok := p.countedConns[conn]
	if !ok {
		return
	}
	delete(p.countedConns, conn)
	if addr == "" {
		addr = countedAddr
	}
	count := p.endpointConnCounts[addr]
	if count <= 1 {
		delete(p.endpointConnCounts, addr)
		return
	}
	p.endpointConnCounts[addr] = count - 1
}

func (p *connPool) closeCountedConn(conn gnet.Conn, addr string, after time.Duration) {
	// Decrement the count immediately but delay the physical close. This way the
	// next rebalance round sees an accurate curConnCount (excluding these
	// "dying" conns), while existing users of the conn can finish their work
	// during the close-delay window. Subsequent OnConnClosed/Put events for the
	// same conn become no-ops because countedConns[conn] is already gone, so
	// the count is not double-decremented.
	p.decEndpointConnCount(conn, addr)
	CloseConn(conn, after)
}

func (p *connPool) popIdleConn() gnet.Conn {
	conn := p.idleConns[0]
	copy(p.idleConns, p.idleConns[1:])
	p.idleConns[len(p.idleConns)-1] = nil
	p.idleConns = p.idleConns[:len(p.idleConns)-1]
	return conn
}

func (p *connPool) closeIdleConns() {
	for _, conn := range p.idleConns {
		addr := GetRemoteAddr(conn)
		p.closeCountedConn(conn, addr, 0)
	}
	p.idleConns = nil
}

func (p *connPool) removeIdleConn(target gnet.Conn) {
	if target == nil || len(p.idleConns) == 0 {
		return
	}
	for i, conn := range p.idleConns {
		if conn == target {
			p.idleConns = append(p.idleConns[:i], p.idleConns[i+1:]...)
			return
		}
	}
}

func (p *connPool) removeDeletedEndpointConns() {
	left := make([]gnet.Conn, 0, len(p.idleConns))
	for _, conn := range p.idleConns {
		addr := GetRemoteAddr(conn)
		if _, ok := p.endpointMap[addr]; !ok {
			p.log.Warn("endpoint deleted, close its connection, addr:", addr)
			p.closeCountedConn(conn, addr, defaultConnCloseDelay)
			continue
		}
		left = append(left, conn)
	}
	p.idleConns = left
}

// CloseConn closes a connection after a duration of time
func CloseConn(conn gnet.Conn, after time.Duration) {
	if conn == nil {
		return
	}
	if after <= 0 {
		_ = conn.Close()
		return
	}
	time.AfterFunc(after, func() {
		_ = conn.Close()
	})
}

func (p *connPool) markUnavailable(ep string) {
	if ep == "" || p.getEndpointCount() <= 1 {
		return
	}
	if _, ok := p.endpointMap[ep]; !ok {
		return
	}
	p.log.Debug("endpoint cannot be connected, marking as unavailable, addr: ", ep)
	p.unavailable[ep] = time.Now()
	if _, ok := p.retryCounts[ep]; !ok {
		p.retryCounts[ep] = 0
	}
}

// GetRemoteAddr returns the remote address string of a gnet.Conn for logging.
// It reads the dial-time Endpoint stored in ConnContext via SafeContext().
// Returns "" if no address can be derived.
func GetRemoteAddr(conn gnet.Conn) string {
	if conn == nil {
		return ""
	}

	ctx := conn.SafeContext()
	if ctx == nil {
		return ""
	}

	connCtx, ok := ctx.(ConnContext)
	if !ok {
		return ""
	}
	return connCtx.Endpoint
}

func (p *connPool) sendHeartbeat() {
	if p.heartbeatFunc == nil {
		return
	}

	for _, conn := range p.idleConns {
		if conn == nil {
			continue
		}

		func(conn gnet.Conn) {
			defer func() {
				if rec := recover(); rec != nil {
					p.log.Error("panic when send heartbeat:", rec)
					p.log.Error(string(debug.Stack()))
				}
			}()
			p.heartbeatFunc(conn)
		}(conn)
	}
}

func (p *connPool) cleanExpiredConns() {
	p.log.Debug("cleanExpiredConns()")
	left := make([]gnet.Conn, 0, len(p.idleConns))
	for _, conn := range p.idleConns {
		if p.expired(conn) {
			addr := GetRemoteAddr(conn)
			p.log.Debug("connection expired, close it, addr:", addr, ", err:", nil)
			p.closeCountedConn(conn, addr, defaultConnCloseDelay)
			p.startDial(addr, nil)
			continue
		}
		left = append(left, conn)
	}
	p.idleConns = left
}

func (p *connPool) dump() {
	p.log.Debug("all endpoints:")
	for _, ep := range p.endpoints {
		p.log.Debug(ep)
	}

	dump := false
	for ep := range p.unavailable {
		if !dump {
			p.log.Debug("unavailable endpoints:")
			dump = true
		}
		p.log.Debug(ep)
	}

	p.log.Debug("opened connections:")
	for ep, count := range p.endpointConnCounts {
		p.log.Debug("endpoint: ", ep, ", conns: ", count)
	}
}

func (p *connPool) recover() bool {
	recovered := false
	for ep, lastUnavailable := range p.unavailable {
		if p.pendingDials[ep] > 0 {
			continue
		}
		retries := p.retryCounts[ep]
		if time.Since(lastUnavailable) > p.backoff.Next(retries) {
			p.startDial(ep, nil)
			recovered = true
		}
	}
	if recovered {
		p.log.Debug("recover triggered")
	}
	return recovered
}

func (p *connPool) getConnCount() int {
	totalConnCount := 0
	for _, count := range p.endpointConnCounts {
		totalConnCount += count
	}
	return totalConnCount
}

func (p *connPool) getEndpointCount() int {
	return len(p.endpoints)
}

func (p *connPool) getAvailableEndpointCount() int {
	unavailableEndpointNum := 0
	for ep := range p.unavailable {
		if _, ok := p.endpointMap[ep]; ok {
			unavailableEndpointNum++
		}
	}
	return len(p.endpoints) - unavailableEndpointNum
}

func (p *connPool) getExpectedConnPerEndpoint() int {
	// 当前连接数，我们的连接是延迟关闭了，获取的连接数可能包含这些关闭中的无效的连接，且一般会大于等于初始连接数
	curConnCount := p.getConnCount()
	p.log.Debug("curConnCount: ", curConnCount)
	if curConnCount <= 0 {
		return 1
	}

	// 初始连接数
	initConnCount := float64(p.requiredConnNum)
	p.log.Debug("initConnCount: ", initConnCount)

	// 平均连接数，因为计算当前连接数时，包括了延迟关闭但没完全关闭的连接，有可能不准确，用初始连接数和当前连接数的平均值当作参考
	avgConnCount := (curConnCount + p.requiredConnNum) >> 1
	p.log.Debug("avgConnCount: ", avgConnCount)
	if avgConnCount <= 0 {
		return 1
	}

	// 当前可用节点数
	availableEndpointCount := p.getAvailableEndpointCount()
	p.log.Debug("availableEndpointCount: ", availableEndpointCount)
	if availableEndpointCount <= 0 {
		return 1
	}

	// 当前连接数/节点数，算一个新的每节点连接数，向下取整，避免过大
	estimatedVal := math.Floor(float64(curConnCount) / float64(availableEndpointCount))
	p.log.Debug("conns per endpoint by current conn count: ", estimatedVal)

	// 平均连接数/节点数，参考值
	averageVal := math.Floor(float64(avgConnCount) / float64(availableEndpointCount))
	p.log.Debug("conns per endpoint by average conn count: ", averageVal)

	// 初始每节点连接数
	initialVal := float64(p.connsPerEndpoint)
	p.log.Debug("conns per endpoint of initialization: ", initialVal)

	result := averageVal //nolint:ineffassign
	if estimatedVal < initialVal {
		// 评估值比初始值小，说明加节点了，新节点需要增加连接，原有节点需要减少连接，小步收敛，取评估值和平均值较小的结果
		result = math.Min(estimatedVal, averageVal)
	} else {
		// 评估值比初始值大，说明删节点了，被删节点需要删连接，剩余节点需要增加连接，小步收敛，取初始值和平均值较小的结果
		result = math.Max(initialVal, averageVal)
	}

	// 保底1个
	result = math.Max(1, result)
	p.log.Debug("expectedConnPerEndpoint: ", result)
	return int(result)
}

func (p *connPool) rebalance() {
	expectedConnPerEndpoint := p.getExpectedConnPerEndpoint()
	if expectedConnPerEndpoint <= 0 {
		return
	}

	rebalanced := false
	// Iterating endpointConnCounts while removeEndpointConn -> closeCountedConn
	// may delete or decrement entries in this same map. Per the Go spec, deleting
	// the current key during range is well-defined (the deleted key won't be
	// revisited); we only ever touch the current key, never insert new ones,
	// so iteration is safe here.
	for addr, currentCount := range p.endpointConnCounts {
		if currentCount < expectedConnPerEndpoint {
			if _, ok := p.endpointMap[addr]; !ok {
				continue
			}
			if _, unavailable := p.unavailable[addr]; unavailable {
				continue
			}
			need := expectedConnPerEndpoint - currentCount - p.pendingDials[addr]
			for i := 0; i < need; i++ {
				p.log.Debug("adding connection for addr: ", addr)
				p.startDial(addr, nil)
				rebalanced = true
			}
		} else if currentCount > expectedConnPerEndpoint {
			rebalanced = true
			p.removeEndpointConn(addr, currentCount-expectedConnPerEndpoint)
		}
	}

	for addr := range p.endpointMap {
		if _, ok := p.endpointConnCounts[addr]; ok {
			continue
		}
		if _, unavailable := p.unavailable[addr]; unavailable {
			continue
		}
		need := expectedConnPerEndpoint - p.pendingDials[addr]
		for i := 0; i < need; i++ {
			p.log.Debug("adding connection for addr: ", addr)
			p.startDial(addr, nil)
			rebalanced = true
		}
	}

	if rebalanced {
		p.log.Debug("rebalance triggered")
	}
}

func (p *connPool) removeEndpointConn(addr string, count int) {
	left := make([]gnet.Conn, 0, len(p.idleConns))
	removed := 0
	for _, conn := range p.idleConns {
		connAddr := GetRemoteAddr(conn)
		if connAddr == addr && removed < count {
			p.log.Debug("reducing connection for addr: ", addr)
			p.closeCountedConn(conn, addr, defaultConnCloseDelay)
			removed++
			continue
		}
		left = append(left, conn)
	}
	p.idleConns = left
}
