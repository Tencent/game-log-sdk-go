package main

import (
	"context"
	"fmt"
	"time"

	"git.woa.com/tglog/v3/sdk-go/tglog"
)

func main() {
	client, err := tglog.NewV3Client(
		tglog.WithNetwork("tcp"),
		tglog.WithHost("dev.tglog.com"),
		tglog.WithPort(20004),
	)

	if err != nil {
		fmt.Println(err)
		return
	}

	for i := 0; i < 1000; i++ {
		client.Send(context.Background(), tglog.Message{Name: "test", Payload: []byte("test|a|b|c")})
		time.Sleep(1 * time.Second) // 在这里休眠是为了测试发包过程中修改DNS中的RS时连接能否正更新
	}

	for i := 0; i < 10; i++ {
		client.SendAsync(context.Background(), tglog.Message{Name: "test", Payload: []byte("test|d|e|f")}, nil)
	}
	time.Sleep(3 * time.Second)
	client.Close()
	time.Sleep(3 * time.Second)

}
