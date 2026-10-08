package vm

import (
	"bufio"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/Username59138/sepl/token"
)

const (
	// initialStack is the starting number of value slots; the stack grows
	// as calls need more. A small stack keeps garbage collection cheap.
	initialStack = 1024
	// MaxStack limits the number of value slots.
	maxStackSize = 1 << 22
	// MaxFrames limits call depth, so infinite recursion fails cleanly.
	MaxFrames = 20000
)

type frame struct {
	cl   *Closure
	ip   int
	base int // slot of the first parameter
	ret  int // slot that receives the result (the callee's slot)
}

// VM runs compiled SEPL code. Globals persist between runs, which is what the
// REPL relies on.
type VM struct {
	stack    []Value
	sp       int
	frames   []frame
	open     *Upvalue // open upvalues, sorted by slot, highest first
	builtins []Value

	// Builtin types; SEPL code can add methods to them.
	tInt, tFloat, tStr, tBool, tList, tMap, tRange *TypeObj
	// The type builtin is also the type of types; the others have no name in
	// SEPL (nil and fn are keywords) but type(x) can still return them.
	tType, tNil, tFn, tModule *TypeObj
	otherTypes                map[string]*TypeObj // internal values, by TypeName
	epoch                     int                 // changes whenever methods are added; invalidates lookup caches
	numOps                    bool                // int or float got an operator method: skip the int fast paths

	modules map[string]*ModuleObj
	regexps map[string]*regexp.Regexp

	resultType, optionType *TypeObj // from the prelude, for library functions
	// Importer loads "import name" for code in file fromFile; nil means
	// built-in modules only.
	Importer  func(name, fromFile string) (*ModuleObj, error)
	term      *termState
	rng       *rand.Rand
	inIsStdin bool

	Out *bufio.Writer
	In  *bufio.Reader
}

// New creates a VM writing to out and reading from in (nil means stdin/stdout).
func New(out io.Writer, in io.Reader) *VM {
	if out == nil {
		out = os.Stdout
	}
	inIsStdin := in == nil
	if in == nil {
		in = os.Stdin
	}
	vm := &VM{
		stack:  make([]Value, initialStack),
		frames: make([]frame, 0, MaxFrames),
		Out:    bufio.NewWriterSize(out, 64<<10),
		In:     bufio.NewReader(in),

		inIsStdin: inIsStdin,
	}
	vm.builtins = make([]Value, len(builtinList))
	for i, b := range builtinList {
		if !builtinTypeNames[b.Name] {
			vm.builtins[i] = Obj(b)
			continue
		}
		t := newType(b.Name)
		t.builtin = true
		t.ctor = b.Fn
		vm.builtins[i] = Obj(t)
		switch b.Name {
		case "int":
			vm.tInt = t
		case "float":
			vm.tFloat = t
		case "str":
			vm.tStr = t
		case "bool":
			vm.tBool = t
		case "list":
			vm.tList = t
		case "map":
			vm.tMap = t
		case "range":
			vm.tRange = t
		case "type":
			vm.tType = t
		}
	}
	vm.tNil = hiddenType("nil")
	vm.tFn = hiddenType("fn")
	vm.tModule = hiddenType("module")
	vm.otherTypes = map[string]*TypeObj{}
	return vm
}

// ---------------------------------------------------------------- errors

// TraceEntry is one call in a runtime error's stack trace.
type TraceEntry struct {
	Func string
	File string
	Pos  token.Pos
}

// RuntimeError is an error while running a program. Trace[0] is where it
// happened, the next entries are the calls that led there.
type RuntimeError struct {
	Msg   string
	Trace []TraceEntry
}

func (e *RuntimeError) Error() string {
	if len(e.Trace) == 0 {
		return e.Msg
	}
	t := e.Trace[0]
	return fmt.Sprintf("%s:%s: %s", t.File, t.Pos, e.Msg)
}

// passThrough is implemented by errors that already say where they happened,
// such as a syntax error in an imported module: the VM returns them unchanged.
type passThrough interface {
	error
	PassThrough()
}

// ExitError is returned when the program calls exit(code).
type ExitError struct {
	Code int
}

func (e *ExitError) Error() string { return fmt.Sprintf("exit(%d)", e.Code) }

// trap turns an error raised at ip into a RuntimeError with a stack trace.
func (vm *VM) trap(ip, sp int, err error) error {
	switch err.(type) {
	case *RuntimeError, *ExitError, passThrough:
		return err
	}
	vm.frames[len(vm.frames)-1].ip = ip
	vm.sp = sp
	re := vm.traceError(err.Error())
	if _, ok := err.(callerError); ok && len(re.Trace) > 1 {
		re.Trace = re.Trace[1:] // a wrong argument is the caller's mistake
	}
	return re
}

// callerError is an error reported at the call that led to it.
type callerError struct{ error }

