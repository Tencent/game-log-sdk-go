package main

import (
	"fmt"
	"go.uber.org/atomic"
	"math"
)

func main() {
	/*
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
	*/
	var index atomic.Uint64
	index.Store(math.MaxUint64)
	fmt.Println(index.Load())
	index.Add(1)
	fmt.Println(index.Load())
}
