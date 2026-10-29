package distill

import (
	"bytes"
	"strconv"
)

// Minimal PDF content stream tokenizer. It understands every token type that
// can appear in a content stream (numbers, names, literal and hex strings,
// arrays, dictionaries, operators) and skips inline image data.

type opKind int

const (
	kNum opKind = iota
	kName
	kStr
	kArr
	kDict
	kBool
	kNull
)

type operand struct {
	kind opKind
	num  float64
	name string
	str  []byte
	arr  []operand
}

type lexer struct {
	b []byte
	p int
}

func isWS(c byte) bool {
	return c == ' ' || c == '\n' || c == '\r' || c == '\t' || c == '\f' || c == 0
}

func isDelim(c byte) bool {
	switch c {
	case '(', ')', '<', '>', '[', ']', '{', '}', '/', '%':
		return true
	}
	return false
}

func (l *lexer) skipWS() {
	for l.p < len(l.b) {
		c := l.b[l.p]
		if isWS(c) {
			l.p++
		} else if c == '%' {
			for l.p < len(l.b) && l.b[l.p] != '\n' && l.b[l.p] != '\r' {
				l.p++
			}
		} else {
			return
		}
	}
}

// token kinds returned by next
const (
	tEOF = iota
	tOperand
	tOperator
	tArrStart
	tArrEnd
	tDictStart
	tDictEnd
)

func (l *lexer) regular() string {
	s := l.p
	for l.p < len(l.b) && !isWS(l.b[l.p]) && !isDelim(l.b[l.p]) {
		l.p++
	}
	return string(l.b[s:l.p])
}

func (l *lexer) next() (int, operand, string) {
	l.skipWS()
	if l.p >= len(l.b) {
		return tEOF, operand{}, ""
	}
	c := l.b[l.p]
	switch c {
	case '/':
		l.p++
		return tOperand, operand{kind: kName, name: decodeName(l.regular())}, ""
	case '(':
		l.p++
		return tOperand, operand{kind: kStr, str: l.literal()}, ""
	case '<':
		if l.p+1 < len(l.b) && l.b[l.p+1] == '<' {
			l.p += 2
			return tDictStart, operand{}, ""
		}
		l.p++
		return tOperand, operand{kind: kStr, str: l.hex()}, ""
	case '>':
		l.p++
		if l.p < len(l.b) && l.b[l.p] == '>' {
			l.p++
		}
		return tDictEnd, operand{}, ""
	case '[':
		l.p++
		return tArrStart, operand{}, ""
	case ']':
		l.p++
		return tArrEnd, operand{}, ""
	case '{', '}', ')':
		l.p++
		return l.next()
	}
	tok := l.regular()
	if tok == "" {
		l.p++
		return l.next()
	}
	if (tok[0] >= '0' && tok[0] <= '9') || tok[0] == '-' || tok[0] == '+' || tok[0] == '.' {
		f, err := strconv.ParseFloat(tok, 64)
		if err != nil {
			f = lenientNumber(tok)
		}
		return tOperand, operand{kind: kNum, num: f}, ""
	}
	switch tok {
	case "true", "false":
		return tOperand, operand{kind: kBool}, ""
	case "null":
		return tOperand, operand{kind: kNull}, ""
	}
	return tOperator, operand{}, tok
}

