package tglog

import (
	"context"
	"errors"
	"math/rand"
	"runtime/debug"
	"strconv"
	"time"

	"github.com/panjf2000/gnet/v2"
	v3 "github.com/tencent/game-log-sdk-proto/pbgo"

	"github.com/tencent/game-log-sdk-go/syncx"
	"github.com/tencent/game-log-sdk-go/util"

	"github.com/tencent/game-log-sdk-go/bufferpool"
	"github.com/tencent/game-log-sdk-go/logger"

	"go.uber.org/atomic"
)

const (
	defaultHeartbeatInterval = 60
	defaultMapCleanInterval  = 20
	defaultMapCleanThreshold = 500000
)

type workerState int32

const (
	// worker states
	stateInit = iota
	stateReady
	stateClosing
	stateClosed
)

var (
	errOK               = &errNo{code: 0, strCode: "0", message: "OK"}
	errSendTimeout      = &errNo{code: 10001, strCode: "10001", message: "message send timeout"}
	errSendFailed       = &errNo{code: 10002, strCode: "10002", message: "message send failed"} // nolint: unused
	errProducerClosed   = &errNo{code: 10003, strCode: "10003", message: "producer already been closed"}
	errSendQueueIsFull  = &errNo{code: 10004, strCode: "10004", message: "producer send queue is full"}
	errContextExpired   = &errNo{code: 10005, strCode: "10005", message: "message context expired"}
	errNewConnFailed    = &errNo{code: 10006, strCode: "10006", message: "new conn failed"}
	errConnWriteFailed  = &errNo{code: 10007, strCode: "10007", message: "conn write failed"}
	errConnReadFailed   = &errNo{code: 10008, strCode: "10008", message: "conn read failed"}
	errLogToLong        = &errNo{code: 10009, strCode: "10009", message: "input log is too long"} // nolint: unused
	errBadLog           = &errNo{code: 10010, strCode: "10010", message: "input log is invalid"}
	errServerError      = &errNo{code: 10011, strCode: "10011", message: "server error"} // nolint: unused
	errServerPanic      = &errNo{code: 10012, strCode: "10012", message: "server panic"}
	workerBusy          = &errNo{code: 10013, strCode: "10013", message: "worker is busy"}
	errNoMatchReq4Rsp   = &errNo{code: 10014, strCode: "10014", message: "no match unacknowledged request for response"}
	errConnClosedByPeer = &errNo{code: 10015, strCode: "10015", message: "conn closed by peer"}
	errUnknown          = &errNo{code: 20001, strCode: "20001", message: "unknown"}
)

type errNo struct {
	code    int
	strCode string
	message string
}

func (e *errNo) Error() string {
	return e.message
}

func (e *errNo) getCode() int { // nolint: unused
	return e.code
}

func (e *errNo) getStrCode() string {
	return e.strCode
}

func getErrorCode(err error) string {
	if err == nil {
		return errOK.getStrCode()
	}

	var t *errNo
	switch {
	case errors.As(err, &t):
		return t.getStrCode()
	default:
		return errUnknown.getStrCode()
	}
}

