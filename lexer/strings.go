package lexer

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// FPart is one piece of an f-string: either literal text or an expression.
type FPart struct {
	IsExpr bool
	Text   string // literal text with escapes decoded (IsExpr == false)
	Expr   string // expression source code (IsExpr == true)
	Offset int    // byte offset of the piece inside the raw f-string
}

// FStringError is an error inside an f-string at a byte offset of its raw text.
type FStringError struct {
	Offset int
	Msg    string
}

func (e *FStringError) Error() string { return fmt.Sprintf("offset %d: %s", e.Offset, e.Msg) }

// SplitFString splits the raw text of an f-string (between the quotes) into
// literal text and {expression} pieces. "{{" and "}}" are literal braces.
func SplitFString(raw string) ([]FPart, error) {
	var parts []FPart
	var text strings.Builder
	textStart := 0

	flush := func() error {
		if text.Len() == 0 {
			return nil
		}
		val, off, msg := unescape(text.String())
		if msg != "" {
			return &FStringError{Offset: textStart + off, Msg: msg}
		}
		parts = append(parts, FPart{Text: val, Offset: textStart})
		text.Reset()
		return nil
	}

	for i := 0; i < len(raw); {
		c := raw[i]
		switch {
		case c == '\\':
			n := escapeLen(raw, i)
			text.WriteString(raw[i : i+n])
			i += n
		// "{{" and "}}" become the escapes "\{" and "\}": same length, so
		// error offsets inside the text stay correct.
		case c == '{' && i+1 < len(raw) && raw[i+1] == '{':
			text.WriteString(`\{`)
			i += 2
		case c == '}' && i+1 < len(raw) && raw[i+1] == '}':
			text.WriteString(`\}`)
			i += 2
		case c == '}':
			return nil, &FStringError{Offset: i, Msg: "single '}' in f-string: write '}}' for a literal brace"}
		case c == '{':
			if err := flush(); err != nil {
				return nil, err
			}
			end := matchBrace(raw, i)
			if end < 0 {
				return nil, &FStringError{Offset: i, Msg: "unclosed '{' in f-string"}
			}
			expr := raw[i+1 : end]
			if strings.TrimSpace(expr) == "" {
				return nil, &FStringError{Offset: i, Msg: "empty expression in f-string"}
			}
			parts = append(parts, FPart{IsExpr: true, Expr: expr, Offset: i + 1})
			i = end + 1
			textStart = i
		default:
			text.WriteByte(c)
			i++
		}
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return parts, nil
}

// escapeLen returns the length in bytes of the escape sequence at s[i] == '\\'.
func escapeLen(s string, i int) int {
	if i+1 >= len(s) || s[i+1] == '\n' {
		return 1
	}
	if s[i+1] == 'u' && i+2 < len(s) && s[i+2] == '{' {
		for j := i + 3; j < len(s) && s[j] != '\n' && s[j] != '"'; j++ {
			if s[j] == '}' {
				return j - i + 1
			}
		}
		return 2
	}
	_, size := utf8.DecodeRuneInString(s[i+1:])
	return 1 + size
}

// stringEnd returns the index of the quote that closes a plain string whose
// content starts at s[i], or -1 if the line or the input ends first.
func stringEnd(s string, i int) int {
	for i < len(s) {
		switch s[i] {
		case '"':
			return i
		case '\n':
			return -1
		case '\\':
			i += escapeLen(s, i)
		default:
			i++
		}
	}
	return -1
}

// matchBrace returns the index of the '}' matching the '{' at s[i], skipping
// nested braces and strings, or -1 if the line or the input ends first.
func matchBrace(s string, i int) int {
	depth := 0
	for i < len(s) {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		case '"':
			j := stringEnd(s, i+1)
			if j < 0 {
				return -1
			}
			i = j
		case '\n':
			return -1
		}
		i++
	}
	return -1
}

// fstringEnd returns the index of the quote that closes an f-string whose
// content starts at s[i]. If there is none it returns -1 and, when the cause
// is an unclosed '{', the index of that brace (otherwise -1).
func fstringEnd(s string, i int) (end, brace int) {
	for i < len(s) {
		switch s[i] {
		case '"':
			return i, -1
		case '\n':
			return -1, -1
		case '\\':
			i += escapeLen(s, i)
			continue
		case '{':
			if i+1 < len(s) && s[i+1] == '{' {
				i += 2
				continue
			}
			j := matchBrace(s, i)
			if j < 0 {
				return -1, i
			}
			i = j + 1
			continue
		}
		i++
	}
	return -1, -1
}

// unescape decodes escape sequences. On error it returns the byte offset of
// the bad escape and a message.
func unescape(s string) (val string, errOff int, msg string) {
	if strings.IndexByte(s, '\\') < 0 {
		return s, 0, ""
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		c := s[i]
		if c != '\\' {
			b.WriteByte(c)
			i++
			continue
		}
		if i+1 >= len(s) {
			return "", i, "unfinished escape sequence"
		}
		switch e := s[i+1]; e {
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case 'r':
			b.WriteByte('\r')
		case '0':
			b.WriteByte(0)
		case '\\', '"', '{', '}':
			b.WriteByte(e)
		case 'u':
			n := escapeLen(s, i)
			if n < 5 || s[i+2] != '{' {
				return "", i, `invalid unicode escape: use \u{1F600}`
			}
			hex := s[i+3 : i+n-1]
			v, err := strconv.ParseUint(hex, 16, 32)
			if err != nil || len(hex) > 6 || v > utf8.MaxRune || (0xD800 <= v && v <= 0xDFFF) {
				return "", i, fmt.Sprintf(`invalid unicode escape \u{%s}`, hex)
			}
			b.WriteRune(rune(v))
			i += n
			continue
		default:
			r, _ := utf8.DecodeRuneInString(s[i+1:])
			return "", i, fmt.Sprintf(`unknown escape sequence '\%c'`, r)
		}
		i += 2
	}
	return b.String(), 0, ""
}
