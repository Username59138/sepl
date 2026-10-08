package interp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTypeAnnotations(t *testing.T) {
	expect(t, "typed and inferred variables", `
let a int = 5
let b := "text"
let c float = 2
let d int = nil
a = 7
b = "more"
c = 3
d = 4
print(a, b, c, d, type(c))
fn f():
    let x := [1]
    x = [2, 3]
    let y str
    y = "set"
    return f"{x} {y}"
print(f())
`, "7 more 3.0 4 float\n[2, 3] set")

	expect(t, "untyped variables stay free", `
let x = 5
x = "now a string"
let x2 int = 1
if true:
    let x2 = "shadow without a type"
    x2 = [1]
    print(x2)
print(x)
`, "[1]\nnow a string")

	expect(t, "params, results, variadic", `
fn add(a int, b int) int:
    return a + b
fn area(r float) float:
    return 3 * r * r
fn total(...nums int) int:
    let s = 0
    for n in nums:
        s += n
    return s
fn maybe(flag bool) int:
    if flag:
        return 1
print(add(2, 3), area(1), total(1, 2, 3), maybe(false))
`, "5 3.0 6 nil")

	expect(t, "structs, enums, inheritance and traits", `
struct animal:
    let name str
struct dog is animal:
    let good := true
struct box
add impl to_str to box:
    fn to_str(self):
        return "box"
enum io_err:
    denied
enum net_err is io_err:
    timeout
fn greet(a animal) str:
    return "hi " + a.name
fn show(x to_str) str:
    return str(x)
fn retry(e net_err) str:
    return f"retry after {e}"
print(greet(dog(name: "Rex")), show(box()), retry(io_err::denied))
let n := 1
n += 2
print(n)
`, "hi Rex box retry after io_err::denied\n3")

	expect(t, "typed fields and variant fields", `
struct point:
    let x int = 0
    let y float = 0
    let tags := []
    const MAX int = 10
enum shape:
    circle(r float)
    rect(w int, h int)
let p = point(x: 1, y: 2)
p.x = 5
print(p, point::MAX, shape::circle(2), shape::rect(1, 2))
`, "point(x: 5, y: 2.0, tags: []) 10 shape::circle(2.0) shape::rect(1, 2)")

	expect(t, ":= fields from any default", `
struct point:
    let x := 0
    let y := 0
fn origin():
    return point()
struct segment:
    let start := origin()
    let end := point(x: 1, y: 1)
    let label := f"seg {1 + 1}"
let s = segment()
s.end = point(x: 5, y: 5)
print(s, segment(start: point(x: 2)).start)
`, `segment(start: point(x: 0, y: 0), end: point(x: 5, y: 5), label: "seg 2") point(x: 2, y: 0)`)

	expect(t, "closures assign to typed outer variables", `
fn counter():
    let n int = 0
    fn inc():
        n += 1
        return n
    return inc
let c = counter()
c()
print(c())
`, "2")
}

func TestTypeErrors(t *testing.T) {
	expectErr(t, "assign wrong type", "let a int = 5\na = \"x\"", "runtime", 2, "a must be int, not str")
	expectErr(t, "declare wrong type", "let a int = \"x\"", "runtime", 1, "a must be int, not str")
	expectErr(t, "inferred type is fixed", "let b := \"s\"\nb = 1", "runtime", 2, "b must be str, not int")
	expectErr(t, "compound assignment", "fn f():\n    let n int = 1\n    n += 0.5\nf()", "runtime", 3, "n must be int, not float")
	expectErr(t, "argument", "fn f(a int):\n    return a\nf(\"x\")", "runtime", 3, "argument 'a' of f() must be int, not str")
	expectErr(t, "variadic argument", "fn f(...n int):\n    return n\nf(1, \"x\")", "runtime", 3, "argument 'n' of f() must be int, not str")
	expectErr(t, "result", "fn f() int:\n    return \"x\"\nf()", "runtime", 2, "the result of f() must be int, not str")
	expectErr(t, "result through ?", "fn g():\n    return result::err(\"e\")\nfn f() int:\n    let v = g()?\n    return v\nf()", "runtime", 4,
		"the result of f() must be int, not result::err")
	expectErr(t, "parent struct where child is expected", "struct a\nstruct b is a\nfn f(x b):\n    return x\nf(a())", "runtime", 5,
		"argument 'x' of f() must be b, not a")
	expectErr(t, "child enum value where parent is expected", "enum p:\n    x\nenum c is p:\n    y\nfn f(v p):\n    return v\nf(c::y)", "runtime", 7,
		"argument 'v' of f() must be p, not c::y")
	expectErr(t, "trait not taken", "struct box\nimpl box:\n    fn to_str(self): return \"b\"\nfn f(x to_str):\n    return x\nf(box())", "runtime", 6,
		"argument 'x' of f() must be to_str, not box")
	expectErr(t, "field type", "struct p:\n    let x int\np(x: \"no\")", "runtime", 3, "field 'x' of p must be int, not str")
	expectErr(t, "field assignment", "struct p:\n    let x int\nlet a = p()\na.x = 1.5", "runtime", 4, "field 'x' of p must be int, not float")
	expectErr(t, "field default", "struct p:\n    let x int = \"s\"", "runtime", 1, "the default of field 'x' of p must be int, not str")
	expectErr(t, "variant field", "enum s:\n    c(r int)\ns::c(\"x\")", "runtime", 3, "field 'r' of s::c must be int, not str")
	expectErr(t, "not a type", "fn f(x print):\n    return x\nf(1)", "runtime", 3, "the type of argument 'x' of f() is not a type but fn")
	expectErr(t, "unknown type", "let x pointt = 1", "compile", 1, "undefined name 'pointt'")
	expectErr(t, ":= from nil", "let x := nil", "runtime", 1, "':=' cannot fix the type of x from nil")
	expectErr(t, ":= field from a computed default", "fn origin():\n    return [0, 0]\nstruct p:\n    let at := origin()\np(at: \"here\")", "runtime", 5,
		"field 'at' of p must be list, not str")
	expectErr(t, ":= field from nil", "fn nothing():\n    return nil\nstruct p:\n    let x := nothing()\np()", "runtime", 5, "':=' cannot fix the type of field 'x' of p from nil")
}

