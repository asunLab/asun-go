package asun

import (
	"bytes"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

// ---------------------------------------------------------------------------
// Decode — ASUN text to Go values
//
// The decoder follows conformance/GRAMMAR.abnf (ASUN 1.5) and its semantic
// rules S1–S10, with the same behaviour as the reference implementation
// asun-rs: slot counts are exact, `@type` hints are authoritative, a null
// slot (empty or `null`) only decodes into a pointer or interface, and
// numbers must fit their target.
// ---------------------------------------------------------------------------

// maxDepth bounds structural nesting (schemas, tuples, arrays). Untrusted
// input could otherwise drive unbounded recursion into a fatal stack
// overflow that aborts the whole process.
const maxDepth = 128

const (
	msgEOF             = "unexpected end of input"
	msgExpectedColon   = "expected ':'"
	msgExpectedParen   = "expected '('"
	msgExpectedBrace   = "expected '{'"
	msgExpectedBracket = "expected '['"
	msgExpectedClose   = "expected ']'"
	msgTrailing        = "trailing characters"
	msgInvalidNumber   = "invalid number"
	msgIntRange        = "integer out of range for target type"
	msgInvalidBool     = "invalid bool"
	msgUnclosedString  = "unclosed string"
	msgUnclosedComment = "unclosed comment"
	msgUnclosedParen   = "unclosed parenthesis"
	msgUnclosedBracket = "unclosed bracket"
	msgUnicodeEscape   = "invalid unicode escape"
	msgDepth           = "input nesting exceeds the depth limit"
	msgEmpty           = "empty document"
	msgBareTuple       = "a top-level tuple needs a schema"
	msgNull            = "null value for a non-optional target"
	msgHint            = "value does not match its schema type hint"
	msgDuplicate       = "duplicate field name in schema"
	msgFieldName       = "invalid bare field name"
	msgControlChar     = "raw control character in quoted string"
	msgFloatRange      = "float literal out of range"
	msgInvalidUTF8     = "invalid utf-8"
	msgNeedsBinding    = "a tuple needs a @{...} binding to decode without a target struct"
)

// Decode parses one ASUN document and stores the result in the value pointed
// to by v.
//
// The document must be exactly one top-level form: `{schema}:(...)` for a
// struct, `[{schema}]:(...),(...)` for a slice of structs, a plain array
// `[...]`, or a scalar. A leading BOM and surrounding whitespace / comments
// are allowed; an empty document, a bare top-level tuple and trailing content
// are errors.
//
// Source fields are matched to target fields by name: extra source fields are
// skipped and target fields absent from the source are left at their zero
// value. An `interface{}` target receives bool, int64, float64, string, nil,
// []any and (for tuples with a schema) map[string]any values.
//
// Decoded strings may share memory with data; do not modify data afterwards.
func Decode(data []byte, v any) error {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return &UnmarshalError{Message: "decode target must be a non-nil pointer"}
	}
	if !utf8.Valid(data) {
		return &UnmarshalError{Message: msgInvalidUTF8}
	}
	d := decoder{data: data}
	if len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF {
		d.pos = 3
	}
	d.skipLayout()
	if d.pos >= len(d.data) {
		return d.unexpectedOr(msgEmpty)
	}
	if d.data[d.pos] == '(' {
		return d.fail(msgBareTuple)
	}
	if err := d.decodeValue(rv.Elem()); err != nil {
		return err
	}
	d.skipLayout()
	if d.pos < len(d.data) {
		return d.unexpectedOr(msgTrailing)
	}
	return nil
}

type decoder struct {
	data  []byte
	pos   int
	depth int
	// Declared type (`@...` binding) of the slot about to be decoded, handed
	// from the enclosing schema to the value's decoder. Consumers take it, so
	// it never leaks into a sibling slot.
	pending *ty
	// Schema of `[{schema}]:` while its rows are decoded; consumed by the row.
	rows *schema
}

// ---------------------------------------------------------------------------
// Schema model
// ---------------------------------------------------------------------------

type tyKind uint8

const (
	tyAny tyKind = iota
	tyInt
	tyFloat
	tyStr
	tyBool
	tyObj // @{...}
	tyArr // @[...]; elem.kind == tyAny for the untyped @[]
)

// ty is the declared type of one schema slot.
type ty struct {
	kind tyKind
	obj  *schema
	elem *ty
}

// hint is the type to hand to a value decoder: nil for an unbound slot.
func (t *ty) hint() *ty {
	if t == nil || t.kind == tyAny {
		return nil
	}
	return t
}

// schema is a parsed `{...}` header: field names and their declared types.
type schema struct {
	names []string
	types []ty
	plans sync.Map // *structInfo → *plan
}

// plan lines a source schema up with one target struct.
type plan struct {
	// Names match 1:1 in order: fields are read positionally.
	exact bool
	// src[i] is the target field for source field i, or -1 to skip it.
	src []int
}

func (s *schema) planFor(si *structInfo) *plan {
	if p, ok := s.plans.Load(si); ok {
		return p.(*plan)
	}
	p := &plan{src: make([]int, len(s.names))}
	p.exact = len(s.names) == len(si.fields)
	for i, name := range s.names {
		j, ok := si.nameIndex[name]
		if !ok {
			j = -1
		}
		p.src[i] = j
		if j != i {
			p.exact = false
		}
	}
	actual, _ := s.plans.LoadOrStore(si, p)
	return actual.(*plan)
}

// ---------------------------------------------------------------------------
// Errors
// ---------------------------------------------------------------------------

func (d *decoder) fail(msg string) error {
	return &UnmarshalError{Pos: d.pos, Message: msg}
}

// unexpected reports the byte at the cursor: end of input, an unclosed
// comment, or the offending character.
func (d *decoder) unexpected() error {
	return d.unexpectedOr(msgEOF)
}

