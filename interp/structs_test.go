package interp

import "testing"

func TestStructBasics(t *testing.T) {
	expect(t, "fields, defaults and methods", `
struct point:
    let x int
    let y int = 0
    let tags = []
    const id
    const ORIGIN = "0,0"

impl point:
    fn len2(self):
        return self.x * self.x + self.y * self.y
    fn moved(self, dx, dy):
        return point(x: self.x + dx, y: self.y + dy, id: self.id + 1)
    fn create(x):
        return point(x: x, id: 0)

let p = point(x: 3, y: 4, id: 7)
print(p, p.x, p.len2(), p.id, point::ORIGIN, p.ORIGIN)
let q = point::create(5)
print(q, q.moved(1, 1))
p.x = 10
p.tags.push("a")
print(p.x, p.tags, q.tags)
print(type(p), point)
`, `point(x: 3, y: 4, tags: [], id: 7) 3 25 7 0,0 0,0
point(x: 5, y: 0, tags: [], id: 0) point(x: 6, y: 1, tags: [], id: 1)
10 ["a"] []
point point`)

	expect(t, "methods as values and fields holding functions", `
struct counter:
    let n = 0
    let on_tick = nil
impl counter:
    fn tick(self):
        self.n += 1
        if self.on_tick: self.on_tick(self.n)
let c = counter()
fn report(n):
    print("tick", n)
c.on_tick = report
let t = c.tick
t()
t()
let push = [].push
print(c.n, type(push))
`, "tick 1\ntick 2\n2 fn")

	expect(t, "objects compare by fields", `
struct p:
    let x
    let y
print(p(x: 1, y: 2) == p(x: 1, y: 2), p(x: 1, y: 2) != p(x: 1, y: 3))
print(p(x: 1, y: 2) in [p(x: 0, y: 0), p(x: 1, y: 2)], [p(x: 1, y: 1)].index_of(p(x: 1, y: 1)))
`, "true true\ntrue 0")

	expect(t, "struct inside a function", `
fn make():
    struct box:
        let v
    impl box:
        fn get(self):
            return self.v
    return box(v: 42)
print(make().get())
`, "42")
}

func TestTraits(t *testing.T) {
	expect(t, "operators", `
struct vec:
    let x
    let y

add impl plus to vec:
    fn plus(self, other):
        return vec(x: self.x + other.x, y: self.y + other.y)

impl vec:
    fn minus(self, other):
        return vec(x: self.x - other.x, y: self.y - other.y)
    fn mul(self, k):
        return vec(x: self.x * k, y: self.y * k)
    fn neg(self):
        return vec(x: -self.x, y: -self.y)
    fn to_str(self):
        return f"<{self.x}, {self.y}>"

let a = vec(x: 1, y: 2)
let b = vec(x: 10, y: 20)
let c = a
c += b
print(a + b, b - a, a * 3, -a, c, [a, b])
print(f"a is {a}", str(a))
`, "<11, 22> <9, 18> <3, 6> <-1, -2> <11, 22> [<1, 2>, <10, 20>]\na is <1, 2> <1, 2>")

	expect(t, "eq and lt", `
struct money:
    let cents
impl money:
    fn eq(self, other):
        return self.cents == other.cents
    fn lt(self, other):
        return self.cents < other.cents
    fn to_str(self):
        return f"${self.cents / 100}"
let a = money(cents: 150)
let b = money(cents: 99)
print(a == money(cents: 150), a < b, a > b, a <= b, a >= b, a != b)
let l = [a, b, money(cents: 500)]
l.sort()
print(l, min(l), max(l))
`, "true false true false true true\n[$0.99, $1.5, $5.0] $0.99 $5.0")

	expect(t, "collection protocols", `
struct bag:
    let items = []
impl bag:
    fn len(self):
        return len(self.items)
    fn contains(self, x):
        return x in self.items
    fn index(self, i):
        return self.items[i]
    fn set_index(self, i, v):
        self.items[i] = v
    fn iter(self):
        return self.items
let b = bag(items: [1, 2, 3])
b[0] = 10
let total = 0
for x in b:
    total += x
print(len(b), 2 in b, 9 in b, b[0], total)
`, "3 true false 10 15")

	expect(t, "methods on builtin types", `
impl int:
    fn double(self):
        return self * 2
    fn is_even(self):
        return self % 2 == 0
impl str:
    fn shout(self):
        return self.upper() + "!"
impl list:
    fn sum(self):
        let s = 0
        for x in self:
            s += x
        return s
print(21.double(), 7.is_even(), "hi".shout(), [1, 2, 3].sum())
`, "42 false HI! 6")

	expect(t, "to_str of a builtin type", `
struct shown
impl shown:
    fn to_str(self)
add impl shown to bool:
    fn to_str(self):
        return if self: "yes" else: "no"
print(true, [false])
`, "yes [no]")

	expect(t, "operators on int can be overridden", `
impl int:
    fn mod(self, other):
        return 42
print(7 % 3, 2 + 3)
`, "42 5")
}