// 说明：
// 这里用定时器更新连接是为了减少还连接时对连接池中一个sync.Map（有锁，虽然是桶级别的锁，还是会有开销 ）的访问以提高性能，
// 其实每次发包时取连接/发包/还连接也可以，但是性能会低一点点，用定时器在服务器域名上下线CLB时更新连接会没有这么及时，但在连接
// 出错时也做了及时更新，对于TCP是可以及时做到更新的，对于UDP，因为是无连接的，则不能及时更新，考虑到运营时我们连接的是服务器的
// CLB而不是直连RS，所以是可以接受的。（如果是直连RS，对于UPD来讲，在DNS缓存刷新的这段时间，每次发包都换一个连接也无法避免会
// 把请求发往被下线的RS，更进一步的优化是通过响应超时/心跳超时来检测连接不可用，但是V1协议是没有响应的，也不好实现。）
type worker struct {
	client             *client                  // 上层client
	index              int                      // worker id
	indexStr           string                   // worker id 字符串格式
	options            *Options                 // 配置
	state              atomic.Int32             // 状态
	log                logger.Logger            // 日志
	conn               atomic.Value             // 连接
	cmdChan            chan interface{}         // 命令管道
	dataChan           chan *sendDataReq        // 数据管道
	dataSemaphore      syncx.Semaphore          // 排队控制信号量
	pendingBatches     map[string]*batchReq     // 待发送批次
	unackedBatches     map[string]*batchReq     // 待确认批次
	sendFailedBatches  chan *sendFailedBatchReq // 发送失败管道，接收batch发送失败事件
	updateConnChan     chan error               // 更新连接管道，接收更新连接通知
	retryBatches       chan *batchReq           // 重试管道，接收待重试的batch
	responseBatches    chan *batchRsp           // 响应管道
	batchTimeoutTicker *time.Ticker             // 批次超时定时器，检测批次最旧的数据是否超过指定时间，超过就算不够一批也直接发送
	sendTimeoutTicker  *time.Ticker             // 发送超时定时器，检测批次是否超过指定时间都没收到响应，是否重传
	heartbeatTicker    *time.Ticker             // 心跳定时器
	mapCleanTicker     *time.Ticker             // map清理定时器
	updateConnTicker   *time.Ticker             // 更新连接定时器，定时从连接池获取连接替换现有连接
	unackedBatchCount  int                      // map清理计数器
	metrics            *metrics                 // 指标
	bufferPool         bufferpool.BufferPool    // 缓冲池
	bytePool           bufferpool.BytePool      // 内存池
	stop               chan struct{}            // 是否停止
}

func newWorker(cli *client, index int, opts *Options) (*worker, error) {
	sendTimeout := opts.SendTimeout / 2
	if sendTimeout == 0 {
		sendTimeout = 5 * time.Second
	}

	w := &worker{
		index:              index,
		indexStr:           strconv.Itoa(index),
		client:             cli,
		options:            opts,
		cmdChan:            make(chan interface{}),
		dataChan:           make(chan *sendDataReq, opts.MaxPendingMessages),
		dataSemaphore:      syncx.NewSemaphore(int32(opts.MaxPendingMessages)),
		pendingBatches:     make(map[string]*batchReq),
		unackedBatches:     make(map[string]*batchReq),
		sendFailedBatches:  make(chan *sendFailedBatchReq, opts.MaxPendingMessages),
		updateConnChan:     make(chan error, 64),
		retryBatches:       make(chan *batchReq, opts.MaxPendingMessages),
		responseBatches:    make(chan *batchRsp, opts.MaxPendingMessages),
		batchTimeoutTicker: time.NewTicker(opts.BatchingMaxPublishDelay),
		sendTimeoutTicker:  time.NewTicker(sendTimeout),
		heartbeatTicker:    time.NewTicker(defaultHeartbeatInterval * time.Second),
		mapCleanTicker:     time.NewTicker(defaultMapCleanInterval * time.Second),
		updateConnTicker:   time.NewTicker(time.Duration(30+rand.Intn(50)) * time.Second), // 随机一点
		metrics:            cli.metrics,
		bufferPool:         opts.BufferPool,
		bytePool:           opts.BytePool,
		log:                opts.Logger,
		stop:               make(chan struct{}),
	}

	// V1协议没有响应，不需要这些定时器
	if opts.isV1 {
		w.sendTimeoutTicker.Stop()
		w.heartbeatTicker.Stop()
		w.mapCleanTicker.Stop()
	}
	// 更新为初始状态
	w.setState(stateInit)

	// 获取连接
	conn, err := cli.getConn()
	if err != nil {
		return nil, err
	}
	w.log.Debug("use conn: ", conn.RemoteAddr().String())
	w.setConn(conn)

	// 启动处理协程
	w.start()
	// 更新为就绪状态
	w.setState(stateReady)

	return w, nil
}

