package vm

import (
	"strings"
)

// Operators and protocols are methods: a + b calls a.plus(b), a == b calls
// a.eq(b), print calls to_str, and so on. Builtin values use Go code unless
// SEPL code has added such a method to their type.

var opMethodName = map[Op]string{
	OpAdd: "plus", OpSub: "minus", OpMul: "mul", OpDiv: "div", OpIDiv: "idiv", OpMod: "mod",
	OpAddI: "plus", OpSubI: "minus", OpMulI: "mul", OpModI: "mod",
}

var immediateBase = map[Op]Op{OpAddI: OpAdd, OpSubI: OpSub, OpMulI: OpMul, OpModI: OpMod}

// builtinTypeOf returns the type object of a builtin value (nil for others).
func (vm *VM) builtinTypeOf(v Value) *TypeObj {
	switch v.K {
	case KInt:
		return vm.tInt
	case KFloat:
		return vm.tFloat
	case KBool:
		return vm.tBool
	case KObj:
		switch v.O.(type) {
		case *StrObj:
			return vm.tStr
		case *ListObj:
			return vm.tList
		case *MapObj:
			return vm.tMap
		case *RangeObj:
			return vm.tRange
		}
	}
	return nil
}

// userMethod returns the SEPL method name of v's type, or nil.
func (vm *VM) userMethod(v Value, name string) (*MethodInfo, error) {
	var t *TypeObj
	switch o := v.O.(type) {
	case *Instance:
		t = o.Type
	case *EnumValue:
		t = o.Type
	default:
		if t = vm.builtinTypeOf(v); t == nil || !t.extended {
			return nil, nil
		}
	}
	m, err := vm.lookup(t, name)
	if err != nil || m == nil || m.Static {
		return nil, err
	}
	if m.abstract() {
		return nil, errorf("method '%s' of %s has no body (it is required by %s)", name, t.Name, m.Origin.Name)
	}
	return m, nil
}

func (vm *VM) callUser(m *MethodInfo, recv Value, args ...Value) (Value, error) {
	all := make([]Value, 0, 1+len(args))
	all = append(append(all, recv), args...)
	return vm.Call(m.Fn, all)
}

func mayHaveMethods(vm *VM, v Value) bool { return v.K == KObj || vm.numOps }

// arith implements + - * / // % (also the immediate forms).
func (vm *VM) arith(op Op, a, b Value) (Value, error) {
	if base, ok := immediateBase[op]; ok {
		op = base
	}
	if mayHaveMethods(vm, a) {
		m, err := vm.userMethod(a, opMethodName[op])
		if err != nil {
			return Nil, err
		}
		if m != nil {
			return vm.callUser(m, a, b)
		}
	}
	if op == OpAdd {
		return Add(a, b)
	}
	return Arith(op, a, b)
}

func (vm *VM) neg(v Value) (Value, error) {
	if mayHaveMethods(vm, v) {
		m, err := vm.userMethod(v, "neg")
		if err != nil {
			return Nil, err
		}
		if m != nil {
			return vm.callUser(m, v)
		}
	}
	return Neg(v)
}

// equal implements ==: the eq method if there is one, otherwise structural
// equality (objects compare field by field).
func (vm *VM) equal(a, b Value) (bool, error) {
	if mayHaveMethods(vm, a) {
		m, err := vm.userMethod(a, "eq")
		if err != nil {
			return false, err
		}
		if m != nil {
			r, err := vm.callUser(m, a, b)
			return Truthy(r), err
		}
	}
	return Equal(a, b), nil
}

// less implements a < b, used by sort.
func (vm *VM) less(a, b Value) (bool, error) {
	return vm.compare(OpLt, a, b)
}

