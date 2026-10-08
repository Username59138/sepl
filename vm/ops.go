package vm

import (
	"fmt"
	"math"
	"strings"
	"unicode/utf8"
)

// opError is a runtime error message; the VM adds the position and trace.
type opError string

func (e opError) Error() string { return string(e) }

func errorf(format string, args ...any) error { return opError(fmt.Sprintf(format, args...)) }

var (
	errOverflow = opError("integer overflow: the result does not fit in a 64-bit int")
	errDivZero  = opError("division by zero")
)

// typeErr explains an operator used with the wrong types. Mixing str and a
// number gets a hint, since SEPL never converts implicitly.
func typeErr(op string, a, b Value) error {
	ta, tb := TypeName(a), TypeName(b)
	msg := fmt.Sprintf("cannot use '%s' with %s and %s", op, ta, tb)
	if (ta == "str" && isNum(b)) || (tb == "str" && isNum(a)) {
		msg += " (convert explicitly: str(x) or int(x))"
	}
	return opError(msg)
}

// ---------------------------------------------------------------- int helpers

func addInt(x, y int64) (int64, error) {
	r := x + y
	if (x^r)&(y^r) < 0 {
		return 0, errOverflow
	}
	return r, nil
}

func subInt(x, y int64) (int64, error) {
	r := x - y
	if (x^y)&(x^r) < 0 {
		return 0, errOverflow
	}
	return r, nil
}

func mulInt(x, y int64) (int64, error) {
	if x > -1<<31 && x < 1<<31 && y > -1<<31 && y < 1<<31 {
		return x * y, nil
	}
	r := x * y
	if x != 0 && (r/x != y || (x == -1 && y == math.MinInt64) || (y == -1 && x == math.MinInt64)) {
		return 0, errOverflow
	}
	return r, nil
}

// floorDiv and floorMod round toward negative infinity, like Python.
func floorDiv(x, y int64) (int64, error) {
	if y == 0 {
		return 0, errDivZero
	}
	if x == math.MinInt64 && y == -1 {
		return 0, errOverflow
	}
	q := x / y
	if x%y != 0 && (x < 0) != (y < 0) {
		q--
	}
	return q, nil
}

func floorMod(x, y int64) (int64, error) {
	if y == 0 {
		return 0, errDivZero
	}
	if y == -1 {
		return 0, nil
	}
	r := x % y
	if r != 0 && (r < 0) != (y < 0) {
		r += y
	}
	return r, nil
}

// ---------------------------------------------------------------- operators

// Add implements a + b for every type (the VM handles int + int itself).
func Add(a, b Value) (Value, error) {
	if a.K == KInt && b.K == KInt {
		r, err := addInt(a.AsInt(), b.AsInt())
		return Int(r), err
	}
	if isNum(a) && isNum(b) {
		return Float(toFloat(a) + toFloat(b)), nil
	}
	switch x := a.O.(type) {
	case *StrObj:
		if y, ok := b.O.(*StrObj); ok {
			return Str(x.S + y.S), nil
		}
	case *ListObj:
		if y, ok := b.O.(*ListObj); ok {
			items := make([]Value, 0, len(x.Items)+len(y.Items))
			items = append(append(items, x.Items...), y.Items...)
			return NewList(items), nil
		}
	}
	return Nil, typeErr("+", a, b)
}

// Arith implements -, *, /, // and %.
func Arith(op Op, a, b Value) (Value, error) {
	if !isNum(a) || !isNum(b) {
		return Nil, typeErr(op.Symbol(), a, b)
	}
	if a.K == KInt && b.K == KInt {
		x, y := a.AsInt(), b.AsInt()
		var r int64
		var err error
		switch op {
		case OpSub:
			r, err = subInt(x, y)
		case OpMul:
			r, err = mulInt(x, y)
		case OpIDiv:
			r, err = floorDiv(x, y)
		case OpMod:
			r, err = floorMod(x, y)
		case OpDiv:
			if y == 0 {
				return Nil, errDivZero
			}
			return Float(float64(x) / float64(y)), nil
		}
		return Int(r), err
	}
	x, y := toFloat(a), toFloat(b)
	switch op {
	case OpSub:
		return Float(x - y), nil
	case OpMul:
		return Float(x * y), nil
	case OpDiv:
		if y == 0 {
			return Nil, errDivZero
		}
		return Float(x / y), nil
	case OpIDiv:
		if y == 0 {
			return Nil, errDivZero
		}
		return Float(math.Floor(x / y)), nil
	case OpMod:
		if y == 0 {
			return Nil, errDivZero
		}
		r := math.Mod(x, y)
		if r != 0 && (r < 0) != (y < 0) {
			r += y
		}
		return Float(r), nil
	}
	return Nil, typeErr(op.Symbol(), a, b)
}

// Neg implements -x.
func Neg(v Value) (Value, error) {
	switch v.K {
	case KInt:
		if v.AsInt() == math.MinInt64 {
			return Nil, errOverflow
		}
		return Int(-v.AsInt()), nil
	case KFloat:
		return Float(-v.AsFloat()), nil
	}
	return Nil, errorf("cannot negate %s", TypeName(v))
}

