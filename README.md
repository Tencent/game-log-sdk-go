# sdk-go



##  sdk-go是什么？

sdk-go是TGLog服务的go语言版本SDK，它负责将TGLog日志以特定的协议和格式上报到TGLog服务器。

==**<font color=red>重要说明：鉴于同步接口的性能比异步接口相差太多，特别是V3协议，请大家使用异步接口。</font>**==

==**<font color=red>重要说明：v0.4.0版本默认开启了UDP探测，由于生产环境服务器都绑在CLB后面，探测不生效，使用了此版本的用户请调用`WithNoUDPProbe()`禁用它或者升级到v0.4.1版本。</font>**==


## 特性

- 支持域名后端RS刷新；
- 支持同步上报；
- 支持批量异步上报；
- 兼容TGLogV1协议；
- 支持TGLogV3协议；
- 支持压缩与加密（V3协议）；
- 支持优雅关闭；



## 使用方法

详细示例请参考：tglog/example_test.go或者：test/test.go。

### 示例

```go
package tglog_test

import (
	"context"
	"fmt"
	"time"

	"git.woa.com/tglog/v3/sdk-go/tglog"
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
					success.Add(1)
				} else {
					failed.Add(1)
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

### 配置项

参见：

- tglog/options.go：所有配置项；
- tglog/options_basic.go：基础配置函数；
- tglog/options_v3.go：V3协议相关配置函数。

#### V3协议特性配置

#### 开启压缩

```go
WithCompress(true)
```

##### 开启加密

```go
WithEncrypt(true)
WithEncryptKey("1234567890abcdef")
```

##### 开启鉴权

```go
WithToken("1234567890abcdef")
WithTokenType("tglog")
WithAuth(true)
```

> 说明：开启鉴权必须开启加密，否则token会泄漏。

##### 开启签名

```go
WithSign(true)
```

> 说明：开启签名必须开启加密和鉴权，否则token会泄漏，且没有可用于签名的token。

### 版本选择

- V1版本。数据以明文形式发送，没有响应；适用于内网环境，腾讯游戏通常选V1版本；

- V3版本。数据会用ProtoBuffer编码后再发送，有响应，支持加密、压缩、鉴权、签名。适用于更严格的网络环境，特殊情况选用。

### 发送模式选择

- 同步模式。发送完一个请求，收到响应（如果有）再发下一个请求，无并发；不推荐选用；
- 异步模式。发送完一个请求，可以继续发送一下个，响应（如果有）回来时，通过回调通知发送逻辑，支持并发。建议选用。

### 通信协议选择

- UDP。无连接、低延迟，适用于内网环境，腾讯游戏常用；
- TCP。有连接，可靠，但延迟可能会更高，适用于更严格的网络环境，特殊情况选用。

### 其他

- 压缩。只有V3版本支持，要求有配套的V3版本的服务器；
- 加密。只有V3版本支持，要求有配套的V3版本的服务器，双方要配置相同的密钥；
- 鉴权。只有V3版本支持，要求有配套的V3版本的服务器，开启鉴权需要同时开启加密，所以双方要配置相同的密钥，同时客户端需要配置鉴权token，服务器要配置鉴权密钥；
- 签名。只有V3版本支持，要求有配套的V3版本的服务器，开启签名需要使用到鉴权token，所以需要同时开启鉴权和加密，所以双方要配置相同的密钥，同时客户端需要配置鉴权token，服务器要配置鉴权密钥。

## 性能

### 环境

#### 硬件

客户端：16C32G/tlinux4

服务器：16C64G/tlinux2

内核参数：

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

#### 软件

测试程序：test/test.go

配置：

- 工作者数量（发送协程）：8个；
- 单工作者消息缓冲：200000条；
- 数据发送量：1000,000条；
- 单条数据大小：350B；
- 测试令牌桶限速：500000QPS；
- 单批发送日志条数：20条；
- 单发送日志字节数：10K；

### 数据

#### UDP/V1/同步

```shell
[gunli@VM-115-72-centos /data/home/gunli/workspace/go/tglog/sdk-go/test]$ ./test 
{"level":"info","ts":1678101917.0138125,"caller":"discoverer/dns.go:170","msg":"update domain host list dev.tglog.com: 9.134.68.216"}
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

#### TCP/V1/同步

