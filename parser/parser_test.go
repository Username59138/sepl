package parser

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Username59138/sepl/ast"
	"github.com/Username59138/sepl/token"
)

func expr(t *testing.T, src, want string) {
	t.Helper()
	x, err := ParseExpr(src)
	if err != nil {
		t.Errorf("%s: unexpected error: %v", src, err)
		return
	}
	if got := ast.String(x); got != want {
		t.Errorf("%s\ngot:  %s\nwant: %s", src, got, want)
	}
}

func prog(t *testing.T, src, want string) {
	t.Helper()
	p, err := Parse(src)
	if err != nil {
		t.Errorf("source:\n%s\nunexpected error: %v", src, err)
		return
	}
	if got := ast.String(p); got != want {
		t.Errorf("source:\n%s\ngot:\n%s\nwant:\n%s", src, got, want)
	}
}

func parseErr(t *testing.T, src string, line, col int, msgPart string) {
	t.Helper()
	_, err := Parse(src)
	if err == nil {
		t.Errorf("expected an error for:\n%s", src)
		return
	}
	e := err.(ErrorList)[0]
	if e.Pos.Line != line || e.Pos.Col != col || !strings.Contains(e.Msg, msgPart) {
		t.Errorf("source:\n%s\ngot:  %s\nwant: %d:%d: ...%s...", src, e, line, col, msgPart)
	}
}

func TestPrecedence(t *testing.T) {
	tests := []struct{ src, want string }{
		{"2 + 3", "(+ 2 3)"},
		{"1 + 2 * 3", "(+ 1 (* 2 3))"},
		{"(1 + 2) * 3", "(* (+ 1 2) 3)"},
		{"a - b - c", "(- (- a b) c)"},
		{"7 // 2 % 3", "(% (// 7 2) 3)"},
		{"10 / 4 * 2", "(* (/ 10 4) 2)"},
		{"-2 * 3", "(* (- 2) 3)"},
		{"-x.y", "(- x.y)"},
		{"- -x", "(- (- x))"},
		{"not a and b or c", "(or (and (not a) b) c)"},
		{"a or b and c", "(or a (and b c))"},
		{"not a == b", "(not (== a b))"},
		{"a == b and c != d", "(and (== a b) (!= c d))"},
		{"a + 1 < b * 2", "(< (+ a 1) (* b 2))"},
		{"0..n + 1", "(.. 0 (+ n 1))"},
		{"-1..5", "(.. (- 1) 5)"},
		{"x in list", "(in x list)"},
		{"x not in 0..W", "(not in x (.. 0 W))"},
		{"a in b or c not in d", "(or (in a b) (not in c d))"},
		{"true and nil == nil", "(and true (== nil nil))"},
	}
	for _, tt := range tests {
		expr(t, tt.src, tt.want)
	}
}

func TestPostfix(t *testing.T) {
	tests := []struct{ src, want string }{
		{"f(x)(y)", "(call (call f x) y)"},
		{"a.b.c(1)[2]", "(index (call a.b.c 1) 2)"},
		{"foo::create()", "(call foo::create)"},
		{"net::http::get(url)", "(call net::http::get url)"},
		{"Foo::X + foo.X", "(+ Foo::X foo.X)"},
		{"read(path)?", "(? (call read path))"},
		{"parse(read(p)?)?.value", "(? (call parse (? (call read p)))).value"},
		{"assert!(x > 0)", "(assert! (> x 0))"},
		{"derive_plus!(point)", "(derive_plus! point)"},
		{"point(x: 1, y: 2)", "(call point x: 1 y: 2)"},
		{"print(a, b, pos: p)", "(call print a b pos: p)"},
		{"f()", "(call f)"},
		{"1.to_str()", "(call 1.to_str)"},
		{"shape::circle(5)", "(call shape::circle 5)"},
	}
	for _, tt := range tests {
		expr(t, tt.src, tt.want)
	}
}

func TestLiterals(t *testing.T) {
	tests := []struct{ src, want string }{
		{"42", "42"},
		{"0xff", "0xff"},
		{"3.14", "3.14"},
		{`"hi\n"`, `"hi\n"`},
		{"true", "true"},
		{"nil", "nil"},
		{"[1, 2, 3,]", "(list 1 2 3)"},
		{"[]", "(list)"},
		{"[[1], []]", "(list (list 1) (list))"},
		{`{"a": 1, "b": x + 1}`, `(map ("a" 1) ("b" (+ x 1)))`},
		{"{}", "(map)"},
		{"[\n  1,\n  2,\n]", "(list 1 2)"},
		{`f"a{b}c{d + 1}"`, `(f "a" b "c" (+ d 1))`},
		{`f"{ x }"`, `(f x)`},
		{`f"{{lit}} {f(1)}"`, `(f "{lit} " (call f 1))`},
		{`f"{m["k"]}"`, `(f (index m "k"))`},
	}
	for _, tt := range tests {
		expr(t, tt.src, tt.want)
	}
}

