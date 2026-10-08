package vm

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// builtinList is the fixed order of builtins: the compiler refers to them by
// index (see BuiltinNames).
var builtinList = []*Builtin{
	{Name: "print", Fn: biPrint},
	{Name: "len", Fn: biLen},
	{Name: "str", Fn: biStr},
	{Name: "int", Fn: biInt},
	{Name: "float", Fn: biFloat},
	{Name: "type", Fn: biType},
	{Name: "input", Fn: biInput},
	{Name: "abs", Fn: biAbs},
	{Name: "min", Fn: biMin},
	{Name: "max", Fn: biMax},
	{Name: "exit", Fn: biExit},
	{Name: "bool", Fn: biBool},
	{Name: "list", Fn: biList},
	{Name: "map", Fn: biMap},
	{Name: "range", Fn: biRange},
	{Name: "panic", Fn: biPanic},
}

// builtinTypeNames are the builtins that are also types: int("5") converts,
// and "impl int:" adds methods to every int.
var builtinTypeNames = map[string]bool{
	"int": true, "float": true, "str": true, "bool": true, "list": true, "map": true, "range": true,
	"type": true,
}

// BuiltinNames returns the names of the builtins in index order.
func BuiltinNames() []string {
	names := make([]string, len(builtinList))
	for i, b := range builtinList {
		names[i] = b.Name
	}
	return names
}

func noKw(fn string, kw []Kwarg) error {
	if len(kw) > 0 {
		return errorf("%s() has no parameter named '%s'", fn, kw[0].Name)
	}
	return nil
}

// arity checks that a builtin or method got between lo and hi arguments.
func arity(fn string, args []Value, lo, hi int) error {
	n := len(args)
	if n >= lo && n <= hi {
		return nil
	}
	switch {
	case lo == hi:
		return errorf("%s() takes %d argument%s but got %d", fn, lo, plural(lo), n)
	case n < lo:
		return errorf("%s() takes at least %d argument%s but got %d", fn, lo, plural(lo), n)
	}
	return errorf("%s() takes at most %d argument%s but got %d", fn, hi, plural(hi), n)
}

func biPrint(vm *VM, args []Value, kw []Kwarg) (Value, error) {
	var pos *Value
	for i, k := range kw {
		if k.Name != "pos" {
			return Nil, errorf("print() has no parameter named '%s' (only pos)", k.Name)
		}
		pos = &kw[i].Value
	}
	if pos != nil {
		// print(text, pos: p): write at column p.x, row p.y of the terminal
		// (0-based) and do not end the line.
		x, err := vm.getField(*pos, "x")
		if err != nil {
			return Nil, errorf("print(pos: p) needs p with fields x and y: %s", err)
		}
		y, err := vm.getField(*pos, "y")
		if err != nil {
			return Nil, errorf("print(pos: p) needs p with fields x and y: %s", err)
		}
		if x.K != KInt || y.K != KInt {
			return Nil, errorf("print(pos: p) needs int x and y, not %s and %s", TypeName(x), TypeName(y))
		}
		if x.AsInt() < 0 || y.AsInt() < 0 {
			return Nil, nil // outside the screen
		}
		fmt.Fprintf(vm.Out, "\x1b[%d;%dH", y.AsInt()+1, x.AsInt()+1)
	}
	for i, a := range args {
		if i > 0 {
			vm.Out.WriteByte(' ')
		}
		s, err := vm.toStr(a)
		if err != nil {
			return Nil, err
		}
		vm.Out.WriteString(s)
	}
	if pos == nil {
		vm.Out.WriteByte('\n')
	}
	return Nil, nil
}

func biLen(vm *VM, args []Value, kw []Kwarg) (Value, error) {
	if err := noKw("len", kw); err != nil {
		return Nil, err
	}
	if err := arity("len", args, 1, 1); err != nil {
		return Nil, err
	}
	return vm.length(args[0])
}

