package asun

// Regression tests for correctness and hardening fixes.
//
// Port of asun-rs tests/hardening_test.rs. Rust's borrowed `&str` fields map
// to Go strings that share the input buffer; per-thread caches map to
// concurrent goroutines.

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"testing"
	"unsafe"
)

type hS struct {
	A string `asun:"a"`
	B int64  `asun:"b"`
}

func decodeHS(t *testing.T, input string) hS {
	t.Helper()
	var v hS
	if err := Decode([]byte(input), &v); err != nil {
		t.Fatalf("%q: %v", input, err)
	}
	return v
}

func decodeHSErr(input string) error {
	var v hS
	return Decode([]byte(input), &v)
}

// ---------------------------------------------------------------------------
// Schema scanning / cache
// ---------------------------------------------------------------------------

func TestHardeningQuotedBraceInFieldNameIsNotStructural(t *testing.T) {
	type q struct {
		A string `asun:"a}b"`
		B int64  `asun:"b"`
	}
	input := []byte(`{"a}b",b}:(hello,7)`)
	var first, second q
	if err := Decode(input, &first); err != nil {
		t.Fatal("first decode:", err)
	}
	if err := Decode(input, &second); err != nil {
		t.Fatal("second decode:", err)
	}
	if first != second || first.B != 7 || first.A != "hello" {
		t.Fatalf("%+v %+v", first, second)
	}
}

func TestHardeningQuotedBraceSchemaDoesNotCollide(t *testing.T) {
	// Field names that do not match the target are bound by position.
	if v := decodeHS(t, `{"a}x",b}:(one,1)`); v.B != 1 {
		t.Fatalf("%+v", v)
	}
	if v := decodeHS(t, `{"a}yyyy",b}:(two,2)`); v.B != 2 {
		t.Fatalf("%+v", v)
	}
}

func TestHardeningCommentInsideSchemaIsSkipped(t *testing.T) {
	if v := decodeHS(t, "{a/* } not a brace */,b}:(hi,3)"); v.B != 3 {
		t.Fatalf("%+v", v)
	}
}

func TestHardeningUnterminatedQuoteInSchemaErrors(t *testing.T) {
	if err := decodeHSErr(`{"unterminated,b}:(x,1)`); err == nil {
		t.Fatal("expected an error")
	}
}

// The schema cache is bounded; decoding far more distinct schemas than the cap
// must stay correct (eviction must not hand back a stale entry).
func TestHardeningSchemaCacheEvictionStaysCorrect(t *testing.T) {
	type s3 struct {
		A string `asun:"a"`
		B int64  `asun:"b"`
		C int64  `asun:"c"`
	}
	for i := 0; i < 3*maxCachedSchemas/2; i++ {
		var v s3
		in := fmt.Sprintf("{a,b,filler_%d}:(v%d,%d,0)", i, i, i)
		if err := Decode([]byte(in), &v); err != nil {
			t.Fatal(err)
		}
		if v.B != int64(i) || v.A != "v"+strconv.Itoa(i) {
			t.Fatalf("%d: %+v", i, v)
		}
	}
}

// ---------------------------------------------------------------------------
// Depth limiting
// ---------------------------------------------------------------------------

func TestHardeningDeeplyNestedArrayAnnotationIsRejected(t *testing.T) {
	const depth = 200_000
	in := "{a@" + strings.Repeat("[", depth) + strings.Repeat("]", depth) + ",b}:(x,1)"
	err := decodeHSErr(in)
	if err == nil || !strings.Contains(err.Error(), msgDepth) {
		t.Fatalf("want depth error, got %v", err)
	}
}

func TestHardeningDeeplyNestedSchemaAnnotationIsRejected(t *testing.T) {
	const depth = 50_000
	in := "{a@" + strings.Repeat("{x@", depth) + "int" + strings.Repeat("}", depth) + ",b}:(x,1)"
	err := decodeHSErr(in)
	if err == nil || !strings.Contains(err.Error(), msgDepth) {
		t.Fatalf("want depth error, got %v", err)
	}
}

