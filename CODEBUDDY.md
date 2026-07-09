# CODEBUDDY.md

This file provides guidance to CodeBuddy Code when working with code in this repository.

## Project Overview

`game-log-sdk-go` is the Go SDK for Tencent Game Log Service (TGLog). It reports game logs to TGLog servers over UDP or TCP using either the V1 (plain-text, no response) or V3 (ProtoBuffer, with response, optional compress/encrypt/auth/sign) protocol. Module path: `github.com/tencent/game-log-sdk-go`. Requires Go 1.21+ (go.mod declares `go 1.24.0`).

## Common Commands

```bash
# Full quality gate (format + vet + lint + build test binary)
make all

# Run all unit tests
go test ./...

# Run tests for a single package
go test ./tglog/...
go test ./crypto/...
go test ./discoverer/...

# Run a single test
go test -run TestName ./path/to/pkg

# Coverage
go test -cover ./...

# Build the performance/integration test binary (Linux)
GOOS=linux go build -o test/test test/test.go

# Manual format / static checks (also run by `make all`)
gofmt -w .
goimports -w .
go vet ./...
golint -set_exit_status ./...
```

Required dev tools (see `DEV_SETUP.md`): `gofmt`, `goimports` (`golang.org/x/tools/cmd/goimports`), `golint` (`golang.org/x/lint/golint`). `make all` will fail if any are missing.

## Architecture

The SDK is structured as a thin public API (`tglog` package) on top of a set of focused internal helper packages. The high-level data flow for an asynchronous send is:

```
Client.SendAsync ─► pick worker (round-robin over c.workers)
                  ─► worker.dataChan (bounded by MaxPendingMessages, gated by dataSemaphore)
                  ─► worker batches msgs (BatchingMaxMessages / BatchingMaxSize / BatchingMaxPublishDelay)
                  ─► codec encodes (V1 plain or V3 PB + optional snappy compress / AES encrypt / auth / sign)
                  ─► framer adds length-prefixed header (V3/TCP)
                  ─► gnet.Conn from connpool ─► network
                  ─► (V3 only) response routed via responseBatches → matches unackedBatches → fires Callback
```

### Package map

- `tglog/` — public API and the bulk of the logic.
  - `client.go`: `Client` interface, `NewV1Client` / `NewV3Client`, gnet event hooks (`OnTraffic`, `OnClose`, …), discoverer callback (`OnEndpointUpdate`), worker pool management. Constants `maxUDPReqSizeV1/V3`, `maxTCPReqSizeV1/V3` cap batch sizes per transport+protocol.
  - `worker.go`: per-worker goroutine, batching, retries, timeouts, heartbeats, connection refresh, unacked-batch map cleanup. V1 workers stop response-related tickers since V1 has no responses. Internal `errNo` codes (10001–20001) feed both error returns and metrics labels.
  - `codec.go`: V1 plain-text encoding and V3 ProtoBuf request/response encode/decode. Holds `sync.Pool`s for V3 header/req/rsp objects. Handles compression (snappy), encryption (AES via `crypto/`), auth tokens, and signatures.
  - `request.go`: `sendDataReq`, `batchReq`, `batchRsp`, `sendFailedBatchReq` — the message types passed through the worker's channels.
  - `options.go` / `options_basic.go` / `options_v3.go`: `Options` struct + functional-option constructors. `Options.ValidateAndSetDefault()` is the single source of truth for defaults and cross-field validation (e.g. V3+TCP forces `NoFrameHeader=false`, encryption requires `EncryptKey`).
  - `metrics.go`: Prometheus collectors. `Options.MetricsName` must be unique per `Client` instance in a process to avoid collector registration collisions.
  - `message.go`: `Message`, `ParseMessages`, plus reflection-based `ToTGLogString` / `ToTGLogMessage` helpers (TGLog wire format is `name|field1|field2|...\n`, with `|`→`%7C` and `\n`→`%0D` escaping).
  - `example_test.go`: canonical usage examples (also the doc reference from README).
- `discoverer/` — service discovery abstraction. `Discoverer` interface plus a DNS implementation (`dns.go`) that resolves a domain to a host list and notifies registered `EventHandler`s on change. `client` is itself an `EventHandler`, so backend RS changes propagate into the connection pool.
- `connpool/` — `EndpointRestrictedConnPool`: a pool of `gnet.Conn`s keyed by endpoint, refreshed when the discoverer updates the endpoint list and on connection errors.
- `framer/` — length-prefixed frame parsing for V3/TCP (configured by `MaxFrameLen`, `LenFieldOffset`, `LenFieldLength`, `LenAdjustment`, `FrameBytesToStrip`, `PayloadBytesToTrip`).
- `crypto/` — AES key parsing/encryption used by V3 (`ParseKey` accepts 16/24/32-byte raw strings or base64-encoded keys).
- `bufferpool/` — pooled `bytes.Buffer` (`BufferPool`) and pooled `[]byte` of fixed width (`BytePool`) used during encode/decode to avoid per-request allocations.
- `bytecloser/` — wraps a buffer so it can be returned to its pool via `io.Closer`.
- `logger/` — minimal `Logger` interface (compatible with logrus and zap sugar loggers); default impl prints to stdout. Plug in via `WithLogger(...)`.
- `syncx/` — counting `Semaphore` used to bound in-flight messages per worker (separate from the channel capacity so callers can choose blocking vs. non-blocking via `BlockIfQueueIsFull`).
- `util/` — backoff (`util/backoff.go`), zero-copy `BytesToString`/`StringToBytes`, snowflake/UUID ID helpers, CityHash, local IP discovery.
- `test/test.go` — standalone CLI used both as the integration smoke test and the performance benchmark referenced in `README.md`. Built into `test/test` by `make all`.

### Cross-cutting concerns to keep in mind when editing

- **V1 vs V3 divergence is enforced in many places.** `Options.isV1`/`isV3` and `isUDP`/`isTCP` are derived in `ValidateAndSetDefault` and gate behavior in `client`, `worker`, `codec`, and `framer`. When adding a feature, check whether it applies to all four combinations.
- **V3 responses drive lifecycle.** Workers track `unackedBatches` keyed by sequence and clean them on response, timeout-retry, or periodic `mapCleanTicker` sweep. V1 has no responses, so those tickers/maps are intentionally inert.
- **Connection updates come from two sources**: the discoverer (DNS change) and per-conn errors (`updateConnChan`). Both funnel into the worker's connection-replacement path; the rationale (and trade-offs vs. per-send connection lookup) is in the long Chinese comment at the top of `worker.go`.
- **Buffer pools are shared by default across workers.** If a `BufferPool`/`BytePool` is not provided in `Options`, one is created in `ValidateAndSetDefault` and reused. Don't free pool buffers manually — they go back via `bytecloser`.
- **Prometheus collectors are global.** Each `Client` registers with `prometheus.DefaultRegisterer` unless overridden; multiple clients in the same process must use distinct `MetricsName` values.

## Documentation References

- `README.md` / `README_CN.md` — usage, configuration options, version/transport selection, performance numbers, FAQ.
- `DEV_SETUP.md` / `DEV_SETUP_CN.md` — toolchain setup and IDE config.
- `CHANGELOG.md` — version history.
- `tglog/example_test.go` and `test/test.go` — runnable examples for V1/V3, sync/async, UDP/TCP combinations.
