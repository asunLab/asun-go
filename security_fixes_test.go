package asun

import (
	"math"
	"strings"
	"testing"
)

// P0-2: deep nesting must return an error, not fatally overflow the stack.
func TestFix_DeepNestingBounded(t *testing.T) {
	type Any struct{ V any }
	data := "{V}:(" + strings.Repeat("[", 100000) + strings.Repeat("]", 100000) + ")"
	var a Any
	err := Decode([]byte(data), &a)
	if err == nil {
		t.Fatal("expected depth-limit error for deeply nested input")
	}
	if !strings.Contains(err.Error(), "depth limit") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// Legitimate nesting below the limit must still decode.
func TestFix_NestingWithinLimitOK(t *testing.T) {
	type Any struct{ V any }
	// 100 levels — well within maxDepth (128).
	data := "{V}:(" + strings.Repeat("[", 100) + strings.Repeat("]", 100) + ")"
	var a Any
	if err := Decode([]byte(data), &a); err != nil {
		t.Fatalf("legitimate nesting rejected: %v", err)
	}
}

// P1-6: schema cache is capped, not unbounded.
func TestFix_SchemaCacheBounded(t *testing.T) {
	type S struct{ A, B int }
	for i := 0; i < maxCachedSchemas*3; i++ {
		// Distinct schema key per iteration via a unique extra field name.
		pad := strings.Repeat("x", (i%128)+1) + intToKey(i)
		data := []byte("{A,B," + pad + "}:(1,2,9)")
		var s S
		_ = Decode(data, &s)
	}
	count := 0
	schemaCache.m.Range(func(_, _ any) bool { count++; return true })
	if int64(count) > maxCachedSchemas {
		t.Fatalf("cache exceeded cap: %d > %d", count, maxCachedSchemas)
	}
	t.Logf("cache bounded at %d entries (cap %d)", count, maxCachedSchemas)
}

func intToKey(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('a' + n%26)}, b...)
		n /= 26
	}
	return string(b)
}

// decode-side float precision: fractional values must round-trip correctly.
func TestFix_DecodeFloatPrecision(t *testing.T) {
	cases := []struct {
		in   string
		want float64
	}{
		{"0.3", 0.3},
		{"2.675", 2.675},
		{"0.1", 0.1},
		{"8.61", 8.61},
		{"9.95", 9.95},
		{"123456789.123456789", 123456789.123456789},
		{"-0.3", -0.3},
	}
	for _, c := range cases {
		type F struct{ V float64 }
		var f F
		if err := Decode([]byte("{V}:("+c.in+")"), &f); err != nil {
			t.Fatalf("decode %q: %v", c.in, err)
		}
		if f.V != c.want {
			t.Errorf("decode %q -> %v, want %v", c.in, f.V, c.want)
		}
	}
}

// P2-7: \uXXXX escapes must combine surrogate pairs and reject lone/unpaired
// surrogates instead of silently emitting U+FFFD.
func TestFix_UnicodeSurrogates(t *testing.T) {
	type S struct {
		V string `asun:"v"`
	}
	// Valid: surrogate pair for 😀 (U+1F600) and raw astral char must decode.
	ok := []struct{ in, want string }{
		{`{v}:("😀")`, "\U0001F600"},
		{`{v}:("A")`, "A"},
	}
	for _, c := range ok {
		var s S
		if err := Decode([]byte(c.in), &s); err != nil {
			t.Fatalf("decode %q: %v", c.in, err)
		}
		if s.V != c.want {
			t.Errorf("decode %q -> %q, want %q", c.in, s.V, c.want)
		}
	}
	// Invalid: lone/unpaired/mispaired surrogates and bad hex must error.
	bad := []string{
		`{v}:("\uD800")`,       // lone high surrogate
		`{v}:("\uDC00")`,       // lone low surrogate
		`{v}:("\uD83Dabc")`,    // high surrogate not followed by \u
		`{v}:("\uD800A")`, // high surrogate followed by non-low escape
	}
	for _, in := range bad {
		var s S
		if err := Decode([]byte(in), &s); err == nil {
			t.Errorf("expected error for %q, got %q", in, s.V)
		}
	}
}

// encode -0.0 must preserve the sign; float encode must round-trip.
func TestFix_EncodeFloat(t *testing.T) {
	if got := string(appendFloat64(nil, math.Copysign(0, -1))); got != "-0.0" {
		t.Errorf("-0.0 encoded as %q, want %q", got, "-0.0")
	}
	// Round-trip a large random-ish spread through encode+decode.
	type F struct{ V float64 }
	vals := []float64{0.1, 0.2, 0.3, 8.61, 2.35, 0.07, 9.95, 1.15, 2.675, 3.14159265358979, 1e-7, 1234.5678}
	for _, v := range vals {
		enc, err := Encode(F{V: v})
		if err != nil {
			t.Fatal(err)
		}
		var out F
		if err := Decode(enc, &out); err != nil {
			t.Fatalf("decode %s: %v", enc, err)
		}
		if out.V != v {
			t.Errorf("round-trip %v -> %s -> %v", v, enc, out.V)
		}
	}
}
