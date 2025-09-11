# game-log-sdk-go

English | [中文](./README.md)

## What is game-log-sdk-go?

game-log-sdk-go is the Go SDK for Tencent Game Log Service (TencentGameLog, or TGLog). It is responsible for reporting game logs to TGLog servers using specific protocols and formats.

==**<font color=red>Important: Due to significant performance differences between synchronous and asynchronous interfaces, especially with V3 protocol, please use asynchronous interfaces.</font>**==

## Features

- Domain-based backend RS refresh support
- Synchronous reporting support
- Batch asynchronous reporting support
- TGLog V1 protocol compatibility
- TGLog V3 protocol support
- Compression and encryption support (V3 protocol)
- Graceful shutdown support

## Usage

For detailed examples, please refer to: tglog/example_test.go or test/test.go

### Example

```go
package tglog_test

import (
	"context"
	"fmt"
	"time"

	"github.com/tencent/game-log-sdk-go/tglog"

	"go.uber.org/atomic"
)

func ExampleClient_Send() {
	client, err := tglog.NewV1Client(
		tglog.WithNetwork("udp"),
		tglog.WithHost("dev.tglog.com"),
		tglog.WithPort(20001),
	)

	if err != nil {
		fmt.Println(err)
		return
	}

	for i := 0; i < 1000; i++ {
		client.Send(context.Background(), tglog.Message{Name: "test", Payload: []byte("test|a|b|c")})
	}

	client.Close()
}

func ExampleClient_SendAsync() {
	client, err := tglog.NewV1Client(
		tglog.WithNetwork("udp"),
		tglog.WithHost("dev.tglog.com"),
		tglog.WithPort(20003),
	)

	if err != nil {
		fmt.Println(err)
		return
	}

	var success atomic.Uint64
	var failed atomic.Uint64
	for i := 0; i < 1000; i++ {
		client.SendAsync(context.Background(),
			tglog.Message{Name: "test", Payload: []byte("test|a|b|c")},
			func(msg tglog.Message, err error) {
				if err != nil {
					failed.Add(1)
				} else {
					success.Add(1)
				}
			})
	}

	// wait async send finish
	time.Sleep(3 * time.Second)
	fmt.Println("success:", success.Load())
	fmt.Println("failed:", failed.Load())
	client.Close()
}
```

### Configuration Options

See:
- tglog/options.go: All configuration options
- tglog/options_basic.go: Basic configuration functions
- tglog/options_v3.go: V3 protocol-related configuration functions

#### V3 Protocol Feature Configuration

##### Enable Compression
```go
WithCompress(true)
```

##### Enable Encryption
```go
WithEncrypt(true)
WithEncryptKey("1234567890abcdef")
```

##### Enable Authentication
```go
WithToken("1234567890abcdef")
WithTokenType("tglog")
WithAuth(true)
```
> Note: Authentication requires encryption to be enabled, otherwise the token will be exposed.

##### Enable Signature
```go
WithSign(true)
```
> Note: Signature requires both encryption and authentication to be enabled, otherwise the token will be exposed and there will be no token available for signing.

### Version Selection

- **V1 Version**: Data is sent in plain text without response. Suitable for intranet environments. Tencent Games typically use V1.

- **V3 Version**: Data is encoded with ProtoBuffer before sending, with response support. Supports encryption, compression, authentication, and signature. Suitable for stricter network environments and special cases.

### Send Mode Selection

- **Synchronous Mode**: Sends one request, waits for response (if any), then sends the next request. No concurrency. Not recommended.

- **Asynchronous Mode**: Can continue sending next request after sending one. Responses (if any) are notified through callbacks. Supports concurrency. Recommended.

### Communication Protocol Selection

- **UDP**: Connectionless, low latency. Suitable for intranet environments. Commonly used by Tencent Games.

- **TCP**: Connection-based, reliable, but potentially higher latency. Suitable for stricter network environments and special cases.

### Others

- **Compression**: Only supported in V3, requires compatible V3 server.
- **Encryption**: Only supported in V3, requires compatible V3 server with same encryption key configured on both sides.
- **Authentication**: Only supported in V3, requires compatible V3 server. Enabling authentication requires encryption, so both sides need the same encryption key. Client needs authentication token, server needs authentication key.
- **Signature**: Only supported in V3, requires compatible V3 server. Enabling signature uses authentication token, so requires both authentication and encryption. Both sides need the same encryption key, client needs authentication token, server needs authentication key.

