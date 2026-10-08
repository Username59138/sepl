package lexer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Username59138/sepl/token"
)

// kinds lexes src and returns the token types joined by spaces.
func kinds(t *testing.T, src string) string {
	t.Helper()
	toks, err := Tokenize(src)
	if err != nil {
		t.Fatalf("unexpected error: %v\nsource:\n%s", err, src)
	}
	s := make([]string, len(toks))
	for i, tok := range toks {
		s[i] = tok.Type.String()
	}
	return strings.Join(s, " ")
}

func check(t *testing.T, src, want string) {
	t.Helper()
	if got := kinds(t, src); got != want {
		t.Errorf("source:\n%s\ngot:  %s\nwant: %s", src, got, want)
	}
}

func checkErr(t *testing.T, src string, line, col int, msgPart string) {
	t.Helper()
	_, err := Tokenize(src)
	if err == nil {
		t.Fatalf("expected an error for %q", src)
	}
	e := err.(*Error)
	if e.Pos.Line != line || e.Pos.Col != col || !strings.Contains(e.Msg, msgPart) {
		t.Errorf("source %q:\ngot:  %s\nwant: %d:%d: ...%s...", src, e, line, col, msgPart)
	}
}

func TestArithmetic(t *testing.T) {
	check(t, "2 + 3", "INT + INT NEWLINE EOF")
}

func TestEmpty(t *testing.T) {
	check(t, "", "EOF")
	check(t, "\n\n# only a comment\n   \n", "EOF")
}

func TestFunctionBlocks(t *testing.T) {
	src := `fn fact(n):
    if n <= 1:
        return 1
    return n * fact(n - 1)

print(fact(10))
`
	check(t, src, "fn IDENT ( IDENT ) : NEWLINE "+
		"INDENT if IDENT <= INT : NEWLINE "+
		"INDENT return INT NEWLINE "+
		"DEDENT return IDENT * IDENT ( IDENT - INT ) NEWLINE "+
		"DEDENT IDENT ( IDENT ( INT ) ) NEWLINE EOF")
}

func TestNoTrailingNewline(t *testing.T) {
	check(t, "fn f():\n    return 1", "fn IDENT ( ) : NEWLINE INDENT return INT NEWLINE DEDENT EOF")
}

func TestMultipleDedents(t *testing.T) {
	src := "if a:\n    if b:\n        x\ny\n"
	check(t, src, "if IDENT : NEWLINE INDENT if IDENT : NEWLINE INDENT IDENT NEWLINE DEDENT DEDENT IDENT NEWLINE EOF")
}

func TestElse(t *testing.T) {
	src := "if a:\n    x\nelse:\n    y\n"
	check(t, src, "if IDENT : NEWLINE INDENT IDENT NEWLINE DEDENT else : NEWLINE INDENT IDENT NEWLINE DEDENT EOF")
}

func TestBlankLinesAndComments(t *testing.T) {
	src := `# header

let x = 1   # trailing comment

        # an indented comment does not open a block

let y = 2
`
	check(t, src, "let IDENT = INT NEWLINE let IDENT = INT NEWLINE EOF")
}

func TestContinuationAfterOperator(t *testing.T) {
	src := "let sum = a +\n        b +\n        c\nprint(sum)\n"
	check(t, src, "let IDENT = IDENT + IDENT + IDENT NEWLINE IDENT ( IDENT ) NEWLINE EOF")
}

func TestContinuationInsideBlock(t *testing.T) {
	src := "fn f():\n    let s = a +\n            b\n    return s\n"
	check(t, src, "fn IDENT ( ) : NEWLINE INDENT let IDENT = IDENT + IDENT NEWLINE return IDENT NEWLINE DEDENT EOF")
}

func TestContinuationBeforeBlock(t *testing.T) {
	src := "if a and\n   b:\n    go()\n"
	check(t, src, "if IDENT and IDENT : NEWLINE INDENT IDENT ( ) NEWLINE DEDENT EOF")
}