func TestInheritance(t *testing.T) {
	expect(t, "is takes fields and methods", `
struct animal:
    let name
    let legs = 4
impl animal:
    fn describe(self):
        return f"{self.name} has {self.legs} legs"
    fn sound(self):
        return "..."

struct dog is animal:
    let tricks = []
impl dog:
    fn sound(self):
        return "woof"

let d = dog(name: "Rex")
print(d.describe(), d.sound(), d.tricks)
print(animal.sound(d), animal::sound(d))
`, "Rex has 4 legs woof []\n... ...")

	expect(t, "diamond: the same field through two paths", `
struct base:
    let id = 1
struct a is base:
    let x = 2
struct b is base:
    let y = 3
struct c is a, b
print(c())
`, "c(id: 1, x: 2, y: 3)")

	expect(t, "redeclared field wins", `
struct a:
    let v = 1
struct b:
    let v = "two"
struct c is a, b:
    let v = 3.0
print(c().v)
`, "3.0")

	expect(t, "conflicting methods resolved in impl", `
struct a
impl a:
    fn hello(self):
        return "a"
struct b
impl b:
    fn hello(self):
        return "b"
struct c is a, b
impl c:
    fn hello(self):
        return a.hello(self) + b.hello(self)
print(c().hello())
`, "ab")

	expect(t, "impl of a parent after the child is declared", `
struct parent
struct child is parent
impl parent:
    fn greet(self):
        return "hi"
print(child().greet())
`, "hi")

	expect(t, "required methods", `
struct shape
impl shape:
    fn area(self)
    fn describe(self):
        return f"area {self.area()}"
struct square is shape:
    let side
impl square:
    fn area(self):
        return self.side * self.side
print(square(side: 3).describe())
`, "area 9")

	expect(t, "add struct and add impl", `
struct named:
    let name = "?"
impl named:
    fn hello(self):
        return f"hello, {self.name}"
struct thing:
    let weight = 1
let old = thing()
add struct named to thing
add impl named to thing
let t = thing(name: "box")
print(t.hello(), old.name, old.hello(), old)
`, `hello, box ? hello, ? thing(weight: 1, name: "?")`)

	expect(t, "add impl with an override", `
struct greeter
impl greeter:
    fn greet(self):
        return "hello"
    fn twice(self):
        return self.greet() + " " + self.greet()
struct robot
add impl greeter to robot:
    fn greet(self):
        return "BEEP"
print(robot().twice())
`, "BEEP BEEP")
}

func TestStructErrors(t *testing.T) {
	expectErr(t, "positional fields", "struct p:\n    let x\nprint(p(1))", "runtime", 3, "fields are set by name: p(x: ...)")
	expectErr(t, "unknown field", "struct p:\n    let x\np(z: 1)", "runtime", 3, "p has no field 'z'")
	expectErr(t, "missing const field", "struct p:\n    const id\np()", "runtime", 3, "missing field 'id'")
	expectErr(t, "const field is fixed", "struct p:\n    const id\nlet a = p(id: 1)\na.id = 2", "runtime", 4, "it is a const field of p")
	expectErr(t, "type constant", "struct p:\n    const MAX = 5\np::MAX = 6", "syntax", 3, "cannot assign to p::MAX")
	expectErr(t, "new field", "struct p:\n    let x\nlet a = p()\na.z = 1", "runtime", 4, "p has no field 'z' (fields are declared in the struct)")
	expectErr(t, "no method", "struct p\np().fly()", "runtime", 2, "p has no method 'fly'")
	expectErr(t, "abstract method", "struct shape\nimpl shape:\n    fn area(self)\nshape()", "runtime", 4,
		"cannot create shape: the required method 'area' (from shape) has no body")
	expectErr(t, "trait not implemented", "struct v\nadd impl plus to v\nv()", "runtime", 3,
		"cannot create v: the required method 'plus' (from plus) has no body")
	expectErr(t, "field conflict", "struct a:\n    let x\nstruct b:\n    let x\nstruct c is a, b", "runtime", 5,
		"struct c gets field 'x' from both a and b; declare it in c to choose")
	expectErr(t, "method conflict", "struct a\nimpl a:\n    fn f(self): return 1\nstruct b\nimpl b:\n    fn f(self): return 2\nstruct c is a, b\nc()", "runtime", 8,
		"c gets method 'f' from both a and b; define it in 'impl c:' to choose")
	expectErr(t, "add impl conflict", "struct a\nimpl a:\n    fn f(self): return 1\nstruct b\nimpl b:\n    fn f(self): return 2\nadd impl b to a\nstruct c is a\nadd impl b to c", "runtime", 9,
		"c already has method 'f' (from a); override it in 'add impl b to c:'")
	expectErr(t, "add struct field conflict", "struct a:\n    let x\nstruct b:\n    let x\nadd struct a to b", "runtime", 5, "b already has a field 'x'")
	expectErr(t, "static via instance", "struct p\nimpl p:\n    fn make(): return 1\np().make()", "runtime", 4, "make is a static method: call it as p::make(...)")
	expectErr(t, "inherit from int", "struct p is int", "runtime", 1, "int is a builtin type")
	expectErr(t, "operator not supported", "struct p\nprint(p() + 1)", "runtime", 2, "cannot use '+' with p and int")
	expectErr(t, "to_str must return str", "struct p\nimpl p:\n    fn to_str(self): return 5\nprint(p())", "runtime", 4, "to_str() of p must return a str")
	expectErr(t, "unknown module", "import nothing", "runtime", 1, "module 'nothing' not found")
	expectErr(t, "module member", "import math\nmath::nope", "runtime", 2, "module math has no member 'nope'")
	expectErr(t, "dot on module", "import math\nmath.sqrt(4)", "runtime", 2, "use math::sqrt(...)")
}

func TestModules(t *testing.T) {
	expect(t, "math random time", `
import math
import random as rnd
import time
print(math::sqrt(16), math::floor(2.7), math::round(-2.5), math::pi > 3)
rnd::seed(42)
let a = rnd::int(0..1000)
rnd::seed(42)
print(a == rnd::int(0..1000), rnd::int(5, 6), rnd::choice(["only"]))
let x = rnd::float()
print(x >= 0 and x < 1, time::now() > 0)
`, "4.0 2 -3 true\ntrue 5 only\ntrue true")

	expect(t, "term without a terminal", `
import term
print(term::wait_key(), term::wait_key())
`, "f i")
}
