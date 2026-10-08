package interp

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Username59138/sepl/vm"
)

// run executes a program and returns its output, exit code and error.
func run(t *testing.T, src string, args ...string) (string, int, error) {
	t.Helper()
	var out bytes.Buffer
	s := NewSession(&out, strings.NewReader("first line\nsecond\n"))
	code, err := s.RunFile("test.sepl", src, args)
	return out.String(), code, err
}

func expect(t *testing.T, name, src, want string) {
	t.Helper()
	t.Run(name, func(t *testing.T) {
		out, _, err := run(t, src)
		if err != nil {
			if e, ok := err.(*Error); ok {
				t.Fatalf("unexpected error:\n%s", e.Format())
			}
			t.Fatalf("unexpected error: %v", err)
		}
		if strings.TrimRight(out, "\n") != strings.TrimRight(want, "\n") {
			t.Errorf("output:\n%s\nwant:\n%s", out, want)
		}
	})
}

// expectErr checks the kind, line and message of the error a program fails with.
func expectErr(t *testing.T, name, src, kind string, line int, msgPart string) {
	t.Helper()
	t.Run(name, func(t *testing.T) {
		_, _, err := run(t, src)
		e, ok := err.(*Error)
		if !ok {
			t.Fatalf("want a %s error containing %q, got %v", kind, msgPart, err)
		}
		if e.Kind != kind || e.Pos.Line != line || !strings.Contains(e.Msg, msgPart) {
			t.Errorf("got %s error at %s: %s\nwant %s error on line %d containing %q", e.Kind, e.Pos, e.Msg, kind, line, msgPart)
		}
	})
}

func TestArithmetic(t *testing.T) {
	expect(t, "ints", `
print(2 + 3, 10 - 4, 6 * 7, 7 / 2, 7 // 2, -7 // 2, 7 % 3, -7 % 3, 7 % -3)
print(1 + 2 * 3, (1 + 2) * 3, -2 * 3, 2 - -3)
let x = 5
print(x + 1, x - 1, x * 2, x % 2, 1 - x, x // 2)
`, "5 6 42 3.5 3 -4 1 2 -2\n7 9 -6 5\n6 4 10 1 -4 2")

	expect(t, "floats", `
print(1 + 2.5, 2.5 * 2, 10 / 4, 3.0, 0.1 + 0.2, 1e20, -0.5, 7.5 // 2, 7.5 % 2)
print(1 == 1.0, 2 < 2.5, 1 / 3)
`, "3.5 5.0 2.5 3.0 0.30000000000000004 1e+20 -0.5 3.0 1.5\ntrue true 0.3333333333333333")

	expect(t, "comparisons", `
print(1 < 2, 2 <= 2, 3 > 4, 4 >= 5, 1 == 1, 1 != 1, "a" < "b", "b" >= "a")
print([1, 2] == [1, 2], {"a": 1} == {"a": 1}, [1] == [2], nil == nil, nil == false)
`, "true true false false true false true true\ntrue true false true false")

	expect(t, "big numbers", `
let big = 9223372036854775807
print(big, -big - 1)
print(0xff, 0b101, 0o17, 1_000_000)
`, "9223372036854775807 -9223372036854775808\n255 5 15 1000000")
}

func TestTruthinessAndLogic(t *testing.T) {
	expect(t, "falsy values", `
for v in [false, nil, 0, 0.0, "", [], {}, 0..0]:
    if v:
        print("truthy", v)
    else:
        print("falsy")
for v in [true, 1, -1, 0.5, "a", [0], {"k": 0}, 0..1]:
    if not v:
        print("falsy", v)
print("done")
`, strings.Repeat("falsy\n", 8)+"done")

	expect(t, "and or always bool", `
print(1 and 2, 0 and 2, 0 or "", nil or 5, not 0, not "x")
let calls = []
fn f(x):
    calls.push(x)
    return x
print(f(false) and f(true), f(true) or f(false))
print(calls)
`, "true false false true true false\nfalse true\n[false, true]")

	expect(t, "and in conditions", `
let a = 3
if a > 1 and a < 5 and a != 4:
    print("yes")
if a > 1 and a > 10:
    print("no")
while a > 0 and a != 1:
    a -= 1
print(a)
`, "yes\n1")
}