func (d *decoder) unexpectedOr(atEnd string) error {
	if d.pos >= len(d.data) {
		return d.fail(atEnd)
	}
	if d.atComment(d.pos) {
		return d.fail(msgUnclosedComment)
	}
	r, _ := utf8.DecodeRune(d.data[d.pos:])
	return d.fail("unexpected character " + strconv.QuoteRune(r))
}

// scalarError diagnoses a scalar that failed to parse: a null slot gets its
// own error.
func (d *decoder) scalarError(fallback error) error {
	if d.atValueEnd() || d.atNullKeyword() {
		return d.fail(msgNull)
	}
	return fallback
}

func (d *decoder) enter() error {
	d.depth++
	if d.depth > maxDepth {
		return d.fail(msgDepth)
	}
	return nil
}

func (d *decoder) leave() {
	d.depth--
}

func fieldCountMismatch(pos, expected, got int) error {
	return &UnmarshalError{Pos: pos, Message: "field count mismatch: expected " +
		strconv.Itoa(expected) + ", got " + strconv.Itoa(got)}
}

// ---------------------------------------------------------------------------
// Lexical helpers
// ---------------------------------------------------------------------------

func (d *decoder) atComment(pos int) bool {
	return pos+1 < len(d.data) && d.data[pos] == '/' && d.data[pos+1] == '*'
}

// isTokenEndAt reports whether a scalar token (number, keyword) may end at
// pos: end of input, a slot delimiter, layout, or a comment.
func (d *decoder) isTokenEndAt(pos int) bool {
	if pos >= len(d.data) {
		return true
	}
	switch d.data[pos] {
	case ',', ')', ']', ' ', '\t', '\n', '\r':
		return true
	case '/':
		return d.atComment(pos)
	}
	return false
}

// atValueEnd reports an empty slot: the next byte closes or separates it.
func (d *decoder) atValueEnd() bool {
	if d.pos >= len(d.data) {
		return true
	}
	b := d.data[d.pos]
	return b == ',' || b == ')' || b == ']'
}

// atNullKeyword reports the keyword null at the cursor. Only layout may
// follow it in the slot: `null x` is the plain string "null x" (S2).
func (d *decoder) atNullKeyword() bool {
	return d.pos+4 <= len(d.data) && string(d.data[d.pos:d.pos+4]) == "null" &&
		d.isTokenEndAt(d.pos+4) && d.slotEndsAfterLayout(d.pos+4)
}

// slotEndsAfterLayout reports whether only layout separates pos from the end
// of the slot.
func (d *decoder) slotEndsAfterLayout(pos int) bool {
	data := d.data
	for pos < len(data) {
		switch data[pos] {
		case ',', ')', ']':
			return true
		case ' ', '\t', '\n', '\r':
			pos++
		case '/':
			if !d.atComment(pos) {
				return false
			}
			i := bytes.Index(data[pos+2:], []byte("*/"))
			if i < 0 {
				// Unclosed: the layout skipper reports it.
				return true
			}
			pos += 2 + i + 2
		default:
			return false
		}
	}
	return true
}

func (d *decoder) peekIs(b byte) bool {
	return d.pos < len(d.data) && d.data[d.pos] == b
}

func (d *decoder) skipWhitespace() {
	for d.pos < len(d.data) {
		switch d.data[d.pos] {
		case ' ', '\t', '\n', '\r':
			d.pos++
		default:
			return
		}
	}
}

// skipLayout skips whitespace and complete comments (GRAMMAR.abnf `ows`). An
// unterminated comment is left in place so the next token check reports it.
func (d *decoder) skipLayout() {
	for {
		d.skipWhitespace()
		if !d.atComment(d.pos) {
			return
		}
		p := d.pos + 2
		for {
			if p+1 >= len(d.data) {
				return
			}
			if d.data[p] == '*' && d.data[p+1] == '/' {
				break
			}
			p++
		}
		d.pos = p + 2
	}
}

// expectSlotComma consumes the `,` before slot index of a group closed by
// closer, reporting a too-short group precisely.
func (d *decoder) expectSlotComma(closer byte, expected, index int) error {
	d.skipLayout()
	if d.pos < len(d.data) {
		switch d.data[d.pos] {
		case ',':
			d.pos++
			return nil
		case closer:
			return fieldCountMismatch(d.pos, expected, index)
		}
	}
	return d.unexpected()
}

// expectGroupClose consumes closer at the end of a group of expected slots.
func (d *decoder) expectGroupClose(closer byte, expected int) error {
	d.skipLayout()
	if d.pos >= len(d.data) {
		if closer == ')' {
			return d.fail(msgUnclosedParen)
		}
		return d.fail(msgUnclosedBracket)
	}
	switch d.data[d.pos] {
	case closer:
		d.pos++
		return nil
	case ',':
		return fieldCountMismatch(d.pos, expected, expected+1)
	}
	return d.unexpected()
}

func (d *decoder) parseBoolLiteral() (value, ok bool) {
	if d.pos+4 <= len(d.data) && string(d.data[d.pos:d.pos+4]) == "true" && d.isTokenEndAt(d.pos+4) {
		d.pos += 4
		return true, true
	}
	if d.pos+5 <= len(d.data) && string(d.data[d.pos:d.pos+5]) == "false" && d.isTokenEndAt(d.pos+5) {
		d.pos += 5
		return false, true
	}
	return false, false
}

// ---------------------------------------------------------------------------
// Schema parsing
// ---------------------------------------------------------------------------

// findSchemaEnd finds the `}` closing the schema opened at open, skipping
// quoted field names and comments.
func (d *decoder) findSchemaEnd(open int) (int, error) {
	data := d.data
	depth := 1
	pos := open + 1
	for pos < len(data) {
		switch data[pos] {
		case '"':
			pos++
			for {
				if pos >= len(data) {
					return 0, d.fail(msgUnclosedString)
				}
				if data[pos] == '\\' {
					pos += 2
					continue
				}
				if data[pos] == '"' {
					pos++
					break
				}
				pos++
			}
			continue
		case '/':
			if pos+1 < len(data) && data[pos+1] == '*' {
				pos += 2
				for {
					if pos+1 >= len(data) {
						return 0, d.fail(msgUnclosedComment)
					}
					if data[pos] == '*' && data[pos+1] == '/' {
						pos += 2
						break
					}
					pos++
				}
				continue
			}
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return pos, nil
			}
		}
		pos++
	}
	return 0, d.fail(msgEOF)
}

