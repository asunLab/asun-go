package asun

import (
	"encoding/binary"
	"math"
	"reflect"
	"unicode/utf8"
	"unsafe"
)

// DefaultMaxSequenceLen caps attacker-controlled sequence lengths in binary
// input. It also caps the total number of zero-sized elements (slices of
// struct{} and the like) in one input, since they consume no bytes.
const DefaultMaxSequenceLen = 16 * 1024 * 1024

const (
	msgVarint      = "malformed or overflowing varint"
	msgInvalidTag  = "invalid binary tag"
	msgSeqTooLong  = "sequence exceeds decode limit"
	msgTrailingBin = "trailing bytes"
)

// DecodeBinary deserializes one ASUN-BIN value into v. Trailing bytes are
// accepted, for callers that place several values in one buffer; use
// DecodeBinaryExact to reject them.
//
// Decoded strings and byte slices share memory with data; do not modify data
// afterwards.
func DecodeBinary(data []byte, v any) error {
	_, err := decodeBinary(data, v)
	return err
}

// DecodeBinaryExact deserializes exactly one ASUN-BIN value into v and
// rejects trailing bytes.
func DecodeBinaryExact(data []byte, v any) error {
	pos, err := decodeBinary(data, v)
	if err == nil && pos != len(data) {
		return &UnmarshalError{Pos: pos, Message: msgTrailingBin}
	}
	return err
}

// decodeBinary decodes one value and returns the end position.
func decodeBinary(data []byte, v any) (int, error) {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Ptr || rv.IsNil() {
		return 0, &UnmarshalError{Message: "unmarshal target must be a non-nil pointer"}
	}
	d := binDecoder{data: data, zstBudget: DefaultMaxSequenceLen}
	err := d.value(rv.Elem())
	return d.pos, err
}

// binDecoder mirrors asun-rs `BinaryDecoder`.
type binDecoder struct {
	data []byte
	pos  int
	// Zero-sized elements still allowed.
	zstBudget int
	// Sequence nesting, checked against maxDepth like asun-rs. Pointer and
	// interface nesting is bounded separately: Go types can recurse through
	// them, Rust ones cannot.
	depth    int
	ptrDepth int
	// One-entry struct metadata cache: rows repeat one type.
	siType reflect.Type
	si     *structInfo
}

func (d *binDecoder) fail(msg string) error {
	return &UnmarshalError{Pos: d.pos, Message: msg}
}

func (d *binDecoder) take(n uint64) ([]byte, error) {
	if n > uint64(len(d.data)-d.pos) {
		return nil, d.fail(msgEOF)
	}
	b := d.data[d.pos : d.pos+int(n)]
	d.pos += int(n)
	return b, nil
}

func (d *binDecoder) byte() (byte, error) {
	if d.pos >= len(d.data) {
		return 0, d.fail(msgEOF)
	}
	b := d.data[d.pos]
	d.pos++
	return b, nil
}

// uvarint reads a strict LEB128 varint: every value has exactly one valid
// encoding, so a padded last byte (`80 00`) and a tenth byte other than 0x01
// are errors.
func (d *binDecoder) uvarint() (uint64, error) {
	data := d.data
	p := d.pos
	if p >= len(data) {
		return 0, d.fail(msgEOF)
	}
	b := data[p]
	if b < 0x80 {
		d.pos = p + 1
		return uint64(b), nil
	}
	v := uint64(b & 0x7f)
	for i := 1; i < 10; i++ {
		if p+i >= len(data) {
			return 0, d.fail(msgEOF)
		}
		b = data[p+i]
		if i == 9 {
			if b != 1 {
				return 0, d.fail(msgVarint)
			}
			d.pos = p + 10
			return v | 1<<63, nil
		}
		v |= uint64(b&0x7f) << (7 * i)
		if b < 0x80 {
			if b == 0 {
				return 0, d.fail(msgVarint)
			}
			d.pos = p + i + 1
			return v, nil
		}
	}
	return 0, d.fail(msgVarint)
}

func (d *binDecoder) ivarint() (int64, error) {
	u, err := d.uvarint()
	return int64(u>>1) ^ -int64(u&1), err
}

func (d *binDecoder) seqLen() (int, error) {
	n, err := d.uvarint()
	if err != nil {
		return 0, err
	}
	if n > DefaultMaxSequenceLen {
		return 0, d.fail(msgSeqTooLong)
	}
	return int(n), nil
}