func (w *worker) available() bool {
	return w.dataSemaphore.Available() > 0
}

func (w *worker) start() {
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				w.log.Error("panic:", rec)
				w.log.Error(string(debug.Stack()))
				w.metrics.incError(errServerPanic.getStrCode())
			}
		}()

		for {
			select {
			case <-w.stop:
				return
			case req, ok := <-w.cmdChan:
				if !ok {
					continue
				}
				switch r := req.(type) {
				case *closeReq:
					w.handleClose(r)
				}
			case req, ok := <-w.dataChan:
				if !ok {
					continue
				}
				w.handleSendData(req)
			case <-w.batchTimeoutTicker.C:
				// 处理批次超时
				w.handleBatchTimeout()
			case <-w.sendTimeoutTicker.C:
				// 处理发送超时
				w.handleSendTimeout()
			case <-w.mapCleanTicker.C:
				// 定时清理unackedBatches，go语言里map会不停的膨胀
				w.handleCleanMap()
			case <-w.heartbeatTicker.C:
				// 定时发送心跳
				w.handleSendHeartbeat()
			case <-w.updateConnTicker.C:
				// 更新连接
				w.handleUpdateConn()
			case e, ok := <-w.updateConnChan:
				if !ok {
					continue
				}
				// 更新连接
				w.updateConn(nil, e)
			case batch, ok := <-w.sendFailedBatches:
				// 处理发送成功的批次
				if !ok {
					continue
				}
				w.handleSendFailed(batch)
			case batch, ok := <-w.retryBatches:
				// 处理重试的批次
				if !ok {
					continue
				}
				w.handleRetry(batch, true)
			case rsp, ok := <-w.responseBatches:
				// 处理响应
				if !ok {
					continue
				}
				w.handleRsp(rsp)
			}
		}
	}()
}

func (w *worker) doSendAsync(ctx context.Context, msg Message, callback Callback, flushImmediately bool) {
	req := reqPool.Get().(*sendDataReq)
	*req = sendDataReq{
		pool:             reqPool,
		ctx:              ctx,
		msg:              msg,
		callback:         callback,
		flushImmediately: flushImmediately,
		publishTime:      time.Now(),
		metrics:          w.metrics,
		workerID:         w.indexStr,
	}

	if len(msg.Payload) == 0 {
		req.done(errBadLog, "")
		return
	}

	// 已经关闭
	if w.getState() != stateReady {
		req.done(errProducerClosed, "")
		return
	}

	// 日志太长
	if len(msg.Payload) > maxUDPReqSizeV1 && w.options.isUDP {
		req.done(errLogToLong, "")
		return
	}
	if len(msg.Payload) > maxTCPReqSizeV1 && w.options.isTCP {
		req.done(errLogToLong, "")
		return
	}

	// 用一个semaphore来检查sendCh是否已满，生产时，获得信号，消费时，释放信号，可以实现满的时候直接返回
	if w.options.BlockIfQueueIsFull {
		if !w.dataSemaphore.Acquire(ctx) {
			w.log.Warn("queue is full, worker index:", w.index)
			req.done(errContextExpired, "")
			return
		}
	} else {
		if !w.dataSemaphore.TryAcquire() {
			w.log.Warn("queue is full, worker index:", w.index)
			req.done(errSendQueueIsFull, "")
			return
		}
	}

	// 保存信号量，放入管道，当请求done的时候，自动释放信号量
	req.semaphore = w.dataSemaphore
	w.dataChan <- req
	w.metrics.incPending(w.indexStr)
}