func TestNewlinesInsideBrackets(t *testing.T) {
	src := `print(
  x,
  y
)
let m = {
    "a": 1,
}
`
	check(t, src, "IDENT ( IDENT , IDENT ) NEWLINE let IDENT = { STRING : INT , } NEWLINE EOF")
}

func TestInlineIfExpression(t *testing.T) {
	check(t, `let name = if user: user else: "guest"`,
		"let IDENT = if IDENT : IDENT else : STRING NEWLINE EOF")
}

func TestMatch(t *testing.T) {
	src := `let label = match code:
    200, 201: "ok"
    400..500: "client error"
    _: "other"
`
	check(t, src, "let IDENT = match IDENT : NEWLINE INDENT "+
		"INT , INT : STRING NEWLINE "+
		"INT .. INT : STRING NEWLINE "+
		"IDENT : STRING NEWLINE DEDENT EOF")
}

func TestStructsAndTraits(t *testing.T) {
	src := `struct minus
impl minus:
    fn minus(self, x)
add impl minus to my_struct:
    fn minus(self, x):
        return x
`
	check(t, src, "struct IDENT NEWLINE "+
		"impl IDENT : NEWLINE INDENT fn IDENT ( IDENT , IDENT ) NEWLINE DEDENT "+
		"IDENT impl IDENT IDENT IDENT : NEWLINE "+
		"INDENT fn IDENT ( IDENT , IDENT ) : NEWLINE INDENT return IDENT NEWLINE DEDENT DEDENT EOF")
}

func TestContextualWords(t *testing.T) {
	// add, to, is, quote and self are plain identifiers.
	check(t, "math::add(x, y)", "IDENT :: IDENT ( IDENT , IDENT ) NEWLINE EOF")
	check(t, "struct net_err is io_err:", "struct IDENT IDENT IDENT : NEWLINE EOF")
}

func TestOperators(t *testing.T) {
	ops := "+ - * / // % = := += -= *= /= //= %= == != < <= > >= ! ? . .. ... , : ::"
	toks, err := Tokenize(ops)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Fields(ops)
	for i, w := range want {
		tok := toks[i]
		if tok.Type.String() != w || tok.Literal != w {
			t.Errorf("token %d: got %s %q, want %s", i, tok.Type, tok.Literal, w)
		}
	}
	// "::" does not end a statement, so no NEWLINE before EOF.
	if got := toks[len(want)].Type; got != token.EOF {
		t.Errorf("after operators: got %s, want EOF", got)
	}
}

func TestKeywords(t *testing.T) {
	for _, kw := range []string{"let", "const", "fn", "return", "if", "else", "while", "for", "in",
		"break", "continue", "match", "struct", "impl", "enum", "import", "as", "and", "or", "not",
		"true", "false", "nil", "macro"} {
		toks, err := Tokenize(kw)
		if err != nil {
			t.Fatal(err)
		}
		if !toks[0].Type.IsKeyword() || toks[0].Type.String() != kw {
			t.Errorf("%q lexed as %s", kw, toks[0].Type)
		}
	}
}

func TestNumbers(t *testing.T) {
	tests := []struct {
		src  string
		typ  token.Type
		want string
	}{
		{"0", token.INT, "0"},
		{"42", token.INT, "42"},
		{"1_000_000", token.INT, "1000000"},
		{"0xff", token.INT, "0xff"},
		{"0b1010", token.INT, "0b1010"},
		{"0o17", token.INT, "0o17"},
		{"3.14", token.FLOAT, "3.14"},
		{"1e9", token.FLOAT, "1e9"},
		{"2.5e-3", token.FLOAT, "2.5e-3"},
		{"9223372036854775807", token.INT, "9223372036854775807"},
	}
	for _, tt := range tests {
		toks, err := Tokenize(tt.src)
		if err != nil {
			t.Errorf("%s: %v", tt.src, err)
			continue
		}
		if toks[0].Type != tt.typ || toks[0].Literal != tt.want {
			t.Errorf("%s: got %s %q, want %s %q", tt.src, toks[0].Type, toks[0].Literal, tt.typ, tt.want)
		}
	}
	check(t, "0..10", "INT .. INT NEWLINE EOF")
	check(t, "1.to_str()", "INT . IDENT ( ) NEWLINE EOF")
}