func TestStrings(t *testing.T) {
	expect(t, "basics", `
let name = "мир"
print("привет, " + name + "!", len(name), name[0], name[-1])
print(f"{name} has {len(name)} letters, 2 + 3 = {2 + 3}, {{braces}}")
print("ab" in "cab", "x" not in "cab", "a" + "" == "a")
`, "привет, мир! 3 м р\nмир has 3 letters, 2 + 3 = 5, {braces}\ntrue true true")

	expect(t, "methods", `
let s = "  Hello World  "
print(s.trim().upper(), s.trim().lower(), s.trim().len())
print("a,b,c".split(","), "one two  three".split())
print(["x", "y", 1].join("-"), "abc".replace("b", "B"), "hello".find("l"), "hello".find("z"))
print("hello".starts_with("he"), "hello".ends_with("x"), "hello".contains("ell"))
print("héllo".slice(1, 3), "hello".slice(-3), "ab".repeat(3), "añb".chars())
print(5.to_str() + "!", [1, 2].to_str(), nil.to_str())
`, "HELLO WORLD hello world 11\n[\"a\", \"b\", \"c\"] [\"one\", \"two\", \"three\"]\nx-y-1 aBc 2 -1\ntrue false true\nél llo ababab [\"a\", \"ñ\", \"b\"]\n5! [1, 2] nil")

	expect(t, "conversions", `
print(str(42) + "!", int("12") + 1, int(3.9), int(-3.9), float("2.5"), float(2), int(true))
print(type(1), type(1.5), type("s"), type([]), type({}), type(nil), type(true), type(0..3), type(print))
`, "42! 13 3 -3 2.5 2.0 1\nint float str list map nil bool range fn")
}

func TestVariablesAndScopes(t *testing.T) {
	expect(t, "shadowing in blocks", `
let x = 1
if true:
    let x = 2
    print(x)
print(x)
fn f(x):
    let x = x * 10
    return x
print(f(3))
`, "2\n1\n30")

	expect(t, "assignment goes outward", `
let count = 0
fn inc():
    count = count + 1
inc()
inc()
print(count)
`, "2")

	expect(t, "const", `
const LIMIT = 3
let total = 0
for i in 0..LIMIT:
    total += i
print(total, LIMIT)
`, "3 3")

	expect(t, "globals used by earlier functions", `
fn show():
    return later * 2
let later = 21
print(show())
`, "42")
}

func TestClosures(t *testing.T) {
	expect(t, "counter", `
fn make_counter():
    let n = 0
    fn next():
        n += 1
        return n
    return next
let a = make_counter()
let b = make_counter()
print(a(), a(), a(), b())
`, "1 2 3 1")

	expect(t, "shared variable", `
fn pair():
    let v = 0
    fn get():
        return v
    fn set(x):
        v = x
    return [get, set]
let p = pair()
p[1](42)
print(p[0]())
`, "42")

	expect(t, "fresh loop variable per iteration", `
let fns = []
for i in 0..3:
    fn f():
        return i * 10
    fns.push(f)
for x in ["a", "b"]:
    fn g():
        return x
    fns.push(g)
let out = []
for f in fns:
    out.push(f())
print(out)
`, `[0, 10, 20, "a", "b"]`)

	expect(t, "three levels", `
fn outer():
    let x = 1
    fn middle():
        let y = 2
        fn inner():
            x += 10
            return x + y
        return inner
    return middle()
let f = outer()
print(f(), f())
`, "13 23")

	expect(t, "closure in a block value", `
fn make(c):
    let f = if c:
        let secret = 7
        fn get():
            return secret
        get
    else:
        nil
    return f
print(make(true)())
`, "7")

	expect(t, "recursive local function", `
fn main():
    fn fact(n):
        if n <= 1:
            return 1
        return n * fact(n - 1)
    print(fact(10))
`, "3628800")
}

func TestIfAndMatch(t *testing.T) {
	expect(t, "if expression", `
let user = ""
let name = if user: user else: "guest"
let size = if len(name) > 10:
    "long"
else if len(name) > 3:
    "medium"
else:
    "short"
print(name, size)
let z = 100 + if size == "medium": 1 else: 2
print(z)
let none = if false: 1
print(none)
`, "guest medium\n101\nnil")

	expect(t, "match", `
fn describe(code):
    return match code:
        200, 201: "ok"
        300..400: "redirect"
        404: "not found"
        -1: "negative"
        "x": "letter"
        _: "other"
for c in [200, 201, 302, 404, -1, "x", 999]:
    print(describe(c))
`, "ok\nok\nredirect\nnot found\nnegative\nletter\nother")

	expect(t, "match without a match", `
let r = match 5:
    1: "one"
print(r)
match "q":
    "a": print("a")
print("end")
`, "nil\nend")

	expect(t, "locals inside expressions", `
fn f(c):
    let a = 10
    let b = a + match c:
        1:
            let t = a * 2
            t + 1
        _: 0
    return b
print(f(1), f(2))
`, "31 10")

	expect(t, "match on a variable value", `
const LIMIT = 3
let x = 3
match x:
    LIMIT: print("limit")
    _: print("no")
`, "limit")
}

