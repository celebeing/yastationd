package esphome

import (
	"encoding/binary"
	"errors"
	"math"
)

// A small protobuf (proto3) codec for the native API's messages: an encoder that appends fields
// and a decoder that reads a message into its fields. The API's messages are flat or nest one or
// two levels, so writing them by hand beats a generated library and its dependency.

// enc builds one message.
type enc struct{ b []byte }

func (e *enc) key(field, wire int) { e.varint(uint64(field<<3 | wire)) }

func (e *enc) varint(v uint64) { e.b = binary.AppendUvarint(e.b, v) }

// Uint writes a uint32/uint64/enum/bool field (proto3: a zero is not written).
func (e *enc) Uint(field int, v uint64) {
	if v == 0 {
		return
	}
	e.key(field, 0)
	e.varint(v)
}

// UintForce writes a varint field even when it is zero.
func (e *enc) UintForce(field int, v uint64) {
	e.key(field, 0)
	e.varint(v)
}

// Int32 writes an int32 field (negative values as ten-byte varints, as protobuf does).
func (e *enc) Int32(field int, v int32) { e.Uint(field, uint64(int64(v))) }

// Sint32 writes a sint32 field (zigzag), even when zero.
func (e *enc) Sint32(field int, v int32) {
	e.key(field, 0)
	e.varint(uint64(uint32((v << 1) ^ (v >> 31))))
}

// Bool writes a bool field.
func (e *enc) Bool(field int, v bool) {
	if v {
		e.Uint(field, 1)
	}
}

// Bytes writes a bytes field; force writes an empty one too.
func (e *enc) Bytes(field int, v []byte, force bool) {
	if len(v) == 0 && !force {
		return
	}
	e.key(field, 2)
	e.varint(uint64(len(v)))
	e.b = append(e.b, v...)
}

// String writes a string field.
func (e *enc) String(field int, v string) { e.Bytes(field, []byte(v), false) }

// Msg writes a nested message field.
func (e *enc) Msg(field int, m *enc) { e.Bytes(field, m.b, true) }

// field is one decoded field: a varint, or bytes (a string, bytes or a nested message).
type field struct {
	num   int
	wire  int
	v     uint64
	bytes []byte
}

var errTruncated = errors.New("esphome: truncated message")

// decode splits a message into its fields, in order.
func decode(b []byte) ([]field, error) {
	var out []field
	for len(b) > 0 {
		k, n := binary.Uvarint(b)
		if n <= 0 {
			return nil, errTruncated
		}
		b = b[n:]
		f := field{num: int(k >> 3), wire: int(k & 7)}
		switch f.wire {
		case 0:
			v, n := binary.Uvarint(b)
			if n <= 0 {
				return nil, errTruncated
			}
			f.v, b = v, b[n:]
		case 1:
			if len(b) < 8 {
				return nil, errTruncated
			}
			f.v, b = binary.LittleEndian.Uint64(b), b[8:]
		case 2:
			l, n := binary.Uvarint(b)
			if n <= 0 || uint64(len(b)-n) < l {
				return nil, errTruncated
			}
			f.bytes, b = b[n:n+int(l)], b[n+int(l):]
		case 5:
			if len(b) < 4 {
				return nil, errTruncated
			}
			f.v, b = uint64(binary.LittleEndian.Uint32(b)), b[4:]
		default:
			return nil, errors.New("esphome: unsupported wire type")
		}
		out = append(out, f)
	}
	return out, nil
}

// msg is a decoded message with typed getters (the last value of a field wins, as in protobuf).
type msg []field

func parse(b []byte) (msg, error) {
	f, err := decode(b)
	return msg(f), err
}

func (m msg) last(num int) (field, bool) {
	for i := len(m) - 1; i >= 0; i-- {
		if m[i].num == num {
			return m[i], true
		}
	}
	return field{}, false
}

func (m msg) Uint(num int) uint64 {
	f, _ := m.last(num)
	return f.v
}

func (m msg) Bool(num int) bool { return m.Uint(num) != 0 }

func (m msg) Bytes(num int) []byte {
	f, _ := m.last(num)
	return f.bytes
}

func (m msg) String(num int) string { return string(m.Bytes(num)) }

// Float32 reads a float field (wire type 5).
func (m msg) Float32(num int) float32 { return math.Float32frombits(uint32(m.Uint(num))) }
