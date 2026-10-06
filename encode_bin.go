package asun

import (
	"encoding/binary"
	"math"
	"reflect"
	"sync"
	"unsafe"
)

// ---------------------------------------------------------------------------
// ASUN-BIN encoding
//
// The wire format is the one asun-rs writes (docs/SPEC.md §11): bool, int8
// and uint8 are one raw byte; other integers are LEB128 varints (signed ones
// zigzag-encoded); floats are fixed-width little-endian; strings, byte slices
// and slices are prefixed with a varint length / count; a pointer is an
// Option: tag 0x00, or 0x01 followed by the value; struct fields are written
// in order with no framing.
// ---------------------------------------------------------------------------

// appendUvarint appends v as an LEB128 unsigned varint.
func appendUvarint(buf []byte, v uint64) []byte {
	for v >= 0x80 {
		buf = append(buf, byte(v)|0x80)
		v >>= 7
	}
	return append(buf, byte(v))
}

// zigzagEncode maps a signed integer to an unsigned one so that small
// magnitudes (positive or negative) encode into few varint bytes.
func zigzagEncode(v int64) uint64 {
	return uint64((v << 1) ^ (v >> 63))
}

// appendIvarint appends v as zigzag + LEB128 signed varint.
func appendIvarint(buf []byte, v int64) []byte {
	return appendUvarint(buf, zigzagEncode(v))
}

var binBufPool = sync.Pool{
	New: func() any {
		b := make([]byte, 0, 128)
		return &b
	},
}

// EncodeBinary serializes a Go value to ASUN-BIN format.
func EncodeBinary(v any) ([]byte, error) {
	if v == nil {
		return nil, &MarshalError{Message: "cannot marshal nil"}
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Ptr {
		if rv.IsNil() {
			return nil, &MarshalError{Message: "cannot marshal nil pointer"}
		}
		rv = rv.Elem()
	} else {
		// An addressable copy lets float32 bits be read exactly.
		c := reflect.New(rv.Type()).Elem()
		c.Set(rv)
		rv = c
	}
	if err := ensureNoMapType(rv.Type()); err != nil {
		return nil, err
	}

	bp := binBufPool.Get().(*[]byte)
	buf, err := marshalBinValue((*bp)[:0], rv)
	var result []byte
	if err == nil {
		result = append([]byte(nil), buf...)
	}
	if cap(buf) <= 1<<16 {
		*bp = buf
		binBufPool.Put(bp)
	}
	return result, err
}

func marshalBinValue(buf []byte, rv reflect.Value) ([]byte, error) {
	switch rv.Kind() {
	case reflect.Bool:
		if rv.Bool() {
			return append(buf, 1), nil
		}
		return append(buf, 0), nil
	case reflect.Int8:
		return append(buf, byte(rv.Int())), nil
	case reflect.Int16, reflect.Int32, reflect.Int, reflect.Int64:
		return appendIvarint(buf, rv.Int()), nil
	case reflect.Uint8:
		return append(buf, byte(rv.Uint())), nil
	case reflect.Uint16, reflect.Uint32, reflect.Uint, reflect.Uint64, reflect.Uintptr:
		return appendUvarint(buf, rv.Uint()), nil
	case reflect.Float32:
		var bits uint32
		if rv.CanAddr() {
			// Read the bits directly: a float64 round trip would quiet a
			// signalling NaN.
			bits = *(*uint32)(unsafe.Pointer(rv.UnsafeAddr()))
		} else {
			bits = math.Float32bits(float32(rv.Float()))
		}
		return binary.LittleEndian.AppendUint32(buf, bits), nil
	case reflect.Float64:
		return binary.LittleEndian.AppendUint64(buf, math.Float64bits(rv.Float())), nil
	case reflect.String:
		s := rv.String()
		buf = appendUvarint(buf, uint64(len(s)))
		return append(buf, s...), nil
	case reflect.Slice:
		if rv.Type().Elem().Kind() == reflect.Uint8 {
			b := rv.Bytes()
			buf = appendUvarint(buf, uint64(len(b)))
			return append(buf, b...), nil
		}
		return marshalBinSeq(buf, rv)
	case reflect.Array:
		return marshalBinSeq(buf, rv)
	case reflect.Struct:
		si := getStructInfo(rv.Type())
		for i := range si.fields {
			var err error
			buf, err = marshalBinValue(buf, si.field(rv, i))
			if err != nil {
				return buf, err
			}
		}
		return buf, nil
	case reflect.Ptr, reflect.Interface:
		if rv.IsNil() {
			return append(buf, 0), nil
		}
		buf = append(buf, 1)
		return marshalBinValue(buf, rv.Elem())
	case reflect.Map:
		return buf, errMapFieldsUnsupported
	default:
		return buf, &MarshalError{Message: "unsupported type " + rv.Type().String()}
	}
}

func marshalBinSeq(buf []byte, rv reflect.Value) ([]byte, error) {
	n := rv.Len()
	buf = appendUvarint(buf, uint64(n))
	for i := 0; i < n; i++ {
		var err error
		buf, err = marshalBinValue(buf, rv.Index(i))
		if err != nil {
			return buf, err
		}
	}
	return buf, nil
}
