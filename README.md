# sdk-go



##  sdk-go是什么？

sdk-go是TGLog服务的go语言版本SDK，它负责将TGLog日志以特定的协议和格式上报到TGLog服务器。



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



## 性能

### 环境

#### 硬件

客户端：16C32G/tlinux4

服务器：16C64G/tlinux2

#### 软件

测试程序：test/test.go

配置：

- 工作者数量（发送协程）：8；
- 单工作者消息缓冲：200000；
- 数据发送量：1000,000条；
- 单条数据大小：350B；
- 测试令牌桶限速：500000；

### 数据

#### UDP/V1/同步

```shell
rate: 500000
send time: 10.496602519
total time: 10.496624936
sent: 1000000
QPS: 95268.71790667933
success: 1000000
failed: 0
```

#### TCP/V1/同步

```shell
rate: 500000
send time: 22.862611294
total time: 22.862634889
sent: 1000000
QPS: 43739.49043297431
success: 1000000
failed: 0
```

#### UDP/V3/同步

```shell
rate: 500000
send time: 779.226950438
total time: 779.226973905
sent: 1000000
QPS: 1283.323131113677
success: 1000000
failed: 0
```


#### TCP/V3/同步
```shell
rate: 500000
send time: 827.179308563
total time: 827.179331024
sent: 1000000
QPS: 1208.9276925743031
success: 1000000
failed: 0
```

#### UDP/V1/异步
```shell
rate: 500000
send time: 1.990584709
total time: 1.997754459
sent: 1000000
QPS: 500562.01626528316
success: 1000000
failed: 0
```

#### TCP/V1/同步
```shell
rate: 500000
send time: 1.991016065
total time: 1.9992270840000002
sent: 1000000
QPS: 500193.3037037627
success: 1000000
failed: 0
```

#### UDP/V3/异步
```shell
rate: 500000
send time: 1.991210455
total time: 1.997472665
sent: 1000000
QPS: 500632.6331879991
success: 1000000
failed: 0
```

#### TCP/V3/同步
```shell
rate: 500000
send time: 1.990421295
total time: 4.695109748
sent: 1000000
QPS: 212987.566568806
success: 1000000
failed: 0
```


> 说明：
>
> - V1协议同步UDP与TCP性能相差大是因为gnet网络库对UDP和TCP的处理方式不一样，对于UDP，它立即调用sendTo()发送，对于TCP，它会构造一个异步的任务，将数据放入应用层队列，再由调度器调度直到最终写入内核，这个调度有个时间差；
> - V3协议的同步性能与V1版本相差如此之大，是因为V3协议需要等待响应，收到响应才认为一个请求结束，才能发送一下个请求，而V1版本只管发送，不收响应，且V3需要进行PB编解码；
> - V3协议异步UDP与TCP性能相差大主要还是gnet网络库对UDP和TCP的处理方式不一样引起的，另外，UDP接收方也无须拆包，当把批次发送的数据量调大时（每批20条或者10K日志以减少拆包次数），TCP也能以较高的速率发送数据（QPS：35W）。



## FAQ

Q：什么是TGLog？

A：TGLog是腾讯游戏日志服务。TGLog=Tencent Game Log。



Q：TGLog日志格式是什么样的？

A：TGLog日志是一种以"|"分隔字段，以换行符"\n"分隔2条日志的文本，格式为：日志名|值1|值2|...|值x\n。

如：login|2023-02-28 17:00:00|a|b|c|d\n。



Q：TGLog V1协议和V3协议有什么区别？

A：V1版本和V3版本的日志格式是一样的，只是应用层传输协议不一样，V1版本直接以明文形式传输，没有响应，V3版本以PB编码后的二进制形式传输，支持压缩、加密，有响应，适用于更严格的网络环境。



Q：什么使用同步发送，什么时候使用异步发送？

A：看业务需求，一般而言，同步无法并发且会阻塞，性能相对较低，特别是使用V3版本的Client时，因为需要等待响应，性能较差（参见上面性能数据），此时建议使用异步发送。



Q：异步发送时回调函数是必须的吗？

A：不是的，只有你需要关注发送结果时才需要传入回调函数，如果不关心，传入nil即可。



Q：为什么运行的时候会打印很多信息到屏幕上？

A：因为SDKl默认的调试日志是打印到屏幕上的，出现这种情况一般是使用者没有注册外部的日志接口进来，可以调用WithLogger()函数注册一个应用自己的日志接口进来，logrus或者zap的sugar日志对象都可以。