// compare implements < <= > >=. Types with an lt method get all four:
// a > b is b.lt(a), a <= b is not b.lt(a), a >= b is not a.lt(b).
func (vm *VM) compare(op Op, a, b Value) (bool, error) {
	if mayHaveMethods(vm, a) || mayHaveMethods(vm, b) {
		recv, arg := a, b
		if op == OpGt || op == OpLe {
			recv, arg = b, a
		}
		m, err := vm.userMethod(recv, "lt")
		if err != nil {
			return false, err
		}
		if m != nil {
			r, err := vm.callUser(m, recv, arg)
			if err != nil {
				return false, err
			}
			if op == OpLt || op == OpGt {
				return Truthy(r), nil
			}
			return !Truthy(r), nil
		}
	}
	c, err := Compare(a, b, op.Symbol())
	if err != nil {
		return false, err
	}
	switch op {
	case OpLt:
		return c < 0, nil
	case OpLe:
		return c <= 0, nil
	case OpGt:
		return c > 0, nil
	}
	return c >= 0, nil
}

func (vm *VM) contains(c, x Value) (bool, error) {
	if c.K == KObj {
		m, err := vm.userMethod(c, "contains")
		if err != nil {
			return false, err
		}
		if m != nil {
			r, err := vm.callUser(m, c, x)
			return Truthy(r), err
		}
		if l, ok := c.O.(*ListObj); ok {
			for _, v := range l.Items {
				eq, err := vm.equal(v, x)
				if err != nil || eq {
					return eq, err
				}
			}
			return false, nil
		}
		if _, ok := c.O.(*Instance); ok {
			return false, errorf("cannot use 'in' with %s: it has no method contains", TypeName(c))
		}
	}
	return Contains(c, x)
}

func (vm *VM) index(c, i Value) (Value, error) {
	if c.K == KObj {
		if _, ok := c.O.(*ListObj); !ok || vm.tList.extended {
			m, err := vm.userMethod(c, "index")
			if err != nil {
				return Nil, err
			}
			if m != nil {
				return vm.callUser(m, c, i)
			}
		}
	}
	return Index(c, i)
}

func (vm *VM) setIndex(c, i, v Value) error {
	if c.K == KObj {
		m, err := vm.userMethod(c, "set_index")
		if err != nil {
			return err
		}
		if m != nil {
			_, err := vm.callUser(m, c, i, v)
			return err
		}
	}
	return SetIndex(c, i, v)
}

func (vm *VM) length(v Value) (Value, error) {
	m, err := vm.userMethod(v, "len")
	if err != nil {
		return Nil, err
	}
	if m != nil {
		return vm.callUser(m, v)
	}
	return lengthOf(v)
}

func (vm *VM) makeIter(v Value) (Value, error) {
	if v.K == KObj {
		m, err := vm.userMethod(v, "iter")
		if err != nil {
			return Nil, err
		}
		if m != nil {
			r, err := vm.callUser(m, v)
			if err != nil {
				return Nil, err
			}
			if _, ok := r.O.(*Instance); ok {
				return Nil, errorf("iter() of %s must return a list, str, map or range, not %s", TypeName(v), TypeName(r))
			}
			v = r
		}
	}
	return makeIter(v)
}

// toStr converts a value to the text that print shows.
func (vm *VM) toStr(v Value) (string, error) {
	if s, ok := v.O.(*StrObj); ok && !vm.tStr.extended {
		return s.S, nil
	}
	if s, ok, err := vm.userToStr(v); ok || err != nil {
		return s, err
	}
	if s, ok := v.O.(*StrObj); ok {
		return s.S, nil
	}
	return vm.repr(v)
}

// repr renders a value the way it looks inside a list, using to_str methods.
func (vm *VM) repr(v Value) (string, error) {
	var b strings.Builder
	err := writeRepr(&b, v, nil, vm.userToStr)
	return b.String(), err
}

// userToStr calls a SEPL to_str method if v's type has one.
func (vm *VM) userToStr(v Value) (string, bool, error) {
	if !mayHaveMethods(vm, v) && !vm.tBool.extended {
		return "", false, nil
	}
	m, err := vm.userMethod(v, "to_str")
	if err != nil || m == nil {
		return "", false, err
	}
	r, err := vm.callUser(m, v)
	if err != nil {
		return "", true, err
	}
	s, ok := r.AsStr()
	if !ok {
		return "", true, errorf("to_str() of %s must return a str, not %s", TypeName(v), TypeName(r))
	}
	return s, true, nil
}

