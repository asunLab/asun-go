package asun

// Malformed and hostile ASUN-BIN input: every case must return an error (or
// the exact value) without panicking, hanging or exhausting memory or stack.
//
// Port of asun-rs tests/binary_adversarial_test.rs. Not ported, as Go has no
// counterpart: `char` code-point checks, enum variant indexes, and borrowed
// `&str` (Go strings are checked for content; they share the input buffer).
// Rust tuples are Go structs, Option<Option<T>> is **T.

import (
	"bytes"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
)

type advTriple struct {
	A uint8
	B string
	C bool
}

type advRich struct {
	A bool
	B int8
	C int16
	D int32
	E int64
	F uint8
	G uint16
	H uint32
	I uint64
	J float32
	K float64
	M string
	N *string
	O []int64
	P []*uint16
	Q advTriple
	S **int32
}

func advRichValue() advRich {
	empty := ""
	zero, maxU16 := uint16(0), uint16(math.MaxUint16)
	var none *int32
	return advRich{
		A: true,
		B: -128,
		C: math.MinInt16,
		D: math.MaxInt32,
		E: math.MinInt64,
		F: 255,
		G: math.MaxUint16,
		H: math.MaxUint32,
		I: math.MaxUint64,
		J: math.Float32frombits(0x7fc0_0001),
		K: math.Copysign(0, -1),
		M: strings.Repeat("héllo wörld, 中文 🎉", 5),
		N: &empty,
		O: []int64{0, -1, 1, math.MaxInt64, math.MinInt64, 63, 64, -64, -65},
		P: []*uint16{nil, &zero, &maxU16},
		Q: advTriple{7, strings.Repeat("x", 200), false},
		S: &none,
	}
}

type advNode struct {
	Kids []advNode
}

type advEmpty struct{}

func advVarint(v uint64) []byte { return appendUvarint(nil, v) }

func mustEncodeBin(t *testing.T, v any) []byte {
	t.Helper()
	b, err := EncodeBinary(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// wantBinErr asserts that err is a decode error containing msg.
func wantBinErr(t *testing.T, err error, msg string, ctx string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), msg) {
		t.Errorf("%s: want error %q, got %v", ctx, msg, err)
	}
}

func TestBinAdvExtremeValuesRoundtripBitExact(t *testing.T) {
	v := advRichValue()
	b := mustEncodeBin(t, &v)
	var back advRich
	if err := DecodeBinaryExact(b, &back); err != nil {
		t.Fatal(err)
	}
	if math.Float32bits(back.J) != math.Float32bits(v.J) {
		t.Fatalf("NaN payload: %#x", math.Float32bits(back.J))
	}
	if !math.Signbit(back.K) {
		t.Fatal("-0.0 sign lost")
	}
	// NaN != NaN, so compare everything else with J cleared.
	back.J, v.J = 0, 0
	if !reflect.DeepEqual(back, v) {
		t.Fatalf("roundtrip mismatch:\n got %+v\nwant %+v", back, v)
	}
}

func TestBinAdvEveryTruncationIsAnError(t *testing.T) {
	v := advRichValue()
	b := mustEncodeBin(t, &v)
	for n := 0; n < len(b); n++ {
		var out advRich
		if err := DecodeBinary(b[:n], &out); err == nil {
			t.Fatalf("prefix of %d bytes decoded", n)
		}
	}
}

func TestBinAdvByteMutationsNeverPanic(t *testing.T) {
	v := advRichValue()
	b := mustEncodeBin(t, &v)
	for i := range b {
		for _, x := range []byte{0x00, 0x01, 0x02, 0x7f, 0x80, 0xc0, 0xff} {
			m := bytes.Clone(b)
			m[i] = x
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("panic with byte %d = %#x: %v", i, x, r)
					}
				}()
				var out advRich
				_ = DecodeBinary(m, &out)
			}()
		}
	}
}

