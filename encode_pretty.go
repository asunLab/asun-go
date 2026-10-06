package asun

// ---------------------------------------------------------------------------
// Pretty-printed ASUN text — smart indentation over the compact encoders.
//
// Simple structures stay inline:
//
//	{name@str, age@int}:(Alice, 30)
//
// Complex structures expand with 2-space indentation:
//
//	{
//	  id@str,
//	  name@str,
//	  addr@{city@str, zip@int}
//	}:
//	  (E001, John, (NYC, 10001))
//
// The output is byte-for-byte what asun-rs `pretty_format` produces.
// ---------------------------------------------------------------------------

const prettyMaxWidth = 100

// EncodePretty serializes v to pretty-formatted ASUN text.
func EncodePretty(v any) ([]byte, error) {
	compact, err := Encode(v)
	if err != nil {
		return nil, err
	}
	return PrettyFormat(compact), nil
}

// EncodePrettyTyped serializes v to pretty-formatted ASUN text with scalar
// type hints.
func EncodePrettyTyped(v any) ([]byte, error) {
	compact, err := EncodeTyped(v)
	if err != nil {
		return nil, err
	}
	return PrettyFormat(compact), nil
}

// PrettyFormat reformats compact ASUN bytes with smart indentation.
func PrettyFormat(src []byte) []byte {
	if len(src) == 0 {
		return []byte{}
	}
	f := prettyFmt{src: src, mat: buildMatchTable(src), out: make([]byte, 0, len(src)*2)}
	f.writeTop()
	return f.out
}

// atomLen is the length of the token at i whose bytes are never structural
// and are copied verbatim: a quoted string, a `\x` escape inside a plain
// string, or a comment; 0 when src[i] starts none of them. Unterminated
// tokens run to the end of the input.
func atomLen(src []byte, i int) int {
	n := len(src)
	switch src[i] {
	case '"':
		j := i + 1
		for j < n {
			switch src[j] {
			case '\\':
				j += 2
			case '"':
				return j + 1 - i
			default:
				j++
			}
		}
		return n - i
	case '\\':
		if n-i < 2 {
			return n - i
		}
		return 2
	case '/':
		if i+1 < n && src[i+1] == '*' {
			j := i + 2
			for j+1 < n {
				if src[j] == '*' && src[j+1] == '/' {
					return j + 2 - i
				}
				j++
			}
			return n - i
		}
	}
	return 0
}

func buildMatchTable(src []byte) []int32 {
	n := len(src)
	mat := make([]int32, n)
	for i := range mat {
		mat[i] = -1
	}
	stack := make([]int, 0, 32)
	for i := 0; i < n; {
		if a := atomLen(src, i); a > 0 {
			i += a
			continue
		}
		switch src[i] {
		case '{', '(', '[':
			stack = append(stack, i)
		case '}', ')', ']':
			if len(stack) > 0 {
				j := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				mat[j] = int32(i)
				mat[i] = int32(j)
			}
		}
		i++
	}
	return mat
}

type prettyFmt struct {
	src   []byte
	mat   []int32
	out   []byte
	pos   int
	depth int
}

func (f *prettyFmt) writeTop() {
	if f.pos >= len(f.src) {
		return
	}
	if f.src[f.pos] == '[' && f.pos+1 < len(f.src) && f.src[f.pos+1] == '{' {
		f.writeArrayTop()
	} else if f.src[f.pos] == '{' {
		f.writeObjectTop()
	} else {
		f.out = append(f.out, f.src[f.pos:]...)
	}
}

func (f *prettyFmt) writeObjectTop() {
	f.writeGroup()
	if f.pos < len(f.src) && f.src[f.pos] == ':' {
		f.out = append(f.out, ':')
		f.pos++
		if f.pos < len(f.src) {
			c := f.mat[f.pos]
			if c >= 0 && int(c)-f.pos < prettyMaxWidth {
				end := int(c) + 1
				f.writeInline(f.pos, end)
				f.pos = end
			} else {
				f.out = append(f.out, '\n')
				f.depth++
				f.writeIndent()
				f.writeGroup()
				f.depth--
			}
		}
	}
}

