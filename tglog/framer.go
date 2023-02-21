package tglog

import (
	"encoding/binary"
	"errors"
)

// framer types and other constants
const (
	defaultSize = 64 * 1024
)

// framer errors
var (
	errIncompleteFrame                   = errors.New("incomplete frame")
	errInvalidFrameLen                   = errors.New("invalid frame length")
	errInvalidFrameLenCfg                = errors.New("invalid field length for length field base framer, expect(1, 2, 3, 4, 8)")
	errExceedMaxFrameLen                 = errors.New("exceed max frame length")
	errFrameLenLessThanLenFieldEndOffset = errors.New("frame length less then length field end offset")
	errNoEnoughBytesToTrip               = errors.New("no enough bytes to trip")
)

// framer is the interface of a stream framer
type framer interface {
	readFrame(input []byte) (frameLen, payloadOffset, payloadOffsetEnd int, err error)
}

type lengthFieldCfg struct {
	littleEndian bool // 是否小端字节序，默认false
	maxFrameLen  int  // 最大帧长，单位字节
	fieldOffset  int  // 长度字段位移
	fieldLength  int  // 长度字段长度，单位字节
	adjustment   int  // 修正值，可以正可以负
	bytesToStrip int  // 需要截掉的字节数，只在解码的时候有用，获取帧长度时没用
}

type lengthFieldBased struct {
	byteOrder binary.ByteOrder
	cfg       lengthFieldCfg
}

func newLengthField(cfg lengthFieldCfg) (framer, error) {
	if cfg.fieldOffset < 0 {
		return nil, errors.New("invalid field offset for length field base framer")
	}

	if cfg.fieldLength <= 0 {
		return nil, errors.New("invalid field length for length field base framer")
	}

	if cfg.bytesToStrip < 0 {
		return nil, errors.New("invalid bytes to trip for length field base framer")
	}

	if cfg.fieldLength != 1 && cfg.fieldLength != 2 && cfg.fieldLength != 3 && cfg.fieldLength != 4 && cfg.fieldLength != 8 {
		return nil, errInvalidFrameLenCfg
	}

	if cfg.maxFrameLen <= 0 {
		cfg.maxFrameLen = defaultSize
	}

	if cfg.fieldOffset+cfg.fieldLength >= cfg.maxFrameLen {
		return nil, errors.New("invalid field offset and max frame length for length field base framer")
	}

	var byteOrder binary.ByteOrder = binary.BigEndian
	if cfg.littleEndian {
		byteOrder = binary.LittleEndian
	}

	return &lengthFieldBased{byteOrder: byteOrder, cfg: cfg}, nil
}

func (c *lengthFieldBased) readFrame(input []byte) (frameLen, payloadOffset, payloadOffsetEnd int, err error) {
	inLen := len(input)
	if inLen <= 0 {
		return 0, 0, 0, errIncompleteFrame
	}

	// 长度域尾偏移
	fieldEndOffset := c.cfg.fieldOffset + c.cfg.fieldLength

	// 输入长度不够，无法读长度
	if inLen < fieldEndOffset {
		return 0, 0, 0, errIncompleteFrame
	}

	// 长度域缓冲
	lenFieldBuf := input[c.cfg.fieldOffset:fieldEndOffset]
	// 计算未调整长度
	var unajustedLen uint64
	switch c.cfg.fieldLength {
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
		return 0, 0, 0, errInvalidFrameLenCfg
	}

	// 未调整长度非法
	if unajustedLen <= 0 {
		return 0, 0, 0, errInvalidFrameLen
	}

	// 计算调整后长度
	frameLen = fieldEndOffset + c.cfg.adjustment + int(unajustedLen)
	// 调整后长度小于长度域，说明没有载荷
	if frameLen < fieldEndOffset {
		return 0, 0, 0, errFrameLenLessThanLenFieldEndOffset
	}

	// 调整后长度大于最大帧长
	if frameLen > c.cfg.maxFrameLen {
		return 0, 0, 0, errExceedMaxFrameLen
	}

	// 调整后长度大于输入长度，还未够一个包
	if frameLen > inLen {
		return 0, 0, 0, errIncompleteFrame
	}

	// 要截断的长度大于帧长
	if c.cfg.bytesToStrip > frameLen {
		return 0, 0, 0, errNoEnoughBytesToTrip
	}

	// 应用层需要的有效数据长度
	actualFrameLen := frameLen - c.cfg.bytesToStrip
	// 应用层需要的有效数据起始偏移
	actualFrameOffset := c.cfg.bytesToStrip
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