```shell
[gunli@VM-115-72-centos /data/home/gunli/workspace/go/tglog/sdk-go/test]$ ./test -network tcp -port 20002
{"level":"info","ts":1678102160.9546793,"caller":"discoverer/dns.go:170","msg":"update domain host list dev.tglog.com: 9.134.68.216"}
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

#### UDP/V1/异步
```shell
[gunli@VM-115-72-centos /data/home/gunli/workspace/go/tglog/sdk-go/test]$ ./test -async
{"level":"info","ts":1678102264.2002535,"caller":"discoverer/dns.go:170","msg":"update domain host list dev.tglog.com: 9.134.68.216"}
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

#### TCP/V1/异步
```shell
[gunli@VM-115-72-centos /data/home/gunli/workspace/go/tglog/sdk-go/test]$ ./test -network tcp -port 20002 -async
{"level":"info","ts":1678102306.7442007,"caller":"discoverer/dns.go:170","msg":"update domain host list dev.tglog.com: 9.134.68.216"}
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

#### UDP/V3/同步

```shell
[gunli@VM-115-72-centos /data/home/gunli/workspace/go/tglog/sdk-go/test]$ ./test -port 20003 -version v3
{"level":"info","ts":1690426441.4392853,"msg":"update domain host list dev.tglog.com: 9.134.68.216"}
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


#### TCP/V3/同步
```shell
[gunli@VM-115-72-centos /data/home/gunli/workspace/go/tglog/sdk-go/test]$ ./test -network tcp -port 20004 -version v3
{"level":"info","ts":1690426637.2871974,"msg":"update domain host list dev.tglog.com: 9.134.68.216"}
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

#### UDP/V3/异步
```shell
[gunli@VM-115-72-centos /data/home/gunli/workspace/go/tglog/sdk-go/test]$ ./test -port 20003 -version v3 -async
{"level":"info","ts":1690426349.148999,"msg":"update domain host list dev.tglog.com: 9.134.68.216"}
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

#### TCP/V3/异步
```shell
[gunli@VM-115-72-centos /data/home/gunli/workspace/go/tglog/sdk-go/test]$ ./test -network tcp -port 20004 -version v3 -async
{"level":"info","ts":1690426385.8851142,"msg":"update domain host list dev.tglog.com: 9.134.68.216"}
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


> 说明：
>
> - 以上测试数据完整率100%；
> - V1协议同步UDP与TCP性能相差大是因为gnet网络库对UDP和TCP的处理方式不一样，对于UDP，它立即调用sendTo()发送，对于TCP，它会构造一个异步的任务，将数据放入gnet内部队列，再由调度器调度直到最终写入内核，这个调度有个时间差；
> - V3协议与V1协议的同步性能相差如此之大，是因为V3协议需要等待响应，收到响应才认为一个请求结束，才能发送一下个请求，而V1版本只管发送，不收响应，且V3需要进行PB编解码；



## FAQ

Q：什么是TGLog？

A：TGLog是腾讯游戏日志服务。TGLog=Tencent Game Log。



Q：TGLog日志格式是什么样的？

A：TGLog日志是一种以"|"分隔字段，以换行符"\n"分隔2条日志的文本，格式为：日志名|值1|值2|...|值x\n。

如：login|2023-02-28 17:00:00|a|b|c|d\n。如果日志中有"|"或者"\n"，需要转义成""%7C"和"%0D"。



Q：TGLog V1协议和V3协议有什么区别？

A：V1版本和V3版本的日志格式是一样的，只是应用层传输协议不一样，V1版本直接以明文形式传输，没有响应，V3版本以PB编码后的二进制形式传输，支持压缩、加密，有响应，适用于更严格的网络环境。IEG内的游戏业务通常使用V1协议UDP上报即可。



Q：什么使用同步发送，什么时候使用异步发送？

A：看业务需求，一般而言，同步无法并发且会阻塞，性能相对较低，特别是使用V3版本的Client时，因为需要等待响应，性能较差（参见上面性能数据），此时建议使用异步发送。



Q：异步发送时回调函数是必须的吗？

A：不是的，只有你需要关注发送结果时才需要传入回调函数，如果不关心，传入nil即可。



Q：为什么运行的时候会打印很多信息到屏幕上？

A：因为SDKl默认的调试日志是打印到屏幕上的，出现这种情况一般是使用者没有注册外部的日志接口进来，可以调用WithLogger()函数注册一个应用自己的日志接口进来，logrus或者zap的sugar日志对象都可以。



Q：这个SDK可以共享给代理的第三方业务团队吗？

A：可以，但是公司的git仓库不对外开放，可以通过下载的方式共享给外部，同时，这个仓库还依赖于V3协议仓库，需要把协议仓库的代码也一起下载并共享。协议仓库路径：https://git.woa.com/tglog/v3/proto。