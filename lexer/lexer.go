// Package lexer turns SEPL source text into tokens.
//
// Besides ordinary tokens it produces NEWLINE, INDENT and DEDENT:
//
//   - A newline ends a statement only when the line ended with a token that
//     can end an expression (a name, a literal, ')', ']', '}', 'return', ...)
//     or with ':' that opens a block.
//   - After a line that ended with anything else (an operator, a comma, ...)
//     the next line is a continuation: no NEWLINE, and its indentation is
//     ignored.
//   - Inside (), [] and {} newlines and indentation are ignored.
//   - Indentation is spaces only; a tab in indentation is an error.
//   - Blank lines and comment-only lines are skipped.
package lexer

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Username59138/sepl/token"
)

// Error is a lexical error at a position in the source.
type Error struct {
	Pos token.Pos
	Msg string
}

func (e *Error) Error() string { return e.Pos.String() + ": " + e.Msg }

type bracket struct {
	ch  byte
	pos token.Pos
}

// Lexer produces tokens one at a time. Create it with New and call Next
// until it returns EOF, or ILLEGAL on the first error (see Err).
type Lexer struct {
	src  string
	off  int // byte offset of the next character
	line int
	col  int // column of the next character, in characters, 1-based

	indents  []int     // indentation stack, starts with 0
	brackets []bracket // currently open brackets
	queue    []token.Token
	head     int

	lineStart bool       // at the beginning of a physical line
	cont      bool       // the current line continues the previous one
	last      token.Type // last token emitted
	err       *Error
	done      bool
}

// New returns a lexer for src.
func New(src string) *Lexer {
	return &Lexer{
		src:       strings.ReplaceAll(src, "\r\n", "\n"),
		line:      1,
		col:       1,
		indents:   []int{0},
		lineStart: true,
		last:      token.NEWLINE,
	}
}

// NewAt returns a lexer for src whose positions start at pos. The parser
// uses it for expressions embedded in f-strings.
func NewAt(src string, pos token.Pos) *Lexer {
	l := New(src)
	l.line, l.col = pos.Line, pos.Col
	return l
}

// Tokenize returns all tokens of src up to and including EOF.
// On error it returns the tokens before the error and an *Error.
func Tokenize(src string) ([]token.Token, error) {
	l := New(src)
	toks := make([]token.Token, 0, len(src)/4+1)
	for {
		t := l.Next()
		if t.Type == token.ILLEGAL {
			return toks, l.err
		}
		toks = append(toks, t)
		if t.Type == token.EOF {
			return toks, nil
		}
	}
}

// Err returns the error that stopped the lexer, or nil.
func (l *Lexer) Err() *Error { return l.err }

// Next returns the next token. After EOF or ILLEGAL it keeps returning EOF.
func (l *Lexer) Next() token.Token {
	for l.head == len(l.queue) {
		if l.done {
			return token.Token{Type: token.EOF, Pos: l.pos()}
		}
		l.queue = l.queue[:0]
		l.head = 0
		l.scan()
	}
	t := l.queue[l.head]
	l.head++
	return t
}

// scan does one step of work, queueing zero or more tokens.
func (l *Lexer) scan() {
	if l.lineStart {
		l.lineStart = false
		if len(l.brackets) == 0 && !l.cont {
			if l.indentation() {
				return
			}
		} else {
			for l.peek() == ' ' {
				l.advance()
			}
			if l.peek() == '\t' && !l.restIsBlank() {
				l.fail(l.pos(), "tab in indentation: SEPL uses spaces only")
				return
			}
		}
	}

	l.skipSpace()
	if l.off >= len(l.src) {
		l.eof()
		return
	}

	pos := l.pos()
	if l.src[l.off] == '\n' {
		l.advance()
		if len(l.brackets) > 0 {
			return
		}
		switch {
		case endsStatement(l.last):
			l.emit(token.NEWLINE, "", pos)
			l.cont = false
		case l.last != token.NEWLINE && l.last != token.INDENT && l.last != token.DEDENT:
			l.cont = true
		}
		l.lineStart = true
		return
	}
	l.scanToken(pos)
}

// endsStatement reports whether a newline after t ends the statement.
func endsStatement(t token.Type) bool {
	switch t {
	case token.IDENT, token.INT, token.FLOAT, token.STRING, token.FSTRING,
		token.TRUE, token.FALSE, token.NIL,
		token.RETURN, token.BREAK, token.CONTINUE,
		token.RPAREN, token.RBRACKET, token.RBRACE,
		token.QUESTION, token.COLON:
		return true
	}
	return false
}