func TestHardeningRealisticNestingStillWorks(t *testing.T) {
	type L4 struct {
		V int64 `asun:"v"`
	}
	type L3 struct {
		Items []L4 `asun:"items"`
	}
	type L2 struct {
		Items []L3 `asun:"items"`
	}
	type L1 struct {
		Items []L2 `asun:"items"`
	}
	v := L1{Items: []L2{{Items: []L3{{Items: []L4{{V: 42}}}}}}}
	text, err := Encode(v)
	if err != nil {
		t.Fatal(err)
	}
	var back L1
	if err := Decode(text, &back); err != nil {
		t.Fatal(err)
	}
	if back.Items[0].Items[0].Items[0].V != 42 {
		t.Fatalf("%s -> %+v", text, back)
	}
}

// ---------------------------------------------------------------------------
// Escapes and UTF-8
// ---------------------------------------------------------------------------

func TestHardeningPlainValueEscapePreservesUTF8(t *testing.T) {
	if v := decodeHS(t, `{a,b}:(你好\,世界,1)`); v.A != "你好,世界" {
		t.Fatalf("%q", v.A)
	}
	v := decodeHS(t, `{a,b}:(日本語\:テスト\(x\),9)`)
	if v.A != "日本語:テスト(x)" || v.B != 9 {
		t.Fatalf("%+v", v)
	}
}

func TestHardeningQuotedStringPreservesUTF8WithEscapes(t *testing.T) {
	if v := decodeHS(t, `{a,b}:("héllo\n世界\t✓",1)`); v.A != "héllo\n世界\t✓" {
		t.Fatalf("%q", v.A)
	}
}

func TestHardeningUnicodeEscapes(t *testing.T) {
	// U+1F600 GRINNING FACE, written as a UTF-16 surrogate pair.
	if v := decodeHS(t, `{a,b}:("\uD83D\uDE00",1)`); v.A != "😀" {
		t.Fatalf("%q", v.A)
	}
	if v := decodeHS(t, `{a,b}:("\u4f60\u597d",1)`); v.A != "你好" {
		t.Fatalf("%q", v.A)
	}
	for _, in := range []string{`{a,b}:("\uD83D",1)`, `{a,b}:("\uDE00",1)`} {
		err := decodeHSErr(in)
		if err == nil || !strings.Contains(err.Error(), "invalid unicode escape") {
			t.Errorf("%s: want invalid unicode escape, got %v", in, err)
		}
	}
	for _, bad := range []string{`"\u+123"`, `"\uZZZZ"`, `"\u12"`, `"\u 123"`, `"\u你好x"`} {
		if err := decodeHSErr("{a,b}:(" + bad + ",1)"); err == nil {
			t.Errorf("expected error for %s", bad)
		}
	}
}

func TestHardeningSpecialCharStringsRoundtrip(t *testing.T) {
	for _, s := range []string{
		"plain", "", " leading", "trailing ", "with,comma", "with(paren)",
		"with{brace}", "with:colon", "with@at", "with\"quote", "with\\backslash",
		"with\nnewline", "with\ttab", "true", "false", "123", "-4.5e10",
		"with\x7fdel", "unicode 世界 🌍",
		"a longer string that exceeds sixteen bytes, with a comma",
		"null x", "true x", "123 abc",
	} {
		v := hS{A: s, B: 1}
		text, err := Encode(v)
		if err != nil {
			t.Fatal(err)
		}
		var back hS
		if err := Decode(text, &back); err != nil {
			t.Fatalf("%q -> %s -> %v", s, text, err)
		}
		if back != v {
			t.Fatalf("roundtrip failed for %q (encoded %s)", s, text)
		}
	}
}

// ---------------------------------------------------------------------------
// Numbers
// ---------------------------------------------------------------------------

type hNarrow struct {
	I int8  `asun:"i"`
	U uint8 `asun:"u"`
	W int32 `asun:"w"`
}

