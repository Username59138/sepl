package vm

import (
	"sort"
	"strings"
	"unicode/utf8"
)

// method is a method of a builtin type.
type method func(vm *VM, recv Value, args []Value) (Value, error)

var (
	strMethods   map[string]method
	listMethods  map[string]method
	mapMethods   map[string]method
	rangeMethods map[string]method
)

// MethodRef is a method name in the constant table, with its index into the
// method tables of builtin types (-1 if no builtin type has it), so calls
// do not look names up in a hash map.
type MethodRef struct {
	Name string
	ID   int
}

func (*MethodRef) TypeName() string { return "method" }

var (
	methodIDs                             map[string]int
	strByID, listByID, mapByID, rangeByID []method
)

// NewMethodRef makes the constant for a method call.
func NewMethodRef(name string) *MethodRef {
	id, ok := methodIDs[name]
	if !ok {
		id = -1
	}
	return &MethodRef{Name: name, ID: id}
}

func buildMethodTables() {
	methodIDs = map[string]int{}
	for _, t := range []map[string]method{strMethods, listMethods, mapMethods, rangeMethods} {
		for name := range t {
			if _, ok := methodIDs[name]; !ok {
				methodIDs[name] = len(methodIDs)
			}
		}
	}
	table := func(m map[string]method) []method {
		out := make([]method, len(methodIDs))
		for name, f := range m {
			out[methodIDs[name]] = f
		}
		return out
	}
	strByID, listByID, mapByID, rangeByID = table(strMethods), table(listMethods), table(mapMethods), table(rangeMethods)
}

// methodsOf returns the method table for the type of v (nil if none).
func methodsOf(v Value) map[string]method {
	switch v.O.(type) {
	case *StrObj:
		return strMethods
	case *ListObj:
		return listMethods
	case *MapObj:
		return mapMethods
	case *RangeObj:
		return rangeMethods
	}
	return nil
}

func methodByID(v Value, id int) method {
	if id < 0 {
		return nil
	}
	switch v.O.(type) {
	case *ListObj:
		return listByID[id]
	case *StrObj:
		return strByID[id]
	case *MapObj:
		return mapByID[id]
	case *RangeObj:
		return rangeByID[id]
	}
	return nil
}

func hasMethod(v Value, name string) bool {
	if name == "to_str" {
		return true
	}
	_, ok := methodsOf(v)[name]
	return ok
}

// callMethod calls recv.name(args).
func (vm *VM) callMethod(recv Value, ref *MethodRef, args []Value, kw []Kwarg) (Value, error) {
	name := ref.Name
	if len(kw) > 0 {
		return Nil, errorf("%s() has no parameter named '%s'", name, kw[0].Name)
	}
	if m := methodByID(recv, ref.ID); m != nil {
		return m(vm, recv, args)
	}
	if name == "to_str" {
		if err := arity("to_str", args, 0, 0); err != nil {
			return Nil, err
		}
		s, err := vm.toStr(recv)
		return Str(s), err
	}
	return Nil, errorf("%s has no method '%s'", TypeName(recv), name)
}

// sliceBounds turns optional (i, j) arguments into [i, j) within [0, n),
// counting negative indexes from the end and clamping like Python.
func sliceBounds(fn string, args []Value, n int) (int, int, error) {
	if err := arity(fn, args, 1, 2); err != nil {
		return 0, 0, err
	}
	get := func(v Value, def int) (int, error) {
		if v.K == KNil {
			return def, nil
		}
		if v.K != KInt {
			return 0, errorf("%s() needs int indexes, not %s", fn, TypeName(v))
		}
		i := v.AsInt()
		if i < 0 {
			i += int64(n)
		}
		if i < 0 {
			i = 0
		}
		if i > int64(n) {
			i = int64(n)
		}
		return int(i), nil
	}
	i, err := get(args[0], 0)
	if err != nil {
		return 0, 0, err
	}
	j := n
	if len(args) == 2 {
		if j, err = get(args[1], n); err != nil {
			return 0, 0, err
		}
	}
	if j < i {
		j = i
	}
	return i, j, nil
}

func strArg(fn string, v Value) (string, error) {
	s, ok := v.AsStr()
	if !ok {
		return "", errorf("%s() needs a str, not %s", fn, TypeName(v))
	}
	return s, nil
}

