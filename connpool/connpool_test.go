package connpool

import (
	"context"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/panjf2000/gnet/v2"

	"github.com/tencent/game-log-sdk-go/logger"
)

type mockDialer struct {
	mu        sync.Mutex
	conns     []*mockConn
	failFor   map[string]error
	blockCh   chan struct{}
	startedCh chan string
}

func (d *mockDialer) Dial(addr string, ctx any) (gnet.Conn, error) {
	if d.blockCh != nil {
		if d.startedCh != nil {
			d.startedCh <- addr
		}
		<-d.blockCh
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.failFor[addr]; err != nil {
		return nil, err
	}
	conn := &mockConn{remote: mockAddr(addr), ctx: ctx}
	d.conns = append(d.conns, conn)
	return conn, nil
}

func (d *mockDialer) closedCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	closed := 0
	for _, conn := range d.conns {
		if conn.closed.Load() {
			closed++
		}
	}
	return closed
}

func (d *mockDialer) connsFor(addr string) []*mockConn {
	d.mu.Lock()
	defer d.mu.Unlock()
	var conns []*mockConn
	for _, conn := range d.conns {
		if conn.remote.String() == addr {
			conns = append(conns, conn)
		}
	}
	return conns
}

type mockAddr string

func (a mockAddr) Network() string { return "tcp" }
func (a mockAddr) String() string  { return string(a) }

type mockConn struct {
	remote mockAddr
	ctx    any
	closed atomic.Bool
}

func (c *mockConn) Read([]byte) (int, error)                    { return 0, io.EOF }
func (c *mockConn) Write([]byte) (int, error)                   { return 0, nil }
func (c *mockConn) WriteTo(io.Writer) (int64, error)            { return 0, nil }
func (c *mockConn) ReadFrom(io.Reader) (int64, error)           { return 0, nil }
func (c *mockConn) Next(int) ([]byte, error)                    { return nil, io.EOF }
func (c *mockConn) Peek(int) ([]byte, error)                    { return nil, io.EOF }
func (c *mockConn) Discard(int) (int, error)                    { return 0, nil }
func (c *mockConn) InboundBuffered() int                        { return 0 }
func (c *mockConn) SendTo([]byte, net.Addr) (int, error)        { return 0, nil }
func (c *mockConn) Writev([][]byte) (int, error)                { return 0, nil }
func (c *mockConn) Flush() error                                { return nil }
func (c *mockConn) OutboundBuffered() int                       { return 0 }
func (c *mockConn) AsyncWrite([]byte, gnet.AsyncCallback) error { return nil }
func (c *mockConn) AsyncWritev([][]byte, gnet.AsyncCallback) error {
	return nil
}
func (c *mockConn) Fd() int                                { return 0 }
func (c *mockConn) Dup() (int, error)                      { return 0, nil }
func (c *mockConn) SetReadBuffer(int) error                { return nil }
func (c *mockConn) SetWriteBuffer(int) error               { return nil }
func (c *mockConn) SetLinger(int) error                    { return nil }
func (c *mockConn) SetKeepAlivePeriod(time.Duration) error { return nil }
func (c *mockConn) SetKeepAlive(bool, time.Duration, time.Duration, int) error {
	return nil
}
func (c *mockConn) SetNoDelay(bool) error     { return nil }
func (c *mockConn) Context() any              { return c.ctx }
func (c *mockConn) EventLoop() gnet.EventLoop { return nil }
func (c *mockConn) SetContext(ctx any)        { c.ctx = ctx }
func (c *mockConn) LocalAddr() net.Addr       { return mockAddr("local") }
func (c *mockConn) RemoteAddr() net.Addr {
	if c.remote == "" {
		return nil
	}
	return c.remote
}
func (c *mockConn) Wake(gnet.AsyncCallback) error              { return nil }
func (c *mockConn) CloseWithCallback(gnet.AsyncCallback) error { return c.Close() }
func (c *mockConn) Close() error {
	c.closed.Store(true)
	return nil
}
func (c *mockConn) SetDeadline(time.Time) error      { return nil }
func (c *mockConn) SetReadDeadline(time.Time) error  { return nil }
func (c *mockConn) SetWriteDeadline(time.Time) error { return nil }