func TestHardeningOutOfRangeIntegersAreRejected(t *testing.T) {
	for _, in := range []string{
		"{i,u,w}:(999,0,0)", "{i,u,w}:(0,999,0)", "{i,u,w}:(0,0,5000000000)", "{i,u,w}:(0,-1,0)",
	} {
		var v hNarrow
		err := Decode([]byte(in), &v)
		if err == nil || !strings.Contains(err.Error(), msgIntRange) {
			t.Errorf("%s: want %q, got %v", in, msgIntRange, err)
		}
	}
	var v hNarrow
	if err := Decode([]byte("{i,u,w}:(-128,255,-2147483648)"), &v); err != nil {
		t.Fatal(err)
	}
	if v != (hNarrow{-128, 255, -2147483648}) {
		t.Fatalf("%+v", v)
	}
}

type hF struct {
	X float64 `asun:"x"`
}

func floatRoundtrip(t *testing.T, v float64) {
	t.Helper()
	text, err := Encode(hF{X: v})
	if err != nil {
		t.Fatal(err)
	}
	var back hF
	if err := Decode(text, &back); err != nil {
		t.Fatalf("%v encoded as %s: %v", v, text, err)
	}
	if math.Float64bits(back.X) != math.Float64bits(v) {
		t.Fatalf("%v encoded as %s decoded as %v", v, text, back.X)
	}
}

func TestHardeningFloatRoundtripIsExact(t *testing.T) {
	state := uint64(0x243F_6A88_85A3_08D3)
	checked := 0
	for i := 0; i < 300_000; i++ {
		state ^= state << 13
		state ^= state >> 7
		state ^= state << 17
		v := math.Float64frombits(state)
		if math.IsInf(v, 0) || math.IsNaN(v) {
			continue
		}
		floatRoundtrip(t, v)
		checked++
	}
	if checked <= 100_000 {
		t.Fatalf("only %d checked", checked)
	}
}

func TestHardeningFloatSpecialValuesRoundtrip(t *testing.T) {
	for _, v := range []float64{
		0, math.Copysign(0, -1), 1, -1, 0.5, -0.5, 0.25, 50.5, 1e-300, 1e300,
		-math.MaxFloat64, math.MaxFloat64, 2.2250738585072014e-308,
		9_007_199_254_740_992, 9_007_199_254_740_993, 1.7976931348623157e308,
	} {
		floatRoundtrip(t, v)
	}
}

func TestHardeningNegativeZeroKeepsItsSign(t *testing.T) {
	text, err := Encode(hF{X: math.Copysign(0, -1)})
	if err != nil {
		t.Fatal(err)
	}
	if string(text) != "{x}:(-0.0)" {
		t.Fatalf("got %s", text)
	}
	var back hF
	if err := Decode(text, &back); err != nil || !math.Signbit(back.X) {
		t.Fatalf("%v %v", back.X, err)
	}
}

func TestHardeningNonFiniteFloatsAreRejected(t *testing.T) {
	for _, v := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if _, err := Encode(hF{X: v}); err == nil {
			t.Errorf("%v encoded", v)
		}
	}
}

func TestHardeningF32IsRoundedOnce(t *testing.T) {
	type f32 struct {
		V float32 `asun:"v"`
	}
	const lit = "1.00000017881393432617187499"
	want, _ := strconv.ParseFloat(lit, 32)
	if float32(want) != 1+float32(math.Nextafter32(1, 2)-1) {
		t.Fatalf("bad literal: %v", want)
	}
	var got f32
	if err := Decode([]byte("{v}:("+lit+")"), &got); err != nil {
		t.Fatal(err)
	}
	if math.Float32bits(got.V) != math.Float32bits(float32(want)) {
		t.Fatalf("got %v want %v", got.V, float32(want))
	}
}

// ---------------------------------------------------------------------------
// Zero-copy decode
// ---------------------------------------------------------------------------

func inBuffer(buf []byte, s string) bool {
	if len(s) == 0 {
		return false
	}
	base := uintptr(unsafe.Pointer(unsafe.SliceData(buf)))
	p := uintptr(unsafe.Pointer(unsafe.StringData(s)))
	return p >= base && p < base+uintptr(len(buf))
}

type hBorrowed struct {
	ID   int64  `asun:"id"`
	Name string `asun:"name"`
	City string `asun:"city"`
}

