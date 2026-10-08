package interp

import "testing"

func TestTypeOf(t *testing.T) {
	expect(t, "type() gives the type itself", `
struct point:
    let x = 0
struct p3 is point
enum shape:
    circle(r)
    empty
print(type(1) == int, type(2.5) == float, type("s") == str, type(true) == bool)
print(type([]) == list, type({}) == map, type(1..2) == range)
print(type(point()) == point, type(p3()) == point, type(p3()) == p3, type(shape::empty) == shape)
print(type(int) == type, type(type) == type, type(print) == type(fn(): 1), type(nil) == type(nil))
print(type(5)("7") + 1, type("a")(42))
`, "true true true true\ntrue true true\ntrue false true true\ntrue true true true\n8 42")

	expect(t, "a type prints as its name, inside a list as <...>", `
struct point
print(type(1), type(nil), type(print), type(point()), type(int), f"got {type(2.5)}", str(type("a")))
print([type(1), type(point()), type(int)])
`, "int nil fn point type got float str\n[<type int>, <struct point>, <type type>]")

	expect(t, "type annotations", `
struct point
let t type = int
fn name(k type) str:
    return str(k)
print(name(point), t)
`, "point int")

	expectErr(t, "type compared with str", `print(type(1) == "int")`, "runtime", 1,
		"cannot compare a type with a str: type() gives the type itself, so write type(x) == int")
	expectErr(t, "!= too", `print("str" != type("a"))`, "runtime", 1, "write type(x) == str")
	expectErr(t, "hidden types cannot be called", `type(nil)()`, "runtime", 1, "cannot create values of type nil by calling it")
}