func TestBinAdvVarintEdges(t *testing.T) {
	var u uint64
	max := []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01}
	if err := DecodeBinaryExact(max, &u); err != nil || u != math.MaxUint64 {
		t.Fatalf("max: %d %v", u, err)
	}
	// Only the minimal encoding is accepted, so each value has one byte form.
	for _, b := range [][]byte{
		{0x80, 0x00},
		{0x81, 0x00},
		{0xff, 0x80, 0x00},
		{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x00},
	} {
		wantBinErr(t, DecodeBinary(b, &u), msgVarint, "overlong")
	}
	var s string
	wantBinErr(t, DecodeBinary([]byte{0x80, 0x00}, &s), msgVarint, "overlong string length")
	for shift := 0; shift < 64; shift++ {
		for _, v := range []uint64{1 << shift, (1 << shift) - 1, math.MaxUint64 >> shift} {
			var got uint64
			if err := DecodeBinaryExact(advVarint(v), &got); err != nil || got != v {
				t.Fatalf("varint %d: got %d %v", v, got, err)
			}
		}
	}
	tenthTooBig := []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x02}
	wantBinErr(t, DecodeBinary(tenthTooBig, &u), msgVarint, "tenth byte")
	eleven := []byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x00}
	wantBinErr(t, DecodeBinary(eleven, &u), msgVarint, "eleven bytes")
	wantBinErr(t, DecodeBinary([]byte{0x80, 0x80, 0x80}, &u), msgEOF, "unterminated")

	for _, v := range []int64{math.MinInt64, math.MaxInt64, -1, 0, 63, -64, 64, -65} {
		var got int64
		if err := DecodeBinaryExact(mustEncodeBin(t, v), &got); err != nil || got != v {
			t.Fatalf("ivarint %d: got %d %v", v, got, err)
		}
	}
	// Narrow targets reject values that only fit a wider type.
	var u16 uint16
	wantBinErr(t, DecodeBinary(advVarint(65536), &u16), msgIntRange, "u16")
	var i16 int16
	wantBinErr(t, DecodeBinary(mustEncodeBin(t, int32(40000)), &i16), msgIntRange, "i16")
	var i32 int32
	wantBinErr(t, DecodeBinary(mustEncodeBin(t, int64(math.MinInt64)), &i32), msgIntRange, "i32")
	// uint8 is a raw byte, not a varint: a two-byte varint leaves one byte over.
	var u8 uint8
	wantBinErr(t, DecodeBinaryExact(mustEncodeBin(t, uint16(200)), &u8), msgTrailingBin, "u8")
}

func TestBinAdvTagsAndScalarsAreStrict(t *testing.T) {
	var o *uint8
	if err := DecodeBinaryExact([]byte{0}, &o); err != nil || o != nil {
		t.Fatalf("none: %v %v", o, err)
	}
	if err := DecodeBinaryExact([]byte{1, 9}, &o); err != nil || o == nil || *o != 9 {
		t.Fatalf("some: %v %v", o, err)
	}
	for _, tag := range []byte{2, 0x80, 0xff} {
		wantBinErr(t, DecodeBinary([]byte{tag, 9}, &o), msgInvalidTag, "option tag")
	}
	var b bool
	wantBinErr(t, DecodeBinary([]byte{2}, &b), msgInvalidBool, "bool")
	var f32 float32
	wantBinErr(t, DecodeBinary([]byte{0, 0, 0}, &f32), msgEOF, "f32")
	var f64 float64
	wantBinErr(t, DecodeBinary(make([]byte, 7), &f64), msgEOF, "f64")
}

func TestBinAdvLengthsPastTheInputFailFast(t *testing.T) {
	var s string
	wantBinErr(t, DecodeBinary([]byte{5, 'a', 'b'}, &s), msgEOF, "short string")
	wantBinErr(t, DecodeBinary(advVarint(1<<63), &s), msgEOF, "2^63 string")
	wantBinErr(t, DecodeBinary(advVarint(math.MaxUint64), &s), msgEOF, "max string")
	var bs []byte
	wantBinErr(t, DecodeBinary(advVarint(1<<40), &bs), msgSeqTooLong, "2^40 bytes")
	wantBinErr(t, DecodeBinary(advVarint(DefaultMaxSequenceLen), &bs), msgEOF, "limit bytes")
	wantBinErr(t, DecodeBinary(advVarint(DefaultMaxSequenceLen+1), &bs), msgSeqTooLong, "limit+1 bytes")
}

func TestBinAdvLengthPrefixWidthAtBoundaries(t *testing.T) {
	for _, c := range []struct{ n, prefix int }{
		{0, 1}, {63, 1}, {64, 1}, {127, 1}, {128, 2}, {16383, 2}, {16384, 3},
	} {
		s := strings.Repeat("a", c.n)
		b := mustEncodeBin(t, s)
		if len(b)-c.n != c.prefix {
			t.Fatalf("string n=%d: prefix %d", c.n, len(b)-c.n)
		}
		var gs string
		if err := DecodeBinaryExact(b, &gs); err != nil || gs != s {
			t.Fatalf("string n=%d: %v", c.n, err)
		}
		v := bytes.Repeat([]byte{7}, c.n)
		b = mustEncodeBin(t, v)
		if len(b)-c.n != c.prefix {
			t.Fatalf("bytes n=%d: prefix %d", c.n, len(b)-c.n)
		}
		var gv []byte
		if err := DecodeBinaryExact(b, &gv); err != nil || !bytes.Equal(gv, v) {
			t.Fatalf("bytes n=%d: %v", c.n, err)
		}
	}
}

