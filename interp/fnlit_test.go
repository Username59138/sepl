package interp

import "testing"

func TestAnonymousFunctions(t *testing.T) {
	expect(t, "inline body returns its expression", `
let double = fn(x): x * 2
print(double(3), type(double))
print([1, 2, 3].map(fn(x): x * x), [1, 2, 3, 4].filter(fn(x): x % 2 == 0))
print([1, 2, 3].reduce(fn(a, b): a + b, 0))
let words = ["bbb", "a", "cc"]
words.sort(fn(s): len(s))
print(words)
`, "6 fn\n[1, 4, 9] [2, 4]\n6\n[\"a\", \"cc\", \"bbb\"]")

	expect(t, "indented body needs return", `
let g = fn(a, b):
    let s = a + b
    return s * 2
let h = fn(x):
    x + 1
print(g(1, 2), h(1))
`, "6 nil")

	expect(t, "closures", `
fn counter():
    let n = 0
    return fn():
        n += 1
        return n
let c = counter()
c()
print(c(), c())
let fs = []
for i in 0..3:
    fs.push(fn(): i)
print(fs.map(fn(f): f()))
let add = fn(a): fn(b): a + b
print(add(2)(3))
`, "2 3\n[0, 1, 2]\n5")

	expect(t, "anywhere an expression goes", `
print((fn(x): x + 1)(41))
let ops = {"add": fn(a, b): a + b, "mul": fn(a, b): a * b}
print(ops["mul"](3, 4), f"{(fn(x): x * 2)(4)}")
(fn(): print("called"))()
let pick = match 1:
    1: fn(x): x + 1
    _: fn(x): x
print(pick(1))
let sign = fn(x): if x > 0: "pos" else: "neg"
print(sign(1), sign(-1))
`, "42\n12 8\ncalled\n2\npos neg")

	expect(t, "parameters like named functions", `
let f = fn(...xs): len(xs)
let g = fn(a, b): a - b
let h = fn(x int) str: str(x)
print(f(1, 2, 3), g(b: 1, a: 10), h(5))
`, "3 9 5")

	expectErr(t, "typed argument", "let f = fn(a int): a\nf(\"x\")", "runtime", 2, "argument 'a' of <fn>() must be int, not str")
	expectErr(t, "typed result", "let f = fn(x) int: \"s\"\nf(1)", "runtime", 1, "the result of <fn>() must be int, not str")
	expectErr(t, "no parameter list", "let f = fn x: x", "syntax", 1, "expected '(' after 'fn'")
	expectErr(t, "named fn mid-line", "if true: fn f(): 1", "syntax", 1, "'fn' must start on its own line")
}