## Performance

### Environment

#### Hardware

Client: 16C32G/tlinux4
Server: 16C64G/tlinux2

Kernel parameters:
```shell
echo "net.core.rmem_max=256000000" >> /etc/sysctl.conf
echo "net.core.wmem_max=256000000" >> /etc/sysctl.conf
echo "net.core.rmem_default=104857600" >> /etc/sysctl.conf
echo "net.core.wmem_default=104857600" >> /etc/sysctl.conf
echo "net.ipv4.udp_rmem_min=104857600" >> /etc/sysctl.conf
echo "net.ipv4.udp_wmem_min=104857600" >> /etc/sysctl.conf
echo "net.ipv4.tcp_wmem=262144 114688000 131071000" >> /etc/sysctl.conf
echo "net.ipv4.tcp_rmem=262144 114688000 131071000" >> /etc/sysctl.conf
echo "net.core.netdev_max_backlog=10000" >> /etc/sysctl.conf
echo "net.ipv4.tcp_max_syn_backlog=10000" >> /etc/sysctl.conf
echo "net.core.somaxconn=4096" >> /etc/sysctl.conf
sysctl -p
```

#### Software

Test program: test/test.go

Configuration:
- Worker count (send goroutines): 8
- Single worker message buffer: 200,000 messages
- Data volume: 1,000,000 messages
- Single message size: 350B
- Test token bucket rate limit: 500,000 QPS
- Batch send log count: 20 messages
- Single send log bytes: 10K

### Data

#### UDP/V1/Synchronous
```shell
./test 
{"level":"info","ts":1678101917.0138125,"caller":"discoverer/dns.go:170","msg":"update domain host list dev.tglog.com: 127.0.0.1"}
{"level":"info","ts":1678101917.0346782,"caller":"tglog/client.go:337","msg":"client boot"}
version: v1
network: udp
rate: 500000
async: false
send time: 10.229377178
total time: 10.22937747
sent: 1000000
QPS: 97757.65953820062
success: 1000000
failed: 0
{"level":"info","ts":1678101930.5279508,"caller":"tglog/client.go:342","msg":"client shutdown"}
```

#### TCP/V1/Synchronous
```shell
./test -network tcp -port 20002
{"level":"info","ts":1678102160.9546793,"caller":"discoverer/dns.go:170","msg":"update domain host list dev.tglog.com: 127.0.0.1"}
{"level":"info","ts":1678102160.977543,"caller":"tglog/client.go:337","msg":"client boot"}
version: v1
network: tcp
rate: 500000
async: false
send time: 22.547490811
total time: 22.547491006
sent: 1000000
QPS: 44350.8326373828
success: 1000000
failed: 0
{"level":"info","ts":1678102186.962323,"caller":"tglog/client.go:342","msg":"client shutdown"}
```

#### UDP/V1/Asynchronous
```shell
./test -async
{"level":"info","ts":1678102264.2002535,"caller":"discoverer/dns.go:170","msg":"update domain host list dev.tglog.com: 127.0.0.1"}
{"level":"info","ts":1678102264.2173593,"caller":"tglog/client.go:337","msg":"client boot"}
version: v1
network: udp
rate: 500000
async: true
send time: 1.990934325
total time: 1.999216429
sent: 1000000
QPS: 500195.9695280195
success: 1000000
failed: 0
{"level":"info","ts":1678102269.2219534,"caller":"tglog/client.go:342","msg":"client shutdown"}
```

#### TCP/V1/Asynchronous
```shell
./test -network tcp -port 20002 -async
{"level":"info","ts":1678102306.7442007,"caller":"discoverer/dns.go:170","msg":"update domain host list dev.tglog.com: 127.0.0.1"}
{"level":"info","ts":1678102306.7653472,"caller":"tglog/client.go:337","msg":"client boot"}
version: v1
network: tcp
rate: 500000
async: true
send time: 1.991454805
total time: 1.9989812630000001
sent: 1000000
QPS: 500254.81404424744
success: 1000000
failed: 0
{"level":"info","ts":1678102312.2622147,"caller":"tglog/client.go:342","msg":"client shutdown"}
```

