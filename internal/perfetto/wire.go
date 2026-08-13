package perfetto

import (
	"encoding/binary"
	"math"
)

const (
	wireVarint  = 0
	wireFixed64 = 1
	wireBytes   = 2
)

func appendTag(dst []byte, field, wire int) []byte {
	return binary.AppendUvarint(dst, uint64(field<<3|wire))
}

func appendUint(dst []byte, field int, value uint64) []byte {
	dst = appendTag(dst, field, wireVarint)
	return binary.AppendUvarint(dst, value)
}

func appendInt(dst []byte, field int, value int64) []byte {
	return appendUint(dst, field, uint64(value))
}

func appendBool(dst []byte, field int, value bool) []byte {
	if value {
		return appendUint(dst, field, 1)
	}
	return appendUint(dst, field, 0)
}

func appendDouble(dst []byte, field int, value float64) []byte {
	dst = appendTag(dst, field, wireFixed64)
	return binary.LittleEndian.AppendUint64(dst, math.Float64bits(value))
}

func appendBytes(dst []byte, field int, value []byte) []byte {
	dst = appendTag(dst, field, wireBytes)
	dst = binary.AppendUvarint(dst, uint64(len(value)))
	return append(dst, value...)
}

func appendString(dst []byte, field int, value string) []byte {
	return appendBytes(dst, field, []byte(value))
}