// Compare orders numbers and strings: -1, 0 or 1.
func Compare(a, b Value, op string) (int, error) {
	if a.K == KInt && b.K == KInt {
		x, y := a.AsInt(), b.AsInt()
		switch {
		case x < y:
			return -1, nil
		case x > y:
			return 1, nil
		}
		return 0, nil
	}
	if isNum(a) && isNum(b) {
		x, y := toFloat(a), toFloat(b)
		switch {
		case x < y:
			return -1, nil
		case x > y:
			return 1, nil
		}
		return 0, nil
	}
	if x, ok := a.O.(*StrObj); ok {
		if y, ok := b.O.(*StrObj); ok {
			return strings.Compare(x.S, y.S), nil
		}
	}
	return 0, errorf("cannot compare %s and %s with '%s'", TypeName(a), TypeName(b), op)
}

// Contains implements x in c.
func Contains(c, x Value) (bool, error) {
	switch o := c.O.(type) {
	case *ListObj:
		for _, v := range o.Items {
			if Equal(v, x) {
				return true, nil
			}
		}
		return false, nil
	case *StrObj:
		s, ok := x.O.(*StrObj)
		if !ok {
			return false, errorf("'in' with a str needs a str on the left, not %s", TypeName(x))
		}
		return strings.Contains(o.S, s.S), nil
	case *MapObj:
		_, found := o.Get(x)
		return found, nil
	case *RangeObj:
		switch x.K {
		case KInt:
			return x.AsInt() >= o.Lo && x.AsInt() < o.Hi, nil
		case KFloat:
			f := x.AsFloat()
			return f >= float64(o.Lo) && f < float64(o.Hi), nil
		}
		return false, nil
	}
	return false, errorf("cannot use 'in' with %s", TypeName(c))
}

// InRange reports lo <= x < hi for match range patterns.
func InRange(x, lo, hi Value) (bool, error) {
	if !isNum(x) {
		return false, nil
	}
	c1, err := Compare(lo, x, "..")
	if err != nil {
		return false, err
	}
	c2, err := Compare(x, hi, "..")
	if err != nil {
		return false, err
	}
	return c1 <= 0 && c2 < 0, nil
}

// MakeRange implements lo..hi.
func MakeRange(lo, hi Value) (Value, error) {
	if lo.K != KInt || hi.K != KInt {
		return Nil, errorf("range bounds must be int, not %s and %s", TypeName(lo), TypeName(hi))
	}
	return Obj(&RangeObj{Lo: lo.AsInt(), Hi: hi.AsInt()}), nil
}

// normIndex turns a possibly negative index into a position in [0, n).
func normIndex(i Value, n int, what string) (int, error) {
	if i.K != KInt {
		return 0, errorf("%s index must be int, not %s", what, TypeName(i))
	}
	x := i.AsInt()
	if x < 0 {
		x += int64(n)
	}
	if x < 0 || x >= int64(n) {
		return 0, errorf("index %d is out of range for a %s of length %d", i.AsInt(), what, n)
	}
	return int(x), nil
}

// Index implements c[i].
func Index(c, i Value) (Value, error) {
	switch o := c.O.(type) {
	case *ListObj:
		n, err := normIndex(i, len(o.Items), "list")
		if err != nil {
			return Nil, err
		}
		return o.Items[n], nil
	case *StrObj:
		n, err := normIndex(i, o.Len(), "str")
		if err != nil {
			return Nil, err
		}
		if o.Len() == len(o.S) { // ASCII
			return Str(o.S[n : n+1]), nil
		}
		for _, r := range o.S {
			if n == 0 {
				return Str(string(r)), nil
			}
			n--
		}
	case *MapObj:
		v, found := o.Get(i)
		if !found {
			if _, err := keyOf(i); err != nil {
				return Nil, err
			}
			return Nil, errorf("key %s not found (m.get(k) returns nil instead)", Repr(i))
		}
		return v, nil
	case *RangeObj:
		n, err := normIndex(i, int(o.Len()), "range")
		if err != nil {
			return Nil, err
		}
		return Int(o.Lo + int64(n)), nil
	}
	return Nil, errorf("cannot index %s", TypeName(c))
}

// SetIndex implements c[i] = v.
func SetIndex(c, i, v Value) error {
	switch o := c.O.(type) {
	case *ListObj:
		n, err := normIndex(i, len(o.Items), "list")
		if err != nil {
			return err
		}
		o.Items[n] = v
		return nil
	case *MapObj:
		return o.Set(i, v)
	case *StrObj:
		return opError("a str cannot be changed; build a new one instead")
	}
	return errorf("cannot assign to an element of %s", TypeName(c))
}

// runeSlice returns the characters of s from rune index i to j.
func runeSlice(s string, i, j int) string {
	if utf8.RuneCountInString(s) == len(s) {
		return s[i:j]
	}
	r := []rune(s)
	return string(r[i:j])
}