func TestIntValues(t *testing.T) {
	x, err := ParseExpr("0x10")
	if err != nil {
		t.Fatal(err)
	}
	if v := x.(*ast.IntLit).Value; v != 16 {
		t.Errorf("0x10 = %d", v)
	}
	x, _ = ParseExpr("2.5e-1")
	if v := x.(*ast.FloatLit).Value; v != 0.25 {
		t.Errorf("2.5e-1 = %g", v)
	}
}

func TestIfExpression(t *testing.T) {
	expr(t, `if a: 1 else: 2`, "(if a (do 1) (do 2))")
	expr(t, `if a: 1 else if b: 2 else: 3`, "(if a (do 1) (if b (do 2) (do 3)))")
	expr(t, `if a: 1`, "(if a (do 1))")

	prog(t, `let name = if user: user else: "guest"`,
		`(let name = (if user (do user) (do "guest")))`)

	prog(t, `let foo = if bar:
    name
else:
    "guest"
print(foo)
`, `(let foo = (if bar (do name) (do "guest")))
(call print foo)`)

	prog(t, `if a:
    x
else if b:
    y
else:
    z
`, "(if a (do x) (if b (do y) (do z)))")

	// Inline "then" with "else" on the next line.
	prog(t, "if a: x\nelse: y\n", "(if a (do x) (do y))")
}

func TestBlockEndsExpression(t *testing.T) {
	// After an indented block the next line is a new statement, even if it
	// starts with something that could continue an expression.
	prog(t, "if a:\n    x\n-y\n", "(if a (do x))\n(- y)")
	prog(t, "if a:\n    x\n[1, 2].len()\n", "(if a (do x))\n(call (list 1 2).len)")
	prog(t, "if a:\n    x\n(b)\n", "(if a (do x))\nb")
}

func TestLetConst(t *testing.T) {
	prog(t, `let a
let b int
let c = 5
let d int = 5
let e := 5
const F = 1
const g float = 2.5
`, `(let a)
(let b int)
(let c = 5)
(let d int = 5)
(let e := 5)
(const F = 1)
(const g float = 2.5)`)
}

func TestAssignments(t *testing.T) {
	prog(t, `x = 1
p.x += 2
a[0] -= 1
n //= 2
m %= 3
s *= 4
q /= 5
`, `(= x 1)
(+= p.x 2)
(-= (index a 0) 1)
(//= n 2)
(%= m 3)
(*= s 4)
(/= q 5)`)
}

func TestLoops(t *testing.T) {
	prog(t, `while count < LIMIT:
    count += 1
    if count == 5: break
    continue
for i in 0..10:
    print(i)
for x in [1, 2]: print(x)
`, `(while (< count LIMIT) (do (+= count 1) (if (== count 5) (do (break))) (continue)))
(for i (.. 0 10) (do (call print i)))
(for x (list 1 2) (do (call print x)))`)
}

func TestFunctions(t *testing.T) {
	prog(t, `fn add(a int, b int) int:
    return a + b
fn print(...items):
    return
fn main(args list) int:
    return 0
fn double(x): return x * 2
fn noop():
    pass_it()
`, `(fn add (a:int b:int) -> int (do (return (+ a b))))
(fn print (...items) (do (return)))
(fn main (args:list) -> int (do (return 0)))
(fn double (x) (do (return (* x 2))))
(fn noop () (do (call pass_it)))`)
}

func TestStructs(t *testing.T) {
	prog(t, `struct foo:
    let field_1
    let count int = 0
    let name := "none"
    const id
    const MAX = 5
struct minus
struct child is foo, bar:
    let extra
`, `(struct foo (let field_1) (let count int = 0) (let name := "none") (const id) (const MAX = 5))
(struct minus)
(struct child is foo bar (let extra))`)
}

func TestImplAndAdd(t *testing.T) {
	prog(t, `impl minus:
    fn minus(self, x)
impl foo:
    fn bar(self, x):
        print(self.field_1, x)
    fn create():
        return foo(id: 1)
add struct bar to foo
add impl minus to my_struct:
    fn minus(self, x):
        return (self.x + self.y) - x
add impl printable to int
`, `(impl minus (fn minus (self x)))
(impl foo (fn bar (self x) (do (call print self.field_1 x))) (fn create () (do (return (call foo id: 1)))))
(add struct bar to foo)
(add impl minus to my_struct (fn minus (self x) (do (return (- (+ self.x self.y) x)))))
(add impl printable to int)`)
}