func TestNumberErrors(t *testing.T) {
	checkErr(t, "123abc", 1, 4, "invalid character 'a' in number")
	checkErr(t, "007", 1, 1, "leading zeros")
	checkErr(t, "1__0", 1, 1, "'_' must separate digits")
	checkErr(t, "1_", 1, 1, "'_' must separate digits")
	checkErr(t, "0x", 1, 1, "missing digits after 0x")
	checkErr(t, "0b102", 1, 5, "invalid character '2' in number")
	checkErr(t, "1e", 1, 2, "invalid character 'e' in number")
	checkErr(t, "99999999999999999999", 1, 1, "too large")
}

func TestStrings(t *testing.T) {
	toks, err := Tokenize(`"hello\n\t\"q\" \\ \u{1F600} {x} привет"`)
	if err != nil {
		t.Fatal(err)
	}
	want := "hello\n\t\"q\" \\ 😀 {x} привет"
	if toks[0].Type != token.STRING || toks[0].Literal != want {
		t.Errorf("got %s %q, want %q", toks[0].Type, toks[0].Literal, want)
	}
}

func TestStringErrors(t *testing.T) {
	checkErr(t, `"abc`, 1, 1, "unterminated string")
	checkErr(t, "\"abc\ndef\"", 1, 1, "unterminated string")
	checkErr(t, `x = "a\qb"`, 1, 7, `unknown escape sequence '\q'`)
	checkErr(t, `"\u{110000}"`, 1, 2, "invalid unicode escape")
	checkErr(t, `"\u1234"`, 1, 2, "invalid unicode escape")
}

func TestFString(t *testing.T) {
	src := `print(f"hello, {name}! 2 + 3 = {2 + 3} {{x}}")`
	check(t, src, "IDENT ( FSTRING ) NEWLINE EOF")

	toks, _ := Tokenize(src)
	raw := toks[2].Literal
	if raw != `hello, {name}! 2 + 3 = {2 + 3} {{x}}` {
		t.Fatalf("raw literal: %q", raw)
	}
	parts, err := SplitFString(raw)
	if err != nil {
		t.Fatal(err)
	}
	want := []FPart{
		{Text: "hello, ", Offset: 0},
		{IsExpr: true, Expr: "name", Offset: 8},
		{Text: "! 2 + 3 = ", Offset: 13},
		{IsExpr: true, Expr: "2 + 3", Offset: 24},
		{Text: " {x}", Offset: 30},
	}
	if len(parts) != len(want) {
		t.Fatalf("got %d parts %+v, want %d", len(parts), parts, len(want))
	}
	for i := range want {
		if parts[i] != want[i] {
			t.Errorf("part %d: got %+v, want %+v", i, parts[i], want[i])
		}
	}
}

func TestFStringNestedStrings(t *testing.T) {
	toks, err := Tokenize(`f"{d["key"]} and {"}"} \u{41}"`)
	if err != nil {
		t.Fatal(err)
	}
	parts, err := SplitFString(toks[0].Literal)
	if err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for _, p := range parts {
		if p.IsExpr {
			got = append(got, "{"+p.Expr+"}")
		} else {
			got = append(got, p.Text)
		}
	}
	if want := `{d["key"]}| and |{"}"}| A`; strings.Join(got, "|") != want {
		t.Errorf("got %q, want %q", strings.Join(got, "|"), want)
	}
}

func TestFStringPrefixOnlyWhenAttached(t *testing.T) {
	check(t, "f(x)", "IDENT ( IDENT ) NEWLINE EOF")
	check(t, `f "x"`, "IDENT STRING NEWLINE EOF")
}