#### UDP/V3/Synchronous
```shell
./test -port 20003 -version v3
{"level":"info","ts":1690426441.4392853,"msg":"update domain host list dev.tglog.com: 127.0.0.1"}
{"level":"info","ts":1690426441.4584458,"msg":"client boot"}
version: v3
network: udp
rate: 500000
async: false
send time: 159.237631829
total time: 159.237631975
sent: 1000000
QPS: 6279.9225760717045
success: 1000000
failed: 0
{"level":"info","ts":1690426603.9774907,"msg":"client shutdown"}
```

#### TCP/V3/Synchronous
```shell
./test -network tcp -port 20004 -version v3
{"level":"info","ts":1690426637.2871974,"msg":"update domain host list dev.tglog.com: 127.0.0.1"}
{"level":"info","ts":1690426637.3054972,"msg":"client boot"}
version: v3
network: tcp
rate: 500000
async: false
send time: 164.919230376
total time: 164.919230559
sent: 1000000
QPS: 6063.574251531868
success: 1000000
failed: 0
{"level":"info","ts":1690426805.3286662,"msg":"client shutdown"}
```

#### UDP/V3/Asynchronous
```shell
./test -port 20003 -version v3 -async
{"level":"info","ts":1690426349.148999,"msg":"update domain host list dev.tglog.com: 127.0.0.1"}
{"level":"info","ts":1690426349.1617053,"msg":"client boot"}
version: v3
network: udp
rate: 500000
async: true
send time: 1.9908360809999999
total time: 1.9968420500000001
sent: 1000000
QPS: 500790.7360524584
success: 1000000
failed: 0
{"level":"info","ts":1690426354.661024,"msg":"client shutdown"}
```

#### TCP/V3/Asynchronous
```shell
./test -network tcp -port 20004 -version v3 -async
{"level":"info","ts":1690426385.8851142,"msg":"update domain host list dev.tglog.com: 127.0.0.1"}
{"level":"info","ts":1690426385.9020836,"msg":"client boot"}
version: v3
network: tcp
rate: 500000
async: true
send time: 1.995837593
total time: 2.003611851
sent: 1000000
QPS: 499098.6649938716
success: 1000000
failed: 0
{"level":"info","ts":1690426391.4043891,"msg":"client shutdown"}
```

> Notes:
> - All test data shows 100% completion rate
> - The performance difference between UDP and TCP in V1 synchronous mode is due to gnet network library's different handling: for UDP, it immediately calls sendTo(); for TCP, it constructs an asynchronous task, puts data into gnet's internal queue, then scheduled until written to kernel, which has a time delay.
> - The large performance gap between V3 and V1 synchronous protocols is because V3 needs to wait for responses - a request is only considered complete after receiving a response, then the next can be sent. V1 only sends without waiting for responses, and V3 requires PB encoding/decoding.

## FAQ

**Q: What is TGLog?**

A: TGLog is Tencent Game Log Service. TGLog = Tencent Game Log.

**Q: What is the TGLog log format?**

A: TGLog logs use "|" to separate fields and "\n" to separate log entries. Format: logname|value1|value2|...|valueX\n

Example: login|2023-02-28 17:00:00|a|b|c|d\n. If logs contain "|" or "\n", they need to be escaped as "%7C" and "%0D".

**Q: What's the difference between TGLog V1 and V3 protocols?**

A: V1 and V3 have the same log format but different application layer transport protocols. V1 transmits in plain text without response. V3 transmits in PB-encoded binary format, supports compression and encryption, has responses, suitable for stricter network environments. IEG games typically use V1 protocol.

**Q: Should I use TCP or UDP?**

A: IEG games typically use V1 protocol with UDP, mainly for historical reasons. The original IEG game server framework (tsf4g) was single-threaded, and for latency-sensitive games like MOBA and FPS, TCP reconnection and retransmission delays might affect user experience. However, for Go-based game development, GC latency impact should have been evaluated. For Go SDK users, UDP and TCP have the same concurrency model and latency, so TCP is recommended for data integrity. Please specify TCP protocol when applying for access.

**Q: When to use synchronous vs asynchronous sending?**

A: Depends on business requirements. Generally, synchronous mode cannot be concurrent and blocks, with relatively lower performance, especially with V3 client due to waiting for responses (see performance data above). Asynchronous sending is recommended in such cases.

**Q: Is the callback function required for asynchronous sending?**

A: No, it's only needed when you care about the send result. Pass nil if you don't care.

**Q: Why does it print so much information to the screen when running?**

A: The SDK's default debug logs print to screen. This usually happens when users haven't registered an external logging interface. You can call WithLogger() to register your application's logging interface. Both logrus and zap sugar logging objects work.