// parseSchema parses the `{...}` schema at the cursor through the cache.
func (d *decoder) parseSchema() (*schema, error) {
	if !d.peekIs('{') {
		return nil, d.fail(msgExpectedBrace)
	}
	open := d.pos
	end, err := d.findSchemaEnd(open)
	if err != nil {
		return nil, err
	}
	key := d.data[open : end+1]
	if s, ok := schemaCache.Load(unsafeString(key)); ok {
		d.pos = end + 1
		return s.(*schema), nil
	}
	s, err := d.parseSchemaBody()
	if err != nil {
		return nil, err
	}
	schemaCache.LoadOrStore(string(key), s)
	return s, nil
}

func (d *decoder) parseSchemaBody() (*schema, error) {
	if err := d.enter(); err != nil {
		return nil, err
	}
	s, err := d.parseSchemaBodyInner()
	d.leave()
	return s, err
}

func isBareNameByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '_'
}

func (d *decoder) parseSchemaBodyInner() (*schema, error) {
	if !d.peekIs('{') {
		return nil, d.fail(msgExpectedBrace)
	}
	d.pos++
	s := &schema{}
	d.skipLayout()
	if d.peekIs('}') {
		d.pos++
		return s, nil
	}
	for {
		d.skipLayout()
		if d.pos >= len(d.data) {
			return nil, d.fail(msgEOF)
		}
		var name string
		if d.data[d.pos] == '"' {
			n, err := d.parseQuotedString()
			if err != nil {
				return nil, err
			}
			// Schemas outlive the input in the cache: own the name.
			name = strings.Clone(n)
		} else {
			start := d.pos
			for d.pos < len(d.data) && isBareNameByte(d.data[d.pos]) {
				d.pos++
			}
			okEnd := true
			if d.pos < len(d.data) {
				switch d.data[d.pos] {
				case ',', '}', '@', ' ', '\t', '\n', '\r':
				case '/':
					okEnd = d.atComment(d.pos)
				default:
					okEnd = false
				}
			}
			if start == d.pos {
				// `{a,}` / `{,a}`: no empty field slots in a schema.
				return nil, d.unexpected()
			}
			if !okEnd {
				return nil, d.fail(msgFieldName)
			}
			name = string(d.data[start:d.pos])
		}
		d.skipLayout()
		t := ty{kind: tyAny}
		if d.peekIs('@') {
			d.pos++
			d.skipLayout()
			b, err := d.parseBinding()
			if err != nil {
				return nil, err
			}
			t = b
		}
		s.names = append(s.names, name)
		s.types = append(s.types, t)
		d.skipLayout()
		if d.peekIs(',') {
			d.pos++
			continue
		}
		if d.peekIs('}') {
			d.pos++
			break
		}
		return nil, d.unexpected()
	}
	// S6: duplicate names would make by-name mapping ambiguous.
	if len(s.names) <= 16 {
		for i := 1; i < len(s.names); i++ {
			for j := 0; j < i; j++ {
				if s.names[i] == s.names[j] {
					return nil, d.fail(msgDuplicate)
				}
			}
		}
	} else {
		seen := make(map[string]struct{}, len(s.names))
		for _, n := range s.names {
			if _, dup := seen[n]; dup {
				return nil, d.fail(msgDuplicate)
			}
			seen[n] = struct{}{}
		}
	}
	return s, nil
}

// parseBinding parses the binding after `@` (GRAMMAR.abnf `binding`).
func (d *decoder) parseBinding() (ty, error) {
	if err := d.enter(); err != nil {
		return ty{}, err
	}
	t, err := d.parseBindingInner()
	d.leave()
	return t, err
}

func (d *decoder) parseBindingInner() (ty, error) {
	if d.peekIs('{') {
		s, err := d.parseSchemaBody()
		if err != nil {
			return ty{}, err
		}
		return ty{kind: tyObj, obj: s}, nil
	}
	if d.peekIs('[') {
		d.pos++
		d.skipLayout()
		if d.peekIs(']') {
			d.pos++
			return ty{kind: tyArr, elem: &ty{kind: tyAny}}, nil
		}
		inner, err := d.parseBinding()
		if err != nil {
			return ty{}, err
		}
		d.skipLayout()
		if !d.peekIs(']') {
			return ty{}, d.fail("expected ']' in array type annotation")
		}
		d.pos++
		return ty{kind: tyArr, elem: &inner}, nil
	}
	start := d.pos
	for d.pos < len(d.data) && (isBareNameByte(d.data[d.pos]) && d.data[d.pos] != '_') {
		d.pos++
	}
	switch string(d.data[start:d.pos]) {
	case "int":
		return ty{kind: tyInt}, nil
	case "float":
		return ty{kind: tyFloat}, nil
	case "str":
		return ty{kind: tyStr}, nil
	case "bool":
		return ty{kind: tyBool}, nil
	case "":
		return ty{}, d.fail("expected schema type after '@'")
	}
	return ty{}, d.fail("unsupported schema type '" + string(d.data[start:d.pos]) + "'; use int, str, float, or bool")
}

// ---------------------------------------------------------------------------
// Skipping unknown source fields
// ---------------------------------------------------------------------------