func (vm *VM) traceError(msg string) *RuntimeError {
	e := &RuntimeError{Msg: msg}
	for i := len(vm.frames) - 1; i >= 0; i-- {
		f := vm.frames[i]
		p := f.cl.Proto
		at := f.ip - 1
		if at < 0 {
			at = 0
		}
		var pos token.Pos
		if at < len(p.Pos) {
			pos = p.Pos[at]
		}
		e.Trace = append(e.Trace, TraceEntry{Func: p.Name, File: p.File, Pos: pos})
	}
	return e
}

func (vm *VM) reset() {
	vm.closeUpvals(0)
	vm.frames = vm.frames[:0]
	vm.sp = 0
}

// ---------------------------------------------------------------- entry points

// Run executes the top-level code of a script.
func (vm *VM) Run(cl *Closure) (Value, error) {
	vm.reset()
	if !vm.ensureStack(cl.Proto.MaxStack + 2) {
		return Nil, &RuntimeError{Msg: "the program needs too much stack"}
	}
	vm.stack[0] = Obj(cl)
	vm.sp = 1
	vm.frames = append(vm.frames, frame{cl: cl, base: 1, ret: 0})
	v, err := vm.run(0)
	if err != nil {
		vm.reset()
	}
	return v, err
}

// Call calls a SEPL function from Go. It can be used while the VM is
// running, for example by a builtin.
func (vm *VM) Call(fn Value, args []Value) (Value, error) {
	start := vm.sp
	if !vm.ensureStack(start + len(args) + 2) {
		return Nil, opError("stack overflow")
	}
	vm.stack[start] = fn
	copy(vm.stack[start+1:], args)
	vm.sp = start + 1 + len(args)
	depth := len(vm.frames)
	pushed, err := vm.callValue(start, len(args), nil)
	if err != nil {
		vm.sp = start
		return Nil, err
	}
	if !pushed {
		vm.sp = start
		return vm.stack[start], nil
	}
	v, err := vm.run(depth)
	vm.sp = start
	return v, err
}

// CallTop runs a function with no caller frame (for example main).
func (vm *VM) CallTop(fn Value, args []Value) (Value, error) {
	vm.reset()
	v, err := vm.Call(fn, args)
	if err != nil {
		if _, ok := err.(*RuntimeError); !ok {
			if _, ok := err.(*ExitError); !ok {
				err = &RuntimeError{Msg: err.Error()}
			}
		}
		vm.reset()
	}
	return v, err
}

// ---------------------------------------------------------------- calls

// callValue calls the value in slot callee with argc arguments above it, the
// last len(kw) of them named. A SEPL function gets a new frame (pushed is
// true); a builtin runs right away and leaves its result in the callee slot.
func (vm *VM) callValue(callee, argc int, kw KwNames) (pushed bool, err error) {
	switch f := vm.stack[callee].O.(type) {
	case *Closure:
		return true, vm.enterClosure(f, callee+1, argc, kw, callee)
	case *BoundMethod:
		recv := f.Recv
		if f.Fn.K == KNil { // a method implemented in Go
			return vm.invokeBound(callee, f, argc, kw)
		}
		cl := f.Fn.O.(*Closure)
		vm.stack[callee] = recv
		return true, vm.enterClosure(cl, callee, argc+1, kw, callee)
	case *Builtin, *TypeObj, *VariantCtor:
		npos := argc - len(kw)
		var kwargs []Kwarg
		for i, name := range kw {
			kwargs = append(kwargs, Kwarg{Name: name, Value: vm.stack[callee+1+npos+i]})
		}
		args := vm.stack[callee+1 : callee+1+npos]
		vm.sp = callee + 1 + argc
		var res Value
		switch f := f.(type) {
		case *Builtin:
			res, err = f.Fn(vm, args, kwargs)
		case *TypeObj:
			res, err = vm.construct(f, args, kwargs)
		case *VariantCtor:
			res, err = vm.makeVariant(f, args, kwargs)
		}
		if err != nil {
			return false, err
		}
		vm.stack[callee] = res
		vm.sp = callee + 1
		return false, nil
	}
	return false, errorf("%s is not a function", TypeName(vm.stack[callee]))
}

// invokeBound calls a Go method taken as a value (f = items.push).
func (vm *VM) invokeBound(callee int, f *BoundMethod, argc int, kw KwNames) (bool, error) {
	vm.stack[callee] = f.Recv
	return vm.invoke(callee, NewMethodRef(f.Name), argc, kw)
}