func TestHardeningPlainStringsShareTheInput(t *testing.T) {
	input := []byte("{id,name,city}:(7,Alice,Shanghai)")
	var v hBorrowed
	if err := Decode(input, &v); err != nil {
		t.Fatal(err)
	}
	if v.ID != 7 || v.Name != "Alice" || !inBuffer(input, v.Name) {
		t.Fatalf("%+v (shared=%v)", v, inBuffer(input, v.Name))
	}
	rows := []byte("[{id,name,city}]:(1,Alice,SH),(2,Bob,BJ)")
	var vs []hBorrowed
	if err := Decode(rows, &vs); err != nil {
		t.Fatal(err)
	}
	if len(vs) != 2 || vs[1].Name != "Bob" || !inBuffer(rows, vs[1].Name) {
		t.Fatalf("%+v", vs)
	}
}

// ---------------------------------------------------------------------------
// Allocation behaviour
// ---------------------------------------------------------------------------

// A nested array must be sized from its own contents, never from "how much
// input is left", which would make decoding quadratic.
func TestHardeningNestedArraysAreNotOverReserved(t *testing.T) {
	type row struct {
		ID   int64   `asun:"id"`
		Tags []int64 `asun:"tags"`
	}
	rows := make([]row, 2000)
	for i := range rows {
		rows[i] = row{ID: int64(i), Tags: []int64{int64(i), int64(i + 1)}}
	}
	text, err := Encode(rows)
	if err != nil {
		t.Fatal(err)
	}
	if len(text) <= 20_000 {
		t.Fatal("need a document big enough to matter")
	}
	var back []row
	if err := Decode(text, &back); err != nil {
		t.Fatal(err)
	}
	if len(back) != len(rows) {
		t.Fatalf("len %d", len(back))
	}
	for _, r := range back {
		if len(r.Tags) != 2 || cap(r.Tags) > 8 {
			t.Fatalf("nested slice len %d cap %d", len(r.Tags), cap(r.Tags))
		}
	}
}

// ---------------------------------------------------------------------------
// Concurrency
// ---------------------------------------------------------------------------

func TestHardeningConcurrentDecodesAreIndependent(t *testing.T) {
	type s3 struct {
		A string `asun:"a"`
		B int64  `asun:"b"`
		C int64  `asun:"c"`
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				var v s3
				in := fmt.Sprintf("{a,b,t%d_%d}:(x%d,%d,0)", g, i, i, i)
				if err := Decode([]byte(in), &v); err != nil {
					errs <- err
					return
				}
				if v.B != int64(i) || v.A != "x"+strconv.Itoa(i) {
					errs <- fmt.Errorf("%s -> %+v", in, v)
					return
				}
			}
		}(g)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

// `null x` is a plain string (S2: only exactly `null` is the keyword).
func TestHardeningNullPrefixedPlainStringRoundTrips(t *testing.T) {
	type o struct {
		S *string `asun:"s"`
		N *string `asun:"n"`
	}
	s := "null x"
	text, err := Encode(o{S: &s})
	if err != nil {
		t.Fatal(err)
	}
	if string(text) != "{s,n}:(null x,)" {
		t.Fatalf("got %s", text)
	}
	var back o
	if err := Decode(text, &back); err != nil || back.S == nil || *back.S != s || back.N != nil {
		t.Fatalf("%+v %v", back, err)
	}
	// The keyword itself, with trailing layout, is still null.
	back = o{S: &s, N: &s}
	if err := Decode([]byte("{s,n}:(null /* c */ , null )"), &back); err != nil || back.S != nil || back.N != nil {
		t.Fatalf("%+v %v", back, err)
	}
	// Untyped decoding resolves whole tokens only.
	var m any
	if err := Decode([]byte("{a,b,c,d}:(null x,true x,123 abc,1.5 kg)"), &m); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"a": "null x", "b": "true x", "c": "123 abc", "d": "1.5 kg"}
	if fmt.Sprint(m) != fmt.Sprint(want) {
		t.Fatalf("got %v", m)
	}
}