// skipValue skips the value in one slot, leaving the cursor on the `,` / `)`
// / `]` that ends it. Every iteration consumes input or returns.
func (d *decoder) skipValue() error {
	d.skipLayout()
	var stack [maxDepth]byte
	depth := 0
	for {
		if d.pos >= len(d.data) {
			if depth == 0 {
				return nil
			}
			return d.fail(msgEOF)
		}
		b := d.data[d.pos]
		switch b {
		case '"':
			if err := d.skipQuotedString(); err != nil {
				return err
			}
		case '\\':
			if d.pos+1 >= len(d.data) {
				return d.fail(`invalid escape: \\`)
			}
			d.pos += 2
		case '/':
			if d.atComment(d.pos) {
				before := d.pos
				d.skipLayout()
				if d.pos == before {
					return d.fail(msgUnclosedComment)
				}
			} else {
				d.pos++
			}
		case '(', '[':
			if depth >= maxDepth {
				return d.fail(msgDepth)
			}
			stack[depth] = b
			depth++
			d.pos++
		case ')', ']':
			if depth == 0 {
				return nil
			}
			open := byte('(')
			if b == ']' {
				open = '['
			}
			if stack[depth-1] != open {
				return d.fail("unexpected character " + strconv.QuoteRune(rune(b)))
			}
			depth--
			d.pos++
		case ',':
			if depth == 0 {
				return nil
			}
			d.pos++
		case '{', '}':
			return d.fail("unexpected character " + strconv.QuoteRune(rune(b)))
		default:
			d.pos++
		}
	}
}

func (d *decoder) skipQuotedString() error {
	d.pos++
	for d.pos < len(d.data) {
		b := d.data[d.pos]
		switch {
		case b == '"':
			d.pos++
			return nil
		case b == '\\':
			d.pos += 2
		case b < 0x20:
			return d.fail(msgControlChar)
		default:
			d.pos++
		}
	}
	return d.fail(msgUnclosedString)
}

// ---------------------------------------------------------------------------
// Strings
// ---------------------------------------------------------------------------

// plainStop marks bytes that end a run of plain-string content.
var plainStop = func() [256]bool {
	var t [256]bool
	for i := 0; i < 0x20; i++ {
		t[i] = true
	}
	for _, c := range []byte(`,()[]{}"\/`) {
		t[c] = true
	}
	return t
}()

// parsePlainValue parses a plain (unquoted) string (GRAMMAR.abnf
// `plain-string`), stopping at a delimiter, a control character or a comment
// and trimming trailing whitespace. It returns a view of the input and
// whether the value contains escapes.
func (d *decoder) parsePlainValue() (string, bool, error) {
	start := d.pos
	hasEscape := false
	data := d.data
	for {
		for d.pos < len(data) && !plainStop[data[d.pos]] {
			d.pos++
		}
		if d.pos >= len(data) {
			break
		}
		b := data[d.pos]
		if b == '\\' {
			hasEscape = true
			if d.pos+1 >= len(data) {
				return "", false, d.fail(`invalid escape: \\`)
			}
			d.pos += 2
			continue
		}
		if b == '\t' {
			d.pos++
			continue
		}
		if b == '/' && !d.atComment(d.pos) {
			d.pos++
			continue
		}
		break
	}
	end := d.pos
	for end > start && (data[end-1] == ' ' || data[end-1] == '\t') {
		end--
	}
	raw := data[start:end]
	if hasEscape && !utf8.Valid(raw) {
		return "", false, d.fail(`invalid escape: \\`)
	}
	return unsafeString(raw), hasEscape, nil
}