func newTestPool(t *testing.T, endpoints []string) (*connPool, *mockDialer) {
	t.Helper()
	dialer := &mockDialer{failFor: make(map[string]error)}
	pool, err := NewConnPool(endpoints, 1, 8, dialer, logger.Std(), -1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool.(*connPool), dialer
}

func waitFor(t *testing.T, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met before timeout")
}

func TestGetPut(t *testing.T) {
	pool, _ := newTestPool(t, []string{"127.0.0.1:1"})
	if got := pool.NumPooled(); got != 1 {
		t.Fatalf("NumPooled() = %d, want 1", got)
	}

	conn, err := pool.Get()
	if err != nil {
		t.Fatal(err)
	}
	if got := pool.NumPooled(); got != 0 {
		t.Fatalf("NumPooled() after Get = %d, want 0", got)
	}

	pool.Put(conn, nil)
	waitFor(t, func() bool { return pool.NumPooled() == 1 })
}

func TestIdleConnsAreFIFO(t *testing.T) {
	pool, _ := newTestPool(t, []string{"127.0.0.1:1", "127.0.0.1:2"})
	first, err := pool.Get()
	if err != nil {
		t.Fatal(err)
	}
	second, err := pool.Get()
	if err != nil {
		t.Fatal(err)
	}

	pool.Put(first, nil)
	pool.Put(second, nil)
	waitFor(t, func() bool { return pool.NumPooled() == 2 })

	got, err := pool.Get()
	if err != nil {
		t.Fatal(err)
	}
	if got != first {
		t.Fatal("Get did not return the oldest idle connection first")
	}
}

func TestUpdateEndpointsRemovesDeletedIdleConns(t *testing.T) {
	pool, _ := newTestPool(t, []string{"127.0.0.1:1", "127.0.0.1:2"})
	pool.UpdateEndpoints([]string{"127.0.0.1:2"}, nil, []string{"127.0.0.1:1"})

	waitFor(t, func() bool { return pool.NumPooled() == 1 })
}

func TestOnConnClosedRemovesIdleConn(t *testing.T) {
	pool, _ := newTestPool(t, []string{"127.0.0.1:1"})
	conn, err := pool.Get()
	if err != nil {
		t.Fatal(err)
	}
	pool.Put(conn, nil)
	waitFor(t, func() bool { return pool.NumPooled() == 1 })

	pool.OnConnClosed(conn, nil)
	waitFor(t, func() bool { return pool.NumPooled() == 0 })
}

func TestClose(t *testing.T) {
	pool, dialer := newTestPool(t, []string{"127.0.0.1:1"})
	pool.Close()

	if _, err := pool.Get(); err == nil {
		t.Fatal("Get() after Close returned nil error")
	}
	if dialer.closedCount() == 0 {
		t.Fatal("Close() did not close idle connections")
	}
}

func TestConcurrentCommands(t *testing.T) {
	pool, _ := newTestPool(t, []string{"127.0.0.1:1", "127.0.0.1:2"})
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, err := pool.Get()
			if err != nil {
				return
			}
			pool.Put(conn, nil)
		}()
	}
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pool.UpdateEndpoints([]string{"127.0.0.1:1", "127.0.0.1:2"}, nil, nil)
		}()
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("concurrent commands timed out")
	}

	if _, err := pool.Get(); err != nil {
		t.Fatal(err)
	}
}

func TestGetAfterClose(t *testing.T) {
	pool, _ := newTestPool(t, []string{"127.0.0.1:1"})
	pool.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		_, _ = pool.Get()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("Get() after Close did not return")
	}
}

func TestCloseDuringDialClosesDialResult(t *testing.T) {
	dialer := &mockDialer{failFor: make(map[string]error)}
	pool, err := NewConnPool([]string{"127.0.0.1:1"}, 1, 1, dialer, logger.Std(), -1)
	if err != nil {
		t.Fatal(err)
	}
	p := pool.(*connPool)
	conn, err := p.Get()
	if err != nil {
		t.Fatal(err)
	}

	dialer.blockCh = make(chan struct{})
	dialer.startedCh = make(chan string, 1)
	getDone := make(chan error, 1)
	go func() {
		_, err := p.Get()
		getDone <- err
	}()
	select {
	case <-dialer.startedCh:
	case <-time.After(time.Second):
		t.Fatal("dial did not start")
	}

	p.Close()
	close(dialer.blockCh)
	p.Put(conn, nil)

	select {
	case err := <-getDone:
		if err == nil {
			t.Fatal("Get during Close returned nil error")
		}
	case <-time.After(time.Second):
		t.Fatal("Get during Close did not return")
	}
	waitFor(t, func() bool { return dialer.closedCount() >= 2 })
}

