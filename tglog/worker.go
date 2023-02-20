package tglog

import (
	"context"
	"errors"
	"git.woa.com/tglog/v3/sdk-go/syncx"
	"net"
	"strconv"
	"sync"
	"time"

	"git.woa.com/tglog/v3/sdk-go/bufferpool"
	"git.woa.com/tglog/v3/sdk-go/logger"

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
	errCodeOK              = "0"
	errSendTimeout         = errors.New("message send timeout")
	errCodeSendTimeout     = "10001"
	errSendFailed          = errors.New("message send failed")
	errCodeSendFailed      = "10002"
	errProducerClosed      = errors.New("producer already been closed")
	errCodeProducerClosed  = "10003"
	errSendQueueIsFull     = errors.New("producer send queue is full")
	errCodeSendQueueIsFull = "10004"
	errContextExpired      = errors.New("message context expired")
	errCodeContextExpired  = "10005"
	errLogToLong           = errors.New("input log is too long")
	errCodeLogToLong       = "10006"
	errCodeNewConnFailed   = "10007"
	errCodeConnWriteFailed = "10008"
	errCodeConnReadFailed  = "10009"
	errCodeUnknown         = "20001"
	rspPool                *sync.Pool
)

func getErrorCode(err error) string {
	if err == nil {
		return errCodeOK
	}
	if err == errSendTimeout {
		return errCodeSendTimeout
	}
	if err == errSendFailed {
		return errCodeSendFailed
	}
	if err == errProducerClosed {
		return errCodeProducerClosed
	}
	if err == errSendQueueIsFull {
		return errCodeSendQueueIsFull
	}
	if err == errContextExpired {
		return errCodeContextExpired
	}
	if err == errLogToLong {
		return errCodeLogToLong
	}
	return errCodeUnknown
}

func init() {
	rspPool = &sync.Pool{
		New: func() interface{} {
			return &batchRsp{}
		},
	}
}

type worker struct {
	client             *client               // 上层client
	index              int                   // worker id
	indexStr           string                // worker id 字符串格式
	options            *Options              // 配置
	state              atomic.Int32          // 状态
	log                logger.Logger         // 日志
	conn               atomic.Value          // 用原子操作更新异常的连接
	cmdChan            chan interface{}      // 命令管道
	dataChan           chan *sendDataReq     // 数据管道
	dataSemaphore      syncx.Semaphore       //
	pendingBatches     map[string]*batchReq  // 待发送批次
	unackedBatches     map[string]*batchReq  // 待确认批次
	retryBatches       chan *batchReq        // 重试管道
	responseBatches    chan *batchRsp        // 响应管道
	batchTimeoutTicker *time.Ticker          // 批次超时定时器，检测批次最旧的数据是否超过指定时间，超过就算不够一批也直接发送
	sendTimeoutTicker  *time.Ticker          // 发送超时定时器，检测批次是否超过指定时间都没收到响应，是否重传
	heartbeatTicker    *time.Ticker          // 心跳定时器
	mapCleanTicker     *time.Ticker          // map清理定时器
	unackedBatchCount  int                   // map清理计数器
	metrics            *metrics              // 指标
	bufferPool         bufferpool.BufferPool // 缓冲池
	bytePool           bufferpool.BytePool   // 内存池
}

func newWorker(cli *client, index int, opts *Options) (*worker, error) {
	w := &worker{
		client:             cli,
		index:              index,
		indexStr:           strconv.Itoa(index),
		options:            opts,
		cmdChan:            make(chan interface{}),
		dataChan:           make(chan *sendDataReq, opts.MaxPendingMessages),
		dataSemaphore:      syncx.NewSemaphore(int32(opts.MaxPendingMessages)),
		pendingBatches:     make(map[string]*batchReq),
		unackedBatches:     make(map[string]*batchReq),
		retryBatches:       make(chan *batchReq, opts.MaxPendingMessages),
		responseBatches:    make(chan *batchRsp, opts.MaxPendingMessages),
		batchTimeoutTicker: time.NewTicker(opts.BatchingMaxPublishDelay),
		sendTimeoutTicker:  time.NewTicker(opts.SendTimeout),
		heartbeatTicker:    time.NewTicker(defaultHeartbeatInterval * time.Second),
		mapCleanTicker:     time.NewTicker(defaultMapCleanInterval * time.Second),
		metrics:            cli.metrics,
		bufferPool:         opts.BufferPool,
		bytePool:           opts.BytePool,
		log:                opts.Logger,
	}

	// V1协议没有响应，不需要这些定时器
	if opts.isV1 {
		w.sendTimeoutTicker.Stop()
		w.heartbeatTicker.Stop()
		w.mapCleanTicker.Stop()
	}

	w.setState(stateInit)

	conn, err := cli.getConn()
	if err != nil {
		return nil, err
	}
	w.setConn(conn)

	go w.start()
	w.setState(stateReady)

	return w, nil
}