func (w *worker) send(ctx context.Context, msg Message) error {
	var err error

	// 防止竞争写（实际上响应和请求目前在一个协程中，不存在竞争）
	isDone := atomic.NewBool(false)
	doneCh := make(chan struct{})

	w.doSendAsync(ctx, msg, func(msg Message, e error) {
		if isDone.CompareAndSwap(false, true) {
			err = e       // 保存错误
			close(doneCh) // 通知外部处理完成
		}
	}, true)

	// 等待请求处理完成
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-doneCh:
		return err
	}
}

func (w *worker) sendAsync(ctx context.Context, msg Message, callback Callback) {
	w.doSendAsync(ctx, msg, callback, false)
}

func (w *worker) handleSendData(req *sendDataReq) {
	// w.log.Debug("worker[", w.index, "] handleSendData")
	// tglog没有TID的概念，所有数据都可以放一批，只需要一个缓冲的批次就行，所以用一个key就好
	const key = "only-you"
	batch, ok := w.pendingBatches[key]
	needNewBatch := false
	if ok {
		// 如果是UDP，且当前batch的数据加上新来的数据长度已经超过最大包上限，先把当前的batch发出去
		if w.options.isUDP {
			sendExistBatch := false
			totalLen := len(req.msg.Payload) + batch.dataSize
			if w.options.isV1 {
				if totalLen > maxUDPReqSizeV1 {
					sendExistBatch = true
				}
			}
			if w.options.isV3 {
				if totalLen > maxUDPReqSizeV3 {
					sendExistBatch = true
				}
			}
			if sendExistBatch {
				w.sendBatch(batch, true)
				delete(w.pendingBatches, key)
				needNewBatch = true
			}
		}

	} else {
		needNewBatch = true
	}

	if needNewBatch {
		batch = batchPool.Get().(*batchReq)
		dataReqs := batch.dataReqs
		if dataReqs == nil {
			dataReqs = make([]*sendDataReq, 0, w.options.BatchingMaxMessages)
		}
		*batch = batchReq{
			pool:       batchPool,
			batchID:    buildBatchID(w.indexStr),
			options:    w.options,
			dataReqs:   dataReqs,
			batchTime:  time.Now(),
			retries:    0,
			bufferPool: w.bufferPool,
			bytePool:   w.bytePool,
			metrics:    w.metrics,
		}
		// w.log.Debug("worker[", w.index, "] new a batch:", batch.batchID)
		w.pendingBatches[key] = batch
	}

	// map存的是指针，直接修改
	batch.append(req)

	// 不需要立即发送，批次的消息条数没到上限，批次的总大小也没到上限，继续等待
	if !req.flushImmediately &&
		len(batch.dataReqs) < w.options.BatchingMaxMessages &&
		batch.dataSize < w.options.BatchingMaxSize {
		return
	}

	// 发送并从待发送队列中删除
	w.sendBatch(batch, true)
	delete(w.pendingBatches, key)
}

