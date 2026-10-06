package asun

import (
	"bytes"
	"math"
	"reflect"
	"strconv"
	"sync"
)

// ---------------------------------------------------------------------------
// Encode — Go values to ASUN text
//
// The output is byte-for-byte what asun-rs produces for the equivalent Rust
// value: a struct is `{schema}:(...)`, a slice of structs `[{schema}]:(...)`,
// anything else a plain value. Schema bindings come from the encoded values
// (the first row of a slice of structs), exactly as in asun-rs: nested
// structs get `@{...}`, slices `@[...]`, and EncodeTyped adds scalar hints.
// ---------------------------------------------------------------------------

// Encode serializes v to ASUN text with a plain schema.
func Encode(v any) ([]byte, error) {
	return encodeInner(v, false)
}

// EncodeTyped serializes v to ASUN text with scalar type hints in the schema.
func EncodeTyped(v any) ([]byte, error) {
	return encodeInner(v, true)
}

var encPool = sync.Pool{New: func() any { return &encoder{} }}

func encodeInner(v any, typed bool) ([]byte, error) {
	rv := reflect.ValueOf(v)
	if out, ok, err := encodeStaticTop(rv, typed); ok {
		return out, err
	}
	if rv.IsValid() {
		if err := ensureNoMapType(rv.Type()); err != nil {
			return nil, err
		}
	}
	e := encPool.Get().(*encoder)
	e.reset(typed)
	err := e.encodeValue(rv)
	var out []byte
	if err == nil {
		if len(e.buf) == 0 {
			// A top-level null writes nothing into its (absent) slot, and an
			// empty document is invalid.
			e.buf = append(e.buf, "null"...)
		}
		out = append([]byte(nil), e.buf...)
	}
	if cap(e.buf) <= 1<<16 {
		encPool.Put(e)
	}
	return out, err
}

// encodeStaticTop encodes a static struct (or a non-nil pointer to one)
// directly; ok is false for any other value. Static structs hold no maps.
func encodeStaticTop(rv reflect.Value, typed bool) (_ []byte, ok bool, _ error) {
	if rv.Kind() == reflect.Pointer && !rv.IsNil() {
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return nil, false, nil
	}
	si := getStructInfo(rv.Type())
	if !si.static {
		return nil, false, nil
	}
	hdr := staticHeader(si, typed)
	if hdr == nil {
		return nil, false, nil
	}
	e := encPool.Get().(*encoder)
	buf := append(e.buf[:0], hdr...)
	buf = append(buf, ':', '(')
	buf, err := appendStaticFields(buf, rv, si)
	var out []byte
	if err == nil {
		buf = append(buf, ')')
		out = append([]byte(nil), buf...)
	}
	e.buf = buf
	if cap(buf) <= 1<<16 {
		encPool.Put(e)
	}
	return out, true, err
}

// encoder mirrors asun-rs `Encoder`, including its schema capture state.
type encoder struct {
	buf     []byte
	inTuple bool
	first   bool
	typed   bool
	// Type hint recorded for the field being encoded ("" = none).
	hint string
	// Top-level slice of structs.
	inTopSeq        bool
	topSeqDataStart int
	topSeqCaptured  bool
	topSeqFields    []string
	topSeqTypes     []string
	topSeqSchemas   [][]byte
	// Schema fragment bubbled up from a nested struct / slice (nil = none).
	nestedSchema []byte
	// Rows 2+ of a homogeneous slice of structs reuse row 1's schema.
	skipCapture bool
	// The top-level sequence is encoding one of its direct elements.
	topSeqDirect bool
	// Cached header of a static row type (nil = use the capture).
	topSeqStatic []byte
	// Set while computing a static header, which must use the capture.
	noStatic bool
	// Header of the last top-level struct, without the `:`.
	topHeader []byte
	// One-entry struct metadata cache: rows repeat one type.
	siType reflect.Type
	si     *structInfo
}

