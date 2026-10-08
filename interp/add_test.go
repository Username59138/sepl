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

func TestAddAnywhere(t *testing.T) {
	expect(t, "in a function, a block, a lambda and after ':'", `
struct a
struct b:
    let k = 1
struct c
impl c:
    fn hi(self):
        return "hi"
struct d
impl d:
    fn bye(self):
        return "bye"
fn patch():
    add struct b to a
patch()
if true: add impl c to a
let f = fn():
    add impl d to a
f()
for i in 0..3:
    add struct b to a
print(a(), a().hi(), a().bye())
`, "a(k: 1) hi bye")
}

func TestAddTo(t *testing.T) {
	expect(t, "fields, constants and methods in place", `
struct user:
    let name = "?"
struct admin is user
let old = user(name: "old")
fn register(t type):
    add to t:
        let id = 0
        let tags = []
        const KIND = "registered"
        fn show(self):
            return f"#{self.id} {self.name}"
register(user)
print(user(name: "ann", id: 3).show(), admin(name: "root").show(), old.show(), user::KIND, admin::KIND)
let a = user()
let b = user()
a.tags.push("x")
print(a.tags, b.tags)
add to user:
    let age int = 0
print(user(age: 5).age)
add to int:
    fn twice(self):
        return self * 2
print(4.twice())
`, "#3 ann #0 root #0 old registered registered\n[\"x\"] []\n5\n8")

	expect(t, "a child's own field wins", "struct a\nstruct b is a:\n    let v = 9\nadd to a:\n    let v = 1\nprint(a(), b())", "a(v: 1) b(v: 9)")
	expect(t, "only constants", "struct p\nstruct q is p\nadd to p:\n    const K = 1\nprint(p::K, q::K)", "1 1")
	expect(t, "add struct with only constants", "struct c:\n    const MAX = 3\nstruct p\nadd struct c to p\nprint(p::MAX)", "3")

	expectErr(t, "existing field", "struct p:\n    let x = 1\nadd to p:\n    let x = 2", "runtime", 3, "p already has a field 'x' (from p)")
	expectErr(t, "existing constant", "struct p\nadd to p:\n    const K = 1\nadd to p:\n    const K = 2", "runtime", 4, "p already has a constant 'K'")
	expectErr(t, "typed default", "struct p\nadd to p:\n    let a int = \"s\"", "runtime", 2, "the default of field 'a' of add to p must be int, not str")
	expectErr(t, "fields on a builtin type", "add to int:\n    let x = 1", "runtime", 1, "cannot add fields to the builtin type int")
	expectErr(t, "only fields and methods", "struct p\nadd to p:\n    print(1)", "syntax", 3, "expected a field ('let' or 'const') or a method ('fn') in 'add to'")
}

func TestRegisterExample(t *testing.T) {
	out, _, err := run(t, readExample(t, "register.sepl"))
	want := "<user #1: user(name: \"ann\", id: 1)>\n<order #2: order(total: 42, id: 2)>\nregistered 3\n"
	if err != nil || out != want {
		t.Errorf("register.sepl: err %v, out %q", err, out)
	}
}