func (w *worker) sendBatch(b *batchReq, retryOnFail bool) {
	// w.log.Debug("worker[", w.index, "] sendBatch")
	// 检查重试次数是否超过最大重试次数
	if b.retries > w.options.MaxRetries {
		b.done(errSendTimeout)
		return
	}

	b.lastSendTime = time.Now()
	_, err := b.encode()
	if err != nil {
		b.done(err)
		return
	}

	onOK := func() {
		// V1不需要响应，发送成功就认为成功
		if w.options.isV1 {
			b.done(nil)
		}
	}

	onErr := func(c gnet.Conn, e error, inCallback bool) {
		defer func() {
			if rec := recover(); rec != nil {
				w.log.Error("panic:", rec)
				w.log.Error(string(debug.Stack()))
				w.metrics.incError(errServerPanic.getStrCode())
			}
		}()

		w.metrics.incError(errConnWriteFailed.getStrCode())
		w.log.Error("send batch failed, err: ", e, ", inCallback: ", inCallback, ", logNum:", len(b.dataReqs))

		// 已经处于关闭状态
		if w.getState() == stateClosed {
			b.done(errConnWriteFailed)
			return
		}

		// 重要：AsyncWrite调用成功，batch就会被放入w.unackedBatches，现在出错了，要把它从w.unackedBatches中删除掉，
		// 因为这个回调是异步并发的，不能直接修改w.unackedBatches，并发写会panic，这里把batch放入管道，在管道的接收端做
		// 删除和重传操作
		if inCallback {
			// 不能在gnet的异步回调中触发连接更新，更新连接有可能创建新连接，创建新连接时调用dial函数因为和回调是同一个协程，会死锁阻塞
			w.updateConnAsync(errConnWriteFailed)
			// 不同协程，放入管道传回主协程，在主协程中将请求从w.unackedBatches队列中删除并重传
			w.sendFailedBatches <- &sendFailedBatchReq{batch: b, retry: retryOnFail}
			return
		}

		// 在同一个协程里，直接放入重试队列或者结束发送
		// 网络错误，换一个连接
		w.updateConn(c, errConnWriteFailed)
		if retryOnFail {
			// w.retryBatches <- b
			w.backoffRetry(context.Background(), b)
		} else {
			b.done(errConnWriteFailed)
		}
	}

	// w.log.Debug("worker[", w.index, "] write to:", conn.RemoteAddr())
	// 非常重要：我们使用的是gnet异步框架，在不同的协程中发送数据，必须调用AsyncWrite，Write只能在gnet.OnTraffic()中调用
	conn := w.getConn()
	if b.retries > 0 {
		w.log.Debug("retry batch to conn:", conn.RemoteAddr(), ", workerID:", w.index,
			", batchID:", b.batchID, ", logNum:", len(b.dataReqs))
	}
	err = conn.AsyncWrite(b.buffer.Bytes(), func(c gnet.Conn, e error) error {
		// 如果是UDP，回调无意义，参数c和e都是nil，具体可以查看AsyncWrite()的代码实现
		if w.options.isUDP {
			return nil
		}
		if e != nil {
			onErr(c, e, true)
		} else {
			onOK()
		}
		return nil
	})

	if err != nil {
		onErr(conn, err, false)
		return
	} else if w.options.isUDP {
		// 如果是UDP，成功失败都要在conn.AsyncWrite()返回后处理，而不是在它的回调中处理
		onOK()
	}

	// V3协议才需要等响应确认
	if w.options.isV3 {
		// 重要：因为是异步发送，AsyncWrite调用成功并不表示发送成功，尽管还没送成功，这里提前放入待确认队列，如果AsyncWrite最终发送失败，
		// 再在AsyncWrite的回调onErr()中删除，如果AsyncWrite最终发送成功，则在收到响应后再删除
		w.unackedBatchCount++
		w.unackedBatches[b.batchID] = b
	}
}

func (w *worker) handleSendFailed(b *sendFailedBatchReq) {
	// 发送失败，从待确认列表中删除，并重传，重传的时候会放回来，因为只有V3协议会把请求放入w.unackedBatches，
	// 这里检查一下是否是V3协议，避免删除其他数据
	if w.options.isV3 {
		delete(w.unackedBatches, b.batch.batchID)
	}
	if b.retry {
		w.backoffRetry(context.Background(), b.batch)
	} else {
		b.batch.done(errConnWriteFailed)
	}
}