func (e *encoder) reset(typed bool) {
	*e = encoder{buf: e.buf[:0], first: true, typed: typed, siType: e.siType, si: e.si}
}

func (e *encoder) structInfo(t reflect.Type) *structInfo {
	if t != e.siType {
		e.si = getStructInfo(t)
		e.siType = t
	}
	return e.si
}

// staticHeader returns the cached `{schema}` of a static struct type: the
// capture of its zero value, which is the capture of every value.
func staticHeader(si *structInfo, typed bool) []byte {
	i := 0
	if typed {
		i = 1
	}
	if h := si.headers[i].Load(); h != nil {
		return *h
	}
	c := &encoder{first: true, typed: typed, noStatic: true}
	if err := c.encodeStruct(reflect.New(si.structType).Elem()); err != nil {
		return nil
	}
	h := bytes.Clone(c.topHeader)
	h = h[:len(h):len(h)]
	si.headers[i].Store(&h)
	return h
}

func (e *encoder) pushSeparator() {
	if !e.first {
		e.buf = append(e.buf, ',')
	}
	e.first = false
}

func (e *encoder) setHint(h string) {
	if e.typed && e.hint == "" {
		e.hint = h
	}
}

func (e *encoder) encodeValue(rv reflect.Value) error {
	switch rv.Kind() {
	case reflect.Invalid:
		e.pushSeparator()
	case reflect.Bool:
		e.pushSeparator()
		e.setHint("bool")
		if rv.Bool() {
			e.buf = append(e.buf, "true"...)
		} else {
			e.buf = append(e.buf, "false"...)
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		e.pushSeparator()
		e.setHint("int")
		e.buf = strconv.AppendInt(e.buf, rv.Int(), 10)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		e.pushSeparator()
		e.setHint("int")
		e.buf = strconv.AppendUint(e.buf, rv.Uint(), 10)
	case reflect.Float32, reflect.Float64:
		v := rv.Float()
		if math.IsInf(v, 0) || math.IsNaN(v) {
			return errNonFinite
		}
		e.pushSeparator()
		e.setHint("float")
		e.buf = appendFloat64(e.buf, v)
	case reflect.String:
		e.pushSeparator()
		e.setHint("str")
		e.buf = appendStr(e.buf, rv.String())
	case reflect.Pointer, reflect.Interface:
		if rv.IsNil() {
			e.pushSeparator()
			return nil
		}
		return e.encodeValue(rv.Elem())
	case reflect.Slice, reflect.Array:
		return e.encodeSeq(rv)
	case reflect.Struct:
		return e.encodeStruct(rv)
	case reflect.Map:
		return errMapFieldsUnsupported
	default:
		return &MarshalError{Message: "unsupported type " + rv.Type().String()}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Sequences (asun-rs `begin_seq` / `SeqEncoder`)
// ---------------------------------------------------------------------------

func (e *encoder) encodeSeq(rv reflect.Value) error {
	n := rv.Len()
	if n > 0 && !e.noStatic {
		if et := rv.Type().Elem(); et.Kind() == reflect.Struct {
			if si := e.structInfo(et); si.static {
				if hdr := staticHeader(si, e.typed); hdr != nil {
					return e.encodeStaticSeq(rv, n, si, hdr)
				}
			}
		}
	}
	if n > 0 && e.skipCapture && e.inTuple && isScalarKind(rv.Type().Elem().Kind()) {
		return e.encodeScalarSeq(rv, n)
	}
	isTop := !e.inTuple
	if isTop {
		// Both top-level forms start with `[`: `[{schema}]:rows` and `[v,...]`.
		e.inTopSeq = true
		e.inTuple = true
		e.buf = append(e.buf, '[')
		e.topSeqDataStart = len(e.buf)
		e.topSeqCaptured = false
		e.topSeqFields = nil
		e.topSeqTypes = nil
		e.topSeqStatic = nil
	} else {
		e.topSeqDirect = false
		e.pushSeparator()
		e.buf = append(e.buf, '[')
	}
	dataStart := len(e.buf)
	first := true
	skipWasSet := false
	headerDone := false
	hasNull := false
	var cachedNested []byte

	for i := 0; i < n; i++ {
		if !first {
			e.buf = append(e.buf, ',')
		}
		wasFirst := first
		first = false
		e.first = true
		var err error
		if isTop {
			before := len(e.buf)
			e.topSeqDirect = true
			err = e.encodeValue(rv.Index(i))
			e.topSeqDirect = false
			hasNull = hasNull || len(e.buf) == before
		} else {
			err = e.encodeValue(rv.Index(i))
		}
		if isTop && !headerDone && e.topSeqCaptured {
			e.skipCapture = true
			skipWasSet = true
			// The header is known after the first struct row: insert it in
			// front of that row.
			headerDone = true
			header := e.topSeqHeader()
			e.buf = append(e.buf, header...)
			copy(e.buf[e.topSeqDataStart+len(header):], e.buf[e.topSeqDataStart:len(e.buf)-len(header)])
			copy(e.buf[e.topSeqDataStart:], header)
		}
		if wasFirst && !isTop && e.nestedSchema != nil {
			cachedNested = append([]byte(nil), e.nestedSchema...)
			e.skipCapture = true
			skipWasSet = true
		}
		if err != nil {
			return err
		}
	}

	if skipWasSet {
		e.skipCapture = false
	}
	if cachedNested != nil {
		e.nestedSchema = cachedNested
	}
	// `[]` is the empty array; a single null element must be explicit.
	loneNull := !first && len(e.buf) == dataStart
	if isTop {
		if e.topSeqCaptured && hasNull {
			return &MarshalError{Message: "cannot encode a null row in a [{schema}]: sequence"}
		}
		if loneNull {
			e.buf = append(e.buf, "null"...)
		}
		if !headerDone {
			e.buf = append(e.buf, ']')
		}
		e.inTopSeq = false
	} else {
		if loneNull {
			e.buf = append(e.buf, "null"...)
		}
		e.buf = append(e.buf, ']')
		switch {
		case e.skipCapture:
			e.nestedSchema = nil
			if e.typed {
				e.hint = ""
			}
		case e.nestedSchema != nil:
			w := make([]byte, 0, len(e.nestedSchema)+2)
			w = append(w, '[')
			w = append(w, e.nestedSchema...)
			e.nestedSchema = append(w, ']')
		case e.hint != "":
			e.nestedSchema = []byte("[" + e.hint + "]")
			e.hint = ""
		default:
			e.nestedSchema = []byte("[]")
		}
	}
	e.first = false
	return nil
}

// encodeStaticSeq encodes a non-empty sequence of static structs: the same
// bytes as the generic path, without per-row schema bookkeeping.
func (e *encoder) encodeStaticSeq(rv reflect.Value, n int, si *structInfo, hdr []byte) error {
	isTop := !e.inTuple
	if isTop {
		e.inTuple = true
		e.buf = append(e.buf, '[')
		e.buf = append(e.buf, hdr...)
		e.buf = append(e.buf, ']', ':')
	} else {
		e.topSeqDirect = false
		e.pushSeparator()
		e.buf = append(e.buf, '[')
	}
	buf := e.buf
	var err error
	for i := 0; i < n; i++ {
		if i > 0 {
			buf = append(buf, ',')
		}
		buf = append(buf, '(')
		if buf, err = appendStaticFields(buf, rv.Index(i), si); err != nil {
			e.buf = buf
			return err
		}
		buf = append(buf, ')')
	}
	e.buf = buf
	if !isTop {
		e.buf = append(e.buf, ']')
		if e.skipCapture {
			e.nestedSchema = nil
			if e.typed {
				e.hint = ""
			}
		} else {
			w := make([]byte, 0, len(hdr)+2)
			w = append(w, '[')
			w = append(w, hdr...)
			e.nestedSchema = append(w, ']')
		}
	}
	e.first = false
	return nil
}

func isScalarKind(k reflect.Kind) bool {
	switch k {
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64, reflect.String:
		return true
	}
	return false
}

// encodeScalarSeq encodes a non-empty nested sequence of scalars whose
// schema is not being captured (rows after the first).
func (e *encoder) encodeScalarSeq(rv reflect.Value, n int) error {
	e.topSeqDirect = false
	e.pushSeparator()
	buf := append(e.buf, '[')
	var err error
	for i := 0; i < n; i++ {
		if i > 0 {
			buf = append(buf, ',')
		}
		if buf, _, err = appendScalar(buf, rv.Index(i)); err != nil {
			e.buf = buf
			return err
		}
	}
	e.buf = append(buf, ']')
	e.nestedSchema = nil
	if e.typed {
		e.hint = ""
	}
	e.first = false
	return nil
}

// topSeqHeader writes `{name@binding,...}]:` from the first row's capture.
func (e *encoder) topSeqHeader() []byte {
	if e.topSeqStatic != nil {
		out := make([]byte, 0, len(e.topSeqStatic)+3)
		out = append(out, e.topSeqStatic...)
		return append(out, "]:"...)
	}
	out := make([]byte, 0, 64)
	out = append(out, '{')
	for i, f := range e.topSeqFields {
		if i > 0 {
			out = append(out, ',')
		}
		out = appendSchemaFieldName(out, f)
		if i < len(e.topSeqSchemas) && e.topSeqSchemas[i] != nil {
			out = append(out, '@')
			out = append(out, e.topSeqSchemas[i]...)
		} else if e.typed && i < len(e.topSeqTypes) && e.topSeqTypes[i] != "" {
			out = append(out, '@')
			out = append(out, e.topSeqTypes[i]...)
		}
	}
	return append(out, "}]:"...)
}

// ---------------------------------------------------------------------------
// Structs (asun-rs `begin_struct` / `StructEncoder`)
// ---------------------------------------------------------------------------

type capture struct {
	fields  []string
	types   []string
	schemas [][]byte
}

func (c *capture) writeSchema(out []byte, typed bool) []byte {
	out = append(out, '{')
	for i, f := range c.fields {
		if i > 0 {
			out = append(out, ',')
		}
		out = appendSchemaFieldName(out, f)
		if c.schemas[i] != nil {
			out = append(out, '@')
			out = append(out, c.schemas[i]...)
		} else if typed && c.types[i] != "" {
			out = append(out, '@')
			out = append(out, c.types[i]...)
		}
	}
	return append(out, '}')
}

func (e *encoder) encodeStruct(rv reflect.Value) error {
	si := e.structInfo(rv.Type())
	isTop := !e.inTuple
	captureForSeq := !isTop && e.inTopSeq && e.topSeqDirect && !e.topSeqCaptured
	e.topSeqDirect = false
	if si.static && !e.noStatic && (isTop || !e.skipCapture) {
		if hdr := staticHeader(si, e.typed); hdr != nil {
			return e.encodeStaticStruct(rv, si, hdr, isTop, captureForSeq)
		}
	}
	var cap *capture
	if isTop || !e.skipCapture {
		n := len(si.fields)
		cap = &capture{fields: make([]string, 0, n), schemas: make([][]byte, 0, n)}
		if e.typed {
			cap.types = make([]string, 0, n)
		}
	}
	if isTop {
		e.buf = append(e.buf, '(')
		e.inTuple = true
	} else {
		e.pushSeparator()
		e.buf = append(e.buf, '(')
	}

	for i := range si.fields {
		if cap != nil {
			cap.fields = append(cap.fields, si.fields[i].name)
			e.hint = ""
			e.nestedSchema = nil
		}
		if i > 0 {
			e.buf = append(e.buf, ',')
		}
		fv := si.field(rv, i)
		if cap == nil {
			// No capture: a scalar needs none of the encoder state.
			buf, ok, err := appendScalar(e.buf, fv)
			e.buf = buf
			if err != nil {
				return err
			}
			if ok {
				continue
			}
		}
		e.first = true
		e.inTuple = true
		if err := e.encodeValue(fv); err != nil {
			return err
		}
		if cap != nil {
			cap.schemas = append(cap.schemas, e.nestedSchema)
			e.nestedSchema = nil
			if e.typed {
				cap.types = append(cap.types, e.hint)
				e.hint = ""
			}
		} else {
			e.nestedSchema = nil
			e.hint = ""
		}
	}

	e.buf = append(e.buf, ')')
	if cap == nil {
		e.first = false
		e.hint = ""
		return nil
	}
	if isTop {
		header := cap.writeSchema(make([]byte, 0, len(cap.fields)*16+4), e.typed)
		e.topHeader = header
		header = append(header, ':')
		h := len(header)
		e.buf = append(e.buf, header...)
		copy(e.buf[h:], e.buf[:len(e.buf)-h])
		copy(e.buf, header)
		return nil
	}
	e.first = false
	if captureForSeq {
		e.topSeqCaptured = true
		e.topSeqFields = cap.fields
		e.topSeqSchemas = cap.schemas
		if e.typed {
			e.topSeqTypes = cap.types
		}
	} else {
		e.nestedSchema = cap.writeSchema(make([]byte, 0, 64), e.typed)
	}
	e.hint = ""
	return nil
}

// encodeStaticStruct encodes a struct whose schema is the cached hdr. It
// produces exactly what the capture path does, without capturing.
func (e *encoder) encodeStaticStruct(rv reflect.Value, si *structInfo, hdr []byte, isTop, captureForSeq bool) error {
	if isTop {
		e.buf = append(e.buf, hdr...)
		e.buf = append(e.buf, ':', '(')
		e.inTuple = true
	} else {
		e.pushSeparator()
		e.buf = append(e.buf, '(')
	}
	var err error
	if e.buf, err = appendStaticFields(e.buf, rv, si); err != nil {
		return err
	}
	e.buf = append(e.buf, ')')
	e.hint = ""
	if isTop {
		e.topHeader = hdr
		return nil
	}
	e.first = false
	if captureForSeq {
		e.topSeqCaptured = true
		e.topSeqStatic = hdr
	} else if !e.skipCapture {
		e.nestedSchema = hdr
	}
	return nil
}

// appendStaticFields writes the slots of a static struct: scalars and nested
// static structs need none of the encoder's schema state.
func appendStaticFields(buf []byte, rv reflect.Value, si *structInfo) ([]byte, error) {
	for i := range si.fields {
		if i > 0 {
			buf = append(buf, ',')
		}
		fv := si.field(rv, i)
		var ok bool
		var err error
		if buf, ok, err = appendScalar(buf, fv); err != nil {
			return buf, err
		}
		if !ok { // static struct
			buf = append(buf, '(')
			if buf, err = appendStaticFields(buf, fv, getStructInfo(fv.Type())); err != nil {
				return buf, err
			}
			buf = append(buf, ')')
		}
	}
	return buf, nil
}

// appendScalar writes a bool, integer, float or string value; ok is false
// for any other kind, which is left to the caller.
func appendScalar(buf []byte, fv reflect.Value) (_ []byte, ok bool, _ error) {
	switch fv.Kind() {
	case reflect.Bool:
		if fv.Bool() {
			return append(buf, "true"...), true, nil
		}
		return append(buf, "false"...), true, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.AppendInt(buf, fv.Int(), 10), true, nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return strconv.AppendUint(buf, fv.Uint(), 10), true, nil
	case reflect.Float32, reflect.Float64:
		v := fv.Float()
		if math.IsInf(v, 0) || math.IsNaN(v) {
			return buf, true, errNonFinite
		}
		return appendFloat64(buf, v), true, nil
	case reflect.String:
		return appendStr(buf, fv.String()), true, nil
	}
	return buf, false, nil
}

// ---------------------------------------------------------------------------
// Scalars
// ---------------------------------------------------------------------------

var errNonFinite = &MarshalError{Message: "cannot serialize non-finite float (NaN/Infinity)"}

// exactIntLimit is 2^53: above it consecutive integers are not all
// representable, so a plain decimal is not necessarily the shortest form.
const exactIntLimit = 9007199254740992.0

// appendFloat64 formats v like asun-rs: integers as `N.0`, one- and
// two-decimal values directly, everything else in the shortest round-trip
// form laid out as the Rust `ryu` crate does.
func appendFloat64(buf []byte, v float64) []byte {
	if math.Abs(v) < exactIntLimit {
		if v == math.Trunc(v) {
			if v == 0 && math.Signbit(v) {
				return append(buf, "-0.0"...)
			}
			buf = strconv.AppendInt(buf, int64(v), 10)
			return append(buf, '.', '0')
		}
		s10 := v * 10
		if s10 == math.Trunc(s10) && math.Abs(s10) < exactIntLimit {
			k := int64(s10)
			if float64(k)/10 == v {
				return appendScaled(buf, k, 10)
			}
		}
		s100 := v * 100
		if s100 == math.Trunc(s100) && math.Abs(s100) < exactIntLimit {
			k := int64(s100)
			if float64(k)/100 == v {
				return appendScaled(buf, k, 100)
			}
		}
	}
	return appendRyu(buf, v)
}

// appendScaled writes k/scale (scale 10 or 100), dropping a trailing zero of
// the second decimal.
func appendScaled(buf []byte, k int64, scale uint64) []byte {
	mag := uint64(k)
	if k < 0 {
		buf = append(buf, '-')
		mag = uint64(-k)
	}
	buf = strconv.AppendUint(buf, mag/scale, 10)
	buf = append(buf, '.')
	f := mag % scale
	if scale == 10 {
		return append(buf, byte('0'+f))
	}
	buf = append(buf, byte('0'+f/10))
	if f%10 != 0 {
		buf = append(buf, byte('0'+f%10))
	}
	return buf
}

// appendRyu writes the shortest round-trip digits of v in the layout of the
// Rust `ryu` crate: plain decimals for 1e-5 <= |v| < 1e16, otherwise
// `d.ddde±x` without a `+`.
func appendRyu(buf []byte, v float64) []byte {
	var tmp [32]byte
	s := strconv.AppendFloat(tmp[:0], v, 'e', -1, 64)
	if s[0] == '-' {
		buf = append(buf, '-')
		s = s[1:]
	}
	// s is "d[.ddd]e±xx".
	ePos := 0
	for s[ePos] != 'e' {
		ePos++
	}
	var digits [24]byte
	nd := 0
	for _, c := range s[:ePos] {
		if c != '.' {
			digits[nd] = c
			nd++
		}
	}
	exp, _ := strconv.Atoi(string(s[ePos+1:]))
	length := nd
	kk := exp + 1 // 10^(kk-1) <= v < 10^kk
	k := kk - length
	switch {
	case 0 <= k && kk <= 16:
		buf = append(buf, digits[:nd]...)
		for i := length; i < kk; i++ {
			buf = append(buf, '0')
		}
		return append(buf, '.', '0')
	case 0 < kk && kk <= 16:
		buf = append(buf, digits[:kk]...)
		buf = append(buf, '.')
		return append(buf, digits[kk:nd]...)
	case -5 < kk && kk <= 0:
		buf = append(buf, '0', '.')
		for i := 0; i < -kk; i++ {
			buf = append(buf, '0')
		}
		return append(buf, digits[:nd]...)
	case length == 1:
		buf = append(buf, digits[0], 'e')
		return strconv.AppendInt(buf, int64(kk-1), 10)
	default:
		buf = append(buf, digits[0], '.')
		buf = append(buf, digits[1:nd]...)
		buf = append(buf, 'e')
		return strconv.AppendInt(buf, int64(kk-1), 10)
	}
}

// needsQuote marks bytes that may force a string value to be quoted.
var needsQuote = func() [256]bool {
	var t [256]bool
	for i := 0; i < 0x20; i++ {
		t[i] = true
	}
	for _, c := range []byte(`,()[]{}/"\`) {
		t[c] = true
	}
	t[0x7f] = true
	return t
}()

// stringNeedsQuoting reports whether s must be quoted (asun-rs `quote_scan`).
//
// A plain string may contain anything except `, ( ) [ ] { } " \`, control
// characters and the sequence `/*`; it must not start or end with
// whitespace, and it must not read back as another type (`true`, `false`,
// `null`, a number). Interior spaces, `@`, `:`, `/` and `*` stay bare.
func stringNeedsQuoting(s string) bool {
	n := len(s)
	if n == 0 {
		return true
	}
	for i := 0; i < n; i++ {
		c := s[i]
		if !needsQuote[c] {
			continue
		}
		if c == '/' {
			if i+1 < n && s[i+1] == '*' {
				return true
			}
			continue
		}
		return true
	}
	if s[n-1] == ' ' {
		return true
	}
	switch first := s[0]; {
	case first == '-' || first == '+' || first == '.' || (first >= '0' && first <= '9'):
	case first == ' ':
		return true
	case first == 't' || first == 'f' || first == 'n':
		return s == "true" || s == "false" || s == "null"
	case first == 0xEF:
		return len(s) >= 3 && s[1] == 0xBB && s[2] == 0xBF
	default:
		return false
	}
	// Anything the decoder might re-read as a number.
	i := 0
	if s[0] == '-' || s[0] == '+' {
		i = 1
	}
	sawDigit, sawDot, sawExp := false, false, false
	for ; i < n; i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
			sawDigit = true
		case c == '.' && !sawDot && !sawExp:
			sawDot = true
		case (c == 'e' || c == 'E') && sawDigit && !sawExp:
			sawExp = true
			if i+1 < n && (s[i+1] == '+' || s[i+1] == '-') {
				i++
			}
			sawDigit = false
		default:
			return false
		}
	}
	return sawDigit
}

func appendEscaped(buf []byte, s string) []byte {
	const hex = "0123456789abcdef"
	buf = append(buf, '"')
	start := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 0x20 && c != '"' && c != '\\' && c != 0x7f {
			continue
		}
		buf = append(buf, s[start:i]...)
		switch c {
		case '"':
			buf = append(buf, '\\', '"')
		case '\\':
			buf = append(buf, '\\', '\\')
		case '\n':
			buf = append(buf, '\\', 'n')
		case '\r':
			buf = append(buf, '\\', 'r')
		case '\t':
			buf = append(buf, '\\', 't')
		case 0x08:
			buf = append(buf, '\\', 'b')
		case 0x0c:
			buf = append(buf, '\\', 'f')
		default:
			buf = append(buf, '\\', 'u', '0', '0', hex[c>>4], hex[c&0xf])
		}
		start = i + 1
	}
	buf = append(buf, s[start:]...)
	return append(buf, '"')
}

func appendStr(buf []byte, s string) []byte {
	if stringNeedsQuoting(s) {
		return appendEscaped(buf, s)
	}
	return append(buf, s...)
}

// schemaFieldNameNeedsQuoting: a bare name is `1*(ALPHA / DIGIT / "_")`;
// all-digit names and the keywords are quoted too.
func schemaFieldNameNeedsQuoting(s string) bool {
	if s == "" {
		return true
	}
	allDigits := true
	for i := 0; i < len(s); i++ {
		c := s[i]
		isDigit := c >= '0' && c <= '9'
		if !(isDigit || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_') {
			return true
		}
		if !isDigit {
			allDigits = false
		}
	}
	return allDigits || s == "true" || s == "false" || s == "null"
}

func appendSchemaFieldName(buf []byte, s string) []byte {
	if schemaFieldNameNeedsQuoting(s) {
		return appendEscaped(buf, s)
	}
	return append(buf, s...)
}