// indentation measures the indentation of the next non-blank line and
// queues INDENT/DEDENT tokens. It reports whether scanning must stop
// (end of file or an error).
func (l *Lexer) indentation() bool {
	for {
		n := 0
		for l.peek() == ' ' {
			l.advance()
			n++
		}
		if l.off >= len(l.src) {
			l.eof()
			return true
		}
		switch l.src[l.off] {
		case '\n':
			l.advance()
			continue
		case '#', '\t', '\r':
			if l.restIsBlank() {
				l.skipToEOL()
				continue
			}
			if l.src[l.off] == '\t' {
				l.fail(l.pos(), "tab in indentation: SEPL uses spaces only")
				return true
			}
		}
		l.setIndent(n)
		return l.done
	}
}

func (l *Lexer) setIndent(n int) {
	pos := l.pos()
	top := l.indents[len(l.indents)-1]
	if n > top {
		l.indents = append(l.indents, n)
		l.emit(token.INDENT, "", pos)
		return
	}
	for n < top {
		l.indents = l.indents[:len(l.indents)-1]
		l.emit(token.DEDENT, "", pos)
		top = l.indents[len(l.indents)-1]
	}
	if n != top {
		l.fail(pos, fmt.Sprintf("indentation of %d spaces does not match any outer block (expected %d)", n, top))
	}
}

func (l *Lexer) eof() {
	if len(l.brackets) > 0 {
		b := l.brackets[len(l.brackets)-1]
		l.fail(b.pos, fmt.Sprintf("'%c' is never closed", b.ch))
		return
	}
	pos := l.pos()
	if endsStatement(l.last) {
		l.emit(token.NEWLINE, "", pos)
	}
	for len(l.indents) > 1 {
		l.indents = l.indents[:len(l.indents)-1]
		l.emit(token.DEDENT, "", pos)
	}
	l.emit(token.EOF, "", pos)
	l.done = true
}

func (l *Lexer) fail(pos token.Pos, msg string) {
	l.err = &Error{Pos: pos, Msg: msg}
	l.emit(token.ILLEGAL, msg, pos)
	l.done = true
}

func (l *Lexer) emit(t token.Type, lit string, pos token.Pos) {
	l.queue = append(l.queue, token.Token{Type: t, Literal: lit, Pos: pos})
	l.last = t
}

// --- character helpers ---

func (l *Lexer) pos() token.Pos { return token.Pos{Line: l.line, Col: l.col} }

// posAhead returns the position of byte offset i on the current line (i >= l.off).
func (l *Lexer) posAhead(i int) token.Pos {
	return token.Pos{Line: l.line, Col: l.col + utf8.RuneCountInString(l.src[l.off:i])}
}

func (l *Lexer) peek() byte { return l.peekAt(0) }

func (l *Lexer) peekAt(k int) byte {
	if l.off+k < len(l.src) {
		return l.src[l.off+k]
	}
	return 0
}

func (l *Lexer) advance() {
	if l.off >= len(l.src) {
		return
	}
	c := l.src[l.off]
	if c < utf8.RuneSelf {
		l.off++
		if c == '\n' {
			l.line++
			l.col = 1
		} else {
			l.col++
		}
		return
	}
	_, size := utf8.DecodeRuneInString(l.src[l.off:])
	l.off += size
	l.col++
}

func (l *Lexer) skipSpace() {
	for l.off < len(l.src) {
		switch l.src[l.off] {
		case ' ', '\t', '\r':
			l.advance()
		case '#':
			l.skipToEOL()
		default:
			return
		}
	}
}

func (l *Lexer) skipToEOL() {
	for l.off < len(l.src) && l.src[l.off] != '\n' {
		l.advance()
	}
}

// restIsBlank reports whether the rest of the current line is only
// whitespace and/or a comment.
func (l *Lexer) restIsBlank() bool {
	for i := l.off; i < len(l.src); i++ {
		switch l.src[i] {
		case ' ', '\t', '\r':
		case '\n', '#':
			return true
		default:
			return false
		}
	}
	return true
}

func isLetter(r rune) bool {
	if r < utf8.RuneSelf {
		return 'a' <= r && r <= 'z' || 'A' <= r && r <= 'Z' || r == '_'
	}
	return unicode.IsLetter(r)
}