func (w *worker) backoffRetry(ctx context.Context, batch *batchReq) {
	if batch.retries >= w.options.MaxRetries {
		batch.done(errSendTimeout)
		// w.log.Debug("to many reties, batch done:", batch.batchID)
		return
	}

	// 已经处于关闭状态
	if w.getState() == stateClosed {
		w.log.Warn("worker is closed when we are going to retry")
		batch.done(errSendTimeout)
		return
	}

	batch.retries++
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				w.log.Error("panic:", rec)
				w.log.Error(string(debug.Stack()))
				w.metrics.incError(errServerPanic.getStrCode())
			}
		}()

		// 使用 ExponentialBackoff 计算退避时间
		backoff := util.ExponentialBackoff{
			InitialInterval: 100 * time.Millisecond,
			MaxInterval:     10 * time.Second,
			Multiplier:      2.0,
			Randomization:   0.2,
		}

		waitTime := backoff.Next(batch.retries)

		select {
		case <-time.After(waitTime):
			// 再次检查是否已经处于关闭状态
			if w.getState() == stateClosed {
				w.log.Warn("worker is closed when we are going to retry")
				batch.done(errSendTimeout)
				return
			}

			// 放入重试队列
			w.log.Debug("put to retry...")
			w.retryBatches <- batch
		case <-ctx.Done():
			// 进程退出
			w.log.Warn("app exit when we are going to retry")
			batch.done(errSendTimeout)
		}
	}()
}

func (w *worker) handleRetry(batch *batchReq, retryOnFail bool) {
	// 重试
	w.metrics.incRetry(w.indexStr)
	w.log.Debug("retry batch...", ", workerID:", w.index, ", batchID:", batch.batchID)
	w.sendBatch(batch, retryOnFail)
}

func (w *worker) handleBatchTimeout() {
	for key, batch := range w.pendingBatches {
		if time.Since(batch.batchTime) > w.options.BatchingMaxPublishDelay {
			// w.log.Debug("worker[", w.index, "] batch timeout, send it now:", batch.batchID, ", key:", key)
			w.sendBatch(batch, true)
			delete(w.pendingBatches, key)
		}
	}
}

func (w *worker) handleSendTimeout() {
	// 这里可能会比较低效，或许需要一个带排序且能够O(1)查找的数据结构
	for batchID, batch := range w.unackedBatches {
		if time.Since(batch.lastSendTime) > w.options.SendTimeout {
			// w.log.Debug("worker[", w.index, "] send timeout, resend it now:", batch.batchID, "batchID:", batchID)
			// 放入重试队列
			// w.retryBatches <- batch
			w.backoffRetry(context.Background(), batch)
			// 因为重传的时候会再次放入w.unackedBatches，这里先删除
			// 且重传是在另一个协程里，这里不删除的话响应会来时batch会done掉并释放资源，重传的时候可能会异常，所以也要先删除
			delete(w.unackedBatches, batchID)
			w.metrics.incTimeout(w.indexStr)
		}
	}
}

func (w *worker) handleCleanMap() {
	// 写了50W次清理一次
	if w.unackedBatchCount < defaultMapCleanThreshold {
		return
	}

	// w.log.Debug("clean map")
	// 创建新的map，将旧数据复制过来
	newMap := make(map[string]*batchReq)
	for k, v := range w.unackedBatches {
		newMap[k] = v
	}

	// 用新的map替换旧的map
	w.unackedBatches = newMap
	// 计数器清
	w.unackedBatchCount = 0
}

