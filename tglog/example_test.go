package tglog_test

import (
	"context"
	"fmt"
	"testing"
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

type tglogHelperRecord struct {
	Name   string
	hidden int
	When   *time.Time
}

func TestToTGLogHelpersHandlePointerAndUnexportedField(t *testing.T) {
	record := &tglogHelperRecord{Name: "ok", hidden: 1}
	want := "tglogHelperRecord|ok||\n"

	if got := tglog.ToTGLogString(record); got != want {
		t.Fatalf("ToTGLogString() = %q, want %q", got, want)
	}

	msg := tglog.ToTGLogMessage(record)
	if msg.Name != "tglogHelperRecord" {
		t.Fatalf("ToTGLogMessage().Name = %q", msg.Name)
	}
	if got := string(msg.Payload); got != want {
		t.Fatalf("ToTGLogMessage().Payload = %q, want %q", got, want)
	}
}

func TestToTGLogHelpersHandleNilAndNonStruct(t *testing.T) {
	var record *tglogHelperRecord
	if got := tglog.ToTGLogString(record); got != "tglogHelperRecord\n" {
		t.Fatalf("ToTGLogString(nil pointer) = %q", got)
	}

	if got := tglog.ToTGLogString(1); got != "int\n" {
		t.Fatalf("ToTGLogString(non-struct) = %q", got)
	}

	if got := tglog.ToTGLogString(nil); got != "\n" {
		t.Fatalf("ToTGLogString(nil) = %q", got)
	}
}
