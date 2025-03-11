package tglog

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/tencent/game-log-sdk-go/util"
)

// const values
const (
	// TimeFormat is the TGLog time format
	TimeFormat = "2006-01-02 15:04:05"
)

// Message is the TGLog log record, it includes a name and its content
type Message struct {
	Name     string            // 事件名/日志名
	Payload  []byte            // 内容
	Headers  map[string]string // 消息头，预留，目前不会发到服务器
	MetaData interface{}       // 可以携带任意数据，这些数据不会发到服务器，可以在回调中获取，方便应用层存储一些上下文
}

// ParseMessages parses a byte slice into Message slice,
// it is useful for parsing the content of a file into Message slice
func ParseMessages(raw []byte) ([]Message, error) {
	rawMessages := bytes.Split(raw, []byte{'\n'})
	output := make([]Message, 0, len(rawMessages))
	for _, r := range rawMessages {
		index := bytes.Index(r, []byte{'|'})
		if index <= 0 {
			continue
		}
		name := r[0:index]
		newEvent := Message{Name: util.BytesToString(name), Payload: append(r, '\n')}
		output = append(output, newEvent)
	}

	if len(output) == 0 {
		return nil, errors.New("no messages")
	}

	return output, nil
}

// ToTGLogString converts an object into a TGLog format string.
// Note that this func uses reflect to traverse the fields of an object, it is recommended that you
// implement you own converting function for your objects in the case that performance is critical.
func ToTGLogString(obj interface{}) string {
	r := reflect.ValueOf(obj)
	n := reflect.TypeOf(obj).Name()

	var sb strings.Builder
	sb.WriteString(n)

	fieldNum := r.NumField()
	for i := 0; i < fieldNum; i++ {
		sb.WriteString("|")
		f := r.Field(i).Interface()
		sb.WriteString(toString(f))
	}
	sb.WriteString("\n")

	return sb.String()
}

// ToTGLogMessage converts an object into a TGLog Message
func ToTGLogMessage(obj interface{}) Message {
	r := reflect.ValueOf(obj)
	n := reflect.TypeOf(obj).Name()

	var bb bytes.Buffer
	bb.Grow(512) // 日志通常在500个字节左右
	bb.WriteString(n)

	fieldNum := r.NumField()
	for i := 0; i < fieldNum; i++ {
		bb.WriteString("|")
		f := r.Field(i).Interface()
		bb.WriteString(toString(f))
	}
	bb.WriteString("\n")

	return Message{
		Name:    n,
		Payload: bb.Bytes(),
	}
}

// GetTGLogName gets the name from a TGLog string,
// input: name|a|b|c, output: name
func GetTGLogName(log string) (string, error) {
	index := strings.Index(log, "|")
	if index <= 0 {
		return "", errors.New("no name")
	}
	return log[0:index], nil
}

// toString
func toString(f interface{}) string {
	switch t := f.(type) {
	case string:
		return t
	case []byte:
		return hex.EncodeToString(t)
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case int32:
		return strconv.FormatInt(int64(t), 10)
	case int16:
		return strconv.FormatInt(int64(t), 10)
	case int8:
		return strconv.FormatInt(int64(t), 10)
	case uint:
		return strconv.FormatUint(uint64(t), 10)
	case uint64:
		return strconv.FormatUint(t, 10)
	case uint32:
		return strconv.FormatUint(uint64(t), 10)
	case uint16:
		return strconv.FormatUint(uint64(t), 10)
	case uint8:
		return strconv.FormatUint(uint64(t), 10)
	case float64:
		return strconv.FormatFloat(t, 'f', 3, 64)
	case float32:
		return strconv.FormatFloat(float64(t), 'f', 3, 64)
	case bool:
		return strconv.FormatBool(t)
	case time.Time:
		return t.Format(TimeFormat)
	case *time.Time:
		return t.Format(TimeFormat)
	case time.Duration:
		return t.String()
	default:
		return fmt.Sprintf("%v", f)
	}
}