func TestFStringErrors(t *testing.T) {
	checkErr(t, `f"a } b"`, 1, 5, "single '}'")
	checkErr(t, `f"{}"`, 1, 3, "empty expression")
	checkErr(t, `f"{x"`, 1, 3, "unclosed '{'")
	checkErr(t, `f"abc`, 1, 1, "unterminated f-string")
	checkErr(t, `f"a\qb"`, 1, 4, "unknown escape")
}

func TestIndentationErrors(t *testing.T) {
	checkErr(t, "if a:\n\tb\n", 2, 1, "tab in indentation")
	checkErr(t, "if a:\n    b\n  c\n", 3, 3, "does not match any outer block")
	checkErr(t, "x = a +\n\tb\n", 2, 1, "tab in indentation")
}

func TestWhitespaceOnlyLinesWithTabs(t *testing.T) {
	check(t, "a\n\t\n  \t  \nb\n", "IDENT NEWLINE IDENT NEWLINE EOF")
}

func TestCRLF(t *testing.T) {
	check(t, "if a:\r\n    b\r\n", "if IDENT : NEWLINE INDENT IDENT NEWLINE DEDENT EOF")
}

func TestBracketErrors(t *testing.T) {
	checkErr(t, "(a]", 1, 3, "']' does not match '(' opened at 1:1")
	checkErr(t, "a)", 1, 2, "nothing to close")
	checkErr(t, "print(1,\n2\n", 1, 6, "'(' is never closed")
}

func TestFriendlyHints(t *testing.T) {
	checkErr(t, "a; b", 1, 2, "no semicolons")
	checkErr(t, "'x'", 1, 1, "double quotes")
	checkErr(t, "a && b", 1, 3, "use 'and'")
	checkErr(t, "a || b", 1, 3, "use 'or'")
	checkErr(t, "a @ b", 1, 3, "unexpected character '@'")
}

func TestUnicodePositions(t *testing.T) {
	toks, err := Tokenize(`let имя = "привет" + x`)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		typ token.Type
		col int
	}{{token.LET, 1}, {token.IDENT, 5}, {token.ASSIGN, 9}, {token.STRING, 11}, {token.PLUS, 20}, {token.IDENT, 22}}
	for i, w := range want {
		if toks[i].Type != w.typ || toks[i].Pos.Col != w.col {
			t.Errorf("token %d: got %s at %s, want %s at col %d", i, toks[i].Type, toks[i].Pos, w.typ, w.col)
		}
	}
}

func TestNextAfterError(t *testing.T) {
	l := New("a ; b")
	if tok := l.Next(); tok.Type != token.IDENT {
		t.Fatalf("got %s", tok)
	}
	if tok := l.Next(); tok.Type != token.ILLEGAL {
		t.Fatalf("got %s", tok)
	}
	for i := 0; i < 3; i++ {
		if tok := l.Next(); tok.Type != token.EOF {
			t.Fatalf("after error got %s", tok)
		}
	}
}

func TestExamples(t *testing.T) {
	files, _ := filepath.Glob("../examples/*.sepl")
	if len(files) == 0 {
		t.Skip("no examples found")
	}
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		toks, err := Tokenize(string(src))
		if err != nil {
			t.Errorf("%s: %v", f, err)
			continue
		}
		depth := 0
		for _, tok := range toks {
			switch tok.Type {
			case token.INDENT:
				depth++
			case token.DEDENT:
				depth--
			}
			if depth < 0 {
				t.Errorf("%s: unbalanced DEDENT at %s", f, tok.Pos)
			}
		}
		if depth != 0 {
			t.Errorf("%s: %d INDENTs never closed", f, depth)
		}
	}
}

func BenchmarkLexer(b *testing.B) {
	src, err := os.ReadFile("../examples/snake.sepl")
	if err != nil {
		b.Skip(err)
	}
	big := strings.Repeat(string(src)+"\n", 200)
	b.SetBytes(int64(len(big)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l := New(big)
		for l.Next().Type != token.EOF {
		}
	}
}