func TestAddIsContextual(t *testing.T) {
	prog(t, "math::add(x, y)\nlet add = 1\nadd = add + 1\nlet to = 2\n",
		"(call math::add x y)\n(let add = 1)\n(= add (+ add 1))\n(let to = 2)")
}

func TestEnums(t *testing.T) {
	prog(t, `enum shape:
    circle(r)
    rect(w int, h int)
    empty
enum net_err is io_err:
    timeout
    refused(port)
`, `(enum shape circle(r) rect(w:int h:int) empty)
(enum net_err is io_err timeout refused(port))`)
}

func TestImports(t *testing.T) {
	prog(t, "import math\nimport net::http as h\n", "(import math)\n(import net::http as h)")
}

func TestMacros(t *testing.T) {
	prog(t, `macro twice(x):
    return x
derive_plus!(point)
`, `(macro twice (x) (do (return x)))
(derive_plus! point)`)
}

func TestMatch(t *testing.T) {
	prog(t, `match s:
    shape::circle(r): area(r)
    shape::rect(w, _):
        print(w)
    result::ok(shape::circle(r)): r
    shape::empty: 0
    1, 2: "small"
    3..10: "mid"
    -1: "negative"
    "up", nil: "other"
    LIMIT: "limit"
    _: "anything"
`, `(match s (case shape::circle(r) (do (call area r))) (case shape::rect(w _) (do (call print w))) `+
		`(case result::ok(shape::circle(r)) (do r)) (case shape::empty (do 0)) (case 1 2 (do "small")) `+
		`(case 3..10 (do "mid")) (case (- 1) (do "negative")) (case "up" nil (do "other")) `+
		`(case LIMIT (do "limit")) (case _ (do "anything")))`)

	prog(t, `let label = match code:
    200, 201: "ok"
    _: "other"
print(label)
`, `(let label = (match code (case 200 201 (do "ok")) (case _ (do "other"))))
(call print label)`)
}

func TestTryAndResult(t *testing.T) {
	prog(t, `fn load(path) result:
    let text = read_file(path)?
    return result::ok(parse(text)?)
`, `(fn load (path) -> result (do (let text = (? (call read_file path))) (return (call result::ok (? (call parse text))))))`)
}

func TestPositions(t *testing.T) {
	p, err := Parse("let x = a + b\nprint(f\"ab {y}\")\n")
	if err != nil {
		t.Fatal(err)
	}
	let := p.Stmts[0].(*ast.LetStmt)
	bin := let.Value.(*ast.Binary)
	if bin.At != (token.Pos{Line: 1, Col: 11}) || bin.Y.Pos() != (token.Pos{Line: 1, Col: 13}) {
		t.Errorf("binary at %s, right operand at %s", bin.At, bin.Y.Pos())
	}
	call := p.Stmts[1].(*ast.ExprStmt).X.(*ast.Call)
	fs := call.Args[0].Value.(*ast.FString)
	if got := fs.Parts[1].X.Pos(); got != (token.Pos{Line: 2, Col: 13}) {
		t.Errorf("f-string expression at %s, want 2:13", got)
	}
}

func TestErrors(t *testing.T) {
	tests := []struct {
		src       string
		line, col int
		msg       string
	}{
		{"if a\n    b\n", 1, 5, "expected ':' after the if condition"},
		{"fn f(x)\n    return x\n", 1, 8, "expected ':' after the function signature"},
		{"if a:\nb\n", 2, 1, "expected an indented block"},
		{"    x\n", 1, 5, "unexpected indentation"},
		{"else:\n    x\n", 1, 1, "'else' without a matching 'if'"},
		{"let x int := 5\n", 1, 11, "':=' infers the type"},
		{"x := 5\n", 1, 3, "write 'let x := ...'"},
		{"const X := 1\n", 1, 9, "a const never changes"},
		{"a < b < c\n", 1, 7, "comparisons cannot be chained"},
		{"1 = 2\n", 1, 3, "cannot assign"},
		{"f() = 2\n", 1, 5, "cannot assign"},
		{"f(x: 1, 2)\n", 1, 9, "positional arguments must come before named"},
		{"fn f(...a, b):\n    1\n", 1, 6, "'...' parameter must be the last"},
		{"enum e:\n    a(...x)\n", 2, 7, "only allowed in function parameters"},
		{"struct p:\n    fn m(self):\n        1\n", 2, 5, "methods go into 'impl p:'"},
		{"struct p:\n    x\n", 2, 5, "expected a field"},
		{"impl p:\n    let x\n", 2, 5, "only methods ('fn')"},
		{"add struct a to b:\n    fn m():\n        1\n", 1, 18, "'add struct' has no body"},
		{"add impl a b\n", 1, 12, "expected 'to'"},
		{"enum e: a\n", 1, 9, "must start on a new indented line"},
		{"let y = 1 +\nstruct p\n", 1, 11, "incomplete expression: the line ends with '+'"},
		{"let y = a and\n)\n", 2, 1, "nothing to close"},
		{"match x:\n    f\"a\": 1\n", 2, 5, "f-strings cannot be used as patterns"},
		{"match x:\n    a + b: 1\n", 2, 7, "expected ':' after the match pattern"},
		{"match x:\n    (1): 1\n", 2, 5, "expected a pattern"},
		{"x!\n", 1, 2, "unexpected '!'"},
		{"f!(a: 1)\n", 1, 7, "macro arguments cannot be named"},
		{"print(f\"{x + }\")\n", 1, 14, "the end of the f-string expression"},
		{"print(f\"{x y}\")\n", 1, 12, "in an f-string expression"},
		{"let x = 'a'\n", 1, 9, "double quotes"},
		{"for x y:\n    1\n", 1, 7, "expected 'in'"},
		{"let = 1\n", 1, 5, "expected a variable name"},
		{"while x: fn f(): 1\n", 1, 10, "'fn' must start on its own line"},
		{"x = 1 2\n", 1, 7, "expected end of line, found number 2"},
	}
	for _, tt := range tests {
		parseErr(t, tt.src, tt.line, tt.col, tt.msg)
	}
}