func TestLoops(t *testing.T) {
	expect(t, "while with break and continue", `
let i = 0
let out = []
while true:
    i += 1
    if i % 2 == 0:
        continue
    if i > 7:
        break
    out.push(i)
print(out)
`, "[1, 3, 5, 7]")

	expect(t, "for over everything", `
for x in [1, 2]:
    print(x)
for ch in "аб":
    print(ch)
for k in {"a": 1, "b": 2}:
    print(k)
let r = 2..4
for n in r:
    print(n)
for n in 3..3:
    print("never")
`, "1\n2\nа\nб\na\nb\n2\n3")

	expect(t, "nested loops with break and continue", `
let out = []
for i in 0..4:
    if i == 1:
        continue
    for j in 0..10:
        if j == 2:
            break
        out.push(f"{i}{j}")
print(out.join(" "))
`, "00 01 20 21 30 31")

	expect(t, "continue in for-range with locals", `
let total = 0
for i in 0..10:
    let doubled = i * 2
    if doubled % 3 == 0:
        continue
    total += doubled
print(total)
`, "54")

	expect(t, "loop variable is a copy", `
for i in 0..3:
    i = i * 100
    print(i)
`, "0\n100\n200")

	expect(t, "range bounds from variables", `
let lo = -2
let hi = 2
let out = []
for i in lo..hi:
    out.push(i)
print(out)
`, "[-2, -1, 0, 1]")
}

func TestFunctions(t *testing.T) {
	expect(t, "mutual recursion", `
fn is_even(n):
    if n == 0: return true
    return is_odd(n - 1)
fn is_odd(n):
    if n == 0: return false
    return is_even(n - 1)
print(is_even(10), is_odd(7))
`, "true true")

	expect(t, "named and variadic arguments", `
fn point(x, y):
    return f"({x}, {y})"
print(point(1, 2), point(y: 2, x: 1), point(1, y: 5))
fn total(...nums):
    let s = 0
    for n in nums:
        s += n
    return s
print(total(), total(1), total(1, 2, 3))
fn label(name, ...rest):
    return f"{name}: {rest}"
print(label("a"), label("b", 1, 2))
`, "(1, 2) (1, 2) (1, 5)\n0 1 6\na: [] b: [1, 2]")

	expect(t, "functions are values", `
fn twice(f, x):
    return f(f(x))
fn add3(x):
    return x + 3
fn adder(n):
    fn add(x):
        return x + n
    return add
print(twice(add3, 1), twice(adder(10), 1), print == print)
fn nothing():
    let x = 1
print(nothing())
`, "7 21 true\nnil")

	expect(t, "deep recursion grows the stack", `
fn sum(n):
    if n == 0:
        return 0
    return n + sum(n - 1)
print(sum(10000))
`, "50005000")
}

func TestCollections(t *testing.T) {
	expect(t, "lists", `
let l = [3, 1, 2]
l.push(5)
l.insert(0, 9)
print(l, len(l), l[0], l[-1])
print(l.pop(), l.pop(0), l)
l[0] += 10
l.sort()
print(l, l.index_of(13), l.index_of(7), 2 in l, 7 not in l)
l.reverse()
print(l, l + [0], l.slice(1), l.slice(0, -1), [[1], [nil, true]])
let c = l.copy()
c.push(1)
print(len(l), len(c), l == c)
`, "[9, 3, 1, 2, 5] 5 9 5\n5 9 [3, 1, 2]\n[1, 2, 13] 2 -1 true true\n[13, 2, 1] [13, 2, 1, 0] [2, 1] [13, 2] [[1], [nil, true]]\n3 4 false")

	expect(t, "maps", `
let m = {"a": 1, "b": 2}
m["c"] = 3
m["a"] += 10
print(m, len(m), m["a"], m.get("z"), m.get("z", 0), "b" in m)
print(m.keys(), m.values(), m.remove("b"), m)
let n = {}
n[1] = "int key"
print(n[1.0], {1.5: "x"}[1.5])
for k in m:
    print(k, m[k])
`, "{\"a\": 11, \"b\": 2, \"c\": 3} 3 11 nil 0 true\n[\"a\", \"b\", \"c\"] [11, 2, 3] 2 {\"a\": 11, \"c\": 3}\nint key x\na 11\nc 3")

	expect(t, "ranges", `
let r = 2..6
print(r, len(r), r[1], 4 in r, 6 in r, r.to_list())
`, "2..6 4 3 true false [2, 3, 4, 5]")

	expect(t, "self-referencing list prints", `
let l = [1]
l.push(l)
print(l)
`, "[1, [...]]")
}

