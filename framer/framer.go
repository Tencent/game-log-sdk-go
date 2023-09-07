// Package framer provides a simple framing protocol for binary data.
package framer

import (
	"encoding/binary"
	"errors"
)

// Framer types and other constants
const (
	defaultSize = 64 * 1024
)

// Framer errors
var (
	ErrIncompleteFrame    = errors.New("incomplete frame")
	ErrInvalidFrameLen    = errors.New("invalid frame length")
	ErrInvalidFrameLenCfg = errors.New(
		"invalid field length for length field base Framer, expect(1, 2, 3, 4, 8)")
	ErrExceedMaxFrameLen                 = errors.New("exceed max frame length")
	ErrFrameLenLessThanLenFieldEndOffset = errors.New("frame length less then length field end offset")
	ErrNoEnoughBytesToTrip               = errors.New("no enough bytes to trip")
)

// Framer is the interface of a stream Framer
type Framer interface {
	// ReadFrame reads a frame from the raw stream input according to its config,
	// return the frame length, the payload beginning and ending offset
	ReadFrame(input []byte) (frameLen, payloadOffset, payloadOffsetEnd int, err error)
}

// LengthFieldCfg is the config of a length field base framer
type LengthFieldCfg struct {
	LittleEndian bool // 是否小端字节序，默认false
	MaxFrameLen  int  // 最大帧长，单位字节
	FieldOffset  int  // 长度字段位移
	FieldLength  int  // 长度字段长度，单位字节
	Adjustment   int  // 修正值，可以正可以负
	BytesToStrip int  // 需要截掉的字节数，只在解码的时候有用，获取帧长度时没用
}

type lengthFieldBased struct {
	byteOrder binary.ByteOrder
	cfg       LengthFieldCfg
}

// NewLengthField news a length field based framer
func NewLengthField(cfg LengthFieldCfg) (Framer, error) {
	if cfg.FieldOffset < 0 {
		return nil, errors.New("invalid field offset for length field base Framer")
	}

	if cfg.FieldLength <= 0 {
		return nil, errors.New("invalid field length for length field base Framer")
	}

	if cfg.BytesToStrip < 0 {
		return nil, errors.New("invalid bytes to trip for length field base Framer")
	}

	if cfg.FieldLength != 1 && cfg.FieldLength != 2 && cfg.FieldLength != 3 &&
		cfg.FieldLength != 4 && cfg.FieldLength != 8 {
		return nil, ErrInvalidFrameLenCfg
	}

	if cfg.MaxFrameLen <= 0 {
		cfg.MaxFrameLen = defaultSize
	}

	if cfg.FieldOffset+cfg.FieldLength >= cfg.MaxFrameLen {
		return nil, errors.New("invalid field offset and max frame length for length field base Framer")
	}

	var byteOrder binary.ByteOrder = binary.BigEndian
	if cfg.LittleEndian {
		byteOrder = binary.LittleEndian
	}

	return &lengthFieldBased{byteOrder: byteOrder, cfg: cfg}, nil
}

func (c *lengthFieldBased) ReadFrame(input []byte) (frameLen, payloadOffset, payloadOffsetEnd int, err error) {
	inLen := len(input)
	if inLen <= 0 {
		return 0, 0, 0, ErrIncompleteFrame
	}

	// 长度域尾偏移
	fieldEndOffset := c.cfg.FieldOffset + c.cfg.FieldLength

	// 输入长度不够，无法读长度
	if inLen < fieldEndOffset {
		return 0, 0, 0, ErrIncompleteFrame
	}

	// 长度域缓冲
	lenFieldBuf := input[c.cfg.FieldOffset:fieldEndOffset]
	// 计算未调整长度
	var unajustedLen uint64
	switch c.cfg.FieldLength {
	case 1:
		unajustedLen = uint64(lenFieldBuf[0])
	case 2:
		unajustedLen = uint64(c.byteOrder.Uint16(lenFieldBuf))
	case 3:
		unajustedLen = uint24(c.byteOrder, lenFieldBuf)
	case 4:
		unajustedLen = uint64(c.byteOrder.Uint32(lenFieldBuf))
	case 8:
		unajustedLen = c.byteOrder.Uint64(lenFieldBuf)
	default:
		return 0, 0, 0, ErrInvalidFrameLenCfg
	}

	// 未调整长度非法
	if unajustedLen <= 0 {
		return 0, 0, 0, ErrInvalidFrameLen
	}

	// 计算调整后长度
	frameLen = fieldEndOffset + c.cfg.Adjustment + int(unajustedLen)
	// 调整后长度小于长度域，说明没有载荷
	if frameLen < fieldEndOffset {
		return 0, 0, 0, ErrFrameLenLessThanLenFieldEndOffset
	}

	// 调整后长度大于最大帧长
	if frameLen > c.cfg.MaxFrameLen {
		return 0, 0, 0, ErrExceedMaxFrameLen
	}

	// 调整后长度大于输入长度，还未够一个包
	if frameLen > inLen {
		return 0, 0, 0, ErrIncompleteFrame
	}

	// 要截断的长度大于帧长
	if c.cfg.BytesToStrip > frameLen {
		return 0, 0, 0, ErrNoEnoughBytesToTrip
	}

	// 应用层需要的有效数据长度
	actualFrameLen := frameLen - c.cfg.BytesToStrip
	// 应用层需要的有效数据起始偏移
	actualFrameOffset := c.cfg.BytesToStrip
	// 应用层需要的有效数据结束偏移
	actualFrameOffsetEnd := actualFrameOffset + actualFrameLen

	return frameLen, actualFrameOffset, actualFrameOffsetEnd, nil
}

func uint24(byteOrder binary.ByteOrder, b []byte) uint64 {
	_ = b[2]
	if byteOrder == binary.LittleEndian {
		return uint64(b[0]) | uint64(b[1])<<8 | uint64(b[2])<<16
	}
	return uint64(b[2]) | uint64(b[1])<<8 | uint64(b[0])<<16
}