func TestErrorRecovery(t *testing.T) {
	src := `let = 1
let ok = 2
fn f(1):
    return 1
if a
    b
fine()
print(1
`
	p, err := Parse(src)
	if err == nil {
		t.Fatal("expected errors")
	}
	var lines []int
	for _, e := range err.(ErrorList) {
		lines = append(lines, e.Pos.Line)
	}
	want := []int{1, 3, 5, 8}
	if len(lines) != len(want) {
		t.Fatalf("errors on lines %v, want %v\n%v", lines, want, err)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Fatalf("errors on lines %v, want %v\n%v", lines, want, err)
		}
	}
	// The good statements around the broken ones are still in the tree.
	got := ast.String(p)
	if !strings.Contains(got, "(let ok = 2)") || !strings.Contains(got, "(call fine)") {
		t.Errorf("partial tree lost good statements:\n%s", got)
	}
}

func TestErrorInsideBlockDoesNotLeak(t *testing.T) {
	src := `fn f():
    let = 1
    return 2
fn g():
    return 3
`
	p, err := Parse(src)
	if err == nil || len(err.(ErrorList)) != 1 {
		t.Fatalf("want exactly one error, got %v", err)
	}
	if got := ast.String(p); got != "(fn f () (do (return 2)))\n(fn g () (do (return 3)))" {
		t.Errorf("got:\n%s", got)
	}
}

func TestErrorLimit(t *testing.T) {
	src := strings.Repeat("let = 1\n", 30)
	_, err := Parse(src)
	if n := len(err.(ErrorList)); n != maxErrors {
		t.Errorf("got %d errors, want %d", n, maxErrors)
	}
}

func TestExamples(t *testing.T) {
	files, _ := filepath.Glob("../examples/*.sepl")
	if len(files) == 0 {
		t.Skip("no examples")
	}
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Parse(string(src)); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
}

func BenchmarkParser(b *testing.B) {
	src, err := os.ReadFile("../examples/snake.sepl")
	if err != nil {
		b.Skip(err)
	}
	big := strings.Repeat(string(src)+"\n", 200)
	b.SetBytes(int64(len(big)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Parse(big); err != nil {
			b.Fatal(err)
		}
	}
}

func TestFnLiterals(t *testing.T) {
	expr(t, "fn(x): x * 2", "(fn (x) (do (return (* x 2))))")
	expr(t, "fn(a int, ...r) str: a", "(fn (a:int ...r) -> str (do (return a)))")
	expr(t, "fn(a): fn(b): a + b", "(fn (a) (do (return (fn (b) (do (return (+ a b)))))))")
	expr(t, "f(fn(x): x, 2)", "(call f (fn (x) (do (return x))) 2)")
	parseErr(t, "let f = fn x: x", 1, 12, "expected '(' after 'fn'")
}

func TestAnnotationExpressions(t *testing.T) {
	prog(t, "let y type(x) = 1", "(let y (call type x) = 1)")
	prog(t, "fn f(a, b type(a)) type(a):\n    return b", "(fn f (a b:(call type a)) -> (call type a) (do (return b)))")
}

func TestAddTo(t *testing.T) {
	prog(t, "add to type(p):\n    let id = 0\n    fn f(self): return 1",
		"(add to (call type p) (let id = 0) (fn f (self) (do (return 1))))")
	parseErr(t, "add to p:\n    x = 1", 2, 5, "expected a field ('let' or 'const') or a method ('fn') in 'add to'")
}
