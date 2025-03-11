package tglog

import (
	"bytes"
	"crypto/md5"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math/rand"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/tencent/game-log-sdk-go/bufferpool"
	"github.com/tencent/game-log-sdk-go/crypto"
	"github.com/tencent/game-log-sdk-go/util"

	"github.com/gogo/protobuf/types"
	"github.com/klauspost/compress/snappy"
	v3 "github.com/tencent/game-log-sdk-proto/pbgo"
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
	// V3HeaderPool is the v3 header pool
	V3HeaderPool *sync.Pool
	// V3ReqPool is the v3 request pool
	V3ReqPool *sync.Pool
	// V3RspPool v3 response pool
	V3RspPool *sync.Pool
)

func init() {
	var err error
	localIP, err = util.GetFirstPrivateIP()
	if err != nil {
		localIP = "127.0.0.1"
	}

	sequence.Store(rand.New(rand.NewSource(time.Now().UnixNano())).Uint64())
	V3HeaderPool = &sync.Pool{
		New: func() interface{} {
			return &v3.ReqHeader{}
		},
	}

	V3ReqPool = &sync.Pool{
		New: func() interface{} {
			return &v3.Req{}
		},
	}

	V3RspPool = &sync.Pool{
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
func BuildV3HeartbeatReq(appID, appName, appVer, network, reqID, token, tokenType string, appMetaData []byte,
	header *v3.ReqHeader, body *v3.Req) (*v3.ReqHeader, *v3.Req, error) {
	ts := timestamppb.Now()
	if body == nil {
		body = &v3.Req{}
	} else {
		body.Reset()
	}

	// header can be nil for a heartbeat request
	if header != nil {
		header.Reset()

		header.AppID = appID
		header.AppName = appName
		header.AppVer = appVer
		header.SdkLang = language
		header.SdkVer = sdkVersion
		header.SdkOS = platform
		header.Network = network
		header.ProtoVer = protoVer
		header.HostIP = localIP
		header.Ts = &types.Timestamp{Seconds: ts.Seconds, Nanos: ts.Nanos}
		header.Token = token
		header.TokenType = tokenType
		header.Sig = "" // we will sign when encoding
	}

	body.ReqID = reqID
	body.AppMetaData = appMetaData
	body.Req = &v3.Req_HeartbeatReq{
		HeartbeatReq: &v3.HeartbeatReq{Ping: &types.Timestamp{Seconds: ts.Seconds, Nanos: ts.Nanos}},
	}

	return header, body, nil
}

// BuildV3LogReq builds a TGLog v3 log request
func BuildV3LogReq(appID, appName, appVer, network, reqID, token, tokenType string, messages []Message,
	labels map[string]string, annotations map[string]string, appMetaData []byte,
	header *v3.ReqHeader, body *v3.Req) (*v3.ReqHeader, *v3.Req, error) {
	if len(messages) == 0 {
		return nil, nil, errors.New("input message slice is empty")
	}

	pbLogs := make([]*v3.Log, 0, len(messages))

	for _, msg := range messages {
		pbLogs = append(pbLogs, &v3.Log{Name: msg.Name, Content: util.BytesToString(msg.Payload), Seq: nextSeq()})
	}

	logReq := &v3.Req_LogReq{
		LogReq: &v3.LogReq{
			Labels:      labels,
			Annotations: annotations,
			Logs:        pbLogs,
		},
	}

	ts := timestamppb.Now()

	// header can NOT be nil for a heartbeat request
	if header == nil {
		header = &v3.ReqHeader{}
	} else {
		header.Reset()
	}

	if body == nil {
		body = &v3.Req{}
	} else {
		body.Reset()
	}

	header.AppID = appID
	header.AppName = appName
	header.AppVer = appVer
	header.SdkLang = language
	header.SdkVer = sdkVersion
	header.SdkOS = platform
	header.Network = network
	header.ProtoVer = protoVer
	header.HostIP = localIP
	header.Ts = &types.Timestamp{Seconds: ts.Seconds, Nanos: ts.Nanos}
	header.Token = token
	header.TokenType = tokenType
	header.Sig = "" // we will sign when encoding

	body.ReqID = reqID
	body.AppMetaData = appMetaData
	body.Req = logReq

	return header, body, nil
}

// EncodeV3Req encodes a TGLog v3 request into bytes
func EncodeV3Req(header *v3.ReqHeader, body *v3.Req,
	noFrameHeader, compress, encrypt, auth, sign bool, encryptKey string,
	bb *bytes.Buffer, littleEndian bool) ([]byte, error) {
	if body == nil {
		return nil, errors.New("input request is nil")
	}

	if bb == nil {
		bb = &bytes.Buffer{}
	}

	bodyBytes, err := body.Marshal()
	if err != nil {
		return nil, err
	}

	// 无帧头、不压缩、不加密、不签名，直接返回
	if noFrameHeader && !compress && !encrypt && !auth && !sign {
		bb.Grow(len(bodyBytes))
		bb.Write(bodyBytes)
		return bb.Bytes(), nil
	}

	var flags uint8
	var bodyBuf = bodyBytes
	// 先压缩，大于512字节再压缩
	if compress && len(bodyBuf) > 512 {
		flags = flags | uint8(v3.Flag_FLAG_COMPRESSED)
		bodyBuf = snappy.Encode(nil, bodyBuf)
	}

	// 再加密
	if encrypt {
		flags = flags | uint8(v3.Flag_FLAG_ENCRYPTED)
		key := []byte(encryptKey)
		bodyBuf, err = crypto.AesEncrypt(bodyBuf, key)
		if err != nil {
			return nil, err
		}
	}

	var headerBuf []byte
	if (auth || sign) && header != nil {
		if sign {
			header.Sig = Sign(header.Network, header.HostIP, header.Token, header.Ts.Seconds, bodyBuf)
		}

		headerBuf, err = header.Marshal()
		if err != nil {
			return nil, err
		}

		// 先压缩，大于512字节再压缩
		if compress && len(headerBuf) > 512 {
			flags = flags | uint8(v3.Flag_FLAG_COMPRESSED_HEADER)
			headerBuf = snappy.Encode(nil, headerBuf)
		}

		// 签名必须加密，否则token会泄漏
		flags = flags | uint8(v3.Flag_FLAG_ENCRYPTED_HEADER)
		key := []byte(encryptKey)
		headerBuf, err = crypto.AesEncrypt(headerBuf, key)
		if err != nil {
			return nil, err
		}
	}

	frameHeaderLen := int(v3.Len_MAGIC + v3.Len_PACKAGE + v3.Len_FLAGS + v3.Len_HEADER + v3.Len_RESERVE)
	length := frameHeaderLen + len(headerBuf) + len(bodyBuf)
	bb.Grow(length)

	// write magic, 2 bytes
	magicBuf := make([]byte, 2)
	binary.BigEndian.PutUint16(magicBuf, uint16(v3.Magic_VAL))
	_, err = bb.Write(magicBuf)
	if err != nil {
		return nil, err
	}

	// write length, 4 bytes
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

	// write flags,1 bytes
	_, err = bb.Write([]byte{flags})
	if err != nil {
		return nil, err
	}

	// write header length, 2 bytes
	headerLengthBuf := make([]byte, 2)
	byteOrder.PutUint16(headerLengthBuf, uint16(len(headerBuf)))
	_, err = bb.Write(headerLengthBuf)
	if err != nil {
		return nil, err
	}

	// write reserved, 1 bytes
	_, err = bb.Write([]byte{0x00})
	if err != nil {
		return nil, err
	}

	// write header
	if headerBuf != nil {
		_, err = bb.Write(headerBuf)
		if err != nil {
			return nil, err
		}
	}

	// write body
	_, err = bb.Write(bodyBuf)
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
			return nil, errors.New("unmarshal failed: " + err.Error())
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
			return nil, errors.New("unmarshal failed: " + err.Error())
		}
	}

	return rsp, nil
}

// Sign signs a request
func Sign(uri, method, token string, ts int64, data []byte) string {
	md5sum := md5.Sum(data)
	return signData(uri, method, token, ts, md5sum[:])
}

func signData(uri, method, token string, ts int64, dataList ...[]byte) string {
	var bb bytes.Buffer
	dataLen := 0
	for _, data := range dataList {
		dataLen += len(data)
	}
	bb.Grow(len(uri) + len(method) + len(token) + 8 + dataLen)

	// 按顺序拼接
	bb.WriteString(uri)
	bb.WriteString(method)

	bb.WriteString(token)

	var tsBuf [8]byte
	binary.LittleEndian.PutUint64(tsBuf[:], uint64(ts))
	bb.Write(tsBuf[:])

	for _, data := range dataList {
		bb.Write(data)
	}

	md5sum := md5.Sum(bb.Bytes())
	return strings.ToLower(hex.EncodeToString(md5sum[:]))
}