func init() {
	strMethods = map[string]method{
		"len": func(vm *VM, r Value, a []Value) (Value, error) {
			if err := arity("len", a, 0, 0); err != nil {
				return Nil, err
			}
			return Int(int64(r.O.(*StrObj).Len())), nil
		},
		"upper": strFn("upper", strings.ToUpper),
		"lower": strFn("lower", strings.ToLower),
		"trim":  strFn("trim", strings.TrimSpace),
		"split": func(vm *VM, r Value, a []Value) (Value, error) {
			if err := arity("split", a, 0, 1); err != nil {
				return Nil, err
			}
			s := r.O.(*StrObj).S
			var parts []string
			if len(a) == 0 {
				parts = strings.Fields(s)
			} else {
				sep, err := strArg("split", a[0])
				if err != nil {
					return Nil, err
				}
				if sep == "" {
					return Nil, opError("split() separator cannot be empty")
				}
				parts = strings.Split(s, sep)
			}
			items := make([]Value, len(parts))
			for i, p := range parts {
				items[i] = Str(p)
			}
			return NewList(items), nil
		},
		"contains":    strPred("contains", strings.Contains),
		"starts_with": strPred("starts_with", strings.HasPrefix),
		"ends_with":   strPred("ends_with", strings.HasSuffix),
		"find": func(vm *VM, r Value, a []Value) (Value, error) {
			if err := arity("find", a, 1, 1); err != nil {
				return Nil, err
			}
			sub, err := strArg("find", a[0])
			if err != nil {
				return Nil, err
			}
			s := r.O.(*StrObj).S
			i := strings.Index(s, sub)
			if i < 0 {
				return Int(-1), nil
			}
			return Int(int64(utf8.RuneCountInString(s[:i]))), nil
		},
		"replace": func(vm *VM, r Value, a []Value) (Value, error) {
			if err := arity("replace", a, 2, 2); err != nil {
				return Nil, err
			}
			old, err := strArg("replace", a[0])
			if err != nil {
				return Nil, err
			}
			nw, err := strArg("replace", a[1])
			if err != nil {
				return Nil, err
			}
			return Str(strings.ReplaceAll(r.O.(*StrObj).S, old, nw)), nil
		},
		"repeat": func(vm *VM, r Value, a []Value) (Value, error) {
			if err := arity("repeat", a, 1, 1); err != nil {
				return Nil, err
			}
			if a[0].K != KInt || a[0].AsInt() < 0 {
				return Nil, opError("repeat() needs a non-negative int")
			}
			return Str(strings.Repeat(r.O.(*StrObj).S, int(a[0].AsInt()))), nil
		},
		"chars": func(vm *VM, r Value, a []Value) (Value, error) {
			if err := arity("chars", a, 0, 0); err != nil {
				return Nil, err
			}
			var items []Value
			for _, c := range r.O.(*StrObj).S {
				items = append(items, Str(string(c)))
			}
			return NewList(items), nil
		},
		"slice": func(vm *VM, r Value, a []Value) (Value, error) {
			s := r.O.(*StrObj)
			i, j, err := sliceBounds("slice", a, s.Len())
			if err != nil {
				return Nil, err
			}
			return Str(runeSlice(s.S, i, j)), nil
		},
	}

	listMethods = map[string]method{
		"len": func(vm *VM, r Value, a []Value) (Value, error) {
			if err := arity("len", a, 0, 0); err != nil {
				return Nil, err
			}
			return Int(int64(len(r.O.(*ListObj).Items))), nil
		},
		"push": func(vm *VM, r Value, a []Value) (Value, error) {
			if err := arity("push", a, 1, 1<<20); err != nil {
				return Nil, err
			}
			l := r.O.(*ListObj)
			if n := len(l.Items) + len(a); n > cap(l.Items) {
				// Double the capacity: fewer copies than append's growth.
				bigger := make([]Value, len(l.Items), 2*n+4)
				copy(bigger, l.Items)
				l.Items = bigger
			}
			l.Items = append(l.Items, a...)
			return Nil, nil
		},
		"pop": func(vm *VM, r Value, a []Value) (Value, error) {
			if err := arity("pop", a, 0, 1); err != nil {
				return Nil, err
			}
			l := r.O.(*ListObj)
			if len(l.Items) == 0 {
				return Nil, opError("pop() from an empty list")
			}
			i := len(l.Items) - 1
			if len(a) == 1 {
				n, err := normIndex(a[0], len(l.Items), "list")
				if err != nil {
					return Nil, err
				}
				i = n
			}
			v := l.Items[i]
			l.Items = append(l.Items[:i], l.Items[i+1:]...)
			return v, nil
		},
		"insert": func(vm *VM, r Value, a []Value) (Value, error) {
			if err := arity("insert", a, 2, 2); err != nil {
				return Nil, err
			}
			l := r.O.(*ListObj)
			if a[0].K != KInt {
				return Nil, errorf("insert() needs an int index, not %s", TypeName(a[0]))
			}
			i := a[0].AsInt()
			if i < 0 {
				i += int64(len(l.Items))
			}
			if i < 0 || i > int64(len(l.Items)) {
				return Nil, errorf("index %d is out of range for insert into a list of length %d", a[0].AsInt(), len(l.Items))
			}
			l.Items = append(l.Items, Nil)
			copy(l.Items[i+1:], l.Items[i:])
			l.Items[i] = a[1]
			return Nil, nil
		},
		"contains": func(vm *VM, r Value, a []Value) (Value, error) {
			if err := arity("contains", a, 1, 1); err != nil {
				return Nil, err
			}
			ok, err := vm.contains(r, a[0])
			return Bool(ok), err
		},
		"index_of": func(vm *VM, r Value, a []Value) (Value, error) {
			if err := arity("index_of", a, 1, 1); err != nil {
				return Nil, err
			}
			for i, v := range r.O.(*ListObj).Items {
				eq, err := vm.equal(v, a[0])
				if err != nil {
					return Nil, err
				}
				if eq {
					return Int(int64(i)), nil
				}
			}
			return Int(-1), nil
		},
		"reverse": func(vm *VM, r Value, a []Value) (Value, error) {
			if err := arity("reverse", a, 0, 0); err != nil {
				return Nil, err
			}
			items := r.O.(*ListObj).Items
			for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
				items[i], items[j] = items[j], items[i]
			}
			return Nil, nil
		},
		// sort() sorts in place; sort(key) sorts by key(item).
		"sort": func(vm *VM, r Value, a []Value) (Value, error) {
			if err := arity("sort", a, 0, 1); err != nil {
				return Nil, err
			}
			l := r.O.(*ListObj)
			if len(a) == 0 {
				return Nil, vm.sortValues(l.Items)
			}
			keys := make([]Value, len(l.Items))
			for i, x := range l.Items {
				k, err := vm.Call(a[0], []Value{x})
				if err != nil {
					return Nil, err
				}
				keys[i] = k
			}
			idx := make([]int, len(keys))
			for i := range idx {
				idx[i] = i
			}
			var err error
			sort.SliceStable(idx, func(i, j int) bool {
				if err != nil {
					return false
				}
				lt, e := vm.less(keys[idx[i]], keys[idx[j]])
				if e != nil {
					err = e
				}
				return lt
			})
			if err != nil {
				return Nil, err
			}
			sorted := make([]Value, len(idx))
			for i, k := range idx {
				sorted[i] = l.Items[k]
			}
			copy(l.Items, sorted)
			return Nil, nil
		},
		"join": func(vm *VM, r Value, a []Value) (Value, error) {
			if err := arity("join", a, 1, 1); err != nil {
				return Nil, err
			}
			sep, err := strArg("join", a[0])
			if err != nil {
				return Nil, err
			}
			parts := make([]string, 0, len(r.O.(*ListObj).Items))
			for _, v := range r.O.(*ListObj).Items {
				s, err := vm.toStr(v)
				if err != nil {
					return Nil, err
				}
				parts = append(parts, s)
			}
			return Str(strings.Join(parts, sep)), nil
		},
		"clear": func(vm *VM, r Value, a []Value) (Value, error) {
			if err := arity("clear", a, 0, 0); err != nil {
				return Nil, err
			}
			r.O.(*ListObj).Items = nil
			return Nil, nil
		},
		"copy": func(vm *VM, r Value, a []Value) (Value, error) {
			if err := arity("copy", a, 0, 0); err != nil {
				return Nil, err
			}
			return NewList(append([]Value(nil), r.O.(*ListObj).Items...)), nil
		},
		"slice": func(vm *VM, r Value, a []Value) (Value, error) {
			items := r.O.(*ListObj).Items
			i, j, err := sliceBounds("slice", a, len(items))
			if err != nil {
				return Nil, err
			}
			return NewList(append([]Value(nil), items[i:j]...)), nil
		},
	}

	mapMethods = map[string]method{
		"len": func(vm *VM, r Value, a []Value) (Value, error) {
			if err := arity("len", a, 0, 0); err != nil {
				return Nil, err
			}
			return Int(int64(r.O.(*MapObj).Len())), nil
		},
		"keys": func(vm *VM, r Value, a []Value) (Value, error) {
			if err := arity("keys", a, 0, 0); err != nil {
				return Nil, err
			}
			return NewList(append([]Value(nil), r.O.(*MapObj).keys...)), nil
		},
		"values": func(vm *VM, r Value, a []Value) (Value, error) {
			if err := arity("values", a, 0, 0); err != nil {
				return Nil, err
			}
			return NewList(append([]Value(nil), r.O.(*MapObj).vals...)), nil
		},
		"contains": func(vm *VM, r Value, a []Value) (Value, error) {
			if err := arity("contains", a, 1, 1); err != nil {
				return Nil, err
			}
			_, ok := r.O.(*MapObj).Get(a[0])
			return Bool(ok), nil
		},
		"get": func(vm *VM, r Value, a []Value) (Value, error) {
			if err := arity("get", a, 1, 2); err != nil {
				return Nil, err
			}
			if v, ok := r.O.(*MapObj).Get(a[0]); ok {
				return v, nil
			}
			if len(a) == 2 {
				return a[1], nil
			}
			return Nil, nil
		},
		"remove": func(vm *VM, r Value, a []Value) (Value, error) {
			if err := arity("remove", a, 1, 1); err != nil {
				return Nil, err
			}
			v, _ := r.O.(*MapObj).Delete(a[0])
			return v, nil
		},
		"clear": func(vm *VM, r Value, a []Value) (Value, error) {
			if err := arity("clear", a, 0, 0); err != nil {
				return Nil, err
			}
			r.O.(*MapObj).Clear()
			return Nil, nil
		},
		"copy": func(vm *VM, r Value, a []Value) (Value, error) {
			if err := arity("copy", a, 0, 0); err != nil {
				return Nil, err
			}
			return Obj(r.O.(*MapObj).Copy()), nil
		},
	}

	rangeMethods = map[string]method{
		"len": func(vm *VM, r Value, a []Value) (Value, error) {
			if err := arity("len", a, 0, 0); err != nil {
				return Nil, err
			}
			return Int(r.O.(*RangeObj).Len()), nil
		},
		"contains": func(vm *VM, r Value, a []Value) (Value, error) {
			if err := arity("contains", a, 1, 1); err != nil {
				return Nil, err
			}
			ok, _ := Contains(r, a[0])
			return Bool(ok), nil
		},
		"to_list": func(vm *VM, r Value, a []Value) (Value, error) {
			if err := arity("to_list", a, 0, 0); err != nil {
				return Nil, err
			}
			rg := r.O.(*RangeObj)
			if rg.Len() > 1<<26 {
				return Nil, opError("range is too large to turn into a list")
			}
			items := make([]Value, 0, rg.Len())
			for i := rg.Lo; i < rg.Hi; i++ {
				items = append(items, Int(i))
			}
			return NewList(items), nil
		},
	}
	addCollectionMethods()
	buildMethodTables()
}

func strFn(name string, f func(string) string) method {
	return func(vm *VM, r Value, a []Value) (Value, error) {
		if err := arity(name, a, 0, 0); err != nil {
			return Nil, err
		}
		return Str(f(r.O.(*StrObj).S)), nil
	}
}

func strPred(name string, f func(string, string) bool) method {
	return func(vm *VM, r Value, a []Value) (Value, error) {
		if err := arity(name, a, 1, 1); err != nil {
			return Nil, err
		}
		s, err := strArg(name, a[0])
		if err != nil {
			return Nil, err
		}
		return Bool(f(r.O.(*StrObj).S, s)), nil
	}
}