// ReprValue renders a value for the REPL.
func (vm *VM) ReprValue(v Value) string {
	s, err := vm.repr(v)
	if err != nil {
		return Repr(v)
	}
	return s
}

// ---------------------------------------------------------------- calls

// enterClosure starts a call of cl whose nargs arguments are in the slots
// from first on; the result will go to slot ret.
func (vm *VM) enterClosure(cl *Closure, first, nargs int, kw KwNames, ret int) error {
	p := cl.Proto
	if kw != nil || nargs != p.NParams || p.Variadic {
		vals, err := bindArgs(p, vm.stack[first:first+nargs], kw)
		if err != nil {
			return err
		}
		copy(vm.stack[first:], vals)
		vm.sp = first + len(vals)
	}
	if len(vm.frames) == cap(vm.frames) || !vm.ensureStack(first+p.MaxStack+1) {
		return opError("stack overflow: too many nested calls (endless recursion?)")
	}
	vm.frames = append(vm.frames, frame{cl: cl, base: first, ret: ret})
	return nil
}

// invoke calls recv.name(args); the receiver is in slot recv and the result
// goes there. It reports whether a SEPL function was entered (a new frame).
func (vm *VM) invoke(recv int, ref *MethodRef, argc int, kw KwNames) (bool, error) {
	rv := vm.stack[recv]
	name := ref.Name
	vm.sp = recv + 1 + argc

	var t *TypeObj
	switch o := rv.O.(type) {
	case *Instance:
		t = o.Type
	case *EnumValue:
		t = o.Type
	case *TypeObj:
		// Foo.method(...): the method as a plain function (self passed explicitly).
		fn, err := vm.scope(rv, name)
		if err != nil {
			return false, err
		}
		vm.stack[recv] = fn
		return vm.callValue(recv, argc, kw)
	case *ModuleObj:
		return false, errorf("use %s::%s(...) to call a function of a module", o.Name, name)
	default:
		if bt := vm.builtinTypeOf(rv); bt != nil && bt.extended {
			t = bt
		}
	}

	if t != nil {
		m, err := vm.lookup(t, name)
		if err != nil {
			return false, err
		}
		if m != nil {
			if m.abstract() {
				return false, errorf("method '%s' of %s has no body (it is required by %s)", name, t.Name, m.Origin.Name)
			}
			if m.Static {
				return false, errorf("%s is a static method: call it as %s::%s(...)", name, t.Name, name)
			}
			cl, ok := m.Fn.O.(*Closure)
			if !ok {
				return false, errorf("method '%s' of %s is not a function", name, t.Name)
			}
			return true, vm.enterClosure(cl, recv, argc+1, kw, recv)
		}
		if inst, ok := rv.O.(*Instance); ok {
			if i, ok := inst.Type.fieldIdx[name]; ok {
				// A field that holds a function.
				v, err := vm.getField(rv, inst.Type.Fields[i].Name)
				if err != nil {
					return false, err
				}
				vm.stack[recv] = v
				return vm.callValue(recv, argc, kw)
			}
			if name != "to_str" {
				return false, errorf("%s has no method '%s'", inst.Type.Name, name)
			}
		}
	}

	// A method implemented in Go.
	npos := argc - len(kw)
	var kwargs []Kwarg
	for i, n := range kw {
		kwargs = append(kwargs, Kwarg{Name: n, Value: vm.stack[recv+1+npos+i]})
	}
	v, err := vm.callMethod(rv, ref, vm.stack[recv+1:recv+1+npos], kwargs)
	if err != nil {
		return false, err
	}
	vm.stack[recv] = v
	vm.sp = recv + 1
	return false, nil
}
