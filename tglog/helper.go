package tglog

import (
	"encoding/hex"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unsafe"
)

// const values
const (
	// TimeFormat is the TGLog time format
	TimeFormat = "2006-01-02 15:04:05"
)

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

// BytesToString without copy
func BytesToString(bytes []byte) string {
	return *(*string)(unsafe.Pointer(&bytes))
}

// StringToBytes without copy
func StringToBytes(str string) []byte {
	x := (*[2]uintptr)(unsafe.Pointer(&str))
	h := [3]uintptr{x[0], x[1], x[1]}
	return *(*[]byte)(unsafe.Pointer(&h))
}
