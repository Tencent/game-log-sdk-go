package tglog

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand"
	"runtime"
	"sync"
	"time"

	v3 "git.woa.com/tglog/v3/proto/pbgo"
	"git.woa.com/tglog/v3/sdk-go/bufferpool"
	"git.woa.com/tglog/v3/sdk-go/crypto"
	"git.woa.com/tglog/v3/sdk-go/util"
	"github.com/gogo/protobuf/types"
	"github.com/klauspost/compress/snappy"
	"go.uber.org/atomic"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var (
	localIP    = ""
	platform   = runtime.GOOS
	language   = runtime.Version()
	sdkVersion = "v0.1.0"
	sequence   atomic.Uint64
	protoVer   = fmt.Sprintf("%d.%d.%d", v3.ProtoVer_MAJOR, v3.ProtoVer_MINOR, v3.ProtoVer_PATCH)
	v3ReqPool  *sync.Pool
	v3RspPool  *sync.Pool
)

func init() {
	var err error
	localIP, err = util.GetFirstPrivateIP()
	if err != nil {
		localIP = "127.0.0.1"
	}

	sequence.Store(rand.New(rand.NewSource(time.Now().UnixNano())).Uint64())
	v3ReqPool = &sync.Pool{
		New: func() interface{} {
			return &v3.Req{}
		},
	}
	v3RspPool = &sync.Pool{
		New: func() interface{} {
			return &v3.Rsp{}
		},
	}
}

// EncodeV1 encodes messages into TGLog v1 request bytes
func EncodeV1(messages []Message, bb *bytes.Buffer) ([]byte, error) {
	if bb == nil {
		bb = &bytes.Buffer{}
	}

	for i := 0; i < len(messages); i++ {
		bb.Write(messages[i].Payload)
		if messages[i].Payload[len(messages[i].Payload)-1] != '\n' {
			bb.WriteByte('\n')
		}
	}
	return bb.Bytes(), nil
}

func nextSeq() uint64 {
	seq := sequence.Load()
	sequence.Add(1)
	return seq
}

// BuildV3HeartbeatReq builds a TGLog v3 heartbeat request
func BuildV3HeartbeatReq(appID, appName, appVer, network, reqID, token string, req *v3.Req) (*v3.Req, error) {
	ts := timestamppb.Now()
	if req == nil {
		req = &v3.Req{}
	} else {
		req.Reset()
	}

	req.Header = &v3.ReqHeader{
		Context: &v3.Context{
			AppID:    appID,
			AppName:  appName,
			AppVer:   appVer,
			SdkLang:  language,
			SdkVer:   sdkVersion,
			SdkOS:    platform,
			Network:  network,
			ProtoVer: protoVer,
			HostIP:   localIP,
		},
		ReqID: reqID,
		Ts:    &types.Timestamp{Seconds: ts.Seconds, Nanos: ts.Nanos},
		Token: token,
		Sig:   "", // todo sig
	}
	req.Req = &v3.Req_HeartbeatReq{
		HeartbeatReq: &v3.HeartbeatReq{Ping: &types.Timestamp{Seconds: ts.Seconds, Nanos: ts.Nanos}},
	}

	return req, nil
}

// BuildV3LogReq builds a TGLog v3 log request
func BuildV3LogReq(appID, appName, appVer, network, reqID, token string, messages []Message,
	labels map[string]string, annotations map[string]string, req *v3.Req) (*v3.Req, error) {
	if len(messages) == 0 {
		return nil, errors.New("input message slice is empty")
	}

	pbLogs := v3.Logs{
		Logs: make([]*v3.Log, 0, len(messages)),
	}

	for _, msg := range messages {
		pbLogs.Logs = append(pbLogs.Logs, &v3.Log{Name: msg.Name, Content: util.BytesToString(msg.Payload), Seq: nextSeq()})
	}

	logReq := &v3.Req_LogReq{
		LogReq: &v3.LogReq{
			Meta: &v3.Meta{
				Labels:      labels,
				Annotations: annotations,
			},
			Logs: &pbLogs,
		},
	}

	ts := timestamppb.Now()

	if req == nil {
		req = &v3.Req{}
	} else {
		req.Reset()
	}

	req.Header = &v3.ReqHeader{
		Context: &v3.Context{
			AppID:    appID,
			AppName:  appName,
			AppVer:   appVer,
			SdkLang:  language,
			SdkVer:   sdkVersion,
			SdkOS:    platform,
			Network:  network,
			ProtoVer: protoVer,
			HostIP:   localIP,
		},
		ReqID: reqID,
		Ts:    &types.Timestamp{Seconds: ts.Seconds, Nanos: ts.Nanos},
		Token: token,
		Sig:   "", // todo sig
	}
	req.Req = logReq

	return req, nil
}