// bindArgs matches positional and named arguments to the parameters of p.
func bindArgs(p *Proto, args []Value, kw KwNames) ([]Value, error) {
	n := p.NParams
	fixed := n
	if p.Variadic {
		fixed = n - 1
	}
	npos := len(args) - len(kw)
	out := make([]Value, n)
	set := make([]bool, n)
	if npos > fixed && !p.Variadic {
		return nil, errorf("%s() takes %d argument%s but got %d", p.Name, fixed, plural(fixed), npos)
	}
	for i := 0; i < npos && i < fixed; i++ {
		out[i], set[i] = args[i], true
	}
	if p.Variadic {
		var rest []Value
		if npos > fixed {
			rest = append(rest, args[fixed:npos]...)
		}
		out[n-1], set[n-1] = NewList(rest), true
	}
	for j, name := range kw {
		idx := -1
		for i := 0; i < fixed; i++ {
			if p.ParamNames[i] == name {
				idx = i
				break
			}
		}
		if idx < 0 {
			return nil, errorf("%s() has no parameter named '%s'", p.Name, name)
		}
		if set[idx] {
			return nil, errorf("%s() got argument '%s' twice", p.Name, name)
		}
		out[idx], set[idx] = args[npos+j], true
	}
	for i := 0; i < fixed; i++ {
		if !set[i] {
			return nil, errorf("missing argument '%s' in call to %s()", p.ParamNames[i], p.Name)
		}
	}
	return out, nil
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// ensureStack grows the stack to at least n slots. Captured variables refer
// to slots by index, so moving the stack is safe.
func (vm *VM) ensureStack(n int) bool {
	if n < len(vm.stack) {
		return true
	}
	if n > maxStackSize {
		return false
	}
	size := len(vm.stack)
	for size <= n {
		size *= 2
	}
	bigger := make([]Value, size)
	copy(bigger, vm.stack)
	vm.stack = bigger
	return true
}

// ---------------------------------------------------------------- upvalues

func (vm *VM) captureUpval(slot int) *Upvalue {
	var prev *Upvalue
	u := vm.open
	for u != nil && u.slot > slot {
		prev, u = u, u.next
	}
	if u != nil && u.slot == slot {
		return u
	}
	n := &Upvalue{slot: slot, open: true, next: u}
	if prev == nil {
		vm.open = n
	} else {
		prev.next = n
	}
	return n
}

// closeUpvals moves every captured variable at slot >= from off the stack.
func (vm *VM) closeUpvals(from int) {
	for vm.open != nil && vm.open.slot >= from {
		u := vm.open
		u.closed = vm.stack[u.slot]
		u.open = false
		vm.open = u.next
	}
}

// ---------------------------------------------------------------- iterators

type listIter struct {
	l *ListObj
	i int
}
type strIter struct {
	s string
	i int
}
type mapIter struct {
	m *MapObj
	i int
}
type rangeIter struct {
	cur, hi int64
}

func (*listIter) TypeName() string  { return "iterator" }
func (*strIter) TypeName() string   { return "iterator" }
func (*mapIter) TypeName() string   { return "iterator" }
func (*rangeIter) TypeName() string { return "iterator" }

func makeIter(v Value) (Value, error) {
	switch o := v.O.(type) {
	case *ListObj:
		return Obj(&listIter{l: o}), nil
	case *StrObj:
		return Obj(&strIter{s: o.S}), nil
	case *MapObj:
		return Obj(&mapIter{m: o}), nil
	case *RangeObj:
		return Obj(&rangeIter{cur: o.Lo, hi: o.Hi}), nil
	}
	return Nil, errorf("cannot iterate over %s", TypeName(v))
}

// next returns the next item of an iterator, or false when it is done.
func iterNext(it Object) (Value, bool) {
	switch it := it.(type) {
	case *listIter:
		if it.i >= len(it.l.Items) {
			return Nil, false
		}
		v := it.l.Items[it.i]
		it.i++
		return v, true
	case *rangeIter:
		if it.cur >= it.hi {
			return Nil, false
		}
		v := Int(it.cur)
		it.cur++
		return v, true
	case *strIter:
		if it.i >= len(it.s) {
			return Nil, false
		}
		r := it.s[it.i:]
		n := 1
		if r[0] >= 0x80 {
			_, n = utf8.DecodeRuneInString(r)
		}
		it.i += n
		return Str(r[:n]), true
	case *mapIter:
		if it.i >= len(it.m.keys) {
			return Nil, false
		}
		v := it.m.keys[it.i]
		it.i++
		return v, true
	}
	return Nil, false
}

// ---------------------------------------------------------------- the loop

func fieldError(x Value, name string) error {
	if hasMethod(x, name) {
		return errorf("%s has no field '%s'; to call the method write .%s()", TypeName(x), name, name)
	}
	return errorf("%s has no field '%s'", TypeName(x), name)
}

// run executes instructions until the frame count drops to stop.
func (vm *VM) run(stop int) (Value, error) {
	st := vm.stack
	fr := &vm.frames[len(vm.frames)-1]
	code := fr.cl.Proto.Code
	consts := fr.cl.Proto.Consts
	ip, base, sp := fr.ip, fr.base, vm.sp
	env := fr.cl.Proto.Env
	var result Value

	for {
		ins := code[ip]
		ip++
		switch Op(ins) {
		case OpConst:
			st[sp] = consts[ins>>8]
			sp++
		case OpInt:
			st[sp] = Value{K: KInt, N: uint64(int64(int32(ins) >> 8))}
			sp++
		case OpNil:
			st[sp] = Value{}
			sp++
		case OpTrue:
			st[sp] = True
			sp++
		case OpFalse:
			st[sp] = False
			sp++
		case OpPop:
			sp--
		case OpPopN:
			sp -= int(ins >> 8)
		case OpCloseN:
			n := int(ins >> 8)
			vm.closeUpvals(sp - n)
			sp -= n
		case OpSlide:
			n := int(ins >> 8)
			st[sp-1-n] = st[sp-1]
			sp -= n
		case OpCloseSlide:
			n := int(ins >> 8)
			top := st[sp-1]
			vm.closeUpvals(sp - 1 - n)
			st[sp-1-n] = top
			sp -= n
		case OpDup:
			st[sp] = st[sp-1]
			sp++
		case OpDup2:
			st[sp] = st[sp-2]
			st[sp+1] = st[sp-1]
			sp += 2

		case OpGetLocal:
			st[sp] = st[base+int(ins>>8)]
			sp++
		case OpSetLocal:
			sp--
			st[base+int(ins>>8)] = st[sp]
		case OpGetUpval:
			if u := fr.cl.Upvals[ins>>8]; u.open {
				st[sp] = st[u.slot]
			} else {
				st[sp] = u.closed
			}
			sp++
		case OpSetUpval:
			sp--
			if u := fr.cl.Upvals[ins>>8]; u.open {
				st[u.slot] = st[sp]
			} else {
				u.closed = st[sp]
			}
		case OpGetGlobal:
			v := env.Values[ins>>8]
			if v.K == kUndef {
				return Nil, vm.trap(ip, sp, errorf("'%s' is used before it is defined", env.Names[ins>>8]))
			}
			st[sp] = v
			sp++
		case OpSetGlobal:
			if env.Values[ins>>8].K == kUndef {
				return Nil, vm.trap(ip, sp, errorf("'%s' is assigned before it is defined", env.Names[ins>>8]))
			}
			sp--
			env.Values[ins>>8] = st[sp]
		case OpDefGlobal:
			sp--
			env.Values[ins>>8] = st[sp]
		case OpGetBuiltin:
			st[sp] = vm.builtins[ins>>8]
			sp++

		case OpAdd:
			a, b := st[sp-2], st[sp-1]
			if a.K == KInt && b.K == KInt && !vm.numOps {
				x, y := int64(a.N), int64(b.N)
				r := x + y
				if (x^r)&(y^r) >= 0 {
					st[sp-2].N = uint64(r)
					sp--
					break
				}
			}
			fr.ip, vm.sp = ip, sp
			v, err := vm.arith(OpAdd, a, b)
			st = vm.stack
			if err != nil {
				return Nil, vm.trap(ip, sp, err)
			}
			st[sp-2] = v
			sp--
		case OpSub:
			a, b := st[sp-2], st[sp-1]
			if a.K == KInt && b.K == KInt && !vm.numOps {
				x, y := int64(a.N), int64(b.N)
				r := x - y
				if (x^y)&(x^r) >= 0 {
					st[sp-2].N = uint64(r)
					sp--
					break
				}
			}
			fr.ip, vm.sp = ip, sp
			v, err := vm.arith(OpSub, a, b)
			st = vm.stack
			if err != nil {
				return Nil, vm.trap(ip, sp, err)
			}
			st[sp-2] = v
			sp--
		case OpMul, OpDiv, OpIDiv, OpMod:
			a, b := st[sp-2], st[sp-1]
			if a.K == KObj || b.K == KObj || vm.numOps {
				fr.ip, vm.sp = ip, sp
			}
			v, err := vm.arith(Op(ins), a, b)
			st = vm.stack
			if err != nil {
				return Nil, vm.trap(ip, sp, err)
			}
			st[sp-2] = v
			sp--
		case OpNeg:
			fr.ip, vm.sp = ip, sp
			v, err := vm.neg(st[sp-1])
			st = vm.stack
			if err != nil {
				return Nil, vm.trap(ip, sp, err)
			}
			st[sp-1] = v
		case OpNot:
			st[sp-1] = Bool(!Truthy(st[sp-1]))
		case OpToBool:
			st[sp-1] = Bool(Truthy(st[sp-1]))

		case OpEq, OpNe:
			a, b := st[sp-2], st[sp-1]
			var eq bool
			if a.K != KObj && !vm.numOps {
				eq = Equal(a, b)
			} else {
				fr.ip, vm.sp = ip, sp
				var err error
				eq, err = vm.equal(a, b)
				st = vm.stack
				if err != nil {
					return Nil, vm.trap(ip, sp, err)
				}
			}
			st[sp-2] = Bool(eq == (Op(ins) == OpEq))
			sp--
		case OpLt, OpLe, OpGt, OpGe:
			a, b := st[sp-2], st[sp-1]
			var r bool
			if a.K == KInt && b.K == KInt && !vm.numOps {
				x, y := int64(a.N), int64(b.N)
				switch Op(ins) {
				case OpLt:
					r = x < y
				case OpLe:
					r = x <= y
				case OpGt:
					r = x > y
				default:
					r = x >= y
				}
			} else {
				fr.ip, vm.sp = ip, sp
				var err error
				r, err = vm.compare(Op(ins), a, b)
				st = vm.stack
				if err != nil {
					return Nil, vm.trap(ip, sp, err)
				}
			}
			st[sp-2] = Bool(r)
			sp--
		case OpIn:
			fr.ip, vm.sp = ip, sp
			ok, err := vm.contains(st[sp-1], st[sp-2])
			st = vm.stack
			if err != nil {
				return Nil, vm.trap(ip, sp, err)
			}
			st[sp-2] = Bool(ok)
			sp--
		case OpInRange:
			ok, err := InRange(st[sp-3], st[sp-2], st[sp-1])
			if err != nil {
				return Nil, vm.trap(ip, sp, err)
			}
			st[sp-3] = Bool(ok)
			sp -= 2

		case OpJump:
			ip = int(ins >> 8)
		case OpJumpIfFalse:
			sp--
			v := st[sp]
			if v.K == KBool {
				if v.N == 0 {
					ip = int(ins >> 8)
				}
			} else if !Truthy(v) {
				ip = int(ins >> 8)
			}
		case OpJumpIfTrue:
			sp--
			v := st[sp]
			if v.K == KBool {
				if v.N != 0 {
					ip = int(ins >> 8)
				}
			} else if Truthy(v) {
				ip = int(ins >> 8)
			}

		case OpCall, OpCallKw:
			argc := int(ins >> 8)
			var kw KwNames
			if Op(ins) == OpCallKw {
				kw = consts[code[ip]].O.(KwNames)
				ip++
			}
			callee := sp - argc - 1
			// Fast path: a SEPL function called with exactly its parameters.
			if cl, ok := st[callee].O.(*Closure); ok && kw == nil && argc == cl.Proto.NParams && !cl.Proto.Variadic &&
				len(vm.frames) < cap(vm.frames) && callee+1+cl.Proto.MaxStack < len(st) {
				fr.ip = ip
				vm.frames = append(vm.frames, frame{cl: cl, base: callee + 1, ret: callee})
				fr = &vm.frames[len(vm.frames)-1]
				code, consts, env = cl.Proto.Code, cl.Proto.Consts, cl.Proto.Env
				ip, base = 0, callee+1
				break
			}
			fr.ip = ip
			vm.sp = sp
			pushed, err := vm.callValue(callee, argc, kw)
			st = vm.stack
			if err != nil {
				return Nil, vm.trap(ip, sp, err)
			}
			if pushed {
				fr = &vm.frames[len(vm.frames)-1]
				code, consts, env = fr.cl.Proto.Code, fr.cl.Proto.Consts, fr.cl.Proto.Env
				ip, base, sp = fr.ip, fr.base, vm.sp
			} else {
				sp = vm.sp
			}

		case OpInvoke, OpInvokeKw:
			argc := int(ins >> 8)
			ref := consts[code[ip]].O.(*MethodRef)
			ip++
			var kw KwNames
			if Op(ins) == OpInvokeKw {
				kw = consts[code[ip]].O.(KwNames)
				ip++
			}
			recv := sp - argc - 1
			fr.ip = ip
			vm.sp = sp
			pushed, err := vm.invoke(recv, ref, argc, kw)
			st = vm.stack
			if err != nil {
				return Nil, vm.trap(ip, sp, err)
			}
			if pushed {
				fr = &vm.frames[len(vm.frames)-1]
				code, consts, env = fr.cl.Proto.Code, fr.cl.Proto.Consts, fr.cl.Proto.Env
				ip, base, sp = fr.ip, fr.base, vm.sp
			} else {
				sp = vm.sp
			}

		case OpReturn, OpReturnNil:
			result = Value{}
			if Op(ins) == OpReturn {
				result = st[sp-1]
			}
			goto doReturn

		case OpTry:
			// x? : go on at A with the value, or fall through to the
			// return code the compiler put after TRY.
			fr.ip, vm.sp = ip, sp
			next, v, err := vm.try(st[sp-1])
			st = vm.stack
			if err != nil {
				return Nil, vm.trap(ip, sp, err)
			}
			st[sp-1] = v
			if next {
				ip = int(ins >> 8)
			}

		case OpCheckType, OpCheckKeep, OpCheckArg:
			// Fast path: a builtin type and a value of exactly that type.
			if t, ok := st[sp-1].O.(*TypeObj); ok && t.builtin {
				if v := st[sp-2]; v.K == KNil || (v.K == KInt && t == vm.tInt) || vm.builtinTypeOf(v) == t {
					if Op(ins) != OpCheckKeep {
						sp--
					}
					break
				}
			}
			fr.ip, vm.sp = ip, sp
			v, err := vm.checkType(st[sp-2], st[sp-1], consts[ins>>8].O.(*StrObj).S)
			if err != nil {
				if Op(ins) == OpCheckArg {
					err = callerError{err}
				}
				return Nil, vm.trap(ip, sp, err)
			}
			st[sp-2] = v
			if Op(ins) != OpCheckKeep {
				sp--
			}
		case OpCheckParam:
			slot, tslot := base+int(ins>>8&0xfff), base+int(ins>>20)
			v, t := st[slot], st[tslot]
			if tt, ok := t.O.(*TypeObj); ok && tt.builtin {
				if v.K == KNil || (v.K == KInt && tt == vm.tInt) || vm.builtinTypeOf(v) == tt {
					break
				}
			}
			p := fr.cl.Proto
			i := int(ins >> 8 & 0xfff)
			fr.ip, vm.sp = ip, sp
			nv, err := vm.checkType(v, t, fmt.Sprintf("argument '%s' of %s()", p.ParamNames[i], p.Name))
			if err != nil {
				return Nil, vm.trap(ip, sp, callerError{err})
			}
			st[slot] = nv
		case OpCheckResult:
			v, t := st[sp-1], st[base+int(ins>>8)]
			if tt, ok := t.O.(*TypeObj); ok && tt.builtin {
				if v.K == KNil || (v.K == KInt && tt == vm.tInt) || vm.builtinTypeOf(v) == tt {
					break
				}
			}
			fr.ip, vm.sp = ip, sp
			nv, err := vm.checkType(v, t, "the result of "+fr.cl.Proto.Name+"()")
			if err != nil {
				return Nil, vm.trap(ip, sp, err)
			}
			st[sp-1] = nv
		case OpCheckEach:
			ctx := consts[ins>>8].O.(*StrObj).S
			if l, ok := st[sp-2].O.(*ListObj); ok {
				for i, x := range l.Items {
					v, err := vm.checkType(x, st[sp-1], ctx)
					if err != nil {
						return Nil, vm.trap(ip, sp, callerError{err})
					}
					l.Items[i] = v
				}
			}
			sp--
		case OpTypeOf:
			t, err := vm.typeOfValue(st[sp-1], consts[ins>>8].O.(*StrObj).S)
			if err != nil {
				return Nil, vm.trap(ip, sp, err)
			}
			st[sp] = t
			sp++

		case OpClosure:
			p := consts[ins>>8].O.(*Proto)
			cl := &Closure{Proto: p, Upvals: make([]*Upvalue, len(p.Upvals))}
			for i, d := range p.Upvals {
				if d.Local {
					cl.Upvals[i] = vm.captureUpval(base + d.Index)
				} else {
					cl.Upvals[i] = fr.cl.Upvals[d.Index]
				}
			}
			st[sp] = Obj(cl)
			sp++

		case OpList:
			n := int(ins >> 8)
			items := make([]Value, n)
			copy(items, st[sp-n:sp])
			sp -= n
			st[sp] = NewList(items)
			sp++
		case OpMap:
			n := int(ins >> 8)
			m := NewMap()
			for i := sp - 2*n; i < sp; i += 2 {
				if err := m.Set(st[i], st[i+1]); err != nil {
					return Nil, vm.trap(ip, sp, err)
				}
			}
			sp -= 2 * n
			st[sp] = Obj(m)
			sp++
		case OpRange:
			v, err := MakeRange(st[sp-2], st[sp-1])
			if err != nil {
				return Nil, vm.trap(ip, sp, err)
			}
			st[sp-2] = v
			sp--
		case OpIndex:
			c, i := st[sp-2], st[sp-1]
			if l, ok := c.O.(*ListObj); ok && i.K == KInt && i.N < uint64(len(l.Items)) && !vm.tList.extended {
				st[sp-2] = l.Items[i.N]
				sp--
				break
			}
			fr.ip, vm.sp = ip, sp
			v, err := vm.index(c, i)
			st = vm.stack
			if err != nil {
				return Nil, vm.trap(ip, sp, err)
			}
			st[sp-2] = v
			sp--
		case OpSetIndex:
			fr.ip, vm.sp = ip, sp
			err := vm.setIndex(st[sp-3], st[sp-2], st[sp-1])
			st = vm.stack
			if err != nil {
				return Nil, vm.trap(ip, sp, err)
			}
			sp -= 3
		case OpGetField:
			name := consts[ins>>8].O.(*StrObj).S
			if inst, ok := st[sp-1].O.(*Instance); ok {
				if i, ok := inst.Type.fieldIdx[name]; ok && i < len(inst.Fields) {
					st[sp-1] = inst.Fields[i]
					break
				}
			}
			fr.ip, vm.sp = ip, sp
			v, err := vm.getField(st[sp-1], name)
			st = vm.stack
			if err != nil {
				return Nil, vm.trap(ip, sp, err)
			}
			st[sp-1] = v
		case OpSetField:
			fr.ip, vm.sp = ip, sp
			err := vm.setField(st[sp-2], consts[ins>>8].O.(*StrObj).S, st[sp-1])
			st = vm.stack
			if err != nil {
				return Nil, vm.trap(ip, sp, err)
			}
			sp -= 2
		case OpBuildStr:
			n := int(ins >> 8)
			var b strings.Builder
			for _, v := range st[sp-n : sp] {
				if s, ok := v.O.(*StrObj); ok {
					b.WriteString(s.S)
				} else {
					fr.ip = ip
					vm.sp = sp
					s, err := vm.toStr(v)
					st = vm.stack
					if err != nil {
						return Nil, vm.trap(ip, sp, err)
					}
					b.WriteString(s)
				}
			}
			sp -= n
			st[sp] = Str(b.String())
			sp++

		case OpIter:
			fr.ip, vm.sp = ip, sp
			v, err := vm.makeIter(st[sp-1])
			st = vm.stack
			if err != nil {
				return Nil, vm.trap(ip, sp, err)
			}
			st[sp-1] = v
		case OpForNext:
			slot := base + int(code[ip])
			ip++
			v, ok := iterNext(st[slot].O)
			if !ok {
				ip = int(ins >> 8)
				break
			}
			st[slot+1] = v
		case OpForPrep:
			if st[sp-2].K != KInt || st[sp-1].K != KInt {
				return Nil, vm.trap(ip, sp, errorf("range bounds must be int, not %s and %s",
					TypeName(st[sp-2]), TypeName(st[sp-1])))
			}
		case OpForRange:
			slot := base + int(code[ip])
			ip++
			c := &st[slot]
			if int64(c.N) >= int64(st[slot+1].N) {
				ip = int(ins >> 8)
				break
			}
			st[slot+2] = *c
			c.N++

		case OpAddI, OpSubI, OpMulI, OpModI:
			k := int64(int32(ins) >> 8)
			a := st[sp-1]
			if a.K == KInt && !vm.numOps {
				x := int64(a.N)
				switch Op(ins) {
				case OpAddI:
					if r := x + k; (x^r)&(k^r) >= 0 {
						st[sp-1].N = uint64(r)
						continue
					}
				case OpSubI:
					if r := x - k; (x^k)&(x^r) >= 0 {
						st[sp-1].N = uint64(r)
						continue
					}
				case OpMulI:
					if x > -1<<31 && x < 1<<31 {
						st[sp-1].N = uint64(x * k)
						continue
					}
				case OpModI:
					if k > 0 {
						r := x % k
						if r < 0 {
							r += k
						}
						st[sp-1].N = uint64(r)
						continue
					}
				}
			}
			fr.ip, vm.sp = ip, sp
			v, err := vm.arith(Op(ins), a, Int(k))
			st = vm.stack
			if err != nil {
				return Nil, vm.trap(ip, sp, err)
			}
			st[sp-1] = v

		case OpJumpNotEq, OpJumpNotNe:
			a, b := st[sp-2], st[sp-1]
			var eq bool
			if a.K == KInt && b.K == KInt && !vm.numOps {
				eq = a.N == b.N
			} else if a.K != KObj && !vm.numOps {
				eq = Equal(a, b)
			} else {
				fr.ip, vm.sp = ip, sp
				var err error
				eq, err = vm.equal(a, b)
				st = vm.stack
				if err != nil {
					return Nil, vm.trap(ip, sp, err)
				}
			}
			sp -= 2
			if eq != (Op(ins) == OpJumpNotEq) {
				ip = int(ins >> 8)
			}
		case OpJumpNotLt, OpJumpNotLe, OpJumpNotGt, OpJumpNotGe:
			a, b := st[sp-2], st[sp-1]
			var ok bool
			if a.K == KInt && b.K == KInt && !vm.numOps {
				x, y := int64(a.N), int64(b.N)
				switch Op(ins) {
				case OpJumpNotLt:
					ok = x < y
				case OpJumpNotLe:
					ok = x <= y
				case OpJumpNotGt:
					ok = x > y
				default:
					ok = x >= y
				}
			} else {
				fr.ip, vm.sp = ip, sp
				var err error
				ok, err = vm.compare(jumpCompare(Op(ins)), a, b)
				st = vm.stack
				if err != nil {
					return Nil, vm.trap(ip, sp, err)
				}
			}
			sp -= 2
			if !ok {
				ip = int(ins >> 8)
			}

		case OpIncLocal:
			v := &st[base+int(ins>>8&0xfff)]
			k := int64(int32(ins) >> 20)
			if v.K == KInt && !vm.numOps {
				x := int64(v.N)
				if r := x + k; (x^r)&(k^r) >= 0 {
					v.N = uint64(r)
					break
				}
			}
			slot := base + int(ins>>8&0xfff)
			fr.ip, vm.sp = ip, sp
			r, err := vm.arith(OpAdd, *v, Int(k))
			st = vm.stack
			if err != nil {
				return Nil, vm.trap(ip, sp, err)
			}
			st[slot] = r
		case OpAddLocal:
			v := &st[base+int(ins>>8&0xfff)]
			w := st[base+int(ins>>20)]
			if v.K == KInt && w.K == KInt && !vm.numOps {
				x, y := int64(v.N), int64(w.N)
				if r := x + y; (x^r)&(y^r) >= 0 {
					v.N = uint64(r)
					break
				}
			}
			slot := base + int(ins>>8&0xfff)
			fr.ip, vm.sp = ip, sp
			r, err := vm.arith(OpAdd, *v, w)
			st = vm.stack
			if err != nil {
				return Nil, vm.trap(ip, sp, err)
			}
			st[slot] = r
		case OpCloseFrom:
			vm.closeUpvals(base + int(ins>>8))

		case OpStruct:
			d := consts[ins>>8].O.(*StructDesc)
			n := d.Pushed()
			v, err := vm.makeStruct(d, st[sp-n:sp-n+d.NParents], st[sp-n+d.NParents:sp])
			if err != nil {
				return Nil, vm.trap(ip, sp, err)
			}
			sp -= n
			st[sp] = v
			sp++
		case OpImpl:
			d := consts[ins>>8].O.(*ImplDesc)
			n := d.Bodies()
			if err := vm.implement(st[sp-1-n], d, st[sp-n:sp]); err != nil {
				return Nil, vm.trap(ip, sp, err)
			}
			sp -= n + 1
		case OpAddStruct:
			if err := vm.addStruct(st[sp-2], st[sp-1]); err != nil {
				return Nil, vm.trap(ip, sp, err)
			}
			sp -= 2
		case OpAddImpl:
			d := consts[ins>>8].O.(*ImplDesc)
			n := d.Bodies()
			if err := vm.addImpl(st[sp-2-n], st[sp-1-n], d, st[sp-n:sp]); err != nil {
				return Nil, vm.trap(ip, sp, err)
			}
			sp -= n + 2
		case OpScope:
			fr.ip, vm.sp = ip, sp
			v, err := vm.scope(st[sp-1], consts[ins>>8].O.(*StrObj).S)
			if err != nil {
				return Nil, vm.trap(ip, sp, err)
			}
			st[sp-1] = v
		case OpImport:
			fr.ip, vm.sp = ip, sp
			var m *ModuleObj
			var err error
			name := consts[ins>>8].O.(*StrObj).S
			if vm.Importer != nil {
				m, err = vm.Importer(name, fr.cl.Proto.File)
			} else {
				m, err = vm.module(name)
			}
			st = vm.stack
			if err != nil {
				return Nil, vm.trap(ip, sp, err)
			}
			st[sp] = Obj(m)
			sp++

		case OpEnum:
			d := consts[ins>>8].O.(*EnumDesc)
			n := d.Pushed()
			v, err := vm.makeEnum(d, st[sp-n:sp-n+d.NParents], st[sp-n+d.NParents:sp])
			if err != nil {
				return Nil, vm.trap(ip, sp, err)
			}
			sp -= n
			st[sp] = v
			sp++
		case OpIsVariant:
			ok, err := isVariant(st[sp-2], st[sp-1], int(ins>>8)-1)
			if err != nil {
				return Nil, vm.trap(ip, sp, err)
			}
			st[sp-2] = Bool(ok)
			sp--
		case OpVariantField:
			v, err := variantField(st[sp-1], int(ins>>8))
			if err != nil {
				return Nil, vm.trap(ip, sp, err)
			}
			st[sp-1] = v
		case OpMatchEq:
			fr.ip, vm.sp = ip, sp
			ok, err := vm.matchEqual(st[sp-2], st[sp-1])
			st = vm.stack
			if err != nil {
				return Nil, vm.trap(ip, sp, err)
			}
			st[sp-2] = Bool(ok)
			sp--

		default:
			return Nil, vm.trap(ip, sp, errorf("internal error: unknown opcode %d", Op(ins)))
		}
		continue

	doReturn:
		if vm.open != nil && vm.open.slot >= base {
			vm.closeUpvals(base)
		}
		st[fr.ret] = result
		sp = fr.ret + 1
		vm.frames = vm.frames[:len(vm.frames)-1]
		if len(vm.frames) == stop {
			vm.sp = sp
			return result, nil
		}
		vm.sp = sp
		fr = &vm.frames[len(vm.frames)-1]
		code, consts, env = fr.cl.Proto.Code, fr.cl.Proto.Consts, fr.cl.Proto.Env
		ip, base, sp = fr.ip, fr.base, vm.sp
	}
}

// try implements x?: it calls x.try(), which returns flow::next(v) to go on
// with v, or flow::exit(v) to return v from the current function.
func (vm *VM) try(x Value) (next bool, v Value, err error) {
	m, err := vm.userMethod(x, "try")
	if err != nil {
		return false, Nil, err
	}
	if m == nil {
		return false, Nil, errorf("'?' needs a value with a try method (like result or option), not %s", TypeName(x))
	}
	r, err := vm.callUser(m, x)
	if err != nil {
		return false, Nil, err
	}
	e, ok := r.O.(*EnumValue)
	if !ok || e.Variant.Origin.Name != "flow" || len(e.Fields) != 1 {
		return false, Nil, errorf("try() of %s must return flow::next(value) or flow::exit(value), not %s", TypeName(x), Repr(r))
	}
	return e.Variant.Name == "next", e.Fields[0], nil
}

// jumpCompare is the comparison a compare-and-jump instruction tests.
func jumpCompare(op Op) Op {
	switch op {
	case OpJumpNotLt:
		return OpLt
	case OpJumpNotLe:
		return OpLe
	case OpJumpNotGt:
		return OpGt
	}
	return OpGe
}