// writeFiles creates files in a temporary directory and returns its path.
func writeFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, src := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func runIn(t *testing.T, dir, mainFile string) (string, error) {
	t.Helper()
	var out strings.Builder
	s := NewSession(&out, strings.NewReader(""))
	path := filepath.Join(dir, mainFile)
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.RunFile(path, string(src), nil)
	return out.String(), err
}

func TestFileModules(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"main.sepl": `
import geo
import geo as g2
import util::text as text
import math

fn main():
    let p = geo::point(x: 3, y: 4)
    print(geo::dist(p), geo::ORIGIN, p, g2::point == geo::point)
    print(text::shout("hi"), math::sqrt(9.0), geo::loaded)
    match geo::classify(p):
        geo::kind::far: print("far")
        _: print("near")
`,
		"geo.sepl": `
import math
print("loading geo")
let loaded = 1
const ORIGIN = "0,0"
struct point:
    let x int
    let y int
enum kind:
    near
    far
fn dist(p point) float:
    return math::sqrt(float(_square(p.x) + _square(p.y)))
fn classify(p point) kind:
    return if dist(p) > 2: kind::far else: kind::near
fn _square(n):
    return n * n
fn main():
    print("never runs")
`,
		"util/text.sepl": `
fn shout(s str) str:
    return s.upper() + "!"
`,
	})
	out, err := runIn(t, dir, "main.sepl")
	if err != nil {
		t.Fatalf("%v\n%s", err, formatErr(err))
	}
	want := "loading geo\n5.0 0,0 point(x: 3, y: 4) true\nHI! 3.0 1\nfar\n"
	if out != want {
		t.Errorf("output:\n%s\nwant:\n%s", out, want)
	}
}

func formatErr(err error) string {
	if e, ok := err.(*Error); ok {
		return e.Format()
	}
	return ""
}

func TestModuleErrors(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		kind  string
		file  string
		line  int
		msg   string
	}{
		{"private name", map[string]string{"main.sepl": "import m\nm::_secret()", "m.sepl": "fn _secret(): return 1"},
			"runtime", "main.sepl", 2, "'_secret' is private to module m"},
		{"missing name", map[string]string{"main.sepl": "import m\nm::nope", "m.sepl": "let x = 1"},
			"runtime", "main.sepl", 2, "module m has no member 'nope'"},
		{"cycle", map[string]string{"main.sepl": "import a", "a.sepl": "import b", "b.sepl": "import a"},
			"runtime", "b.sepl", 1, "circular import: a.sepl imports b.sepl imports a.sepl"},
		{"cycle back to main", map[string]string{"main.sepl": "import a", "a.sepl": "import main"},
			"runtime", "a.sepl", 1, "circular import"},
		{"syntax error in module", map[string]string{"main.sepl": "import m", "m.sepl": "let x = (1 +"},
			"syntax", "m.sepl", 1, "never closed"},
		{"runtime error in module", map[string]string{"main.sepl": "import m", "m.sepl": "let x = 1 // 0"},
			"runtime", "m.sepl", 1, "division by zero"},
		{"not found", map[string]string{"main.sepl": "import nothere"},
			"runtime", "main.sepl", 1, "module 'nothere' not found"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := writeFiles(t, c.files)
			_, err := runIn(t, dir, "main.sepl")
			e, ok := err.(*Error)
			if !ok {
				t.Fatalf("want an error, got %v", err)
			}
			if e.Kind != c.kind || filepath.Base(e.File) != c.file || e.Pos.Line != c.line || !strings.Contains(e.Msg, c.msg) {
				t.Errorf("got %s error in %s at %s: %s\nwant %s error in %s line %d containing %q",
					e.Kind, filepath.Base(e.File), e.Pos, e.Msg, c.kind, c.file, c.line, c.msg)
			}
		})
	}
}
