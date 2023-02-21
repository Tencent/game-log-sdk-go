package main

import (
	"context"
	"fmt"
	"time"

	"git.woa.com/tglog/v3/sdk-go/tglog"
)

func main() {
	client, err := tglog.NewClient(
		tglog.WithNetwork("udp"),
		tglog.WithHost("dev.tglog.com"),
		tglog.WithPort(20001),
	)

	if err != nil {
		fmt.Println(err)
		return
	}

	for i := 0; i < 10; i++ {
		client.Send(context.Background(), &tglog.Message{Name: "test", Payload: []byte("test|a|b|c")})
	}

	for i := 0; i < 10; i++ {
		client.SendAsync(context.Background(), &tglog.Message{Name: "test", Payload: []byte("test|d|e|f")}, nil)
	}
	time.Sleep(3 * time.Second)
	client.Close()
	time.Sleep(3 * time.Second)
}