func isIdentChar(r rune) bool {
	if r < utf8.RuneSelf {
		return isLetter(r) || '0' <= r && r <= '9'
	}
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

func isDecimal(c byte) bool { return '0' <= c && c <= '9' }

func isDigitIn(c byte, base int) bool {
	switch base {
	case 2:
		return c == '0' || c == '1'
	case 8:
		return '0' <= c && c <= '7'
	case 16:
		return isDecimal(c) || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'
	}
	return isDecimal(c)
}

// --- tokens ---

func (l *Lexer) scanToken(pos token.Pos) {
	r, size := utf8.DecodeRuneInString(l.src[l.off:])
	switch {
	case r == utf8.RuneError && size == 1:
		l.fail(pos, "invalid UTF-8 in source")
	case isLetter(r):
		l.scanIdent(pos)
	case '0' <= r && r <= '9':
		l.scanNumber(pos)
	case r == '"':
		l.scanString(pos)
	default:
		l.scanOperator(pos, r)
	}
}

func (l *Lexer) scanIdent(pos token.Pos) {
	start := l.off
	for l.off < len(l.src) {
		c := l.src[l.off]
		if c < utf8.RuneSelf { // fast path: ASCII
			if !isIdentChar(rune(c)) {
				break
			}
			l.off++
			l.col++
			continue
		}
		r, size := utf8.DecodeRuneInString(l.src[l.off:])
		if !isIdentChar(r) {
			break
		}
		l.off += size
		l.col++
	}
	word := l.src[start:l.off]
	if word == "f" && l.peek() == '"' {
		l.scanFString(pos)
		return
	}
	l.emit(token.Lookup(word), word, pos)
}

func (l *Lexer) scanNumber(pos token.Pos) {
	start := l.off
	isFloat := false
	base := 10
	if l.peek() == '0' {
		switch l.peekAt(1) {
		case 'x', 'X':
			base = 16
		case 'o', 'O':
			base = 8
		case 'b', 'B':
			base = 2
		}
	}

	if base != 10 {
		l.advance()
		l.advance()
		n, ok := l.digits(base)
		if !ok {
			l.fail(pos, "'_' must separate digits")
			return
		}
		if n == 0 {
			l.fail(pos, fmt.Sprintf("missing digits after %s", l.src[start:start+2]))
			return
		}
	} else {
		if _, ok := l.digits(10); !ok {
			l.fail(pos, "'_' must separate digits")
			return
		}
		// "1.5" is a float, but "0..10" and "1.to_str()" are not.
		if l.peek() == '.' && isDecimal(l.peekAt(1)) {
			isFloat = true
			l.advance()
			if _, ok := l.digits(10); !ok {
				l.fail(pos, "'_' must separate digits")
				return
			}
		}
		if c := l.peek(); c == 'e' || c == 'E' {
			k := 1
			if s := l.peekAt(1); s == '+' || s == '-' {
				k = 2
			}
			if isDecimal(l.peekAt(k)) {
				isFloat = true
				for ; k > 0; k-- {
					l.advance()
				}
				if _, ok := l.digits(10); !ok {
					l.fail(pos, "'_' must separate digits")
					return
				}
			}
		}
	}

	if l.off < len(l.src) {
		if r, _ := utf8.DecodeRuneInString(l.src[l.off:]); isIdentChar(r) {
			l.fail(l.pos(), fmt.Sprintf("invalid character %q in number", r))
			return
		}
	}

	lit := strings.ReplaceAll(l.src[start:l.off], "_", "")
	if isFloat {
		if _, err := strconv.ParseFloat(lit, 64); err != nil {
			l.fail(pos, "float literal is out of range")
			return
		}
		l.emit(token.FLOAT, lit, pos)
		return
	}
	if base == 10 && len(lit) > 1 && lit[0] == '0' {
		l.fail(pos, "leading zeros are not allowed in numbers (use 0o for octal)")
		return
	}
	if _, err := strconv.ParseInt(lit, 0, 64); err != nil {
		l.fail(pos, "integer literal is too large for a 64-bit int")
		return
	}
	l.emit(token.INT, lit, pos)
}

// digits consumes digits of the given base with optional '_' separators.
// It returns how many digits it read and whether the separators were valid.
func (l *Lexer) digits(base int) (n int, ok bool) {
	ok = true
	under := false
	for l.off < len(l.src) {
		c := l.src[l.off]
		if c == '_' {
			if n == 0 || under {
				ok = false
			}
			under = true
			l.advance()
			continue
		}
		if !isDigitIn(c, base) {
			break
		}
		under = false
		n++
		l.advance()
	}
	if under {
		ok = false
	}
	return n, ok
}

func (l *Lexer) scanString(pos token.Pos) {
	l.advance() // opening quote
	start := l.off
	end := stringEnd(l.src, start)
	if end < 0 {
		l.fail(pos, "unterminated string")
		return
	}
	raw := l.src[start:end]
	for l.off <= end {
		l.advance()
	}
	val, errOff, msg := unescape(raw)
	if msg != "" {
		l.fail(token.Pos{Line: pos.Line, Col: pos.Col + 1 + utf8.RuneCountInString(raw[:errOff])}, msg)
		return
	}
	l.emit(token.STRING, val, pos)
}

func (l *Lexer) scanFString(pos token.Pos) {
	l.advance() // opening quote
	start := l.off
	end, brace := fstringEnd(l.src, start)
	if end < 0 {
		if brace >= 0 {
			l.fail(l.posAhead(brace), "unclosed '{' in f-string")
		} else {
			l.fail(pos, "unterminated f-string")
		}
		return
	}
	raw := l.src[start:end]
	for l.off <= end {
		l.advance()
	}
	if _, err := SplitFString(raw); err != nil {
		fe := err.(*FStringError)
		l.fail(token.Pos{Line: pos.Line, Col: pos.Col + 2 + utf8.RuneCountInString(raw[:fe.Offset])}, fe.Msg)
		return
	}
	l.emit(token.FSTRING, raw, pos)
}

func (l *Lexer) scanOperator(pos token.Pos, r rune) {
	if r >= utf8.RuneSelf {
		l.fail(pos, fmt.Sprintf("unexpected character %q", r))
		return
	}
	c, n1, n2 := byte(r), l.peekAt(1), l.peekAt(2)
	t, n := token.ILLEGAL, 1
	pick := func(next byte, long, short token.Type) {
		if n1 == next {
			t, n = long, 2
		} else {
			t = short
		}
	}

	switch c {
	case '+':
		pick('=', token.PLUS_ASSIGN, token.PLUS)
	case '-':
		pick('=', token.MINUS_ASSIGN, token.MINUS)
	case '*':
		pick('=', token.STAR_ASSIGN, token.STAR)
	case '%':
		pick('=', token.PERCENT_ASSIGN, token.PERCENT)
	case '/':
		switch {
		case n1 == '/' && n2 == '=':
			t, n = token.SLASH_SLASH_ASSIGN, 3
		case n1 == '/':
			t, n = token.SLASH_SLASH, 2
		default:
			pick('=', token.SLASH_ASSIGN, token.SLASH)
		}
	case '=':
		pick('=', token.EQ, token.ASSIGN)
	case '!':
		pick('=', token.NOT_EQ, token.BANG)
	case '<':
		pick('=', token.LT_EQ, token.LT)
	case '>':
		pick('=', token.GT_EQ, token.GT)
	case ':':
		switch n1 {
		case ':':
			t, n = token.COLON_COLON, 2
		case '=':
			t, n = token.WALRUS, 2
		default:
			t = token.COLON
		}
	case '.':
		switch {
		case n1 == '.' && n2 == '.':
			t, n = token.ELLIPSIS, 3
		case n1 == '.':
			t, n = token.DOT_DOT, 2
		default:
			t = token.DOT
		}
	case ',':
		t = token.COMMA
	case '?':
		t = token.QUESTION
	case '(':
		t = token.LPAREN
		l.brackets = append(l.brackets, bracket{ch: c, pos: pos})
	case '[':
		t = token.LBRACKET
		l.brackets = append(l.brackets, bracket{ch: c, pos: pos})
	case '{':
		t = token.LBRACE
		l.brackets = append(l.brackets, bracket{ch: c, pos: pos})
	case ')', ']', '}':
		var open byte
		switch c {
		case ')':
			t, open = token.RPAREN, '('
		case ']':
			t, open = token.RBRACKET, '['
		default:
			t, open = token.RBRACE, '{'
		}
		if len(l.brackets) == 0 {
			l.fail(pos, fmt.Sprintf("unexpected '%c': nothing to close", c))
			return
		}
		top := l.brackets[len(l.brackets)-1]
		if top.ch != open {
			l.fail(pos, fmt.Sprintf("'%c' does not match '%c' opened at %s", c, top.ch, top.pos))
			return
		}
		l.brackets = l.brackets[:len(l.brackets)-1]
	case ';':
		l.fail(pos, "SEPL has no semicolons: end the statement with a newline")
		return
	case '\'':
		l.fail(pos, "strings use double quotes: \"...\"")
		return
	case '&':
		l.fail(pos, "unexpected '&': use 'and'")
		return
	case '|':
		l.fail(pos, "unexpected '|': use 'or'")
		return
	default:
		l.fail(pos, fmt.Sprintf("unexpected character %q", r))
		return
	}

	lit := l.src[l.off : l.off+n]
	for i := 0; i < n; i++ {
		l.advance()
	}
	l.emit(t, lit, pos)
}
