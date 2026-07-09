package tglog

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/tencent/game-log-sdk-go/logger"
)

func newRetryTestWorker(t *testing.T) *worker {
	t.Helper()

	m, err := newMetrics("retry_test_"+t.Name(), prometheus.NewRegistry())
	if err != nil {
		t.Fatalf("newMetrics() error = %v", err)
	}

	w := &worker{
		indexStr:          "0",
		options:           &Options{MaxRetries: 3, SendTimeout: time.Millisecond, isV3: true},
		log:               logger.Std(),
		unackedBatches:    make(map[string]*batchReq),
		retryingBatches:   make(map[string]*retryingBatch),
		doneBatches:       make(chan *doneBatchReq, 1),
		retryBatches:      make(chan *retryBatchReq, 1),
		responseBatches:   make(chan *batchRsp, 1),
		sendFailedBatches: make(chan *sendFailedBatchReq, 1),
		metrics:           m,
	}
	w.setState(stateReady)
	return w
}

func newRetryTestBatch(batchID string, done *int) *batchReq {
	return &batchReq{
		batchID:      batchID,
		batchTime:    time.Now(),
		lastSendTime: time.Now(),
		callback: func() {
			(*done)++
		},
	}
}

func TestHandleRspCancelsRetryingBatch(t *testing.T) {
	w := newRetryTestWorker(t)
	done := 0
	batch := newRetryTestBatch("batch-1", &done)
	ctx, cancel := context.WithCancel(context.Background())
	w.retryingBatches[batch.batchID] = &retryingBatch{batch: batch, cancel: cancel}

	w.handleRsp(&batchRsp{batchID: batch.batchID})

	if len(w.retryingBatches) != 0 {
		t.Fatalf("retryingBatches len = %d, want 0", len(w.retryingBatches))
	}
	if done != 1 {
		t.Fatalf("done count = %d, want 1", done)
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("retry context was not canceled")
	}
}

func TestHandleRetryIgnoresStaleRetryRequest(t *testing.T) {
	w := newRetryTestWorker(t)
	done := 0
	batch := newRetryTestBatch("batch-1", &done)
	_, cancel := context.WithCancel(context.Background())
	w.retryingBatches[batch.batchID] = &retryingBatch{batch: batch, cancel: cancel}

	w.handleRsp(&batchRsp{batchID: batch.batchID})
	w.handleRetry(&retryBatchReq{batchID: batch.batchID, batch: batch}, true)

	if done != 1 {
		t.Fatalf("done count = %d, want 1", done)
	}
	if len(w.retryingBatches) != 0 {
		t.Fatalf("retryingBatches len = %d, want 0", len(w.retryingBatches))
	}
}

func TestHandleSendTimeoutMovesBatchToRetrying(t *testing.T) {
	w := newRetryTestWorker(t)
	done := 0
	batch := newRetryTestBatch("batch-1", &done)
	batch.lastSendTime = time.Now().Add(-time.Second)
	w.unackedBatches[batch.batchID] = batch

	w.handleSendTimeout()

	if _, ok := w.unackedBatches[batch.batchID]; ok {
		t.Fatal("batch still exists in unackedBatches")
	}
	if _, ok := w.retryingBatches[batch.batchID]; !ok {
		t.Fatal("batch was not moved to retryingBatches")
	}
	if batch.retries != 1 {
		t.Fatalf("batch retries = %d, want 1", batch.retries)
	}

	w.handleRsp(&batchRsp{batchID: batch.batchID})
}

func TestHandleSendFailedDoesNotDuplicateRetryingBatch(t *testing.T) {
	w := newRetryTestWorker(t)
	done := 0
	batch := newRetryTestBatch("batch-1", &done)
	batch.lastSendTime = time.Now().Add(-time.Second)
	w.unackedBatches[batch.batchID] = batch

	w.handleSendTimeout()
	w.handleSendFailed(&sendFailedBatchReq{batchID: batch.batchID, batch: batch, retry: true})

	if len(w.retryingBatches) != 1 {
		t.Fatalf("retryingBatches len = %d, want 1", len(w.retryingBatches))
	}
	if batch.retries != 1 {
		t.Fatalf("batch retries = %d, want 1", batch.retries)
	}

	w.handleRsp(&batchRsp{batchID: batch.batchID})
}