// parseQuotedString parses a quoted string. Zero-copy when it has no escapes.
func (d *decoder) parseQuotedString() (string, error) {
	d.pos++
	start := d.pos
	data := d.data
	i := d.pos
	for i < len(data) {
		b := data[i]
		if b == '"' {
			d.pos = i + 1
			return unsafeString(data[start:i]), nil
		}
		if b == '\\' || b < 0x20 {
			break
		}
		i++
	}
	if i >= len(data) {
		d.pos = i
		return "", d.fail(msgUnclosedString)
	}
	out := make([]byte, 0, i-start+16)
	out = append(out, data[start:i]...)
	d.pos = i
	for {
		if d.pos >= len(data) {
			return "", d.fail(msgUnclosedString)
		}
		b := data[d.pos]
		switch {
		case b == '"':
			d.pos++
			return unsafeString(out), nil
		case b == '\\':
			d.pos++
			if d.pos >= len(data) {
				return "", d.fail(msgUnclosedString)
			}
			esc := data[d.pos]
			d.pos++
			switch esc {
			case '"', '\\', '/', ',', '(', ')', '[', ']', '{', '}', ':', '@':
				out = append(out, esc)
			case 'n':
				out = append(out, '\n')
			case 't':
				out = append(out, '\t')
			case 'r':
				out = append(out, '\r')
			case 'b':
				out = append(out, 0x08)
			case 'f':
				out = append(out, 0x0c)
			case 'u':
				r, next, err := readUnicodeEscape(data, d.pos)
				if err != nil {
					return "", d.fail(msgUnicodeEscape)
				}
				d.pos = next
				out = utf8.AppendRune(out, r)
			default:
				r, _ := utf8.DecodeRune(data[d.pos-1:])
				return "", d.fail(`invalid escape: \` + string(r))
			}
		case b < 0x20:
			return "", d.fail(msgControlChar)
		default:
			j := d.pos + 1
			for j < len(data) && data[j] != '"' && data[j] != '\\' && data[j] >= 0x20 {
				j++
			}
			out = append(out, data[d.pos:j]...)
			d.pos = j
		}
	}
}

func hex4(b []byte, at int) (rune, bool) {
	if at+4 > len(b) {
		return 0, false
	}
	var cp rune
	for k := 0; k < 4; k++ {
		c := b[at+k]
		var v byte
		switch {
		case c >= '0' && c <= '9':
			v = c - '0'
		case c >= 'a' && c <= 'f':
			v = c - 'a' + 10
		case c >= 'A' && c <= 'F':
			v = c - 'A' + 10
		default:
			return 0, false
		}
		cp = cp<<4 | rune(v)
	}
	return cp, true
}

// readUnicodeEscape reads a `\uXXXX` escape whose first hex digit is at pos,
// joining a UTF-16 surrogate pair. It returns the position past the escape.
func readUnicodeEscape(b []byte, pos int) (rune, int, error) {
	hi, ok := hex4(b, pos)
	if !ok {
		return 0, 0, errUnicodeEscape
	}
	pos += 4
	switch {
	case hi >= 0xD800 && hi < 0xDC00:
		if pos+1 >= len(b) || b[pos] != '\\' || b[pos+1] != 'u' {
			return 0, 0, errUnicodeEscape
		}
		lo, ok := hex4(b, pos+2)
		if !ok || lo < 0xDC00 || lo >= 0xE000 {
			return 0, 0, errUnicodeEscape
		}
		return 0x10000 + (hi-0xD800)<<10 + (lo - 0xDC00), pos + 6, nil
	case hi >= 0xDC00 && hi < 0xE000:
		return 0, 0, errUnicodeEscape
	}
	return hi, pos, nil
}

var errUnicodeEscape = &UnmarshalError{Message: msgUnicodeEscape}

// unescapePlain unescapes a plain (unquoted) value.
func unescapePlain(s string) (string, error) {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' {
			out = append(out, c)
			continue
		}
		i++
		if i >= len(s) {
			return "", &UnmarshalError{Message: msgEOF}
		}
		switch e := s[i]; e {
		case ',', '(', ')', '[', ']', '{', '}', ':', '@', '"', '\\', '/':
			out = append(out, e)
		case 'n':
			out = append(out, '\n')
		case 't':
			out = append(out, '\t')
		case 'r':
			out = append(out, '\r')
		case 'b':
			out = append(out, 0x08)
		case 'f':
			out = append(out, 0x0c)
		case 'u':
			r, next, err := readUnicodeEscape([]byte(s), i+1)
			if err != nil {
				return "", err
			}
			out = utf8.AppendRune(out, r)
			i = next - 1
		default:
			r, _ := utf8.DecodeRuneInString(s[i:])
			return "", &UnmarshalError{Message: `invalid escape: \` + string(r)}
		}
	}
	return unsafeString(out), nil
}

// parseStringSlot parses a string-typed slot (quoted or plain). A null slot
// is an error.
func (d *decoder) parseStringSlot() (string, error) {
	if h := d.pending; h != nil {
		d.pending = nil
		if h.kind != tyStr {
			return "", d.fail(msgHint)
		}
	}
	d.skipLayout()
	if d.peekIs('"') {
		return d.parseQuotedString()
	}
	start := d.pos
	v, hasEscape, err := d.parsePlainValue()
	if err != nil {
		return "", err
	}
	if v == "" {
		return "", d.scalarError(d.unexpected())
	}
	if hasEscape {
		s, err := unescapePlain(v)
		if err != nil {
			e := err.(*UnmarshalError)
			e.Pos = start
			return "", e
		}
		return s, nil
	}
	if v == "null" {
		d.pos = start
		return "", d.fail(msgNull)
	}
	return v, nil
}

// ---------------------------------------------------------------------------
// Numbers
// ---------------------------------------------------------------------------

// scanDigits scans an unsigned decimal digit run that must end at a token
// boundary (`42abc` is not a number).
func (d *decoder) scanDigits() (uint64, error) {
	start := d.pos
	var val uint64
	for d.pos < len(d.data) {
		c := d.data[d.pos] - '0'
		if c > 9 {
			break
		}
		val = val*10 + uint64(c)
		d.pos++
	}
	n := d.pos - start
	if n == 0 || !d.isTokenEndAt(d.pos) {
		return 0, d.fail(msgInvalidNumber)
	}
	if n > 18 {
		// Up to 18 digits cannot overflow; recheck longer runs.
		val = 0
		for _, c := range d.data[start:d.pos] {
			dv := uint64(c - '0')
			if val > (^uint64(0)-dv)/10 {
				return 0, d.fail(msgIntRange)
			}
			val = val*10 + dv
		}
	}
	return val, nil
}

func (d *decoder) parseI64() (int64, error) {
	neg := d.peekIs('-')
	if neg {
		d.pos++
	}
	v, err := d.scanDigits()
	if err != nil {
		return 0, err
	}
	if neg {
		if v > 1<<63 {
			return 0, d.fail(msgIntRange)
		}
		return int64(-v), nil
	}
	if v > 1<<63-1 {
		return 0, d.fail(msgIntRange)
	}
	return int64(v), nil
}

func (d *decoder) parseU64() (uint64, error) {
	neg := d.peekIs('-')
	if neg {
		d.pos++
	}
	v, err := d.scanDigits()
	if err != nil {
		return 0, err
	}
	if neg && v != 0 {
		return 0, d.fail(msgIntRange)
	}
	return v, nil
}

// scanFloat validates `["-"] 1*DIGIT ["." 1*DIGIT] [exponent]` at the cursor
// and returns its text and whether it has a fraction or exponent.
func (d *decoder) scanFloat() ([]byte, bool, error) {
	start := d.pos
	data := d.data
	if d.peekIs('-') {
		d.pos++
	}
	intStart := d.pos
	for d.pos < len(data) && data[d.pos]-'0' <= 9 {
		d.pos++
	}
	if d.pos == intStart {
		return nil, false, d.fail(msgInvalidNumber)
	}
	frac := false
	if d.peekIs('.') {
		frac = true
		d.pos++
		fs := d.pos
		for d.pos < len(data) && data[d.pos]-'0' <= 9 {
			d.pos++
		}
		if d.pos == fs {
			return nil, false, d.fail(msgInvalidNumber)
		}
	}
	if d.peekIs('e') || d.peekIs('E') {
		frac = true
		d.pos++
		if d.peekIs('-') || d.peekIs('+') {
			d.pos++
		}
		es := d.pos
		for d.pos < len(data) && data[d.pos]-'0' <= 9 {
			d.pos++
		}
		if d.pos == es {
			return nil, false, d.fail(msgInvalidNumber)
		}
	}
	if !d.isTokenEndAt(d.pos) {
		return nil, false, d.fail(msgInvalidNumber)
	}
	return data[start:d.pos], frac, nil
}

// parseFloat parses a number into a float of the given bit size. Integers
// are accepted; with intOnly (an @int hint) a fraction or exponent is a hint
// mismatch. Overflow to infinity is an error (S4).
func (d *decoder) parseFloat(bits int, intOnly bool) (float64, error) {
	text, frac, err := d.scanFloat()
	if err != nil {
		return 0, err
	}
	if intOnly && frac {
		return 0, d.fail(msgHint)
	}
	v, err := strconv.ParseFloat(unsafeString(text), bits)
	if err != nil {
		if ne, ok := err.(*strconv.NumError); ok && ne.Err == strconv.ErrRange && v == 0 {
			return v, nil // underflow rounds to zero
		}
		return 0, d.fail(msgFloatRange)
	}
	return v, nil
}

// ---------------------------------------------------------------------------
// Value decoding
// ---------------------------------------------------------------------------

func (d *decoder) takePending() *ty {
	h := d.pending
	d.pending = nil
	return h
}

func (d *decoder) decodeValue(rv reflect.Value) error {
	switch rv.Kind() {
	case reflect.String:
		s, err := d.parseStringSlot()
		if err != nil {
			return err
		}
		rv.SetString(s)
	case reflect.Bool:
		if h := d.takePending(); h != nil && h.kind != tyBool {
			return d.fail(msgHint)
		}
		d.skipLayout()
		v, ok := d.parseBoolLiteral()
		if !ok {
			return d.scalarError(d.fail(msgInvalidBool))
		}
		rv.SetBool(v)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if h := d.takePending(); h != nil && h.kind != tyInt {
			return d.fail(msgHint)
		}
		d.skipLayout()
		start := d.pos
		v, err := d.parseI64()
		if err != nil {
			d.pos = start
			return d.scalarError(err)
		}
		if rv.OverflowInt(v) {
			d.pos = start
			return d.fail(msgIntRange)
		}
		rv.SetInt(v)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		if h := d.takePending(); h != nil && h.kind != tyInt {
			return d.fail(msgHint)
		}
		d.skipLayout()
		start := d.pos
		v, err := d.parseU64()
		if err != nil {
			d.pos = start
			return d.scalarError(err)
		}
		if rv.OverflowUint(v) {
			d.pos = start
			return d.fail(msgIntRange)
		}
		rv.SetUint(v)
	case reflect.Float32, reflect.Float64:
		intOnly := false
		if h := d.takePending(); h != nil {
			switch h.kind {
			case tyFloat:
			case tyInt:
				intOnly = true
			default:
				return d.fail(msgHint)
			}
		}
		d.skipLayout()
		start := d.pos
		bits := 64
		if rv.Kind() == reflect.Float32 {
			bits = 32
		}
		v, err := d.parseFloat(bits, intOnly)
		if err != nil {
			d.pos = start
			return d.scalarError(err)
		}
		rv.SetFloat(v)
	case reflect.Pointer:
		return d.decodeOption(rv)
	case reflect.Struct:
		return d.decodeStruct(rv, getStructInfo(rv.Type()))
	case reflect.Slice:
		return d.decodeSlice(rv)
	case reflect.Array:
		return d.decodeArray(rv)
	case reflect.Interface:
		if rv.NumMethod() != 0 {
			return d.fail("cannot decode into non-empty interface " + rv.Type().String())
		}
		v, err := d.decodeAny()
		if err != nil {
			return err
		}
		if v == nil {
			rv.SetZero()
		} else {
			rv.Set(reflect.ValueOf(v))
		}
	case reflect.Map:
		return d.fail("map fields are not supported")
	default:
		return d.fail("unsupported type " + rv.Type().String())
	}
	return nil
}

// decodeOption decodes a pointer: a null slot (empty or `null`) is nil,
// anything else is decoded into a fresh value.
func (d *decoder) decodeOption(rv reflect.Value) error {
	d.skipLayout()
	if d.atValueEnd() {
		d.pending = nil
		rv.SetZero()
		return nil
	}
	if d.atNullKeyword() {
		d.pending = nil
		d.pos += 4
		rv.SetZero()
		return nil
	}
	p := reflect.New(rv.Type().Elem())
	if err := d.decodeValue(p.Elem()); err != nil {
		return err
	}
	rv.Set(p)
	return nil
}

// headerFollows reports whether `{` follows the `[` at the cursor (a
// `[{schema}]:` header).
func (d *decoder) headerFollows() bool {
	save := d.pos
	d.pos++
	d.skipLayout()
	r := d.peekIs('{')
	d.pos = save
	return r
}

// cautiousCapacity bounds an element-count estimate taken from untrusted
// input to 1 MiB worth of elements.
func cautiousCapacity(n int, elemSize uintptr) int {
	const maxPrealloc = 1 << 20
	if elemSize == 0 {
		elemSize = 1
	}
	if limit := int(maxPrealloc / elemSize); n > limit {
		return limit
	}
	return n
}

// sliceAppender grows a slice value in place, one decoded element at a time.
type sliceAppender struct {
	rv reflect.Value // the target slice (settable)
	n  int
}

func (a *sliceAppender) next() reflect.Value {
	if a.n == a.rv.Cap() {
		c := 2 * a.n
		if c < 4 {
			c = 4
		}
		ns := reflect.MakeSlice(a.rv.Type(), a.n, c)
		reflect.Copy(ns, a.rv)
		a.rv.Set(ns)
	}
	a.rv.SetLen(a.n + 1)
	e := a.rv.Index(a.n)
	a.n++
	return e
}

func (d *decoder) decodeSlice(rv reflect.Value) error {
	if err := d.enter(); err != nil {
		return err
	}
	err := d.decodeSliceInner(rv)
	d.leave()
	return err
}

func (d *decoder) decodeSliceInner(rv reflect.Value) error {
	pending := d.takePending()
	d.skipLayout()
	if !d.peekIs('[') {
		return d.scalarError(d.fail(msgExpectedBracket))
	}
	if d.depth == 1 && pending == nil && d.headerFollows() {
		return d.decodeRows(rv)
	}
	elem, err := d.elemHint(pending)
	if err != nil {
		return err
	}
	d.pos++
	if rv.IsNil() {
		rv.Set(reflect.MakeSlice(rv.Type(), 0, 0))
	} else {
		rv.SetLen(0)
	}
	a := sliceAppender{rv: rv}
	d.skipLayout()
	if d.peekIs(']') {
		d.pos++
		return nil
	}
	for {
		d.pending = elem
		e := a.next()
		e.SetZero()
		if err := d.decodeValue(e); err != nil {
			return err
		}
		if d.peekIs(',') {
			d.pos++
			continue
		}
		d.skipLayout()
		if d.pos >= len(d.data) {
			return d.fail(msgUnclosedBracket)
		}
		switch d.data[d.pos] {
		case ',':
			d.pos++
		case ']':
			d.pos++
			return nil
		default:
			return d.unexpected()
		}
	}
}

func (d *decoder) elemHint(pending *ty) (*ty, error) {
	if pending == nil {
		return nil, nil
	}
	if pending.kind != tyArr {
		return nil, d.fail(msgHint)
	}
	return pending.elem.hint(), nil
}

// decodeArray decodes `[...]` into a Go array, which must receive exactly as
// many elements as it has.
func (d *decoder) decodeArray(rv reflect.Value) error {
	if err := d.enter(); err != nil {
		return err
	}
	defer d.leave()
	elem, err := d.elemHint(d.takePending())
	if err != nil {
		return err
	}
	d.skipLayout()
	if !d.peekIs('[') {
		return d.scalarError(d.fail(msgExpectedBracket))
	}
	d.pos++
	n := rv.Len()
	d.skipLayout()
	if d.peekIs(']') {
		if n != 0 {
			return fieldCountMismatch(d.pos, n, 0)
		}
		d.pos++
		return nil
	}
	for i := 0; ; i++ {
		if i > 0 {
			if err := d.expectSlotComma(']', n, i); err != nil {
				return err
			}
		}
		if i >= n {
			return fieldCountMismatch(d.pos, n, i+1)
		}
		d.pending = elem
		e := rv.Index(i)
		e.SetZero()
		if err := d.decodeValue(e); err != nil {
			return err
		}
		if i == n-1 {
			return d.expectGroupClose(']', n)
		}
	}
}

// decodeRows decodes `[{schema}]:(row),(row),...`: zero or more rows, no
// trailing comma, never a null row (S9).
func (d *decoder) decodeRows(rv reflect.Value) error {
	d.pos++ // '['
	d.skipLayout()
	s, err := d.parseSchema()
	if err != nil {
		return err
	}
	d.skipLayout()
	if !d.peekIs(']') {
		return d.fail(msgExpectedClose)
	}
	d.pos++
	d.skipLayout()
	if !d.peekIs(':') {
		return d.fail(msgExpectedColon)
	}
	d.pos++
	if rv.IsNil() {
		rv.Set(reflect.MakeSlice(rv.Type(), 0, 0))
	} else {
		rv.SetLen(0)
	}
	a := sliceAppender{rv: rv}
	d.skipLayout()
	if !d.peekIs('(') {
		return nil
	}
	rowsStart := d.pos
	for {
		d.rows = s
		e := a.next()
		e.SetZero()
		err := d.decodeValue(e)
		d.rows = nil
		if err != nil {
			return err
		}
		if a.n == 1 {
			// Size the slice from the first row instead of doubling.
			row := d.pos - rowsStart + 1
			want := cautiousCapacity((len(d.data)-d.pos)/row+1, rv.Type().Elem().Size())
			if want > rv.Cap() {
				ns := reflect.MakeSlice(rv.Type(), 1, want)
				reflect.Copy(ns, rv)
				rv.Set(ns)
			}
		}
		if !d.peekIs(',') {
			d.skipLayout()
			if !d.peekIs(',') {
				return nil
			}
		}
		d.pos++
		if !d.peekIs('(') {
			d.skipLayout()
			if !d.peekIs('(') {
				return d.unexpected()
			}
		}
	}
}

// decodeStruct decodes a tuple into a struct. The source field names come
// from the `[{schema}]:` header (rows), the parent's `@{...}` binding, the
// top-level `{schema}:` header, or — for unbound nested data — the target's
// own fields in order.
func (d *decoder) decodeStruct(rv reflect.Value, si *structInfo) error {
	pending := d.takePending()
	if err := d.enter(); err != nil {
		return err
	}
	if !d.peekIs('(') {
		d.skipLayout()
	}
	var src *schema
	switch {
	case d.peekIs('(') && d.rows != nil:
		src = d.rows
		d.rows = nil
		d.pos++
	case d.peekIs('('):
		d.pos++
		if pending != nil {
			if pending.kind != tyObj {
				return d.fail(msgHint)
			}
			src = pending.obj
		}
	case d.peekIs('{') && d.depth == 1:
		s, err := d.parseSchema()
		if err != nil {
			return err
		}
		d.skipLayout()
		if !d.peekIs(':') {
			return d.fail(msgExpectedColon)
		}
		d.pos++
		d.skipLayout()
		if !d.peekIs('(') {
			return d.fail(msgExpectedParen)
		}
		d.pos++
		src = s
	default:
		return d.scalarError(d.fail(msgExpectedParen))
	}

	rv.SetZero()
	var n int
	if src == nil {
		// Unbound nested data: the target's fields, by position.
		n = len(si.fields)
		for i := 0; i < n; i++ {
			if i > 0 {
				if err := d.slotComma(n, i); err != nil {
					return err
				}
			}
			if err := d.decodeValue(si.field(rv, i)); err != nil {
				return err
			}
		}
	} else {
		n = len(src.names)
		p := src.planFor(si)
		for i := 0; i < n; i++ {
			if i > 0 {
				if err := d.slotComma(n, i); err != nil {
					return err
				}
			}
			d.pending = src.types[i].hint()
			if j := p.src[i]; j >= 0 {
				if err := d.decodeValue(si.field(rv, j)); err != nil {
					return err
				}
			} else {
				d.pending = nil
				if err := d.skipValue(); err != nil {
					return err
				}
			}
		}
	}
	d.leave()
	if d.peekIs(')') {
		d.pos++
		return nil
	}
	return d.expectGroupClose(')', n)
}

func (d *decoder) slotComma(n, i int) error {
	if d.peekIs(',') {
		d.pos++
		return nil
	}
	return d.expectSlotComma(')', n, i)
}

// ---------------------------------------------------------------------------
// Untyped (interface{}) decoding
// ---------------------------------------------------------------------------

// decodeAny decodes one slot without a target type: S2 token resolution, or
// the type its `@` hint declares. Tuples need a schema to name their fields.
func (d *decoder) decodeAny() (any, error) {
	hint := d.takePending()
	d.skipLayout()
	if d.atValueEnd() {
		return nil, nil
	}
	if d.atNullKeyword() {
		d.pos += 4
		return nil, nil
	}
	if hint != nil {
		switch hint.kind {
		case tyInt:
			var v int64
			err := d.decodeValue(reflect.ValueOf(&v).Elem())
			return v, err
		case tyFloat:
			var v float64
			d.pending = hint
			err := d.decodeValue(reflect.ValueOf(&v).Elem())
			return v, err
		case tyStr:
			d.pending = hint
			return d.parseStringSlot()
		case tyBool:
			v, ok := d.parseBoolLiteral()
			if !ok {
				return nil, d.fail(msgInvalidBool)
			}
			return v, nil
		case tyObj:
			if !d.peekIs('(') {
				return nil, d.fail(msgHint)
			}
			return d.decodeAnyObject(hint.obj)
		case tyArr:
			if !d.peekIs('[') {
				return nil, d.fail(msgHint)
			}
			return d.decodeAnyArray(hint.elem.hint())
		}
	}
	switch d.data[d.pos] {
	case '"':
		return d.parseQuotedString()
	case '(':
		if s := d.rows; s != nil {
			d.rows = nil
			return d.decodeAnyObject(s)
		}
		return nil, d.fail(msgNeedsBinding)
	case '{':
		if d.depth != 0 {
			return nil, d.unexpected()
		}
		s, err := d.parseSchema()
		if err != nil {
			return nil, err
		}
		d.skipLayout()
		if !d.peekIs(':') {
			return nil, d.fail(msgExpectedColon)
		}
		d.pos++
		d.skipLayout()
		if !d.peekIs('(') {
			return nil, d.fail(msgExpectedParen)
		}
		return d.decodeAnyObject(s)
	case '[':
		if d.depth == 0 && d.headerFollows() {
			return d.decodeAnyRows()
		}
		return d.decodeAnyArray(nil)
	}
	// A keyword or number must be the whole token: `true x` and `123 abc`
	// are plain strings (S2).
	save := d.pos
	if v, ok := d.parseBoolLiteral(); ok {
		if d.slotEndsAfterLayout(d.pos) {
			return v, nil
		}
		d.pos = save
	}
	if b := d.data[d.pos]; b == '-' || b-'0' <= 9 {
		if text, frac, err := d.scanFloat(); err == nil && d.slotEndsAfterLayout(d.pos) {
			if !frac {
				d.pos = save
				v, err := d.parseI64()
				if err != nil {
					return nil, err
				}
				return v, nil
			}
			v, err := strconv.ParseFloat(unsafeString(text), 64)
			if err != nil && v != 0 {
				d.pos = save
				return nil, d.fail(msgFloatRange)
			}
			return v, nil
		}
		d.pos = save
	}
	d.pending = nil
	return d.parseStringSlot()
}

func (d *decoder) decodeAnyObject(s *schema) (any, error) {
	if err := d.enter(); err != nil {
		return nil, err
	}
	d.pos++ // '('
	m := make(map[string]any, len(s.names))
	n := len(s.names)
	for i := 0; i < n; i++ {
		if i > 0 {
			if err := d.slotComma(n, i); err != nil {
				return nil, err
			}
		}
		d.pending = s.types[i].hint()
		v, err := d.decodeAny()
		if err != nil {
			return nil, err
		}
		m[s.names[i]] = v
	}
	d.leave()
	if d.peekIs(')') {
		d.pos++
		return m, nil
	}
	return m, d.expectGroupClose(')', n)
}

func (d *decoder) decodeAnyArray(elem *ty) (any, error) {
	if err := d.enter(); err != nil {
		return nil, err
	}
	defer d.leave()
	d.pos++ // '['
	out := []any{}
	d.skipLayout()
	if d.peekIs(']') {
		d.pos++
		return out, nil
	}
	for {
		d.pending = elem
		v, err := d.decodeAny()
		if err != nil {
			return nil, err
		}
		out = append(out, v)
		d.skipLayout()
		if d.pos >= len(d.data) {
			return nil, d.fail(msgUnclosedBracket)
		}
		switch d.data[d.pos] {
		case ',':
			d.pos++
		case ']':
			d.pos++
			return out, nil
		default:
			return nil, d.unexpected()
		}
	}
}

func (d *decoder) decodeAnyRows() (any, error) {
	d.pos++ // '['
	d.skipLayout()
	s, err := d.parseSchema()
	if err != nil {
		return nil, err
	}
	d.skipLayout()
	if !d.peekIs(']') {
		return nil, d.fail(msgExpectedClose)
	}
	d.pos++
	d.skipLayout()
	if !d.peekIs(':') {
		return nil, d.fail(msgExpectedColon)
	}
	d.pos++
	out := []any{}
	d.skipLayout()
	if !d.peekIs('(') {
		return out, nil
	}
	for {
		row, err := d.decodeAnyObject(s)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
		d.skipLayout()
		if !d.peekIs(',') {
			return out, nil
		}
		d.pos++
		d.skipLayout()
		if !d.peekIs('(') {
			return nil, d.unexpected()
		}
	}
}
