package main

import (
	"context"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"time"

	"go.uber.org/zap/zapcore"

	"github.com/juju/ratelimit"
	"go.uber.org/atomic"
	"go.uber.org/zap"

	"git.woa.com/tglog/v3/sdk-go/tglog"
)

var (
	network   string
	host      string
	port      int
	version   string
	rate      float64
	async     bool
	sendNum   int
	file      string
	token     string
	tokenType string
	auth      bool
	sign      bool
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
	flag.Float64Var(&rate, "rate", 500000, "request send rate")
	flag.BoolVar(&async, "async", false, "async send or not")
	flag.IntVar(&sendNum, "send-num", 1000000, "request send number")
	flag.StringVar(&file, "file", "./sendlogdemo.log", "log file to send")
	flag.StringVar(&token, "token", "AAAAAHeKGDV0ZXN04IVI3EAp3AJZqeIoVECe0lI41Tza205Tue28PKvLY-4", "auth token")
	flag.StringVar(&tokenType, "token-type", "tglog", "auth token type, bearer/tglog")
	flag.BoolVar(&auth, "auth", false, "add auth info or not")
	flag.BoolVar(&sign, "sign", false, "sign the request or not")
	flag.Parse()

	var client tglog.Client
	var err error
	cfg := zap.NewProductionConfig()
	cfg.DisableCaller = true
	cfg.DisableStacktrace = true
	log, err := cfg.Build()
	if err != nil {
		fmt.Println(err)
		return
	}

	if version == "v3" {
		client, err = tglog.NewV3Client(
			tglog.WithNetwork(network),
			tglog.WithHost(host),
			tglog.WithPort(port),
			tglog.WithLogger(log.Sugar().WithOptions(zap.AddStacktrace(zapcore.FatalLevel))),
			tglog.WithWorkerNum(8),
			tglog.WithMaxPendingMessages(200000),
			tglog.WithSocketSendBufferSize(16*1024*1024),
			tglog.WithSocketRecvBufferSize(16*1024*1024),
			tglog.WithWriteBufferSize(16*1024*1024),
			tglog.WithReadBufferSize(16*1024*1024),
			tglog.WithBatchingMaxMessages(20),
			tglog.WithBatchingMaxSize(10*1024),
			tglog.WithAuth(auth),
			tglog.WithSign(sign),
			tglog.WithToken(token),
			tglog.WithTokenType(tokenType),
		)
	} else {
		client, err = tglog.NewV1Client(
			tglog.WithNetwork(network),
			tglog.WithHost(host),
			tglog.WithPort(port),
			tglog.WithLogger(log.Sugar().WithOptions(zap.AddStacktrace(zapcore.FatalLevel))),
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

	bytes, err := os.ReadFile(file)
	if err != nil {
		fmt.Println(err)
		return
	}

	msgs, err := tglog.ParseMessages(bytes)
	if err != nil {
		fmt.Println(err)
		return
	}

	rl := ratelimit.NewBucketWithRate(rate, 5000)

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