func TestBinAdvZeroSizedElementsAreBudgeted(t *testing.T) {
	// One sequence up to the limit is fine.
	var units []struct{}
	if err := DecodeBinaryExact(advVarint(DefaultMaxSequenceLen), &units); err != nil || len(units) != DefaultMaxSequenceLen {
		t.Fatalf("[]struct{}: %d %v", len(units), err)
	}
	var empties []advEmpty
	if err := DecodeBinaryExact(advVarint(DefaultMaxSequenceLen), &empties); err != nil || len(empties) != DefaultMaxSequenceLen {
		t.Fatalf("[]advEmpty: %d %v", len(empties), err)
	}
	// Nested ones would turn a few hundred bytes into ~10^9 iterations.
	buf := advVarint(64)
	for i := 0; i < 64; i++ {
		buf = append(buf, advVarint(DefaultMaxSequenceLen)...)
	}
	start := time.Now()
	var nested [][]struct{}
	wantBinErr(t, DecodeBinary(buf, &nested), msgSeqTooLong, "nested zero-sized")
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("took %v", d)
	}
}

func TestBinAdvPreallocationIsCapped(t *testing.T) {
	// Rust tuples carry no length prefix (Go arrays do), so use structs.
	type oct struct{ A, B, C, D, E, F, G, H uint64 }
	type big struct{ A, B, C, D oct }
	// 1M claimed 256-byte elements backed by 1 MiB of zeros: each element
	// takes 32 bytes, so this ends in EOF without reserving 256 MiB upfront.
	buf := advVarint(1 << 20)
	buf = append(buf, make([]byte, 1<<20)...)
	var out []big
	wantBinErr(t, DecodeBinary(buf, &out), msgEOF, "claimed 1M")
	// A legitimate large vector still decodes.
	v := make([]big, 40_000)
	b := mustEncodeBin(t, v)
	if err := DecodeBinaryExact(b, &out); err != nil || len(out) != 40_000 {
		t.Fatalf("40k: %d %v", len(out), err)
	}
}

func TestBinAdvDeepRecursionIsRejected(t *testing.T) {
	// advNode{Kids: [advNode{Kids: [ ... ]}]}: 0x01 per level, 0x00 innermost.
	nested := func(levels int) []byte {
		return append(bytes.Repeat([]byte{1}, levels), 0)
	}
	ok := nested(127)
	var n advNode
	if err := DecodeBinaryExact(ok, &n); err != nil {
		t.Fatal(err)
	}
	if got := mustEncodeBin(t, n); !bytes.Equal(got, ok) {
		t.Fatal("re-encode differs")
	}
	wantBinErr(t, DecodeBinary(nested(128), &n), msgDepth, "128 levels")
	// A megabyte of nesting must error, not overflow the stack.
	wantBinErr(t, DecodeBinary(nested(1<<20), &n), msgDepth, "1M levels")

	// Depth counts nesting, not total sequences: many deep siblings are fine.
	wide := []byte{200, 1} // 200 kids (two-byte varint)
	for i := 0; i < 200; i++ {
		wide = append(wide, nested(120)...)
	}
	n = advNode{}
	if err := DecodeBinaryExact(wide, &n); err != nil || len(n.Kids) != 200 {
		t.Fatalf("wide: %d %v", len(n.Kids), err)
	}
	// Pointer recursion is bounded too.
	type ptrNode struct{ Next *ptrNode }
	var p ptrNode
	wantBinErr(t, DecodeBinary(bytes.Repeat([]byte{1}, 1<<20), &p), msgDepth, "pointer chain")
}

func TestBinAdvStringsAreValidated(t *testing.T) {
	type strs struct {
		S string
		T []string
	}
	v := strs{S: "zero-copy", T: []string{"a", "", "é"}}
	b := mustEncodeBin(t, v)
	var back strs
	if err := DecodeBinaryExact(b, &back); err != nil || !reflect.DeepEqual(back, v) {
		t.Fatalf("%+v %v", back, err)
	}
	var s string
	wantBinErr(t, DecodeBinary([]byte{2, 0xc3, 0x28}, &s), msgInvalidUTF8, "invalid UTF-8")
}
