package vm

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// call runs a SEPL function for a list method such as map or filter.
func (vm *VM) call1(f Value, args ...Value) (Value, error) { return vm.Call(f, args) }

// snapshot copies a list's items, so a callback that changes the list does
// not disturb the loop.
func snapshot(r Value) []Value { return append([]Value(nil), r.O.(*ListObj).Items...) }

func addCollectionMethods() {
	// map(f): a new list of f(item)
	listMethods["map"] = func(vm *VM, r Value, a []Value) (Value, error) {
		if err := arity("map", a, 1, 1); err != nil {
			return Nil, err
		}
		items := snapshot(r)
		out := make([]Value, len(items))
		for i, x := range items {
			v, err := vm.call1(a[0], x)
			if err != nil {
				return Nil, err
			}
			out[i] = v
		}
		return NewList(out), nil
	}
	// filter(f): the items for which f(item) is true
	listMethods["filter"] = func(vm *VM, r Value, a []Value) (Value, error) {
		if err := arity("filter", a, 1, 1); err != nil {
			return Nil, err
		}
		var out []Value
		for _, x := range snapshot(r) {
			v, err := vm.call1(a[0], x)
			if err != nil {
				return Nil, err
			}
			if Truthy(v) {
				out = append(out, x)
			}
		}
		return NewList(out), nil
	}
	// reduce(f, start): f(f(start, a), b)...
	listMethods["reduce"] = func(vm *VM, r Value, a []Value) (Value, error) {
		if err := arity("reduce", a, 2, 2); err != nil {
			return Nil, err
		}
		acc := a[1]
		for _, x := range snapshot(r) {
			v, err := vm.call1(a[0], acc, x)
			if err != nil {
				return Nil, err
			}
			acc = v
		}
		return acc, nil
	}
	// sum(): the sum of the items (0 for an empty list)
	listMethods["sum"] = func(vm *VM, r Value, a []Value) (Value, error) {
		if err := arity("sum", a, 0, 0); err != nil {
			return Nil, err
		}
		acc := Int(0)
		for _, x := range snapshot(r) {
			v, err := vm.arith(OpAdd, acc, x)
			if err != nil {
				return Nil, err
			}
			acc = v
		}
		return acc, nil
	}
	predicate := func(name string, want bool) method {
		return func(vm *VM, r Value, a []Value) (Value, error) {
			if err := arity(name, a, 1, 1); err != nil {
				return Nil, err
			}
			for _, x := range snapshot(r) {
				v, err := vm.call1(a[0], x)
				if err != nil {
					return Nil, err
				}
				if Truthy(v) == want {
					return Bool(want), nil
				}
			}
			return Bool(!want), nil
		}
	}
	// any(f): is f(item) true for some item? all(f): for every item?
	listMethods["any"] = predicate("any", true)
	listMethods["all"] = predicate("all", false)
	// find(f): option::some(first item with f(item) true) or option::none
	listMethods["find"] = func(vm *VM, r Value, a []Value) (Value, error) {
		if err := arity("find", a, 1, 1); err != nil {
			return Nil, err
		}
		for _, x := range snapshot(r) {
			v, err := vm.call1(a[0], x)
			if err != nil {
				return Nil, err
			}
			if Truthy(v) {
				return vm.some(x), nil
			}
		}
		return vm.none(), nil
	}
	// enumerate(): [[0, a], [1, b], ...]
	listMethods["enumerate"] = func(vm *VM, r Value, a []Value) (Value, error) {
		if err := arity("enumerate", a, 0, 0); err != nil {
			return Nil, err
		}
		items := snapshot(r)
		out := make([]Value, len(items))
		for i, x := range items {
			out[i] = NewList([]Value{Int(int64(i)), x})
		}
		return NewList(out), nil
	}
	// count(x): how many items equal x
	listMethods["count"] = func(vm *VM, r Value, a []Value) (Value, error) {
		if err := arity("count", a, 1, 1); err != nil {
			return Nil, err
		}
		n := 0
		for _, x := range snapshot(r) {
			eq, err := vm.equal(x, a[0])
			if err != nil {
				return Nil, err
			}
			if eq {
				n++
			}
		}
		return Int(int64(n)), nil
	}

	// items(): [[key, value], ...] in insertion order
	mapMethods["items"] = func(vm *VM, r Value, a []Value) (Value, error) {
		if err := arity("items", a, 0, 0); err != nil {
			return Nil, err
		}
		m := r.O.(*MapObj)
		out := make([]Value, len(m.keys))
		for i, k := range m.keys {
			out[i] = NewList([]Value{k, m.vals[i]})
		}
		return NewList(out), nil
	}

	// lines(): the lines of the text, without line endings
	strMethods["lines"] = func(vm *VM, r Value, a []Value) (Value, error) {
		if err := arity("lines", a, 0, 0); err != nil {
			return Nil, err
		}
		s := strings.ReplaceAll(r.O.(*StrObj).S, "\r\n", "\n")
		s = strings.TrimSuffix(s, "\n")
		if s == "" {
			return NewList(nil), nil
		}
		return strList(strings.Split(s, "\n")), nil
	}
	// count(sub): how many times sub occurs
	strMethods["count"] = func(vm *VM, r Value, a []Value) (Value, error) {
		if err := arity("count", a, 1, 1); err != nil {
			return Nil, err
		}
		sub, err := strArg("count", a[0])
		if err != nil {
			return Nil, err
		}
		if sub == "" {
			return Nil, opError("count() needs a non-empty str")
		}
		return Int(int64(strings.Count(r.O.(*StrObj).S, sub))), nil
	}
	// pad_left(width, fill = " ") and pad_right(width, fill = " ")
	pad := func(name string, left bool) method {
		return func(vm *VM, r Value, a []Value) (Value, error) {
			if err := arity(name, a, 1, 2); err != nil {
				return Nil, err
			}
			width, err := intArg(name, a[0])
			if err != nil {
				return Nil, err
			}
			fill := " "
			if len(a) == 2 {
				if fill, err = strArg(name, a[1]); err != nil {
					return Nil, err
				}
				if utf8.RuneCountInString(fill) != 1 {
					return Nil, errorf("%s() fill must be one character", name)
				}
			}
			s := r.O.(*StrObj)
			if width > MaxStrBytes/int64(len(fill)) {
				return Nil, errorf("%s(): the width is too large", name)
			}
			n := int(width) - s.Len()
			if n <= 0 {
				return r, nil
			}
			if left {
				return Str(strings.Repeat(fill, n) + s.S), nil
			}
			return Str(s.S + strings.Repeat(fill, n)), nil
		}
	}
	strMethods["pad_left"] = pad("pad_left", true)
	strMethods["pad_right"] = pad("pad_right", false)
	// is_digit(), is_alpha(), is_space(): all characters are such (and there is at least one)
	class := func(name string, ok func(rune) bool) method {
		return func(vm *VM, r Value, a []Value) (Value, error) {
			if err := arity(name, a, 0, 0); err != nil {
				return Nil, err
			}
			s := r.O.(*StrObj).S
			if s == "" {
				return False, nil
			}
			for _, c := range s {
				if !ok(c) {
					return False, nil
				}
			}
			return True, nil
		}
	}
	strMethods["is_digit"] = class("is_digit", unicode.IsDigit)
	strMethods["is_alpha"] = class("is_alpha", unicode.IsLetter)
	strMethods["is_space"] = class("is_space", unicode.IsSpace)
}
