package main

import (
	"context"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"time"

	"github.com/juju/ratelimit"
	"go.uber.org/atomic"
	"go.uber.org/zap"

	"git.woa.com/tglog/v3/sdk-go/tglog"
)

var (
	network string
	host    string
	port    int
	version string
	rate    int
	async   bool
	sendNum int
)

func randMsg(msgs []tglog.Message) tglog.Message {
	l := len(msgs)
	if l == 0 {
		return tglog.Message{}
	}
	r := rand.Intn(l)
	return msgs[r]
}

func main() {
	flag.StringVar(&network, "network", "udp", "network to use, tcp/udp")
	flag.StringVar(&host, "host", "dev.tglog.com", "server domain name or ip")
	flag.IntVar(&port, "port", 20001, "server port")
	flag.StringVar(&version, "version", "v1", "tglog version to use, v1/v3")
	flag.IntVar(&rate, "rate", 500000, "request send rate")
	flag.BoolVar(&async, "async", false, "async send or not")
	flag.IntVar(&sendNum, "send-num", 1000000, "request send number")
	flag.Parse()

	var client tglog.Client
	var err error
	log, err := zap.NewProduction()
	if err != nil {
		fmt.Println(err)
		return
	}

	if version == "v3" {
		client, err = tglog.NewV3Client(
			tglog.WithNetwork(network),
			tglog.WithHost(host),
			tglog.WithPort(port),
			tglog.WithLogger(log.Sugar()),
			tglog.WithWorkerNum(8),
			tglog.WithMaxPendingMessages(200000),
			tglog.WithSocketSendBufferSize(16*1024*1024),
			tglog.WithSocketRecvBufferSize(16*1024*1024),
			tglog.WithWriteBufferSize(16*1024*1024),
			tglog.WithReadBufferSize(16*1024*1024),
			tglog.WithBatchingMaxMessages(20),
			tglog.WithBatchingMaxSize(10*1024),
		)
	} else {
		client, err = tglog.NewV1Client(
			tglog.WithNetwork(network),
			tglog.WithHost(host),
			tglog.WithPort(port),
			tglog.WithLogger(log.Sugar()),
			tglog.WithWorkerNum(8),
			tglog.WithMaxPendingMessages(200000),
			tglog.WithSocketSendBufferSize(16*1024*1024),
			tglog.WithSocketRecvBufferSize(16*1024*1024),
			tglog.WithWriteBufferSize(16*1024*1024),
			tglog.WithReadBufferSize(16*1024*1024),
			tglog.WithBatchingMaxMessages(20),
			tglog.WithBatchingMaxSize(10*1024),
		)
	}
	if err != nil {
		fmt.Println(err)
		return
	}

	bytes, err := os.ReadFile("./sendlogdemo.log")
	if err != nil {
		fmt.Println(err)
		return
	}

	msgs, err := tglog.ParseMessages(bytes)
	if err != nil {
		fmt.Println(err)
		return
	}

	rl := ratelimit.NewBucketWithRate(float64(rate), 5000)

	startTime := time.Now()
	var success atomic.Uint64
	var failed atomic.Uint64
	sent := 0
	if !async {
		for i := 0; i < sendNum; i++ {
			err = client.Send(
				context.Background(),
				randMsg(msgs),
			)
			if err != nil {
				failed.Add(1)
			} else {
				success.Add(1)
			}
		}
	} else {
		for {
			if rl.TakeAvailable(1) > 0 {
				client.SendAsync(
					context.Background(),
					randMsg(msgs),
					func(msg tglog.Message, err error) {
						if err != nil {
							failed.Add(1)
						} else {
							success.Add(1)
						}
					})
				sent++
				if sent >= sendNum {
					break
				}
			} else {
				time.Sleep(1 * time.Millisecond)
			}
		}
	}

	sendTime := time.Since(startTime).Seconds()
	for {
		if int(success.Load()+failed.Load()) >= sendNum {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	duration := time.Since(startTime).Seconds()
	fmt.Println("version:", version)
	fmt.Println("network:", network)
	fmt.Println("rate:", rate)
	fmt.Println("async:", async)
	fmt.Println("send time:", sendTime)
	fmt.Println("total time:", duration)
	fmt.Println("sent:", sendNum)
	fmt.Println("QPS:", float64(sendNum)/duration)
	fmt.Println("success:", success.Load())
	fmt.Println("failed:", failed.Load())
	time.Sleep(3 * time.Second)
	client.Close()
}
