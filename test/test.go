package main

import (
	"context"
	"flag"
	"fmt"
	"math/rand"
	"net/http"
	_ "net/http/pprof" // import pprof handlers
	"os"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap/zapcore"

	"github.com/juju/ratelimit"
	"go.uber.org/atomic"
	"go.uber.org/zap"

	"github.com/tencent/game-log-sdk-go/tglog"
)

var (
	network      string
	host         string
	port         int
	version      string
	rate         float64
	sync         bool
	sendNum      int
	file         string
	token        string
	tokenType    string
	auth         bool
	sign         bool
	compress     bool
	encrypt      bool
	encryptKey   string
	appID        string
	perf         bool
	metrics      bool
	connLifetime int
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
	flag.BoolVar(&sync, "sync", false, "send synchronously or not")
	flag.IntVar(&sendNum, "send-num", 1000000, "request send number")
	flag.StringVar(&file, "file", "./sendlogdemo.log", "log file to send")
	flag.StringVar(&token, "token", "AAAAAHeKGDV0ZXN04IVI3EAp3AJZqeIoVECe0lI41Tza205Tue28PKvLY-4", "auth token")
	flag.StringVar(&tokenType, "token-type", "tglog", "auth token type, bearer/tglog")
	flag.BoolVar(&auth, "auth", false, "add auth info or not")
	flag.BoolVar(&sign, "sign", false, "sign the request or not")
	flag.BoolVar(&compress, "compress", false, "compress or not")
	flag.BoolVar(&encrypt, "encrypt", false, "encrypt or not")
	flag.StringVar(&encryptKey, "encrypt-key", "0123456789ABCDEF", "encrypt key")
	flag.StringVar(&appID, "app-id", "test", "app ID")
	flag.BoolVar(&perf, "perf", false, "enable perf or not")
	flag.BoolVar(&metrics, "metrics", false, "enable metrics or not")
	flag.IntVar(&connLifetime, "conn-life", 0, "max conn lifetime")
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
			tglog.WithMaxPendingMessages(100000),
			tglog.WithSocketSendBufferSize(4*1024*1024),
			tglog.WithSocketRecvBufferSize(4*1024*1024),
			tglog.WithWriteBufferSize(4*1024*1024),
			tglog.WithReadBufferSize(4*1024*1024),
			tglog.WithBatchingMaxMessages(20),
			tglog.WithBatchingMaxSize(10*1024),
			tglog.WithAuth(auth),
			tglog.WithSign(sign),
			tglog.WithToken(token),
			tglog.WithTokenType(tokenType),
			tglog.WithCompress(compress),
			tglog.WithEncrypt(encrypt),
			tglog.WithEncryptKey(encryptKey),
			tglog.WithAppID(appID),
			tglog.WithBufferPoolSize(4096),
			tglog.WithBytePoolSize(4096),
			tglog.WithMaxConnLifetime(time.Duration(connLifetime)*time.Minute),
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
			tglog.WithMaxRetries(3),
			tglog.WithMaxConnLifetime(time.Duration(connLifetime)*time.Minute),
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

	// perf
	if perf || metrics {
		go func() {
			if metrics {
				http.Handle("/metrics", promhttp.Handler())
			}
			fmt.Println(http.ListenAndServe(":6060", nil))
		}()
	}

	rl := ratelimit.NewBucketWithRate(rate, int64(rate)+1)

	startTime := time.Now()
	var success atomic.Uint64
	var failed atomic.Uint64
	sent := 0
	if sync {
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
		callback := func(msg tglog.Message, err error) {
			if err != nil {
				failed.Add(1)
				fmt.Println(err)
			} else {
				success.Add(1)
			}
		}
		for {
			if rl.TakeAvailable(1) > 0 {
				client.SendAsync(
					context.Background(),
					randMsg(msgs),
					callback)
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
	fmt.Println("sync:", sync)
	fmt.Println("send time:", sendTime)
	fmt.Println("total time:", duration)
	fmt.Println("sent:", sendNum)
	fmt.Println("QPS:", float64(sendNum)/duration)
	fmt.Println("success:", success.Load())
	fmt.Println("failed:", failed.Load())
	time.Sleep(3 * time.Second)

	client.Close()
	if perf || metrics {
		select {}
	}
}