func TestHandleSendFailedIgnoresReusedBatchPointer(t *testing.T) {
	w := newRetryTestWorker(t)
	done := 0
	batch := newRetryTestBatch("batch-1", &done)
	oldBatchID := batch.batchID
	batch.batchID = "batch-2"
	w.unackedBatches[batch.batchID] = batch

	w.handleSendFailed(&sendFailedBatchReq{batchID: oldBatchID, batch: batch, retry: false})

	if done != 0 {
		t.Fatalf("done count = %d, want 0", done)
	}
	if _, ok := w.unackedBatches[batch.batchID]; !ok {
		t.Fatal("current batch was removed by stale send failure")
	}
}

func TestHandleSendFailedDoesNotRetryWhenClosing(t *testing.T) {
	w := newRetryTestWorker(t)
	w.setState(stateClosing)
	done := 0
	batch := newRetryTestBatch("batch-1", &done)
	w.unackedBatches[batch.batchID] = batch

	w.handleSendFailed(&sendFailedBatchReq{batchID: batch.batchID, batch: batch, retry: true})

	if done != 1 {
		t.Fatalf("done count = %d, want 1", done)
	}
	if _, ok := w.unackedBatches[batch.batchID]; ok {
		t.Fatal("unacked batch was not removed")
	}
	if len(w.retryingBatches) != 0 {
		t.Fatalf("retryingBatches len = %d, want 0", len(w.retryingBatches))
	}
}

func TestHandleSendFailedDoneCleansRetryingBatch(t *testing.T) {
	w := newRetryTestWorker(t)
	done := 0
	batch := newRetryTestBatch("batch-1", &done)
	ctx, cancel := context.WithCancel(context.Background())
	w.unackedBatches[batch.batchID] = batch
	w.retryingBatches[batch.batchID] = &retryingBatch{batch: batch, cancel: cancel}

	w.handleSendFailed(&sendFailedBatchReq{batchID: batch.batchID, batch: batch, retry: false})

	if done != 1 {
		t.Fatalf("done count = %d, want 1", done)
	}
	if _, ok := w.retryingBatches[batch.batchID]; ok {
		t.Fatal("retrying batch was not removed")
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("retry context was not canceled")
	}
}

func TestDoneBatchCleansMatchingRetryingState(t *testing.T) {
	w := newRetryTestWorker(t)
	done := 0
	batch := newRetryTestBatch("batch-1", &done)
	_, cancel := context.WithCancel(context.Background())
	w.retryingBatches[batch.batchID] = &retryingBatch{batch: batch, cancel: cancel}

	w.doneBatch(batch.batchID, batch, errSendTimeout)

	if done != 1 {
		t.Fatalf("done count = %d, want 1", done)
	}
	if _, ok := w.retryingBatches[batch.batchID]; ok {
		t.Fatal("retrying batch was not removed")
	}
}

func TestDoneBatchCleansMatchingUnackedState(t *testing.T) {
	w := newRetryTestWorker(t)
	done := 0
	batch := newRetryTestBatch("batch-1", &done)
	w.unackedBatches[batch.batchID] = batch

	w.doneBatch(batch.batchID, batch, errSendTimeout)

	if done != 1 {
		t.Fatalf("done count = %d, want 1", done)
	}
	if _, ok := w.unackedBatches[batch.batchID]; ok {
		t.Fatal("unacked batch was not removed")
	}
}

func TestDoneBatchIgnoresStaleBatch(t *testing.T) {
	w := newRetryTestWorker(t)
	done := 0
	batch := newRetryTestBatch("batch-1", &done)
	staleBatch := newRetryTestBatch("batch-1", &done)
	_, cancel := context.WithCancel(context.Background())
	w.retryingBatches[batch.batchID] = &retryingBatch{batch: batch, cancel: cancel}

	w.doneBatch(staleBatch.batchID, staleBatch, errSendTimeout)

	if done != 0 {
		t.Fatalf("done count = %d, want 0", done)
	}
	if _, ok := w.retryingBatches[batch.batchID]; !ok {
		t.Fatal("retrying batch was removed")
	}

	w.handleRsp(&batchRsp{batchID: batch.batchID})
}

func TestDoneBatchAsyncEnqueuesDoneRequest(t *testing.T) {
	w := newRetryTestWorker(t)
	done := 0
	batch := newRetryTestBatch("batch-1", &done)
	w.unackedBatches[batch.batchID] = batch

	w.doneBatchAsync(batch.batchID, batch, errSendTimeout)
	req := <-w.doneBatches
	w.doneBatch(req.batchID, req.batch, req.err)

	if done != 1 {
		t.Fatalf("done count = %d, want 1", done)
	}
	if _, ok := w.unackedBatches[batch.batchID]; ok {
		t.Fatal("unacked batch was not removed")
	}
}