func TestBuiltins(t *testing.T) {
	expect(t, "math", `
print(abs(-5), abs(2.5), min(3, 1, 2), max([4, 9, 2]), min("b", "a"))
`, "5 2.5 1 9 a")

	expect(t, "input", `
let a = input("> ")
let b = input()
let c = input()
print(a, b, c)
`, "> first line second nil")
}

func TestMainAndExitCodes(t *testing.T) {
	out, code, err := run(t, `
print("top level runs first")
fn main(args):
    print("args:", args)
    return 7
`, "x", "y")
	if err != nil || code != 7 || out != "top level runs first\nargs: [\"x\", \"y\"]\n" {
		t.Errorf("got code %d, err %v, out %q", code, err, out)
	}

	_, code, err = run(t, "fn main():\n    exit(3)\n    print(\"never\")\n")
	if _, ok := err.(*vm.ExitError); !ok || code != 3 {
		t.Errorf("exit: got code %d, err %v", code, err)
	}

	_, code, _ = run(t, "fn main():\n    let x = 1 // 0\n")
	if code != 1 {
		t.Errorf("runtime error should exit with 1, got %d", code)
	}

	out, code, err = run(t, "fn main():\n    print(\"no return\")\n")
	if err != nil || code != 0 || out != "no return\n" {
		t.Errorf("got code %d, err %v, out %q", code, err, out)
	}
}

func TestRuntimeErrors(t *testing.T) {
	expectErr(t, "str plus int", `let x = "5" + 1`, "runtime", 1, "cannot use '+' with str and int (convert explicitly")
	expectErr(t, "overflow", "let x = 9223372036854775807\nlet y = x + 1", "runtime", 2, "integer overflow")
	expectErr(t, "overflow in loop counter", "let x = 9223372036854775807\nx += 1", "runtime", 2, "integer overflow")
	expectErr(t, "division by zero", "print(1 / 0)", "runtime", 1, "division by zero")
	expectErr(t, "modulo by zero", "let z = 0\nprint(5 % z)", "runtime", 2, "division by zero")
	expectErr(t, "not a function", "let x = 5\nx()", "runtime", 2, "int is not a function")
	expectErr(t, "too many args", "fn f(a):\n    return a\nf(1, 2)", "runtime", 3, "f() takes 1 argument but got 2")
	expectErr(t, "missing arg", "fn f(a, b):\n    return a\nf(b: 1)", "runtime", 3, "missing argument 'a'")
	expectErr(t, "unknown named arg", "fn f(a):\n    return a\nf(z: 1)", "runtime", 3, "no parameter named 'z'")
	expectErr(t, "unknown method", "[1].shove(2)", "runtime", 1, "list has no method 'shove'")
	expectErr(t, "field on list", "let l = [1]\nprint(l.size)", "runtime", 2, "list has no field 'size'")
	expectErr(t, "index out of range", "let l = [1, 2]\nprint(l[5])", "runtime", 2, "index 5 is out of range for a list of length 2")
	expectErr(t, "missing key", `let m = {"a": 1}`+"\nprint(m[\"b\"])", "runtime", 2, `key "b" not found`)
	expectErr(t, "bad map key", "let m = {}\nm[[1]] = 2", "runtime", 2, "list cannot be a map key")
	expectErr(t, "compare mixed", `print(1 < "a")`, "runtime", 1, "cannot compare int and str")
	expectErr(t, "iterate int", "for x in 5:\n    print(x)", "runtime", 1, "cannot iterate over int")
	expectErr(t, "float range", "for x in 0..2.5:\n    print(x)", "runtime", 1, "range bounds must be int")
	expectErr(t, "str is immutable", `let s = "ab"`+"\ns[0] = \"x\"", "runtime", 2, "a str cannot be changed")
	expectErr(t, "bad int conversion", `int("abc")`, "runtime", 1, `cannot convert "abc" to int`)
	expectErr(t, "used before defined", "print(later)\nlet later = 1", "runtime", 1, "'later' is used before it is defined")
	expectErr(t, "endless recursion", "fn f(n):\n    return f(n + 1)\nf(0)", "runtime", 2, "stack overflow")
	expectErr(t, "error inside f-string", `print(f"{1 + "a"}")`, "runtime", 1, "cannot use '+'")

	_, _, err := run(t, "fn a():\n    return b()\nfn b():\n    return 1 // 0\nfn main():\n    a()\n")
	e := err.(*Error)
	if len(e.Trace) != 3 || e.Trace[0].Func != "b" || e.Trace[1].Func != "a" || e.Trace[2].Func != "main" {
		t.Errorf("trace: %+v", e.Trace)
	}
	if !strings.Contains(e.Format(), "called from:") {
		t.Errorf("formatted error has no trace:\n%s", e.Format())
	}
}