func (d *binDecoder) value(rv reflect.Value) error {
	switch rv.Kind() {
	case reflect.Bool:
		b, err := d.byte()
		if err != nil {
			return err
		}
		if b > 1 {
			d.pos--
			return d.fail(msgInvalidBool)
		}
		rv.SetBool(b == 1)
	case reflect.Int8:
		b, err := d.byte()
		if err != nil {
			return err
		}
		rv.SetInt(int64(int8(b)))
	case reflect.Uint8:
		b, err := d.byte()
		if err != nil {
			return err
		}
		rv.SetUint(uint64(b))
	case reflect.Int16, reflect.Int32, reflect.Int, reflect.Int64:
		start := d.pos
		v, err := d.ivarint()
		if err != nil {
			return err
		}
		if rv.OverflowInt(v) {
			d.pos = start
			return d.fail(msgIntRange)
		}
		rv.SetInt(v)
	case reflect.Uint16, reflect.Uint32, reflect.Uint, reflect.Uint64, reflect.Uintptr:
		start := d.pos
		v, err := d.uvarint()
		if err != nil {
			return err
		}
		if rv.OverflowUint(v) {
			d.pos = start
			return d.fail(msgIntRange)
		}
		rv.SetUint(v)
	case reflect.Float32:
		b, err := d.take(4)
		if err != nil {
			return err
		}
		bits := binary.LittleEndian.Uint32(b)
		if rv.CanAddr() {
			// Keep the exact bits (a float64 round trip quiets signalling NaNs).
			*(*uint32)(unsafe.Pointer(rv.UnsafeAddr())) = bits
		} else {
			rv.SetFloat(float64(math.Float32frombits(bits)))
		}
	case reflect.Float64:
		b, err := d.take(8)
		if err != nil {
			return err
		}
		rv.SetFloat(math.Float64frombits(binary.LittleEndian.Uint64(b)))
	case reflect.String:
		n, err := d.uvarint()
		if err != nil {
			return err
		}
		start := d.pos
		b, err := d.take(n)
		if err != nil {
			return err
		}
		if !utf8.Valid(b) {
			d.pos = start
			return d.fail(msgInvalidUTF8)
		}
		rv.SetString(unsafeString(b))
	case reflect.Slice:
		return d.slice(rv)
	case reflect.Array:
		return d.array(rv)
	case reflect.Struct:
		if t := rv.Type(); t != d.siType {
			d.si = getStructInfo(t)
			d.siType = t
		}
		si := d.si
		for i := range si.fields {
			if err := d.value(si.field(rv, i)); err != nil {
				return err
			}
		}
	case reflect.Ptr:
		some, err := d.tag()
		if err != nil {
			return err
		}
		if !some {
			rv.SetZero()
			return nil
		}
		if err := d.enterPtr(); err != nil {
			return err
		}
		p := reflect.New(rv.Type().Elem())
		if err := d.value(p.Elem()); err != nil {
			return err
		}
		d.ptrDepth--
		rv.Set(p)
	case reflect.Interface:
		some, err := d.tag()
		if err != nil {
			return err
		}
		if !some {
			rv.SetZero()
			return nil
		}
		if rv.IsNil() {
			return d.fail("cannot unmarshal into nil interface")
		}
		if err := d.enterPtr(); err != nil {
			return err
		}
		// No type information on the wire: decode into the concrete type
		// already held by the interface.
		elem := rv.Elem()
		if elem.Kind() == reflect.Ptr && !elem.IsNil() {
			err = d.value(elem.Elem())
		} else {
			p := reflect.New(elem.Type())
			if err = d.value(p.Elem()); err == nil {
				rv.Set(p.Elem())
			}
		}
		if err != nil {
			return err
		}
		d.ptrDepth--
	case reflect.Map:
		return d.fail("map fields are not supported")
	default:
		return d.fail("unsupported type " + rv.Type().String())
	}
	return nil
}

// tag reads an Option tag: 0x00 none, 0x01 some; anything else is an error.
func (d *binDecoder) tag() (bool, error) {
	b, err := d.byte()
	if err != nil {
		return false, err
	}
	switch b {
	case 0:
		return false, nil
	case 1:
		return true, nil
	}
	d.pos--
	return false, d.fail(msgInvalidTag)
}

func (d *binDecoder) enterPtr() error {
	d.ptrDepth++
	if d.ptrDepth > maxDepth {
		return d.fail(msgDepth)
	}
	return nil
}

func (d *binDecoder) slice(rv reflect.Value) error {
	start := d.pos
	n, err := d.seqLen()
	if err != nil {
		return err
	}
	et := rv.Type().Elem()
	if et.Kind() == reflect.Uint8 {
		b, err := d.take(uint64(n))
		if err != nil {
			return err
		}
		rv.SetBytes(b[:n:n])
		return nil
	}
	if et.Size() == 0 {
		if n > d.zstBudget {
			d.pos = start
			return d.fail(msgSeqTooLong)
		}
		d.zstBudget -= n
	}
	// Recursive types nest through here, so this is where untrusted input
	// could otherwise exhaust the stack.
	if d.depth >= maxDepth {
		return d.fail(msgDepth)
	}
	// Do not preallocate solely from an untrusted count: every non-zero-size
	// element takes at least one byte, and the reservation is capped.
	initial := 0
	if et.Size() != 0 {
		initial = cautiousCapacity(min(n, len(d.data)-d.pos), et.Size())
	}
	s := reflect.MakeSlice(rv.Type(), 0, initial)
	rv.Set(s)
	a := sliceAppender{rv: rv}
	d.depth++
	for i := 0; i < n; i++ {
		if err := d.value(a.next()); err != nil {
			d.depth--
			return err
		}
	}
	d.depth--
	return nil
}

// array decodes a sequence into a Go array, whose length must match.
func (d *binDecoder) array(rv reflect.Value) error {
	start := d.pos
	n, err := d.seqLen()
	if err != nil {
		return err
	}
	if n != rv.Len() {
		d.pos = start
		return fieldCountMismatch(start, rv.Len(), n)
	}
	if rv.Type().Elem().Size() == 0 {
		if n > d.zstBudget {
			d.pos = start
			return d.fail(msgSeqTooLong)
		}
		d.zstBudget -= n
	}
	if d.depth >= maxDepth {
		return d.fail(msgDepth)
	}
	d.depth++
	for i := 0; i < n; i++ {
		e := rv.Index(i)
		e.SetZero()
		if err := d.value(e); err != nil {
			d.depth--
			return err
		}
	}
	d.depth--
	return nil
}
