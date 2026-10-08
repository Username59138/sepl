// Package vm holds SEPL runtime values, the bytecode format and the virtual
// machine that runs it.
package vm

import (
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Kind is the type tag of a Value. Small values (nil, bool, int, float) live
// inside the Value itself; everything else is a heap Object.
type Kind uint8

const (
	KNil Kind = iota
	KBool
	KInt
	KFloat
	KObj
	kUndef // a global whose definition has not run yet
)

// Value is a SEPL value. It is a small struct rather than an interface so
// that numbers never allocate.
type Value struct {
	K Kind
	N uint64 // bool (0/1), int (two's complement), float (IEEE bits)
	O Object // heap object when K == KObj
}

// Object is a heap-allocated value.
type Object interface {
	TypeName() string
}

var (
	Nil   = Value{}
	True  = Value{K: KBool, N: 1}
	False = Value{K: KBool}
	undef = Value{K: kUndef}
)

func Bool(b bool) Value {
	if b {
		return True
	}
	return False
}

func Int(i int64) Value     { return Value{K: KInt, N: uint64(i)} }
func Float(f float64) Value { return Value{K: KFloat, N: math.Float64bits(f)} }
func Str(s string) Value    { return Value{K: KObj, O: &StrObj{S: s, runes: -1}} }
func Obj(o Object) Value    { return Value{K: KObj, O: o} }

func NewList(items []Value) Value { return Obj(&ListObj{Items: items}) }

func (v Value) AsInt() int64     { return int64(v.N) }
func (v Value) AsFloat() float64 { return math.Float64frombits(v.N) }
func (v Value) IsNil() bool      { return v.K == KNil }

// AsStr returns the Go string of a str value.
func (v Value) AsStr() (string, bool) {
	if s, ok := v.O.(*StrObj); ok {
		return s.S, true
	}
	return "", false
}

func isNum(v Value) bool { return v.K == KInt || v.K == KFloat }

func toFloat(v Value) float64 {
	if v.K == KInt {
		return float64(int64(v.N))
	}
	return v.AsFloat()
}

// ---------------------------------------------------------------- objects

// StrObj is an immutable string.
type StrObj struct {
	S     string
	runes int // cached rune count, -1 if unknown
}

func (*StrObj) TypeName() string { return "str" }

// Len returns the number of characters.
func (s *StrObj) Len() int {
	if s.runes < 0 {
		s.runes = utf8.RuneCountInString(s.S)
	}
	return s.runes
}

// ListObj is a mutable list.
type ListObj struct {
	Items []Value
}

func (*ListObj) TypeName() string { return "list" }

// RangeObj is lo..hi: lo included, hi excluded.
type RangeObj struct {
	Lo, Hi int64
}

func (*RangeObj) TypeName() string { return "range" }

func (r *RangeObj) Len() int64 {
	if r.Hi <= r.Lo {
		return 0
	}
	return r.Hi - r.Lo
}

// Closure is a function together with the variables it captured.
type Closure struct {
	Proto  *Proto
	Upvals []*Upvalue
}

func (*Closure) TypeName() string { return "fn" }

// Upvalue is a captured variable. While the variable is still on the stack
// (open) it lives in stack slot; when its scope ends the value moves into
// closed.
type Upvalue struct {
	open   bool
	slot   int
	closed Value
	next   *Upvalue
}

// Builtin is a function implemented in Go.
type Builtin struct {
	Name string
	Fn   func(vm *VM, args []Value, kw []Kwarg) (Value, error)
}

func (*Builtin) TypeName() string { return "fn" }

// Kwarg is a named argument passed to a builtin.
type Kwarg struct {
	Name  string
	Value Value
}

// KwNames is the list of argument names of a call with named arguments; it
// lives in the constant table.
type KwNames []string

func (KwNames) TypeName() string { return "names" }

// ---------------------------------------------------------------- semantics

// TypeName returns the SEPL type name of v.
func TypeName(v Value) string {
	switch v.K {
	case KNil:
		return "nil"
	case KBool:
		return "bool"
	case KInt:
		return "int"
	case KFloat:
		return "float"
	case kUndef:
		return "undefined"
	}
	return v.O.TypeName()
}

// Truthy implements SEPL truthiness: false, nil, 0, 0.0, "", [], {} and an
// empty range are false; everything else is true.
func Truthy(v Value) bool {
	switch v.K {
	case KNil:
		return false
	case KBool, KInt:
		return v.N != 0
	case KFloat:
		return v.AsFloat() != 0
	}
	switch o := v.O.(type) {
	case *StrObj:
		return o.S != ""
	case *ListObj:
		return len(o.Items) > 0
	case *MapObj:
		return o.Len() > 0
	case *RangeObj:
		return o.Hi > o.Lo
	}
	return true
}

// MaxStrBytes is the largest string that methods building a string of a
// requested size (repeat, pad_left, pad_right) agree to make: a size typed
// by mistake becomes a runtime error instead of exhausting memory.
const MaxStrBytes = 1 << 28

// Equal reports whether a == b. Numbers compare by value across int and
// float, lists and maps compare element by element, other objects by identity.
// Values that contain themselves compare without endless recursion: a pair
// of containers already being compared further up counts as equal, so the
// answer is decided by the rest of their contents.
func Equal(a, b Value) bool {
	return equal(a, b, 0, nil)
}

// eqTrackDepth is how deep equal goes before it starts remembering the pairs
// it is comparing; shallow comparisons, the usual case, never allocate.
const eqTrackDepth = 32

type eqPair struct{ x, y any }

func equal(a, b Value, depth int, seen map[eqPair]bool) bool {
	if a.K == KInt && b.K == KInt {
		return a.N == b.N
	}
	if isNum(a) && isNum(b) {
		return toFloat(a) == toFloat(b)
	}
	if a.K != b.K {
		return false
	}
	switch a.K {
	case KNil:
		return true
	case KBool:
		return a.N == b.N
	}
	switch x := a.O.(type) {
	case *StrObj:
		y, ok := b.O.(*StrObj)
		return ok && x.S == y.S
	case *ListObj:
		y, ok := b.O.(*ListObj)
		if !ok || len(x.Items) != len(y.Items) {
			return false
		}
		if x == y {
			return true
		}
		if depth++; depth > eqTrackDepth {
			if seen == nil {
				seen = map[eqPair]bool{}
			}
			if seen[eqPair{x, y}] {
				return true
			}
			seen[eqPair{x, y}] = true
		}
		for i := range x.Items {
			if !equal(x.Items[i], y.Items[i], depth, seen) {
				return false
			}
		}
		return true
	case *MapObj:
		y, ok := b.O.(*MapObj)
		if !ok || x.Len() != y.Len() {
			return false
		}
		if x == y {
			return true
		}
		if depth++; depth > eqTrackDepth {
			if seen == nil {
				seen = map[eqPair]bool{}
			}
			if seen[eqPair{x, y}] {
				return true
			}
			seen[eqPair{x, y}] = true
		}
		for i, k := range x.keys {
			w, found := y.Get(k)
			if !found || !equal(x.vals[i], w, depth, seen) {
				return false
			}
		}
		return true
	case *RangeObj:
		y, ok := b.O.(*RangeObj)
		return ok && *x == *y
	case *Instance:
		y, ok := b.O.(*Instance)
		if !ok || x.Type != y.Type || len(x.Fields) != len(y.Fields) {
			return false
		}
		if x == y {
			return true
		}
		if depth++; depth > eqTrackDepth {
			if seen == nil {
				seen = map[eqPair]bool{}
			}
			if seen[eqPair{x, y}] {
				return true
			}
			seen[eqPair{x, y}] = true
		}
		for i := range x.Fields {
			if !equal(x.Fields[i], y.Fields[i], depth, seen) {
				return false
			}
		}
		return true
	case *EnumValue:
		y, ok := b.O.(*EnumValue)
		if !ok || x.Variant != y.Variant || len(x.Fields) != len(y.Fields) {
			return false
		}
		if depth++; depth > eqTrackDepth {
			if seen == nil {
				seen = map[eqPair]bool{}
			}
			if seen[eqPair{x, y}] {
				return true
			}
			seen[eqPair{x, y}] = true
		}
		for i := range x.Fields {
			if !equal(x.Fields[i], y.Fields[i], depth, seen) {
				return false
			}
		}
		return true
	case *VariantCtor:
		y, ok := b.O.(*VariantCtor)
		return ok && x.Variant == y.Variant
	case *BoundMethod:
		y, ok := b.O.(*BoundMethod)
		return ok && x.Name == y.Name && x.Fn == y.Fn && equal(x.Recv, y.Recv, depth, seen)
	}
	return a.O == b.O
}

// ToStr converts a value to the text that print shows.
func ToStr(v Value) string {
	if s, ok := v.O.(*StrObj); ok {
		return s.S
	}
	return Repr(v)
}

// Repr converts a value to text, quoting strings: this is how values look
// inside lists and maps, and in the REPL.
func Repr(v Value) string {
	var b strings.Builder
	writeRepr(&b, v, nil, nil)
	return b.String()
}

// reprHook lets the VM render values with a SEPL to_str method.
type reprHook func(Value) (string, bool, error)

func writeRepr(b *strings.Builder, v Value, seen map[Object]bool, hook reprHook) error {
	if hook != nil {
		if s, ok, err := hook(v); err != nil {
			return err
		} else if ok {
			b.WriteString(s)
			return nil
		}
	}
	switch v.K {
	case KNil:
		b.WriteString("nil")
		return nil
	case KBool:
		if v.N != 0 {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
		return nil
	case KInt:
		b.WriteString(strconv.FormatInt(int64(v.N), 10))
		return nil
	case KFloat:
		b.WriteString(FormatFloat(v.AsFloat()))
		return nil
	case kUndef:
		b.WriteString("<undefined>")
		return nil
	}
	switch o := v.O.(type) {
	case *StrObj:
		b.WriteString(strconv.Quote(o.S))
	case *ListObj:
		if seen[o] {
			b.WriteString("[...]")
			return nil
		}
		if seen == nil {
			seen = map[Object]bool{}
		}
		seen[o] = true
		b.WriteByte('[')
		for i, x := range o.Items {
			if i > 0 {
				b.WriteString(", ")
			}
			if err := writeRepr(b, x, seen, hook); err != nil {
				return err
			}
		}
		b.WriteByte(']')
		delete(seen, o)
	case *MapObj:
		if seen[o] {
			b.WriteString("{...}")
			return nil
		}
		if seen == nil {
			seen = map[Object]bool{}
		}
		seen[o] = true
		b.WriteByte('{')
		for i, k := range o.keys {
			if i > 0 {
				b.WriteString(", ")
			}
			if err := writeRepr(b, k, seen, hook); err != nil {
				return err
			}
			b.WriteString(": ")
			if err := writeRepr(b, o.vals[i], seen, hook); err != nil {
				return err
			}
		}
		b.WriteByte('}')
		delete(seen, o)
	case *RangeObj:
		b.WriteString(strconv.FormatInt(o.Lo, 10) + ".." + strconv.FormatInt(o.Hi, 10))
	case *Closure:
		b.WriteString("<fn " + o.Proto.Name + ">")
	case *Builtin:
		b.WriteString("<builtin " + o.Name + ">")
	case *Instance:
		if seen[o] {
			b.WriteString(o.Type.Name + "(...)")
			return nil
		}
		if seen == nil {
			seen = map[Object]bool{}
		}
		seen[o] = true
		err := instanceRepr(b, o, func(f Value) error { return writeRepr(b, f, seen, hook) })
		delete(seen, o)
		return err
	case *EnumValue:
		return enumRepr(b, o, func(f Value) error { return writeRepr(b, f, seen, hook) })
	case *VariantCtor:
		b.WriteString("<variant " + o.Type.Name + "::" + o.Variant.Name + ">")
	case *TypeObj:
		b.WriteString("<" + o.kind() + " " + o.Name + ">")
	case *BoundMethod:
		b.WriteString("<method " + o.Name + ">")
	case *ModuleObj:
		b.WriteString("<module " + o.Name + ">")
	default:
		b.WriteString("<" + o.TypeName() + ">")
	}
	return nil
}

// FormatFloat formats like Python: 3.5, 2.0, 1e+20, 1.5e-07.
func FormatFloat(f float64) string {
	switch {
	case math.IsInf(f, 1):
		return "inf"
	case math.IsInf(f, -1):
		return "-inf"
	case math.IsNaN(f):
		return "nan"
	}
	a := math.Abs(f)
	var s string
	if a == 0 || (a >= 1e-4 && a < 1e16) {
		s = strconv.FormatFloat(f, 'f', -1, 64)
	} else {
		s = strconv.FormatFloat(f, 'e', -1, 64)
	}
	if !strings.ContainsAny(s, ".e") {
		s += ".0"
	}
	return s
}

// sortValues sorts in place with a < b (lt methods included).
func (vm *VM) sortValues(items []Value) error {
	var err error
	sort.SliceStable(items, func(i, j int) bool {
		if err != nil {
			return false
		}
		r, e := vm.less(items[i], items[j])
		if e != nil {
			err = e
			return false
		}
		return r
	})
	return err
}

// ModuleObj is a module: import term gives term::key and so on.
type ModuleObj struct {
	Name    string
	Members map[string]Value
}

func (*ModuleObj) TypeName() string { return "module" }