func (f *prettyFmt) writeArrayTop() {
	f.out = append(f.out, '[')
	f.pos++
	f.writeGroup()
	if f.pos < len(f.src) && f.src[f.pos] == ']' {
		f.out = append(f.out, ']')
		f.pos++
	}
	if f.pos < len(f.src) && f.src[f.pos] == ':' {
		f.out = append(f.out, ':', '\n')
		f.pos++
	}
	f.depth++
	first := true
	for f.pos < len(f.src) {
		if f.src[f.pos] == ',' {
			f.pos++
		}
		if f.pos >= len(f.src) {
			break
		}
		if !first {
			f.out = append(f.out, ',', '\n')
		}
		first = false
		f.writeIndent()
		before := f.pos
		f.writeGroup()
		if f.pos == before {
			// A stray closer after the rows: copy it so the loop advances.
			f.out = append(f.out, f.src[f.pos])
			f.pos++
		}
	}
	f.out = append(f.out, '\n')
	f.depth--
}

func (f *prettyFmt) writeGroup() {
	if f.pos >= len(f.src) {
		return
	}
	ch := f.src[f.pos]
	if ch != '{' && ch != '(' && ch != '[' {
		f.writeValue()
		return
	}

	// [{...}] array schema: fuse the brackets.
	if ch == '[' && f.pos+1 < len(f.src) && f.src[f.pos+1] == '{' {
		closeBrace := f.mat[f.pos+1]
		closeBracket := f.mat[f.pos]
		if closeBrace >= 0 && closeBracket >= 0 && closeBrace+1 == closeBracket {
			width := int(closeBracket) - f.pos + 1
			if width <= prettyMaxWidth {
				end := int(closeBracket) + 1
				f.writeInline(f.pos, end)
				f.pos = end
				return
			}
			f.out = append(f.out, '[')
			f.pos++
			f.writeGroup()
			f.out = append(f.out, ']')
			f.pos++
			return
		}
	}

	closePos := f.mat[f.pos]
	if closePos < 0 {
		f.out = append(f.out, ch)
		f.pos++
		return
	}
	cl := int(closePos)
	if cl-f.pos+1 <= prettyMaxWidth {
		f.writeInline(f.pos, cl+1)
		f.pos = cl + 1
		return
	}

	// Expanded form.
	closeCh := f.src[cl]
	f.out = append(f.out, ch)
	f.pos++
	if f.pos >= cl {
		f.out = append(f.out, closeCh)
		f.pos = cl + 1
		return
	}
	f.out = append(f.out, '\n')
	f.depth++
	first := true
	for f.pos < cl {
		// The comma is consumed only between slots: a leading `,` ends an
		// empty (null) first slot and must be kept.
		if !first && f.src[f.pos] == ',' {
			f.pos++
		}
		if !first {
			f.out = append(f.out, ',', '\n')
		}
		first = false
		f.writeIndent()
		f.writeElement(cl)
	}
	f.out = append(f.out, '\n')
	f.depth--
	f.writeIndent()
	f.out = append(f.out, closeCh)
	f.pos = cl + 1
}

func (f *prettyFmt) writeElement(boundary int) {
	for f.pos < boundary && f.src[f.pos] != ',' {
		ch := f.src[f.pos]
		if ch == '{' || ch == '(' || ch == '[' {
			f.writeGroup()
		} else if atomLen(f.src, f.pos) > 0 {
			f.writeAtom()
		} else {
			f.out = append(f.out, ch)
			f.pos++
		}
	}
}

func (f *prettyFmt) writeValue() {
	for f.pos < len(f.src) {
		ch := f.src[f.pos]
		if ch == ',' || ch == ')' || ch == '}' || ch == ']' {
			break
		}
		if atomLen(f.src, f.pos) > 0 {
			f.writeAtom()
		} else {
			f.out = append(f.out, ch)
			f.pos++
		}
	}
}

func (f *prettyFmt) writeAtom() {
	end := f.pos + atomLen(f.src, f.pos)
	f.out = append(f.out, f.src[f.pos:end]...)
	f.pos = end
}

func (f *prettyFmt) writeInline(start, end int) {
	depth := 0
	for i := start; i < end; {
		ch := f.src[i]
		if a := atomLen(f.src, i); a > 0 {
			if a > end-i {
				a = end - i
			}
			f.out = append(f.out, f.src[i:i+a]...)
			i += a
			continue
		}
		switch ch {
		case '{', '(', '[':
			depth++
			f.out = append(f.out, ch)
		case '}', ')', ']':
			depth--
			f.out = append(f.out, ch)
		case ',':
			f.out = append(f.out, ',')
			if depth == 1 {
				f.out = append(f.out, ' ')
			}
		default:
			f.out = append(f.out, ch)
		}
		i++
	}
}

func (f *prettyFmt) writeIndent() {
	for i := 0; i < f.depth; i++ {
		f.out = append(f.out, ' ', ' ')
	}
}