func (w *worker) handleSendHeartbeat() {
	// w.log.Debug("worker[", w.index, "] handleSendHeartbeat")
	if w.options.isV1 {
		return
	}

	body := V3ReqPool.Get().(*v3.Req)
	defer V3ReqPool.Put(body)

	reqID := buildBatchID(w.indexStr)
	header, body, err := BuildV3HeartbeatReq(
		w.options.AppID,
		w.options.AppName,
		w.options.AppVer,
		w.options.Network,
		reqID,
		w.options.Token,
		w.options.TokenType,
		nil,
		nil,
		body)
	if err != nil {
		return
	}

	bb := w.bufferPool.Get()
	bytes, err := EncodeV3Req(header, body, w.options.NoFrameHeader, false, false, false, false, "", bb, false)
	if err != nil {
		return
	}

	onErr := func(c gnet.Conn, e error, inCallback bool) {
		w.metrics.incError(errConnWriteFailed.getStrCode())
		w.log.Error("send heartbeat failed, err:", e)
		if inCallback {
			// 不能在gnet的异步回调中触发连接更新，更新连接有可能创建新连接，创建新连接时调用dial函数因为和回调是同一个协程，会死锁阻塞
			w.updateConnAsync(errConnWriteFailed)
		} else {
			w.updateConn(c, errConnWriteFailed)
		}
	}

	// 非常重要：我们使用的是gnet异步框架，在不同的协程中发送数据，必须调用AsyncWrite，Write只能在gnet.OnTraffic()中调用
	conn := w.getConn()
	err = conn.AsyncWrite(bytes, func(c gnet.Conn, e error) error {
		// 如果是UDP，回调无意义，参数c和e都是nil，具体可以查看AsyncWrite()的代码实现
		if w.options.isUDP {
			return nil
		}
		if e != nil {
			onErr(c, e, true)
		}
		// 发送完，回收
		w.bufferPool.Put(bb)
		return nil
	})

	if err != nil {
		onErr(conn, err, false)
		// 发送失败，回收
		w.bufferPool.Put(bb)
	} else if w.options.isUDP {
		// 如果是UDP，成功失败都要在conn.AsyncWrite()返回后处理，而不是在它的回调中处理
		// 发送成功，回收
		w.bufferPool.Put(bb)
	}

}

func (w *worker) onRsp(rsp *batchRsp) {
	// 已经处于关闭状态
	if w.getState() == stateClosed {
		return
	}
	w.responseBatches <- rsp
}

func (w *worker) handleRsp(rsp *batchRsp) {
	batchID := rsp.batchID
	batch, ok := w.unackedBatches[batchID]
	if !ok {
		// w.log.Debug("worker[", w.index, "] batch not found in unackedBatches map:", batchID)
		w.metrics.incError(errNoMatchReq4Rsp.strCode)
		return
	}

	// w.log.Debug("worker[", w.index, "] batch done:", batchID)
	// 释放资源
	if rsp.code == 0 {
		batch.done(nil)
	} else {
		batch.done(errServerError)
	}

	delete(w.unackedBatches, batchID)
}

func (w *worker) close() {
	// 已经处于关闭状态
	if w.getState() != stateReady {
		return
	}

	req := &closeReq{
		doneCh: make(chan struct{}),
	}
	// 构造一个close请求，放入管道
	w.cmdChan <- req

	// 等待close完成
	<-req.doneCh
	close(w.stop)
}