func (w *worker) start() {
	for {
		select {
		case req, ok := <-w.cmdChan:
			if !ok {
				continue
			}
			switch r := req.(type) {
			case *updateConnReq:
				w.handleUpdateConn(r)
			case *closeReq:
				w.handleClose(r)
				return
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
		case rsp, ok := <-w.responseBatches:
			// 处理响应
			if !ok {
				continue
			}
			w.handleRsp(rsp)
		case batch, ok := <-w.retryBatches:
			// 处理重试的批次
			if !ok {
				continue
			}
			w.handleRetry(batch, true)
		}
	}
}

func (w *worker) doSendAsync(ctx context.Context, msg *Message, callback Callback, flushImmediately bool) {
	req := &sendDataReq{
		ctx:              ctx,
		msg:              msg,
		callback:         callback,
		flushImmediately: flushImmediately,
		publishTime:      time.Now(),
		metrics:          w.metrics,
	}

	// 已经关闭
	if w.getState() != stateReady {
		req.done(errProducerClosed)
		return
	}

	// 日志太长
	if len(msg.Payload) > maxUDPReqSizeV1 && w.options.isUDP {
		req.done(errLogToLong)
		return
	}
	if len(msg.Payload) > maxTCPReqSizeV1 && w.options.isTCP {
		req.done(errLogToLong)
		return
	}

	// 用一个semaphore来检查sendCh是否已满，生产时，获得信号，消费时，释放信号，可以实现满的时候直接返回
	if w.options.BlockIfQueueIsFull {
		if !w.dataSemaphore.Acquire(ctx) {
			w.log.Warn("queue is full, worker index:", w.index, ", server:", w.getConn().RemoteAddr())
			req.done(errContextExpired)
			return
		}
	} else {
		if !w.dataSemaphore.TryAcquire() {
			w.log.Warn("queue is full, worker index:", w.index, ", server:", w.getConn().RemoteAddr())
			req.done(errSendQueueIsFull)
			return
		}
	}

	// 保存信号量，放入管道，当请求done的时候，自动释放信号量
	req.semaphore = w.dataSemaphore
	w.dataChan <- req
	w.metrics.incPending()
}

func (w *worker) send(ctx context.Context, msg *Message) error {
	var err error

	// 防止竞争写（实际上响应和请求目前在一个协程中，不存在竞争）
	isDone := atomic.NewBool(false)
	doneCh := make(chan struct{})

	w.doSendAsync(ctx, msg, func(msg *Message, e error) {
		if isDone.CompareAndSwap(false, true) {
			err = e       // 保存错误
			close(doneCh) // 通知外部处理完成
		}
	}, true)

	// 等待请求处理完成
	<-doneCh
	return err
}

func (w *worker) sendAsync(ctx context.Context, msg *Message, callback Callback) {
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
		batch = &batchReq{
			batchID:    buildBatchID(w.indexStr),
			options:    w.options,
			dataReqs:   make([]*sendDataReq, 0, w.options.BatchingMaxMessages),
			batchTime:  time.Now(),
			retries:    0,
			bufferPool: w.bufferPool,
			bytePool:   w.bytePool,
			metrics:    w.metrics,
		}
		w.log.Debug("worker[", w.index, "] new a batch:", batch.batchID)
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
	b.lastSendTime = time.Now()
	b.encode()
	conn := w.getConn()
	// w.log.Debug("worker[", w.index, "] write to:", conn.RemoteAddr())
	_, err := conn.Write(b.buffer.Bytes())
	if err != nil {
		w.metrics.incError(errCodeConnWriteFailed)
		w.log.Error("send batch failed, error", err)
		// 网络错误，换一个连接
		w.doUpdateConn(conn)
		// 放入重试队列
		if retryOnFail {
			w.retryBatches <- b
		} else {
			b.done(errSendFailed)
		}
		return
	}

	// V3协议会有响应，放入待确认队列
	if w.options.isV3 {
		w.unackedBatchCount++
		w.unackedBatches[b.batchID] = b
	} else {
		b.done(nil)
	}
}

func (w *worker) handleBatchTimeout() {
	for key, batch := range w.pendingBatches {
		if time.Since(batch.batchTime) > w.options.BatchingMaxPublishDelay {
			w.log.Debug("worker[", w.index, "] batch timeout, send it now:", batch.batchID, ", key:", key)
			w.sendBatch(batch, true)
			delete(w.pendingBatches, key)
		}
	}
}

func (w *worker) handleSendTimeout() {
	// 这里可能会比较低效
	for batchID, batch := range w.unackedBatches {
		if time.Since(batch.lastSendTime) > w.options.SendTimeout {
			w.log.Debug("worker[", w.index, "] send timeout, resend it now:", batch.batchID, "batchID:", batchID)
			// 放入重试队列
			w.retryBatches <- batch
			// 因为重传的时候会再次放入w.unackedBatches，这里先删除
			delete(w.unackedBatches, batchID)
			w.metrics.incTimeout()
		}
	}
}

func (w *worker) handleCleanMap() {
	// 写了50W次清理一次
	if w.unackedBatchCount < defaultMapCleanThreshold {
		return
	}
	w.log.Debug("clean map")
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
	if w.options.isV1 {
		return
	}

	req, err := BuildV3HeartbeatReq(
		w.options.AppID,
		w.options.AppName,
		w.options.AppVer,
		w.options.Network)
	if err != nil {
		return
	}

	bb := w.bufferPool.Get()
	defer w.bufferPool.Put(bb)

	bytes, err := EncodeV3Req(req, w.options.NoFrameHeader, false, false, "", bb, false)
	if err != nil {
		return
	}

	w.getConn().Write(bytes)
}

func (w *worker) handleRsp(rsp *batchRsp) {
	defer rspPool.Put(rsp)
	batchID := rsp.batchID
	batch, ok := w.unackedBatches[batchID]
	if !ok {
		w.log.Warn("worker[", w.index, "] batch not found in unackedBatches map:", batchID)
		return
	}
	w.log.Debug("worker[", w.index, "] batch done:", batchID)
	// 释放资源
	batch.done(nil)
	delete(w.unackedBatches, batchID)
}

func (w *worker) handleRetry(batch *batchReq, retryOnFail bool) {
	batch.retries++
	if batch.retries >= w.options.MaxRetries {
		batch.done(errSendTimeout)
		w.log.Debug("to many reties, batch done:", batch.batchID)
		return
	}
	// 重试
	w.metrics.incRetry()
	w.sendBatch(batch, retryOnFail)
}

func (w *worker) close() {
	// 已经处于关闭状态
	if w.getState() != stateReady {
		return
	}

	req := &closeReq{
		doneCh: make(chan struct{}),
	}

	w.cmdChan <- req
	// wait
	<-req.doneCh
}

func (w *worker) handleClose(req *closeReq) {
	if !w.casState(stateReady, stateClosing) {
		close(req.doneCh)
		return
	}
	// 此时，外部已经不能写入请求
	w.setState(stateClosed)

	// 停止batch超时处理，所有请求都会被立即发出去
	w.batchTimeoutTicker.Stop()
	// 停止清理map的定时器
	w.mapCleanTicker.Stop()
	// w.sendTimeoutTicker没有停，发送超时的请求仍然会被处理
	// 消费掉w.dataChan中的数据，先起一个协程关闭dataChan，当没有数据时，下面的for循环消费就不会阻塞
	go func() {
		close(w.dataChan)
	}()
	for s := range w.dataChan {
		w.handleSendData(s)
	}
	// 消费掉w.pendingBatches中的数据，待发送的batch马上发送，只发送一次，失败不重试
	for tid, batch := range w.pendingBatches {
		delete(w.pendingBatches, tid)
		w.sendBatch(batch, false) // 失败不再重试
	}
	// 处理掉w.retryBatches中的数据，先起一个协程关闭retryBatches，当没有数据时，下面的for循环消费就不会阻塞
	go func() {
		close(w.retryBatches)
	}()
	for r := range w.retryBatches {
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
		// 闭关网络连接，读响应的调用会立即返回，这样读协程才不会阻塞在读上，这个要在停止读协程前调用
		_ = w.getConn().Close()
		// 停止命令管道
		close(w.cmdChan)
		// 关闭响应接收队列，这个要在读协程停止之后调用
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

func (w *worker) updateConn(deletedEndpoints map[string]struct{}) {
	req := &updateConnReq{
		deletedEndpoints: deletedEndpoints,
		doneCh:           make(chan struct{}),
	}

	w.cmdChan <- req
	// wait
	<-req.doneCh
}

func (w *worker) doUpdateConn(oldConn net.Conn) {
	newConn, err := w.client.getConn()
	if err != nil {
		w.log.Error("get new conn error:", err)
		w.metrics.incError(errCodeNewConnFailed)
		return
	}

	w.setConn(newConn)
	w.client.closeConn(oldConn)
	w.metrics.incUpdateConn()
}

func (w *worker) handleUpdateConn(r *updateConnReq) {
	defer close(r.doneCh)

	oldConn := w.getConn()
	_, ok := r.deletedEndpoints[oldConn.RemoteAddr().String()]
	if ok {
		w.doUpdateConn(oldConn)
	}
}

func (w *worker) setConn(conn net.Conn) {
	w.conn.Store(conn)
}

func (w *worker) getConn() net.Conn {
	return w.conn.Load().(net.Conn)
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