func lenientNumber(tok string) float64 {
	// Handle malformed numbers such as "--5" or "1.2.3" leniently.
	end := 0
	seenDot := false
	for i := 0; i < len(tok); i++ {
		c := tok[i]
		if c >= '0' && c <= '9' {
			end = i + 1
		} else if c == '.' && !seenDot {
			seenDot = true
		} else if (c == '-' || c == '+') && i == 0 {
		} else {
			break
		}
	}
	s := tok[:end]
	for len(s) > 1 && (s[0] == '-' || s[0] == '+') && (s[1] == '-' || s[1] == '+') {
		s = s[1:]
	}
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

func decodeName(s string) string {
	if !bytes.ContainsRune([]byte(s), '#') {
		return s
	}
	var out []byte
	for i := 0; i < len(s); i++ {
		if s[i] == '#' && i+2 < len(s) {
			if v, err := strconv.ParseUint(s[i+1:i+3], 16, 8); err == nil {
				out = append(out, byte(v))
				i += 2
				continue
			}
		}
		out = append(out, s[i])
	}
	return string(out)
}

func (l *lexer) literal() []byte {
	var out []byte
	depth := 1
	for l.p < len(l.b) {
		c := l.b[l.p]
		l.p++
		switch c {
		case '(':
			depth++
			out = append(out, c)
		case ')':
			depth--
			if depth == 0 {
				return out
			}
			out = append(out, c)
		case '\\':
			if l.p >= len(l.b) {
				return out
			}
			e := l.b[l.p]
			l.p++
			switch e {
			case 'n':
				out = append(out, '\n')
			case 'r':
				out = append(out, '\r')
			case 't':
				out = append(out, '\t')
			case 'b':
				out = append(out, '\b')
			case 'f':
				out = append(out, '\f')
			case '\r':
				if l.p < len(l.b) && l.b[l.p] == '\n' {
					l.p++
				}
			case '\n':
			default:
				if e >= '0' && e <= '7' {
					v := int(e - '0')
					for k := 0; k < 2 && l.p < len(l.b) && l.b[l.p] >= '0' && l.b[l.p] <= '7'; k++ {
						v = v*8 + int(l.b[l.p]-'0')
						l.p++
					}
					out = append(out, byte(v))
				} else {
					out = append(out, e)
				}
			}
		default:
			out = append(out, c)
		}
	}
	return out
}

func unhex(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

func (l *lexer) hex() []byte {
	var out []byte
	var hi byte
	half := false
	for l.p < len(l.b) {
		c := l.b[l.p]
		l.p++
		if c == '>' {
			break
		}
		v, ok := unhex(c)
		if !ok {
			continue
		}
		if half {
			out = append(out, hi<<4|v)
			half = false
		} else {
			hi = v
			half = true
		}
	}
	if half {
		out = append(out, hi<<4)
	}
	return out
}

// readCompound reads the remainder of an array or dictionary whose opening
// token has already been consumed. Dictionaries are returned as arrays of
// alternating keys and values.
func (l *lexer) readCompound(kind opKind, depth int) operand {
	res := operand{kind: kind}
	for {
		t, o, _ := l.next()
		switch t {
		case tEOF:
			return res
		case tOperand:
			res.arr = append(res.arr, o)
		case tArrStart:
			if depth > 32 {
				return res
			}
			res.arr = append(res.arr, l.readCompound(kArr, depth+1))
		case tDictStart:
			if depth > 32 {
				return res
			}
			res.arr = append(res.arr, l.readCompound(kDict, depth+1))
		case tArrEnd:
			if kind == kArr {
				return res
			}
		case tDictEnd:
			if kind == kDict {
				return res
			}
		case tOperator:
			// Operators are not allowed inside compound objects; tolerate.
		}
	}
}

// skipInlineImage advances past inline image data following an ID operator.
func (l *lexer) skipInlineImage(dict []operand) {
	if l.p < len(l.b) && isWS(l.b[l.p]) {
		l.p++
	}
	// Prefer an explicit length (PDF 2.0 /L or /Length) when present.
	for i := 0; i+1 < len(dict); i += 2 {
		if dict[i].kind == kName && (dict[i].name == "L" || dict[i].name == "Length") && dict[i+1].kind == kNum {
			n := int(dict[i+1].num)
			if n >= 0 && l.p+n <= len(l.b) {
				end := l.p + n
				rest := l.b[end:]
				j := 0
				for j < len(rest) && isWS(rest[j]) {
					j++
				}
				if bytes.HasPrefix(rest[j:], []byte("EI")) {
					l.p = end + j + 2
					return
				}
			}
		}
	}
	for i := l.p; i+1 < len(l.b); i++ {
		if l.b[i] == 'E' && l.b[i+1] == 'I' && (i == l.p || isWS(l.b[i-1])) &&
			(i+2 == len(l.b) || isWS(l.b[i+2]) || isDelim(l.b[i+2])) {
			l.p = i + 2
			return
		}
	}
	l.p = len(l.b)
}

// parseContent invokes fn for each operator together with its operands.
func parseContent(b []byte, fn func(op string, args []operand)) {
	l := &lexer{b: b}
	var stack []operand
	for {
		t, o, op := l.next()
		switch t {
		case tEOF:
			return
		case tOperand:
			stack = append(stack, o)
		case tArrStart:
			stack = append(stack, l.readCompound(kArr, 0))
		case tDictStart:
			stack = append(stack, l.readCompound(kDict, 0))
		case tArrEnd, tDictEnd:
		case tOperator:
			if op == "BI" {
				stack = stack[:0]
				var dict []operand
				for {
					t2, o2, op2 := l.next()
					if t2 == tEOF {
						return
					}
					if t2 == tOperator && op2 == "ID" {
						break
					}
					switch t2 {
					case tOperand:
						dict = append(dict, o2)
					case tArrStart:
						dict = append(dict, l.readCompound(kArr, 0))
					case tDictStart:
						dict = append(dict, l.readCompound(kDict, 0))
					}
				}
				l.skipInlineImage(dict)
				fn("BI", dict)
				continue
			}
			fn(op, stack)
			stack = stack[:0]
		}
	}
}
