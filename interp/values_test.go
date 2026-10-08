package interp

import "testing"

func TestCyclicValues(t *testing.T) {
	expect(t, "equality of values that contain themselves", `
let a = [1]
a.push(a)
let b = [1]
b.push(b)
let c = [2]
c.push(c)
print(a == b, a == c, a == a, [a] == [b], a in [b])
let m = {"k": 1}
m["self"] = m
let n = {"k": 1}
n["self"] = n
print(m == n)
struct node:
    let v
    let next
let x = node(v: 1)
x.next = x
let y = node(v: 1)
y.next = node(v: 1, next: y)
let z = node(v: 2)
z.next = z
print(x == y, x == z)
`, "true false true true true\ntrue\ntrue false")
}

func TestHugeStrings(t *testing.T) {
	expectErr(t, "repeat", `print("ab".repeat(9223372036854775807))`, "runtime", 1, "repeat(): the result would be too large")
	expectErr(t, "pad_left", `print("a".pad_left(9000000000000))`, "runtime", 1, "pad_left(): the width is too large")
	expectErr(t, "pad_right", `print("a".pad_right(9000000000000, "*"))`, "runtime", 1, "pad_right(): the width is too large")
	expect(t, "ordinary sizes", `print("ab".repeat(3), len("".repeat(9223372036854775807)), "a".pad_left(3, "*"))`, "ababab 0 **a")
}