func lengthOf(v Value) (Value, error) {
	switch o := v.O.(type) {
	case *StrObj:
		return Int(int64(o.Len())), nil
	case *ListObj:
		return Int(int64(len(o.Items))), nil
	case *MapObj:
		return Int(int64(o.Len())), nil
	case *RangeObj:
		return Int(o.Len()), nil
	}
	return Nil, errorf("%s has no length", TypeName(v))
}

func biStr(vm *VM, args []Value, kw []Kwarg) (Value, error) {
	if err := noKw("str", kw); err != nil {
		return Nil, err
	}
	if err := arity("str", args, 1, 1); err != nil {
		return Nil, err
	}
	if args[0].K == KObj {
		if _, ok := args[0].O.(*StrObj); ok {
			return args[0], nil
		}
	}
	s, err := vm.toStr(args[0])
	return Str(s), err
}

func biInt(vm *VM, args []Value, kw []Kwarg) (Value, error) {
	if err := noKw("int", kw); err != nil {
		return Nil, err
	}
	if err := arity("int", args, 1, 1); err != nil {
		return Nil, err
	}
	v := args[0]
	switch v.K {
	case KInt:
		return v, nil
	case KBool:
		return Int(int64(v.N)), nil
	case KFloat:
		f := math.Trunc(v.AsFloat())
		if math.IsNaN(f) || f < math.MinInt64 || f >= math.MaxInt64 {
			return Nil, errorf("cannot convert %s to int", FormatFloat(v.AsFloat()))
		}
		return Int(int64(f)), nil
	}
	if s, ok := v.AsStr(); ok {
		t := strings.ReplaceAll(strings.TrimSpace(s), "_", "")
		n, err := strconv.ParseInt(t, 10, 64)
		if err != nil {
			return Nil, errorf("cannot convert %s to int", strconv.Quote(s))
		}
		return Int(n), nil
	}
	return Nil, errorf("cannot convert %s to int", TypeName(v))
}

func biFloat(vm *VM, args []Value, kw []Kwarg) (Value, error) {
	if err := noKw("float", kw); err != nil {
		return Nil, err
	}
	if err := arity("float", args, 1, 1); err != nil {
		return Nil, err
	}
	v := args[0]
	switch v.K {
	case KFloat:
		return v, nil
	case KInt:
		return Float(float64(v.AsInt())), nil
	case KBool:
		return Float(float64(v.N)), nil
	}
	if s, ok := v.AsStr(); ok {
		f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
		if err != nil {
			return Nil, errorf("cannot convert %s to float", strconv.Quote(s))
		}
		return Float(f), nil
	}
	return Nil, errorf("cannot convert %s to float", TypeName(v))
}

func biType(vm *VM, args []Value, kw []Kwarg) (Value, error) {
	if err := noKw("type", kw); err != nil {
		return Nil, err
	}
	if err := arity("type", args, 1, 1); err != nil {
		return Nil, err
	}
	return Obj(vm.typeOf(args[0])), nil
}

func biInput(vm *VM, args []Value, kw []Kwarg) (Value, error) {
	if err := noKw("input", kw); err != nil {
		return Nil, err
	}
	if err := arity("input", args, 0, 1); err != nil {
		return Nil, err
	}
	if len(args) == 1 {
		s, err := vm.toStr(args[0])
		if err != nil {
			return Nil, err
		}
		vm.Out.WriteString(s)
	}
	vm.Out.Flush()
	line, err := vm.In.ReadString('\n')
	if err != nil && line == "" {
		return Nil, nil // end of input
	}
	return Str(strings.TrimRight(line, "\r\n")), nil
}

func biAbs(vm *VM, args []Value, kw []Kwarg) (Value, error) {
	if err := noKw("abs", kw); err != nil {
		return Nil, err
	}
	if err := arity("abs", args, 1, 1); err != nil {
		return Nil, err
	}
	v := args[0]
	switch v.K {
	case KInt:
		if v.AsInt() < 0 {
			return Neg(v)
		}
		return v, nil
	case KFloat:
		return Float(math.Abs(v.AsFloat())), nil
	}
	return Nil, errorf("abs() needs a number, not %s", TypeName(v))
}