// TestCloseDrainsQueuedDialResults makes sure that conns carried by dialResult
// commands already queued in cmdCh when Close runs are not leaked.
func TestCloseDrainsQueuedDialResults(t *testing.T) {
	dialer := &mockDialer{failFor: make(map[string]error)}
	pool, err := NewConnPool([]string{"127.0.0.1:1"}, 1, 1, dialer, logger.Std(), -1)
	if err != nil {
		t.Fatal(err)
	}
	p := pool.(*connPool)

	// Drain the connection created by initConns so the pool is empty and
	// idle conn count does not interfere with the leak assertion.
	initialConn, err := p.Get()
	if err != nil {
		t.Fatal(err)
	}
	_ = initialConn

	queuedConn := &mockConn{remote: mockAddr("127.0.0.1:1"), ctx: ConnContext{Endpoint: "127.0.0.1:1"}}
	dialer.mu.Lock()
	dialer.conns = append(dialer.conns, queuedConn)
	dialer.mu.Unlock()

	reply := make(chan getResult, 1)
	p.cmdCh <- poolCmd{
		kind:     poolCmdDialResult,
		addr:     "127.0.0.1:1",
		conn:     queuedConn,
		getReply: reply,
	}

	p.Close()

	select {
	case r := <-reply:
		if r.err == nil {
			t.Fatal("queued Get reply during Close returned nil error")
		}
	case <-time.After(time.Second):
		t.Fatal("queued Get reply was not delivered after Close")
	}

	waitFor(t, func() bool { return queuedConn.closed.Load() })
}

func TestOnConnClosedDoesNotDoubleDecrement(t *testing.T) {
	pool, _ := newTestPool(t, []string{"127.0.0.1:1", "127.0.0.1:2"})
	conn, err := pool.Get()
	if err != nil {
		t.Fatal(err)
	}
	pool.Put(conn, errConnForTest{})
	pool.OnConnClosed(conn, errConnForTest{})

	waitFor(t, func() bool {
		_, err := pool.Get()
		return err == nil
	})
}

func TestOnConnClosedIgnoresUncountedConn(t *testing.T) {
	pool, _ := newTestPool(t, []string{"127.0.0.1:1"})
	uncounted := &mockConn{remote: "", ctx: ConnContext{Endpoint: "127.0.0.1:1"}}
	pool.OnConnClosed(uncounted, nil)
	waitFor(t, func() bool { return pool.NumPooled() == 1 })
}

func TestGetRetriesWhenDialEndpointDeleted(t *testing.T) {
	pool, _ := newTestPool(t, []string{"127.0.0.1:1", "127.0.0.1:2"})
	conn1, err := pool.Get()
	if err != nil {
		t.Fatal(err)
	}
	conn2, err := pool.Get()
	if err != nil {
		t.Fatal(err)
	}
	pool.UpdateEndpoints([]string{"127.0.0.1:2"}, nil, []string{"127.0.0.1:1"})
	pool.Put(conn1, nil)
	pool.Put(conn2, nil)

	waitFor(t, func() bool {
		conn, err := pool.Get()
		if err != nil {
			return false
		}
		defer pool.Put(conn, nil)
		return getRemoteAddr(conn) == "127.0.0.1:2"
	})
}

type errConnForTest struct{}

func (errConnForTest) Error() string { return "connection error" }

func TestNewConnPoolPartialDialFailureSucceeds(t *testing.T) {
	dialer := &mockDialer{failFor: map[string]error{
		"127.0.0.1:1": errConnForTest{},
	}}
	pool, err := NewConnPool([]string{"127.0.0.1:1", "127.0.0.1:2"}, 1, 8, dialer, logger.Std(), -1)
	if err != nil {
		t.Fatalf("NewConnPool returned err with one bad endpoint: %v", err)
	}
	t.Cleanup(pool.Close)

	// At least one connection should have been established (against :2).
	if got := pool.NumPooled(); got < 1 {
		t.Fatalf("NumPooled() = %d, want >= 1", got)
	}
	conn, err := pool.Get()
	if err != nil {
		t.Fatal(err)
	}
	if addr := getRemoteAddr(conn); addr != "127.0.0.1:2" {
		t.Fatalf("Get() returned conn for %q, want 127.0.0.1:2", addr)
	}
	pool.Put(conn, nil)
}

func TestNewConnPoolAllDialFailuresReturnError(t *testing.T) {
	dialer := &mockDialer{failFor: map[string]error{
		"127.0.0.1:1": errConnForTest{},
		"127.0.0.1:2": errConnForTest{},
	}}
	pool, err := NewConnPool([]string{"127.0.0.1:1", "127.0.0.1:2"}, 1, 8, dialer, logger.Std(), -1)
	if err == nil {
		pool.Close()
		t.Fatal("NewConnPool succeeded when every endpoint failed to dial")
	}
}
