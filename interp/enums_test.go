package interp

import "testing"

func TestEnumBasics(t *testing.T) {
	expect(t, "values, equality, fields, keys", `
enum color:
    red
    green
    rgb(r, g, b)

let c = color::rgb(255, g: 128, b: 0)
print(c, c.r, c.g, color::red, type(c), color)
print(color::red == color::red, color::red == color::green, c == color::rgb(255, 128, 0), c != color::rgb(0, 0, 0))
let names = {color::red: "red", color::green: "green"}
print(names[color::green], color::red in [color::green, color::red])
`, `color::rgb(255, 128, 0) 255 128 color::red color <enum color>
true false true true
green true`)

	expect(t, "variant with empty parens", `
enum tick:
    now()
    later
print(tick::now(), tick::now() == tick::now(), tick::later)
`, "tick::now() true tick::later")

	expect(t, "methods, to_str and traits on enums", `
enum dir:
    up
    down
impl dir:
    fn flip(self):
        return match self:
            dir::up: dir::down
            dir::down: dir::up
    fn to_str(self):
        return if self == dir::up: "↑" else: "↓"
print(dir::up.flip(), [dir::up, dir::down], f"{dir::down}")
`, "↓ [↑, ↓] ↓")
}

func TestEnumPatterns(t *testing.T) {
	expect(t, "nested patterns, values inside, wildcards", `
enum shape:
    circle(r)
    rect(w, h)
    empty

fn describe(x):
    return match x:
        result::ok(shape::circle(0)): "dot"
        result::ok(shape::circle(r)): f"circle {r}"
        result::ok(shape::rect(w, 0)): f"flat {w}"
        result::ok(shape::rect(_, 1..10)): "short rect"
        result::ok(shape::rect(w, h)): f"rect {w}x{h}"
        result::ok(shape::empty): "nothing"
        result::err: "error"

for v in [result::ok(shape::circle(0)), result::ok(shape::circle(5)), result::ok(shape::rect(3, 0)),
          result::ok(shape::rect(3, 4)), result::ok(shape::rect(3, 40)), result::ok(shape::empty),
          result::err("x")]:
    print(describe(v))
`, "dot\ncircle 5\nflat 3\nshort rect\nrect 3x40\nnothing\nerror")

	expect(t, "several variant patterns in one arm", `
enum key:
    up
    down
    left
    right
    char(c)
fn kind(k):
    return match k:
        key::up, key::down: "vertical"
        key::left, key::right: "horizontal"
        key::char("q"), key::char("Q"): "quit"
        key::char(_): "other"
for k in [key::up, key::right, key::char("Q"), key::char("x")]:
    print(kind(k))
`, "vertical\nhorizontal\nquit\nother")

	expect(t, "bindings captured by closures, locals in arms", `
enum msg:
    text(s)
    num(n)
let handlers = []
for m in [msg::text("hi"), msg::num(2)]:
    match m:
        msg::text(s):
            let loud = s.upper()
            fn h():
                return loud + "!"
            handlers.push(h)
        msg::num(n):
            fn h2():
                return n * 10
            handlers.push(h2)
let out = []
for h in handlers:
    out.push(h())
print(out)
`, `["HI!", 20]`)

	expect(t, "match value with temporaries below", `
enum v:
    pair(a, b)
fn sum(x):
    return 100 + match x:
        v::pair(a, v::pair(b, c)): a + b + c
        v::pair(a, b): a + b
print(sum(v::pair(1, v::pair(2, 3))), sum(v::pair(1, 2)))
`, "106 103")

	expect(t, "statement match with no arm matching", `
enum e:
    a(x)
    b
match e::b:
    e::a(x): print("a", x)
print("done")
`, "done")
}

func TestEnumInheritance(t *testing.T) {
	expect(t, "child extends the variants", `
enum io_err:
    not_found(path)
    denied

enum net_err is io_err:
    timeout
    refused(port)

impl net_err:
    fn retry(self):
        return match self:
            net_err::timeout, net_err::refused(_): true
            _: false

let e = net_err::denied
print(e == io_err::denied, e, net_err::not_found("/x"))
match net_err::not_found("/etc"):
    io_err::not_found(p): print("parent pattern catches it:", p)
print(net_err::timeout.retry(), e.retry())
`, `true net_err::denied net_err::not_found("/x")
parent pattern catches it: /etc
true false`)
}