func minMax(vm *VM, name string, args []Value, kw []Kwarg, wantLess bool) (Value, error) {
	if err := noKw(name, kw); err != nil {
		return Nil, err
	}
	items := args
	if len(args) == 1 {
		l, ok := args[0].O.(*ListObj)
		if !ok {
			return Nil, errorf("%s() needs a list or several values", name)
		}
		items = l.Items
	}
	if len(items) == 0 {
		return Nil, errorf("%s() of an empty list", name)
	}
	best := items[0]
	for _, v := range items[1:] {
		a, b := v, best
		if !wantLess {
			a, b = best, v
		}
		r, err := vm.less(a, b)
		if err != nil {
			return Nil, err
		}
		if r {
			best = v
		}
	}
	return best, nil
}

func biMin(vm *VM, args []Value, kw []Kwarg) (Value, error) { return minMax(vm, "min", args, kw, true) }
func biMax(vm *VM, args []Value, kw []Kwarg) (Value, error) {
	return minMax(vm, "max", args, kw, false)
}

func biBool(vm *VM, args []Value, kw []Kwarg) (Value, error) {
	if err := noKw("bool", kw); err != nil {
		return Nil, err
	}
	if err := arity("bool", args, 1, 1); err != nil {
		return Nil, err
	}
	return Bool(Truthy(args[0])), nil
}

// list() is an empty list; list(x) collects the items of anything iterable.
func biList(vm *VM, args []Value, kw []Kwarg) (Value, error) {
	if err := noKw("list", kw); err != nil {
		return Nil, err
	}
	if err := arity("list", args, 0, 1); err != nil {
		return Nil, err
	}
	if len(args) == 0 {
		return NewList(nil), nil
	}
	it, err := vm.makeIter(args[0])
	if err != nil {
		return Nil, err
	}
	var items []Value
	for {
		v, ok := iterNext(it.O)
		if !ok {
			return NewList(items), nil
		}
		items = append(items, v)
	}
}

// map() is an empty map; map(m) copies a map.
func biMap(vm *VM, args []Value, kw []Kwarg) (Value, error) {
	if err := noKw("map", kw); err != nil {
		return Nil, err
	}
	if err := arity("map", args, 0, 1); err != nil {
		return Nil, err
	}
	if len(args) == 0 {
		return Obj(NewMap()), nil
	}
	m, ok := args[0].O.(*MapObj)
	if !ok {
		return Nil, errorf("map() needs a map to copy, not %s", TypeName(args[0]))
	}
	return Obj(m.Copy()), nil
}

// panic(msg) stops the program with a runtime error. Expected failures are
// returned as result::err instead; panic is for "this cannot happen".
func biPanic(vm *VM, args []Value, kw []Kwarg) (Value, error) {
	if err := noKw("panic", kw); err != nil {
		return Nil, err
	}
	if err := arity("panic", args, 0, 1); err != nil {
		return Nil, err
	}
	msg := "panic"
	if len(args) == 1 {
		s, err := vm.toStr(args[0])
		if err != nil {
			return Nil, err
		}
		msg = "panic: " + s
	}
	return Nil, opError(msg)
}

// range(lo, hi) is lo..hi.
func biRange(vm *VM, args []Value, kw []Kwarg) (Value, error) {
	if err := noKw("range", kw); err != nil {
		return Nil, err
	}
	if err := arity("range", args, 2, 2); err != nil {
		return Nil, err
	}
	return MakeRange(args[0], args[1])
}

func biExit(vm *VM, args []Value, kw []Kwarg) (Value, error) {
	if err := noKw("exit", kw); err != nil {
		return Nil, err
	}
	if err := arity("exit", args, 0, 1); err != nil {
		return Nil, err
	}
	code := 0
	if len(args) == 1 {
		if args[0].K != KInt {
			return Nil, errorf("exit() needs an int code, not %s", TypeName(args[0]))
		}
		code = int(args[0].AsInt())
	}
	return Nil, &ExitError{Code: code}
}