func TestCompileErrors(t *testing.T) {
	expectErr(t, "undefined name", "print(nmae)", "compile", 1, "undefined name 'nmae'")
	expectErr(t, "assign undeclared (typo)", "let count = 0\ncoutn = 1", "compile", 2, "undefined variable 'coutn' (declare it with 'let coutn = ...')")
	expectErr(t, "assign const", "const X = 1\nX = 2", "compile", 2, "cannot assign to 'X': it is a const")
	expectErr(t, "assign const +=", "fn f():\n    const k = 1\n    k += 1", "compile", 3, "it is a const")
	expectErr(t, "assign builtin", "print = 5", "compile", 1, "cannot assign to the builtin 'print'")
	expectErr(t, "duplicate let", "fn f():\n    let a = 1\n    let a = 2", "compile", 3, "'a' is already declared in this block")
	expectErr(t, "duplicate global", "let a = 1\nlet a = 2", "compile", 2, "'a' is already declared")
	expectErr(t, "duplicate param", "fn f(a, a):\n    return a", "compile", 1, "duplicate parameter 'a'")
	expectErr(t, "break outside loop", "break", "compile", 1, "'break' outside a loop")
	expectErr(t, "return outside fn", "return 1", "compile", 1, "'return' outside a function")
	expectErr(t, "const without value", "const X", "compile", 1, "needs a value")
	expectErr(t, "macro not yet", "macro m(x):\n    return x", "compile", 1, "macros are not supported yet")
	expectErr(t, "syntax", "let x = (1 +", "syntax", 1, "never closed")
}

func TestREPLSession(t *testing.T) {
	var out bytes.Buffer
	s := NewSession(&out, nil)
	steps := []struct{ src, want string }{
		{"2 + 3", "5"},
		{"let x = 5", "nil"},
		{"x * 2", "10"},
		{"fn sq(n): return n * n", "nil"},
		{"sq(x)", "25"},
		{"let x = \"again\"", "nil"},
		{"x", `"again"`},
		{"[1, \"a\"]", `[1, "a"]`},
	}
	for _, st := range steps {
		v, err := s.Eval(st.src)
		if err != nil {
			t.Fatalf("%s: %v", st.src, err)
		}
		if got := vm.Repr(v); got != st.want {
			t.Errorf("%s = %s, want %s", st.src, got, st.want)
		}
	}
	if _, err := s.Eval("1 // 0"); err == nil {
		t.Fatal("expected an error")
	}
	if v, err := s.Eval("sq(3)"); err != nil || vm.Repr(v) != "9" {
		t.Errorf("session broken after an error: %v %v", v, err)
	}
}

func TestExamples(t *testing.T) {
	out, code, err := run(t, readExample(t, "fact.sepl"))
	if err != nil || code != 0 || !strings.Contains(out, "20! = 2432902008176640000 = 2432902008176640000") {
		t.Errorf("fact.sepl: code %d, err %v, out %q", code, err, out)
	}
}

func TestTrickyStack(t *testing.T) {
	expect(t, "closures survive stack growth", `
fn deep(n, fs):
    let v = n * 2
    fn get():
        return v
    fs.push(get)
    if n == 0:
        return 0
    return deep(n - 1, fs)
let fs = []
deep(3000, fs)
print(fs[0](), fs[3000](), len(fs))
`, "6000 0 3001")

	expect(t, "break and continue with captured variables", `
let fns = []
for i in 0..10:
    let label = f"n{i}"
    fn get():
        return label
    fns.push(get)
    if i == 1:
        continue
    if i == 3:
        break
let out = []
for f in fns:
    out.push(f())
print(out)
`, `["n0", "n1", "n2", "n3"]`)

	expect(t, "continue and break inside match", `
let out = []
for x in [1, 2, 3, 4, 5]:
    match x:
        2:
            continue
        4:
            break
        _:
            out.push(x)
print(out)
`, "[1, 3]")

	expect(t, "return from inside loops", `
fn find(items, want):
    for i in 0..len(items):
        for j in 0..len(items[i]):
            if items[i][j] == want:
                return [i, j]
    return nil
print(find([[1, 2], [3, 4]], 4), find([[1]], 9))
`, "[1, 1] nil")
}