// EncodeV3Req encodes a TGLog v3 request into bytes
func EncodeV3Req(req *v3.Req, noFrameHeader, compress, encrypt bool, encryptKey string, bb *bytes.Buffer, littleEndian bool) ([]byte, error) {
	if req == nil {
		return nil, errors.New("input request is nil")
	}

	if bb == nil {
		bb = &bytes.Buffer{}
	}

	reqPayload, err := req.Marshal()
	if err != nil {
		return nil, err
	}

	// 不压缩、不加密、无帧头，直接返回
	if noFrameHeader && !compress && !encrypt {
		bb.Grow(len(reqPayload))
		bb.Write(reqPayload)
		return bb.Bytes(), nil
	}

	var flags uint8
	var payload = reqPayload
	// 先压缩，大于512字节再压缩
	if compress && len(payload) > 512 {
		flags = flags | uint8(v3.Flag_FLAG_COMPRESSED)
		payload = snappy.Encode(nil, payload)
	}

	// 再加密
	if encrypt {
		flags = flags | uint8(v3.Flag_FLAG_ENCRYPTED)
		key := []byte(encryptKey)
		payload, err = crypto.AesEncrypt(payload, key)
		if err != nil {
			return nil, err
		}
	}

	frameHeaderLen := int(v3.Len_MAGIC + v3.Len_PACKAGE + v3.Len_FLAGS + v3.Len_RESERVE)
	length := frameHeaderLen + len(payload)
	bb.Grow(length)

	// write magic
	magicBuf := make([]byte, 2)
	binary.BigEndian.PutUint16(magicBuf, uint16(v3.Magic_VAL))
	_, err = bb.Write(magicBuf)
	if err != nil {
		return nil, err
	}

	// write length
	var byteOrder binary.ByteOrder = binary.BigEndian
	if littleEndian {
		byteOrder = binary.LittleEndian
	}

	lengthBuf := make([]byte, 4)
	byteOrder.PutUint32(lengthBuf, uint32(length))
	_, err = bb.Write(lengthBuf)
	if err != nil {
		return nil, err
	}

	// write flags&reserved 4 bytes
	_, err = bb.Write([]byte{flags, 0x00, 0x00, 0x00})
	if err != nil {
		return nil, err
	}

	// write payload
	_, err = bb.Write(payload)
	if err != nil {
		return nil, err
	}
	return bb.Bytes(), nil
}

// DecodeV3Rsp decode a byte frame into TGLog v3 response
func DecodeV3Rsp(frame []byte, noFrameHeader, verifyMagic bool, bytesToStrip int, encryptKey string,
	bp bufferpool.BytePool, rsp *v3.Rsp) (*v3.Rsp, error) {
	if len(frame) == 0 {
		return nil, errors.New("input frame is empty")
	}

	var compressed, encrypted bool
	var err error
	payload := frame
	// 有帧头
	if !noFrameHeader {
		if verifyMagic {
			magic := binary.BigEndian.Uint16(frame[0:v3.Len_MAGIC])
			if magic != uint16(v3.Magic_VAL) {
				return nil, errors.New("invalid tglog v3 message")
			}
		}
		flags := frame[v3.Len_MAGIC+v3.Len_PACKAGE : v3.Len_MAGIC+v3.Len_PACKAGE+v3.Len_FLAGS][0]
		if flags&uint8(v3.Flag_FLAG_COMPRESSED) > 0 {
			compressed = true
		}
		if flags&uint8(v3.Flag_FLAG_ENCRYPTED) > 0 {
			encrypted = true
		}
		payload = frame[bytesToStrip:]
	}

	if rsp == nil {
		rsp = &v3.Rsp{}
	} else {
		rsp.Reset()
	}

	// 未加密未压缩，直接解包
	if !compressed && !encrypted {
		err = rsp.Unmarshal(payload) // proto.Unmarshal(payload, rsp)
		if err != nil {
			return nil, errors.New("unmarshal failed")
		}
	} else {
		buf := payload
		// 先解密
		if encrypted {
			key := util.StringToBytes(encryptKey)
			buf, err = crypto.AesDecrypt(buf, key)
			if err != nil {
				return nil, errors.New("decrypt failed")
			}
		}
		// 再解压
		if compressed {
			var decodedLen int
			decodedLen, err = snappy.DecodedLen(buf)
			if err != nil {
				return nil, errors.New("decompress failed")
			}

			var decompressBuf []byte
			if bp != nil {
				decompressBuf = bp.Get()
			}

			if cap(decompressBuf) < decodedLen {
				decompressBuf = make([]byte, decodedLen)
			}

			if bp != nil {
				defer bp.Put(decompressBuf) //nolint:staticcheck
			}

			buf, err = snappy.Decode(decompressBuf, buf)
			if err != nil {
				return nil, errors.New("decompress failed")
			}
		}

		err = rsp.Unmarshal(buf) // proto.Unmarshal(buf, rsp)
		if err != nil {
			return nil, errors.New("unmarshal failed")
		}
	}

	return rsp, nil
}
