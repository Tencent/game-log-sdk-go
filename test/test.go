package main

import (
	"context"
	"fmt"
	"git.woa.com/tglog/v3/sdk-go/tglog"
	"time"
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
		client.Send(context.Background(), "test|a|b|c")
	}

	time.Sleep(3 * time.Second)
	client.Close()
}
