package asun

// Adversarial format tests. Expected results follow conformance/GRAMMAR.abnf
// (declared authoritative), falling back to docs/SPEC.md where the grammar is
// silent. Each category runs every case and reports every mismatch.
//
// Port of asun-rs tests/adversarial_test.rs. Not ported, as Go has no
// counterpart: `char` targets, enum fields, and Rust tuple fields (Lead.c
// becomes a struct). Go struct tags cannot express a field name containing
// `,` or an empty name, so Weird drops those two fields.

import (
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Harness
// ---------------------------------------------------------------------------

type advReport struct {
	t     *testing.T
	name  string
	cases int
	fails []string
}

func newAdvReport(t *testing.T, name string) *advReport {
	return &advReport{t: t, name: name}
}

// runDecode decodes input into a new value of typ on a goroutine so a hang is
// reported instead of stalling the run (a hung goroutine is leaked).
func runDecode(input string, typ reflect.Type) (v reflect.Value, err error, outcome string) {
	type result struct {
		v   reflect.Value
		err error
		pan any
	}
	ch := make(chan result, 1)
	go func() {
		var r result
		defer func() {
			r.pan = recover()
			ch <- r
		}()
		p := reflect.New(typ)
		r.err = Decode([]byte(input), p.Interface())
		r.v = p.Elem()
	}()
	select {
	case r := <-ch:
		if r.pan != nil {
			return reflect.Value{}, nil, fmt.Sprintf("PANIC %v", r.pan)
		}
		return r.v, r.err, ""
	case <-time.After(5 * time.Second):
		return reflect.Value{}, nil, "HANG (>5s, infinite loop?)"
	}
}

func showInput(s string) string {
	if len(s) > 120 {
		s = s[:120] + "..."
	}
	return fmt.Sprintf("%q", s)
}

func (r *advReport) fail(input, expected, actual string) {
	r.fails = append(r.fails, fmt.Sprintf("FAIL  %s\n      expected %s\n      actual   %s", input, expected, actual))
}

func (r *advReport) ok(input string, expected any) {
	r.cases++
	v, err, outcome := runDecode(input, reflect.TypeOf(expected))
	switch {
	case outcome != "":
		r.fail(showInput(input), fmt.Sprintf("Ok(%+v)", expected), outcome)
	case err != nil:
		r.fail(showInput(input), fmt.Sprintf("Ok(%+v)", expected), "Err("+err.Error()+")")
	case !looseEqual(v, reflect.ValueOf(expected)):
		r.fail(showInput(input), fmt.Sprintf("Ok(%+v)", expected), fmt.Sprintf("Ok(%+v)", v.Interface()))
	}
}

// err expects decoding input into a value of zero's type to fail.
func (r *advReport) err(input string, zero any) {
	r.cases++
	v, err, outcome := runDecode(input, reflect.TypeOf(zero))
	switch {
	case outcome != "":
		r.fail(showInput(input), "Err", outcome)
	case err == nil:
		r.fail(showInput(input), "Err", fmt.Sprintf("Ok(%+v)", v.Interface()))
	}
}

// roundtrip: encode -> decode must reproduce the value exactly.
func (r *advReport) roundtrip(value any) {
	for _, m := range []struct {
		mode string
		enc  func(any) ([]byte, error)
	}{{"plain", Encode}, {"typed", EncodeTyped}, {"pretty", EncodePretty}} {
		r.cases++
		label := fmt.Sprintf("[%s] %+v", m.mode, value)
		func() {
			defer func() {
				if p := recover(); p != nil {
					r.fail(label, "same", fmt.Sprintf("PANIC %v", p))
				}
			}()
			text, err := m.enc(value)
			if err != nil {
				r.fail(label, "same", "encode Err("+err.Error()+")")
				return
			}
			p := reflect.New(reflect.TypeOf(value))
			if err := Decode(text, p.Interface()); err != nil {
				r.fail(label, "same", fmt.Sprintf("decode Err(%v) on %q", err, text))
				return
			}
			if !looseEqual(p.Elem(), reflect.ValueOf(value)) {
				r.fail(label, "same", fmt.Sprintf("decoded %+v from %q", p.Elem().Interface(), text))
			}
		}()
	}
}

// encodeErr records a case where the encoder itself should refuse the value.
func (r *advReport) encodeErr(value any) {
	r.cases++
	if s, err := Encode(value); err == nil {
		r.fail(fmt.Sprintf("encode %+v", value), "Err", fmt.Sprintf("Ok(%q)", s))
	}
}

func (r *advReport) finish() {
	r.t.Helper()
	if len(r.fails) > 0 {
		r.t.Errorf("=== %s — %d cases, %d failed ===\n%s", r.name, r.cases, len(r.fails), strings.Join(r.fails, "\n"))
	}
}

// looseEqual is reflect.DeepEqual except that nil and empty slices are equal
// (Rust has no nil Vec).
func looseEqual(a, b reflect.Value) bool {
	if a.Kind() != b.Kind() {
		return false
	}
	switch a.Kind() {
	case reflect.Slice, reflect.Array:
		if a.Len() != b.Len() {
			return false
		}
		for i := 0; i < a.Len(); i++ {
			if !looseEqual(a.Index(i), b.Index(i)) {
				return false
			}
		}
		return true
	case reflect.Pointer, reflect.Interface:
		if a.IsNil() || b.IsNil() {
			return a.IsNil() == b.IsNil()
		}
		return looseEqual(a.Elem(), b.Elem())
	case reflect.Struct:
		for i := 0; i < a.NumField(); i++ {
			if !looseEqual(a.Field(i), b.Field(i)) {
				return false
			}
		}
		return true
	case reflect.Float32, reflect.Float64:
		return a.Float() == b.Float()
	}
	return reflect.DeepEqual(a.Interface(), b.Interface())
}

// ---------------------------------------------------------------------------
// Target types
// ---------------------------------------------------------------------------

type (
	advUuid struct {
		ID string `asun:"id uuid"`
	}
	advA struct {
		A string `asun:"a"`
	}
	advOA struct {
		A *string `asun:"a"`
	}
	advS struct {
		A string `asun:"a"`
		B int64  `asun:"b"`
	}
	advI struct {
		I int64 `asun:"i"`
	}
	advOI struct {
		I *int64 `asun:"i"`
	}
	advU8 struct {
		U uint8 `asun:"u"`
	}
	advU64 struct {
		U uint64 `asun:"u"`
	}
	advF struct {
		F float64 `asun:"f"`
	}
	advB struct {
		B bool `asun:"b"`
	}
	advV struct {
		V []int64 `asun:"v"`
	}
	advVO struct {
		V []*int64 `asun:"v"`
	}
	advVS struct {
		V []string `asun:"v"`
	}
	advVV struct {
		V [][]int64 `asun:"v"`
	}
	advP struct {
		X int64 `asun:"x"`
		Y int64 `asun:"y"`
	}
	advN struct {
		N advP `asun:"n"`
	}
	advON struct {
		N *advP `asun:"n"`
	}
	advWeird struct {
		Quote string `asun:"q\"q"`
		CJK   string `asun:"中文"`
		Punct string `asun:"{}[]@:"`
		Space string `asun:" "`
		Digit string `asun:"1st"`
	}
	advA2 struct {
		First string `asun:"1st"`
	}
	advDeep struct {
		Name string     `asun:"name"`
		Tags []string   `asun:"tags"`
		Pts  []advP     `asun:"pts"`
		Opt  *[]*advP   `asun:"opt"`
		Grid [][]string `asun:"grid"`
	}
	advPair struct {
		E *int64 `asun:"e"`
		S string `asun:"s"`
	}
	advLead struct {
		A *int64   `asun:"a"`
		B string   `asun:"b"`
		C *advPair `asun:"c"`
	}
	advOdd struct {
		AB int64  `asun:"a-b"`
		SP string `asun:"s p"`
	}
	advLead2 struct {
		Inner []advOdd `asun:"inner"`
	}
)

func pstr(s string) *string { return &s }
func pi64(i int64) *int64   { return &i }

func aa(s string) advA          { return advA{A: s} }
func sb(s string, b int64) advS { return advS{A: s, B: b} }

// ---------------------------------------------------------------------------
// 1. Unquoted / quoted strings
// ---------------------------------------------------------------------------

func TestAdversarialStrings(t *testing.T) {
	r := newAdvReport(t, "strings")

	// Raw `"` is forbidden in a plain string.
	r.err(`{"id uuid"}:(赛d " sdf@ )`, advUuid{})
	r.ok(`{"id uuid"}:("赛d \" sdf@ ")`, advUuid{ID: "赛d \" sdf@ "})
	r.ok(`{"id uuid"@str}:(赛d)`, advUuid{ID: "赛d"})

	// Raw reserved characters in an unquoted value must be rejected.
	r.ok("{a}:(hello@world)", aa("hello@world"))
	r.ok("{a}:(http://x.com)", aa("http://x.com"))
	r.ok("{a}:(a:b)", aa("a:b"))
	r.err("{a}:(a{b)", advA{})
	r.err("{a}:(a}b)", advA{})
	r.err(`{a}:(a"b")`, advA{})
	r.err(`{a}:("a"b)`, advA{})
	r.err(`{a}:("a" "b")`, advA{})
	r.err("{a}:(a\nb)", advA{}) // raw LF inside a plain string
	r.err("{a}:(a\x01b)", advA{})
	r.err("{a}:(\"a\nb\")", advA{}) // raw LF inside a quoted string
	r.err("{a}:(\"a\x00b\")", advA{})

	// Escapes.
	r.ok(`{a}:(\,\(\)\[\]\{\}\:\@\\\")`, aa(`,()[]{}:@\"`))
	r.ok(`{a}:("\n\t\r\b\f")`, aa("\n\t\r\b\f"))
	r.ok(`{a}:(\u4e2d\u6587)`, aa("中文"))
	r.ok(`{a}:("\u4E2D")`, aa("中"))
	r.ok(`{a}:("\uD83D\uDE00")`, aa("😀")) // surrogate pair
	r.err(`{a}:("\uD800")`, advA{})       // lone surrogate is not a scalar value
	r.err(`{a}:("\uDE00x")`, advA{})
	r.err(`{a}:(\u12)`, advA{})
	r.err(`{a}:("\u12G4")`, advA{})
	r.err(`{a}:(\x)`, advA{})
	r.err(`{a}:("\q")`, advA{})
	r.err(`{a}:(abc\)`, advA{}) // `\)` eats the closer
	r.err(`{a}:("abc\")`, advA{})
	r.err(`{a}:("abc)`, advA{})

	// Whitespace and trimming.
	r.ok("{a}:(  spaced   inside  )", aa("spaced   inside"))
	r.ok("{a}:(\tx\t)", aa("x"))
	r.ok("{a}:(a\tb)", aa("a\tb"))
	r.ok(`{a}:("  x  ")`, aa("  x  "))
	r.ok(`{a}:(   "x"   )`, aa("x"))
	r.ok("{a}:(\u3000x\u3000)", aa("\u3000x\u3000")) // only ASCII ws trimmed
	r.ok("{a}:(中文 字符 🎉)", aa("中文 字符 🎉"))
	r.err(`{a}:(\ x\ )`, advA{}) // `\ ` is not in the escape list
	r.ok(`{a}:("")`, aa(""))

	// Things that look like other types but land in a string.
	r.ok("{a}:(- 5)", aa("- 5"))
	r.ok("{a}:(--1)", aa("--1"))
	r.ok("{a}:(.5)", aa(".5"))
	r.ok("{a}:(1e)", aa("1e"))
	r.ok("{a}:(123abc)", aa("123abc"))
	r.ok("{a}:(a/b*c)", aa("a/b*c"))
	r.err("{a}:(null)", advA{}) // keyword null into a string
	r.ok(`{a}:("null")`, aa("null"))
	r.ok("{a}:(True)", aa("True"))
	r.ok("{a}:(null x)", aa("null x"))

	// Null vs empty string.
	r.ok("{a}:()", advOA{})
	r.ok("{a}:(   )", advOA{})
	r.ok(`{a}:("")`, advOA{A: pstr("")})
	r.ok("{a}:(null x)", advOA{A: pstr("null x")})
	r.err("{a}:()", advA{}) // a non-pointer string cannot be null

	r.finish()
}

// ---------------------------------------------------------------------------
// 2. Numbers and booleans
// ---------------------------------------------------------------------------

func TestAdversarialNumbers(t *testing.T) {
	r := newAdvReport(t, "numbers")

	r.ok("{i}:(-0)", advI{0})
	r.ok("{i}:(007)", advI{7})
	r.ok("{i}:(  42  )", advI{42})
	r.ok("{i}:(9223372036854775807)", advI{math.MaxInt64})
	r.ok("{i}:(-9223372036854775808)", advI{math.MinInt64})
	r.err("{i}:(9223372036854775808)", advI{})
	r.err("{i}:(-9223372036854775809)", advI{})
	r.err("{i}:(99999999999999999999999999)", advI{})
	r.ok("{u}:(18446744073709551615)", advU64{math.MaxUint64})
	r.err("{u}:(18446744073709551616)", advU64{})
	r.err("{u}:(-1)", advU64{})
	r.ok("{u}:(-0)", advU64{0})
	r.ok("{u}:(255)", advU8{255})
	r.err("{u}:(256)", advU8{})
	r.err("{u}:(-1)", advU8{})
	for _, in := range []string{
		"{i}:(+5)", "{i}:(- 5)", "{i}:(4 2)", "{i}:(1.0)", "{i}:(1e3)", "{i}:(0x10)",
		"{i}:(1_000)", `{i}:("42")`, "{i}:(42abc)", "{i}:(-)", "{i}:()",
	} {
		r.err(in, advI{})
	}
	r.ok("{i}:()", advOI{})
	r.err("{i}:(abc)", advOI{})

	r.ok("{f}:(3)", advF{3})
	r.ok("{f}:(-0.0)", advF{math.Copysign(0, -1)})
	r.ok("{f}:(1.5e-3)", advF{1.5e-3})
	r.ok("{f}:(-1.0E+100)", advF{-1.0e100})
	r.ok("{f}:(1e10)", advF{1e10})
	r.ok("{f}:(0.30000000000000004)", advF{0.30000000000000004})
	r.ok("{f}:(1.7976931348623157e308)", advF{math.MaxFloat64})
	r.ok("{f}:(5e-324)", advF{5e-324})
	for _, in := range []string{
		"{f}:(1e309)", // overflows to inf: no inf literal in the grammar
		"{f}:(.5)", "{f}:(5.)", "{f}:(+1.5)", "{f}:(1e)", "{f}:(1e+)", "{f}:(NaN)",
		"{f}:(inf)", "{f}:(infinity)", "{f}:(1.2.3)",
		"{f}:(1,5)", // European decimal -> 2 elements
		`{f}:("1.5")`,
	} {
		r.err(in, advF{})
	}

	r.ok("{b}:(true)", advB{true})
	r.ok("{b}:( false )", advB{false})
	for _, in := range []string{
		"{b}:(True)", "{b}:(TRUE)", "{b}:(1)", "{b}:(yes)", `{b}:("true")`, "{b}:(truex)", "{b}:(t)",
	} {
		r.err(in, advB{})
	}

	r.finish()
}

// ---------------------------------------------------------------------------
// 3. Schema header
// ---------------------------------------------------------------------------

func TestAdversarialSchema(t *testing.T) {
	r := newAdvReport(t, "schema")

	r.ok(`{"a",b}:(x,1)`, sb("x", 1))
	r.ok(`{"a"@str,"b"@int}:(x,1)`, sb("x", 1))
	r.ok("{a@ str , b @int}:(x,1)", sb("x", 1))
	r.ok("{ a , b }:(x,1)", sb("x", 1))
	r.err("{a,b,}:(x,1)", advS{})
	r.ok("{a,b} : (x,1)", sb("x", 1))
	r.ok("{a,b}\r\n:\r\n(x,1)", sb("x", 1))
	r.ok("{a /* c */ , b /* } ) , */}:(x,1)", sb("x", 1))
	r.ok("{b,a}:(1,x)", sb("x", 1))       // reordered -> by name
	r.ok("{a,b,c}:(x,1,zzz)", sb("x", 1)) // unknown extra field skipped
	r.ok(`{a,z,b}:(x,\),1)`, sb("x", 1))
	r.ok(`{a,z,b}:(x,")(,\"",1)`, sb("x", 1))
	r.ok("{a,z@{q@[int]},b}:(x,([1,(2)]),1)", sb("x", 1))
	r.ok("{a,z@[{p,q}],b}:(x,[(1,[a,b]),(2,())],1)", sb("x", 1))

	for _, in := range []string{
		"{a b}:(x,1)",  // bare name with space
		"{中文,b}:(x,1)", // non-ASCII bare name
		"{a-b,b}:(x,1)", "{a.b,b}:(x,1)", "{a@,b}:(x,1)", "{a@int@str,b}:(x,1)",
		"{a@integer,b}:(x,1)", "{a@Str,b}:(x,1)", "{a@[,b}:(x,1)", "{a@{,b}:(x,1)",
		"{a@[]}:([])", // empty array type hint is not in the grammar
		"{,a,b}:(x,1)", "{a,,b}:(x,1)",
		"{a,b", "{a,b}", "{a,b}:", "{a,b}(x,1)", "{a,b}::(x,1)", "{a,b}=(x,1)",
		"{a,b}:[x,1]", "{a,b}:{x,1}", "{a,b}:(x,1)(y,2)", "{a,b}:(x,1),(y,2)",
		"{a,b}:(x,1) junk", "{a,b}:(x,1));",
		"(x,1)", // bare tuple without schema
		"", "   ",
		"{a,a}:(x,y)", // duplicate field name
		"{a,b,b}:(x,1,2)",
		"{a@int,b}:(hello,1)", // S3: the hint is authoritative
		"{a,b@str}:(x,1)",     // hint says str, target int64
		`{"a,b}:(x,1)`, `{"a\",b}:(x,1)`,
	} {
		r.err(in, advS{})
	}
	r.ok("{}:()", sb("", 0)) // missing target fields default

	// Odd quoted field names map through tags.
	r.ok(`{"q\"q","中文","{}[]@:"," ","1st"}:(2,3,4,6,7)`,
		advWeird{Quote: "2", CJK: "3", Punct: "4", Space: "6", Digit: "7"})
	r.ok("{1st}:(x)", advA2{First: "x"})

	r.finish()
}

// ---------------------------------------------------------------------------
// 4. Tuple alignment, commas, comments
// ---------------------------------------------------------------------------

func TestAdversarialTuplesAndComments(t *testing.T) {
	r := newAdvReport(t, "tuples_and_comments")

	for _, in := range []string{
		"{a,b}:(x,1,)", "{a,b}:( x , 1 , )", "{a,b}:(x,1,,)", "{a,b}:(x)",
		"{a,b}:(x,)", // b = null into int64
		"{a,b}:(x,1,2)", "{a,b}:(,x,1)", "{a,b}:(x,1", "{a,b}:x,1)", "{a,b}:((x,1))",
		"{a,b}:(x,(1))", "{a,b}:(x,[1])",
	} {
		r.err(in, advS{})
	}

	for _, in := range []string{
		"/* lead */ {a,b}:(x,1)", "{a,b}:(x,1) /* trail */", "{a,b}:/* mid */(x,1)",
		"/**/{a,b}:(x,1)/**/", "/* a */ /* b */ {a,b}:(x,1)", "/* ** / * */{a,b}:(x,1)",
		"{a,b}:(x, /* c */ 1)", "{a,b}:(/* c */x,1)", "{a,b}:(x,1 /* c */)",
		"\ufeff{a,b}:(x,1)", "\n\n\t {a,b}:(x,1) \n\n", "{a,b}:(x,1)\r\n",
	} {
		r.ok(in, sb("x", 1))
	}
	for _, in := range []string{
		"{a,b}:(x,1) /* unclosed", "/* unclosed {a,b}:(x,1)", "/* /* nested */ */{a,b}:(x,1)",
		"// line comment\n{a,b}:(x,1)", "# hash\n{a,b}:(x,1)",
		"{a,b}:(x/*y*/z,1)", // a comment cannot split a value
		"{a,b}:(x,1)\x00", "{a,b}:(x,1)\u3000",
	} {
		r.err(in, advS{})
	}
	r.ok(`{a,b}:("/* not a comment */",1)`, sb("/* not a comment */", 1))

	r.finish()
}

// ---------------------------------------------------------------------------
// 5. Arrays, nesting, slice of structs
// ---------------------------------------------------------------------------

func TestAdversarialArraysAndNesting(t *testing.T) {
	r := newAdvReport(t, "arrays_and_nesting")

	r.ok("{v@[int]}:([1,2,3])", advV{[]int64{1, 2, 3}})
	r.err("{v@[int]}:([1,2,3,])", advV{}) // trailing null into int64
	r.ok("{v@[int]}:([ 1 , 2 ])", advV{[]int64{1, 2}})
	r.ok("{v@[int]}:([])", advV{[]int64{}})
	r.ok("{v@[int]}:([   ])", advV{[]int64{}})
	r.ok("{v}:([1,2])", advV{[]int64{1, 2}}) // binding omitted
	r.err("{v@[int]}:([1,,3])", advV{})
	r.ok("{v@[int]}:([1,,3])", advVO{[]*int64{pi64(1), nil, pi64(3)}})
	for _, in := range []string{
		"{v@[int]}:([1,2,,])", // S5 also applies to arrays -> [1,2,null]
		"{v@[int]}:([1,2)", "{v@[int]}:([1,2]]", "{v@[int]}:((1,2))", "{v@[int]}:(1,2)",
		"{v@[int]}:()", "{v@[int]}:([1,[2]])", "{v@[int]}:([1 2])",
	} {
		r.err(in, advV{})
	}
	r.ok("{v@[int]}:([/* c */1])", advV{[]int64{1}})
	r.ok(`{v@[str]}:([a\,b,"c,d",\],""])`, advVS{[]string{"a,b", "c,d", "]", ""}})
	r.ok("{v@[str]}:([a:b])", advVS{[]string{"a:b"}})
	r.ok("{v@[[int]]}:([[1],[2,3],[]])", advVV{[][]int64{{1}, {2, 3}, {}}})
	r.err("{v@[[int]]}:([1,[2]])", advVV{})

	r.ok("{n@{x,y}}:((1,2))", advN{advP{1, 2}})
	r.ok("{n@{y,x}}:((2,1))", advN{advP{1, 2}}) // nested reorder
	r.err("{n@{x,y}}:(())", advON{})            // empty tuple for a 2-field schema
	r.ok("{n@{x,y}}:()", advON{})
	for _, in := range []string{
		"{n@{x,y}}:((1,2,3))", "{n@{x,y}}:((1))", "{n@{x,y}}:([1,2])", "{n@{x,y}}:(1,2)",
		"{n@{x,y}}:({1,2})", "{n@{x,y}}:(((1,2)))",
	} {
		r.err(in, advN{})
	}

	v2 := []advS{sb("x", 1), sb("y", 2)}
	for _, in := range []string{
		"[{a,b}]:(x,1),(y,2)", "[{a,b}]:\n  (x,1),\n  (y,2)\n", "[ {a,b} ] : (x,1) , (y,2)",
		"[{b,a}]:(1,x),(2,y)", "[{a,b}]:(x,1),/* c */(y,2)",
	} {
		r.ok(in, v2)
	}
	r.ok("[]", []advS{})
	r.ok("[{a,b}]:", []advS{})
	for _, in := range []string{
		"[{a,b}]:(x,1),(y,2),", "[{a,b}]:(x,1)(y,2)", "[{a,b}]:(x,1),,(y,2)", "[{a,b}]:,(x,1)",
		"[{a,b}]:(x,1),(y)", "[{a,b}]:(x,1),(y,2,3)", "[{a,b}]:(x,1),(y,two)", "[{a,b}:(x,1)",
		"[{a,b}]]:(x,1)", "[{a,b}]:[(x,1)]",
		"{a,b}:(x,1)", // single-object form into a slice
	} {
		r.err(in, []advS{})
	}
	r.err("[{a,b}]:(x,1)", advS{}) // array form into a single struct

	r.finish()
}

// ---------------------------------------------------------------------------
// 6. Hostile sizes / depth — must error cleanly, never crash
// ---------------------------------------------------------------------------

func TestAdversarialHostileInputs(t *testing.T) {
	r := newAdvReport(t, "hostile_inputs")

	r.err("{v@[[int]]}:("+strings.Repeat("[", 200_000)+strings.Repeat("]", 200_000)+")", advVV{})
	r.err("{v@"+strings.Repeat("[", 200_000)+"int"+strings.Repeat("]", 200_000)+"}:([])", advV{})
	r.err("{a,z@"+strings.Repeat("{z@", 100_000)+"q"+strings.Repeat("}", 100_000)+"}:(x)", advA{})
	// The nesting cap applies to skipped values too (SPEC 9.1).
	r.err("{a,z}:(x,"+strings.Repeat("(", 200_000)+strings.Repeat(")", 200_000)+")", advA{})
	r.err("{a}:(\""+strings.Repeat("x", 1_000_000), advA{})
	var fields strings.Builder
	for i := 0; i < 50_000; i++ {
		fmt.Fprintf(&fields, "f%d,", i)
	}
	r.ok("{a,"+fields.String()+"b}:(x,"+strings.Repeat(",", 50_000)+"1)", sb("x", 1))
	big := strings.Repeat("y", 5_000_000)
	r.ok("{a}:("+big+")", aa(big))
	r.ok("{a}:("+strings.Repeat(`\\`, 100_000)+")", aa(strings.Repeat(`\`, 100_000)))
	r.err("{a}:("+strings.Repeat(`\\`, 1000)+`\)`, advA{})
	// Truncate a valid document at every byte: never panic.
	doc := `[{a,z@{p@[{q,r}]},b}]:(x,([(1,"a\"b"),(2,[c])]),1),(y,(),2)`
	for cut := 0; cut < len(doc); cut++ {
		p := doc[:cut]
		// Cutting right after a complete row (`...,1)` / `...,1),`) is valid.
		if !strings.HasSuffix(p, ",1)") && !strings.HasSuffix(p, ",1),") && !strings.HasSuffix(p, ":") {
			r.err(p, []advS{})
		}
	}

	r.finish()
}

// ---------------------------------------------------------------------------
// 7. The encoder must produce text its own decoder accepts unchanged
// ---------------------------------------------------------------------------

func TestAdversarialEncodeRoundtrip(t *testing.T) {
	r := newAdvReport(t, "encode_roundtrip")

	tricky := []string{
		"", " ", "  x  ", "\t", "true", "false", "123", "-5", "007", "1.5", "1e5", "-0",
		"- 5", ".5", "null", "a,b", "(", ")", "[", "]", "{", "}", ":", "@", "\"", "\\",
		"\\u0041", "\n", "\r\n", "\t", "\x00", "\x1f", "\x7f", "/*", "*/", "/* x */",
		"a/*b*/c", "中文", "😀", "a\u2028b", "\ufeff", "\u3000x", "x\u3000", "{a}:(b)",
		"[{a}]:(b),(c)", "\"quoted\"", "trailing\\", "http://x.com/?a=1&b=(2)",
		"e@mail.com", "#tag", "'single'", "`tick`", "null x", "true x", "123 abc",
	}
	for _, s := range tricky {
		r.roundtrip(advA{s})
		r.roundtrip(advOA{pstr(s)})
	}
	r.roundtrip(advOA{})
	r.roundtrip(advVS{tricky})
	r.roundtrip(advVS{[]string{}})
	r.roundtrip(advVS{[]string{""}})
	r.roundtrip(advVO{[]*int64{nil}})
	r.roundtrip(advVO{[]*int64{nil, nil}})
	r.roundtrip(advVO{[]*int64{pi64(1), nil}})
	r.roundtrip(advVV{[][]int64{{}}})
	r.roundtrip(advVV{[][]int64{{}, {}}})
	r.roundtrip(advON{})
	r.roundtrip(advON{&advP{-1, 0}})
	r.roundtrip([]advS{})
	r.roundtrip([]advS{sb("", 0)})
	r.roundtrip([]advS{sb(",", math.MinInt64), sb(")", math.MaxInt64)})
	r.roundtrip(advI{math.MinInt64})
	r.roundtrip(advU64{math.MaxUint64})
	for _, f := range []float64{
		0, math.Copysign(0, -1), 0.30000000000000004, 1e-300, 5e-324, math.MaxFloat64,
		-math.MaxFloat64, 2.2250738585072014e-308, 1e21, 123456789.0,
	} {
		r.roundtrip(advF{f})
	}
	r.roundtrip(advWeird{Quote: "\"", CJK: "中", Punct: "@", Space: " ", Digit: "1"})
	r.roundtrip(advUuid{ID: "赛d \" sdf@ "})
	r.roundtrip(advDeep{
		Name: "  (deep)  ",
		Tags: []string{"", ",", "]"},
		Pts:  []advP{{1, 2}},
		Opt:  &[]*advP{nil, {3, 4}, nil},
		Grid: [][]string{{}, {"", " "}, {"["}},
	})
	r.roundtrip(advDeep{Opt: &[]*advP{}})
	r.roundtrip(advDeep{Name: "x"})

	// Non-finite floats have no textual form in the grammar.
	r.encodeErr(advF{math.NaN()})
	r.encodeErr(advF{math.Inf(1)})
	r.encodeErr(advF{math.Inf(-1)})

	// Pretty expansion (> 100 columns) must keep a leading null slot.
	long := strings.Repeat("z", 120)
	r.roundtrip(advLead{B: long})
	r.roundtrip([]advLead{{B: long, C: &advPair{S: long}}})
	r.roundtrip(advLead{A: pi64(1), B: "b", C: &advPair{E: pi64(1), S: "t"}})
	// Field names outside bare-name must be quoted in every header form.
	odd := advOdd{AB: 1, SP: "x"}
	r.roundtrip(odd)
	r.roundtrip([]advOdd{odd, odd})
	r.roundtrip(advLead2{[]advOdd{odd}})

	r.finish()
}