func (w *worker) handleClose(req *closeReq) {
	if !w.casState(stateReady, stateClosing) {
		close(req.doneCh)
		return
	}

	// 停止batch超时处理，所有请求都会被立即发出去
	w.batchTimeoutTicker.Stop()
	// 停止清理map的定时器
	w.mapCleanTicker.Stop()
	// w.sendTimeoutTicker没有停，发送超时的请求仍然会被处理
	//
	// 停止更新连接的定时器
	w.updateConnTicker.Stop()

	// 消费掉w.dataChan中的数据，先起一个协程关闭dataChan，当没有数据时，下面的for循环消费就不会阻塞
	go func() {
		close(w.dataChan)
	}()
	for s := range w.dataChan {
		w.handleSendData(s)
	}

	// 此时，所有未发送的数据在w.pendingBatches中，消费掉w.pendingBatches中的数据，将待发送的batch马上发送，只发送一次，失败不重试
	for tid, batch := range w.pendingBatches {
		delete(w.pendingBatches, tid)
		w.sendBatch(batch, false) // 失败不再重试
	}

	// 因为是异步发送，在这之前发送出去失败的在回调中还有可能会写w.retryBatches，这里不能直接关闭它
	for i := 0; i < len(w.retryBatches); i++ {
		r := <-w.retryBatches
		w.handleRetry(r, false) // 失败不再重试
	}

	// 此时，只有w.unackedBatches中有数据，这些数据还没有收到响应，给他们注册一个回调，
	// 当所有数据都收到响应或者超时的时候释放所有资源，这里因为在同一个协程内，没有其协程
	// 在修改w.unackedBatches，在这里修改它是安全的

	// 获取剩余数据量
	left := atomic.NewInt32(int32(len(w.unackedBatches)))
	w.log.Debug("worker:", w.index, "unacked:", left.Load())

	closeAll := func() {
		// 关闭发送超时处理定时器
		w.sendTimeoutTicker.Stop()
		// 释放连接
		w.client.putConn(w.getConn(), nil)
		// 关闭命令管道
		close(w.cmdChan)
		// 设置为关闭状态，此后，w.retryBatches/w.sendFailedBatches/w.responseBatches都不再可写，否则会panic
		// 所以，写这些管道的代码之前，要先检查一下当前的状态是不是stateClosed，是的话就不能再写了
		w.setState(stateClosed)
		// 关闭重试管道
		close(w.retryBatches)
		// 关闭发送失败管道
		close(w.sendFailedBatches)
		// 关闭更新连接管道
		close(w.updateConnChan)
		// 关闭响应接收管道
		close(w.responseBatches)
		// 通知调用者close操作完成
		close(req.doneCh)
	}

	// 没有数据了，直接关闭退出
	if left.Load() <= 0 {
		w.log.Debug("no batch left, close now")
		closeAll()
		return
	}

	for id, batch := range w.unackedBatches {
		batch.callback = func() {
			// 收到响应或者超时，计数器-1，当<=0时，说明全部处理完成
			l := left.Add(-1)
			if l <= 0 {
				w.log.Debug("left batches all done, close now")
				closeAll()
			}
		}
		// 重新写回map中
		w.unackedBatches[id] = batch
	}
}

func (w *worker) handleUpdateConn() {
	w.updateConn(nil, nil)
}

func (w *worker) updateConnAsync(err error) {
	// 已经处于关闭状态
	if w.getState() == stateClosed {
		return
	}

	select {
	case w.updateConnChan <- err:
	default:
	}
}

func (w *worker) updateConn(old gnet.Conn, err error) {
	// w.log.Debug("worker[", w.index, "] updateConn, err: ", err)
	newConn, newErr := w.client.getConn()
	if newErr != nil {
		w.log.Error("get new conn error:", newErr)
		w.metrics.incError(errNewConnFailed.getStrCode())
		return
	}

	oldConn := old
	if oldConn == nil {
		oldConn = w.getConn()
	}

	ok := w.casConn(oldConn, newConn)
	if ok {
		// 切换成功且没有错误才放回池子里
		if err == nil {
			w.client.putConn(oldConn, err)
		} else { //nolint:staticcheck
			// 有错误的，一般是对端关闭，gnet会调用OnClose()把连接从池子里删除，这里不放回池里问题也不大
		}
		w.metrics.incUpdateConn(getErrorCode(err))
	} else {
		w.client.putConn(newConn, nil)
	}
}

func (w *worker) setConn(conn gnet.Conn) {
	w.conn.Store(conn)
}

func (w *worker) getConn() gnet.Conn {
	return w.conn.Load().(gnet.Conn)
}

func (w *worker) onConnClosed(conn gnet.Conn, err error) {
	oldConn := w.conn.Load().(gnet.Conn)
	if oldConn == conn {
		w.updateConnAsync(err)
	}
}

func (w *worker) casConn(oldConn, newConn gnet.Conn) bool {
	return w.conn.CompareAndSwap(oldConn, newConn)
}

func (w *worker) setState(state workerState) {
	w.state.Swap(int32(state))
}

func (w *worker) getState() workerState {
	return workerState(w.state.Load())
}

func (w *worker) casState(oldState, newState workerState) bool {
	return w.state.CompareAndSwap(int32(oldState), int32(newState))
}