func TestTryOperator(t *testing.T) {
	expect(t, "result and option", `
fn parse_num(s):
    if s == "":
        return result::err("empty")
    return result::ok(int(s))

fn add_strings(a, b):
    let x = parse_num(a)?
    let y = parse_num(b)?
    return result::ok(x + y)

fn first_even(items):
    for x in items:
        if x % 2 == 0:
            return option::some(x)
    return option::none

fn half_of_first_even(items):
    let x = first_even(items)?
    return option::some(x // 2)

print(add_strings("2", "3"), add_strings("", "3"), add_strings("2", ""))
print(half_of_first_even([1, 3, 8]), half_of_first_even([1, 3]))
print(add_strings("1", "1").unwrap(), add_strings("", "").unwrap_or(-1))
print(result::ok(1).is_ok(), result::err("x").is_err(), option::none.is_none(), option::some(1).unwrap())
`, `result::ok(5) result::err("empty") result::err("empty")
option::some(4) option::none
2 -1
true true true 1`)

	expect(t, "? returns from inside a loop", `
fn check_all(items):
    for x in items:
        let v = (if x > 0: result::ok(x) else: result::err(f"bad {x}"))?
        print("ok", v)
    return result::ok("all good")
print(check_all([1, -2, 3]))
`, "ok 1\nresult::err(\"bad -2\")")

	expect(t, "own type with try", `
enum maybe_num:
    num(n)
    nothing
struct try_num
add impl try to maybe_num:
    fn try(self):
        return match self:
            maybe_num::num(n): flow::next(n)
            _: flow::exit(0)
fn double(m):
    return m? * 2
print(double(maybe_num::num(21)), double(maybe_num::nothing))
`, "42 0")
}

func TestEnumErrors(t *testing.T) {
	expectErr(t, "too many values", "enum e:\n    a(x)\ne::a(1, 2)", "runtime", 3, "e::a takes 1 value but got 2")
	expectErr(t, "missing field", "enum e:\n    a(x, y)\ne::a(1)", "runtime", 3, "missing field 'y' of e::a")
	expectErr(t, "unknown field", "enum e:\n    a(x)\ne::a(z: 1)", "runtime", 3, "e::a has no field 'z'")
	expectErr(t, "enum is not callable", "enum e:\n    a\ne()", "runtime", 3, "e is an enum: make its values with e::variant")
	expectErr(t, "no such variant", "enum e:\n    a\ne::b", "runtime", 3, "enum e has no variant, constant or method named 'b'")
	expectErr(t, "pattern arity", "enum e:\n    a(x, y)\nmatch e::a(1, 2):\n    e::a(x): print(x)", "runtime", 4,
		"the pattern e::a(...) has 1 part, but the variant has 2 fields")
	expectErr(t, "bind in multi-pattern", "enum e:\n    a(x)\n    b(x)\nmatch e::a(1):\n    e::a(x), e::b(x): print(x)", "compile", 5,
		"patterns joined with ',' cannot bind variables like 'x'")
	expectErr(t, "enum values never change", "enum e:\n    a(x)\nlet v = e::a(1)\nv.x = 2", "runtime", 4, "enum values never change")
	expectErr(t, "data variant as key", "enum e:\n    a(x)\nlet m = {e::a(1): 2}", "runtime", 3, "only variants without data can")
	expectErr(t, "duplicate variant", "enum e:\n    a\n    a", "compile", 3, "variant 'a' is declared twice")
	expectErr(t, "? at top level", "let x = result::ok(1)?", "compile", 1, "'?' can only be used inside a function")
	expectErr(t, "? on a number", "fn f():\n    return 5?\nf()", "runtime", 2, "'?' needs a value with a try method (like result or option), not int")
	expectErr(t, "unwrap an error", "fn main():\n    result::err(\"boom\").unwrap()", "runtime", 2, "panic: unwrap() on an error: boom")
	expectErr(t, "inherit from struct", "struct s\nenum e is s:\n    a", "runtime", 2, "enum e can only inherit from enums")
}
