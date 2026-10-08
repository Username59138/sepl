package interp

import "testing"

func TestAddStructReachesChildren(t *testing.T) {
	expect(t, "children declared before and after add", `
import json
struct a:
    let x = 1
struct b is a
struct g is b:
    let z = 0
let old = a()
let oldb = b()
struct mix:
    let y = 2
    let l = []
add struct mix to a
struct c is a
print(a(), b(), g(), c())
print(old, oldb, old == a(), oldb == b(), b(y: 7).y)
print(json::to_str(oldb))
`, "a(x: 1, y: 2, l: []) b(x: 1, y: 2, l: []) g(x: 1, z: 0, y: 2, l: []) c(x: 1, y: 2, l: [])\n"+
		"a(x: 1, y: 2, l: []) b(x: 1, y: 2, l: []) true true 7\n"+
		`{"x":1,"y":2,"l":[]}`)

	expect(t, "a child's own field wins", `
struct a
struct b is a:
    let y = "own"
struct mix:
    let y = 2
add struct mix to a
print(a(), b())
`, `a(y: 2) b(y: "own")`)

	expect(t, "targets of add struct are children too", `
struct a
struct b
add struct a to b
struct mix:
    let q = 1
add struct mix to a
print(b())
`, "b(q: 1)")

	expect(t, "repeated add changes nothing", `
struct a
struct b:
    let k = 1
add struct b to a
add struct b to a
print(a())
`, "a(k: 1)")

	expectErr(t, "conflict in a child", "struct a\nstruct other:\n    let y = 5\nstruct b is a, other\nstruct mix:\n    let y = 2\nadd struct mix to a\nprint(a())",
		"runtime", 7, "cannot add struct mix to a: b (a child of a) already has a field 'y' (from other)")
}

func TestAddPlacement(t *testing.T) {
	expectErr(t, "add struct in a function", "struct a\nstruct b\nfn f():\n    add struct b to a", "compile", 4,
		"'add struct' is a declaration, like struct and impl: put it at the top level of the file")
	expectErr(t, "add impl in a method", "struct a\nstruct b\nimpl a:\n    fn f(self):\n        add impl b to a", "compile", 5,
		"'add impl' is a declaration")
	expectErr(t, "add after ':' on one line", "struct a\nstruct b\nif true: add struct b to a", "syntax", 3, "'add' must start on its own line")
	expectErr(t, "inside a top-level if", "struct a\nstruct b\nif true:\n    add struct b to a", "compile", 4, "'add struct' is a declaration")
	expectErr(t, "inside a top-level loop", "struct a\nstruct b\nfor i in 0..2:\n    add impl b to a", "compile", 4, "'add impl' is a declaration")
	expectErr(t, "inside a lambda", "struct a\nstruct b\nlet f = fn():\n    add struct b to a", "compile", 4, "'add struct' is a declaration")
}
