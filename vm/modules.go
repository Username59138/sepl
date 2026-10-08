package vm

import (
	"math"
	"math/rand/v2"
	"os"
	"os/exec"
	"strings"
	"time"
	"unicode/utf8"
)

// Built-in modules, used with import: import term, then term::key().

var moduleBuilders = map[string]func(vm *VM) map[string]Value{
	"term":   termModule,
	"random": randomModule,
	"math":   mathModule,
	"time":   timeModule,
}

// HasModule reports whether a built-in module exists.
func HasModule(name string) bool {
	_, ok := moduleBuilders[name]
	return ok
}

// ModuleNames lists the built-in modules.
func ModuleNames() []string { return []string{"term", "random", "math", "time"} }

// BuiltinModule returns a built-in module such as term.
func (vm *VM) BuiltinModule(name string) (*ModuleObj, error) { return vm.module(name) }

func (vm *VM) module(name string) (*ModuleObj, error) {
	if m, ok := vm.modules[name]; ok {
		return m, nil
	}
	build, ok := moduleBuilders[name]
	if !ok {
		return nil, errorf("module '%s' not found", name)
	}
	if vm.modules == nil {
		vm.modules = map[string]*ModuleObj{}
	}
	m := &ModuleObj{Name: name, Members: build(vm)}
	vm.modules[name] = m
	return m, nil
}

func fnValue(name string, f func(vm *VM, args []Value, kw []Kwarg) (Value, error)) Value {
	return Obj(&Builtin{Name: name, Fn: f})
}

func intArg(fn string, v Value) (int64, error) {
	if v.K != KInt {
		return 0, errorf("%s() needs an int, not %s", fn, TypeName(v))
	}
	return v.AsInt(), nil
}

func numArg(fn string, v Value) (float64, error) {
	if !isNum(v) {
		return 0, errorf("%s() needs a number, not %s", fn, TypeName(v))
	}
	return toFloat(v), nil
}

// ---------------------------------------------------------------- term

// termState is the keyboard reader of term::key. In a terminal it switches
// off line buffering and echo (so keys arrive without Enter) and restores
// them in Close.
type termState struct {
	keys         chan byte
	saved        string
	cursorHidden bool
}

// InIsTerminal reports whether the VM reads keys from a real terminal.
func (vm *VM) inIsTerminal() bool {
	if !vm.inIsStdin {
		return false
	}
	fi, err := os.Stdin.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func stty(args ...string) (string, error) {
	cmd := exec.Command("stty", args...)
	cmd.Stdin = os.Stdin
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

func (vm *VM) startKeys() *termState {
	if vm.term != nil && vm.term.keys != nil {
		return vm.term
	}
	if vm.term == nil {
		vm.term = &termState{}
	}
	t := vm.term
	if vm.inIsTerminal() {
		if saved, err := stty("-g"); err == nil {
			t.saved = saved
			stty("-icanon", "-echo", "min", "1")
		}
	}
	t.keys = make(chan byte, 256)
	in := vm.In
	go func() {
		for {
			b, err := in.ReadByte()
			if err != nil {
				close(t.keys)
				return
			}
			t.keys <- b
		}
	}()
	return t
}

// Close restores the terminal if term:: functions changed it.
func (vm *VM) Close() {
	vm.Out.Flush()
	t := vm.term
	if t == nil {
		return
	}
	if t.cursorHidden {
		os.Stdout.WriteString("\x1b[?25h")
		t.cursorHidden = false
	}
	if t.saved != "" {
		stty(t.saved)
		t.saved = ""
	}
}

// readKey turns the next bytes into a key name: "up", "down", "left",
// "right", "enter", "space", "esc", "backspace", "tab" or the character.
func readKey(keys chan byte, wait bool) (Value, bool) {
	var b byte
	var ok bool
	if wait {
		b, ok = <-keys
	} else {
		select {
		case b, ok = <-keys:
		default:
			return Nil, true
		}
	}
	if !ok {
		return Nil, false
	}
	next := func() (byte, bool) {
		select {
		case c, ok := <-keys:
			return c, ok
		case <-time.After(15 * time.Millisecond):
			return 0, false
		}
	}
	switch b {
	case 0x1b:
		c, ok := next()
		if !ok || (c != '[' && c != 'O') {
			return Str("esc"), true
		}
		d, _ := next()
		switch d {
		case 'A':
			return Str("up"), true
		case 'B':
			return Str("down"), true
		case 'C':
			return Str("right"), true
		case 'D':
			return Str("left"), true
		}
		return Str("esc"), true
	case '\r', '\n':
		return Str("enter"), true
	case ' ':
		return Str("space"), true
	case '\t':
		return Str("tab"), true
	case 0x7f, 0x08:
		return Str("backspace"), true
	}
	if b < utf8.RuneSelf {
		return Str(string(rune(b))), true
	}
	buf := []byte{b}
	for len(buf) < 4 && !utf8.FullRune(buf) {
		c, ok := next()
		if !ok {
			break
		}
		buf = append(buf, c)
	}
	return Str(string(buf)), true
}

func termModule(vm *VM) map[string]Value {
	return map[string]Value{
		// key() returns the key pressed since the last call, or nil: it never waits.
		"key": fnValue("term::key", func(vm *VM, args []Value, kw []Kwarg) (Value, error) {
			if err := arity("term::key", args, 0, 0); err != nil {
				return Nil, err
			}
			vm.Out.Flush()
			v, _ := readKey(vm.startKeys().keys, false)
			return v, nil
		}),
		// wait_key() waits for a key; nil at the end of input.
		"wait_key": fnValue("term::wait_key", func(vm *VM, args []Value, kw []Kwarg) (Value, error) {
			if err := arity("term::wait_key", args, 0, 0); err != nil {
				return Nil, err
			}
			vm.Out.Flush()
			v, _ := readKey(vm.startKeys().keys, true)
			return v, nil
		}),
		"clear": fnValue("term::clear", func(vm *VM, args []Value, kw []Kwarg) (Value, error) {
			if err := arity("term::clear", args, 0, 0); err != nil {
				return Nil, err
			}
			vm.Out.WriteString("\x1b[2J\x1b[H")
			return Nil, nil
		}),
		// sleep(ms) shows everything printed so far, then pauses.
		"sleep": fnValue("term::sleep", func(vm *VM, args []Value, kw []Kwarg) (Value, error) {
			if err := arity("term::sleep", args, 1, 1); err != nil {
				return Nil, err
			}
			ms, err := numArg("term::sleep", args[0])
			if err != nil {
				return Nil, err
			}
			vm.Out.Flush()
			time.Sleep(time.Duration(ms * float64(time.Millisecond)))
			return Nil, nil
		}),
		"hide_cursor": fnValue("term::hide_cursor", func(vm *VM, args []Value, kw []Kwarg) (Value, error) {
			if vm.term == nil {
				vm.term = &termState{}
			}
			vm.term.cursorHidden = true
			vm.Out.WriteString("\x1b[?25l")
			return Nil, nil
		}),
		"show_cursor": fnValue("term::show_cursor", func(vm *VM, args []Value, kw []Kwarg) (Value, error) {
			if vm.term != nil {
				vm.term.cursorHidden = false
			}
			vm.Out.WriteString("\x1b[?25h")
			return Nil, nil
		}),
	}
}

// ---------------------------------------------------------------- random

func randomModule(vm *VM) map[string]Value {
	if vm.rng == nil {
		vm.rng = rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), 0x5e91))
	}
	return map[string]Value{
		// int(lo..hi) or int(lo, hi): a random int from lo up to hi, hi excluded.
		"int": fnValue("random::int", func(vm *VM, args []Value, kw []Kwarg) (Value, error) {
			if err := arity("random::int", args, 1, 2); err != nil {
				return Nil, err
			}
			var lo, hi int64
			if len(args) == 1 {
				r, ok := args[0].O.(*RangeObj)
				if !ok {
					return Nil, errorf("random::int() needs a range like 0..10 or two ints, not %s", TypeName(args[0]))
				}
				lo, hi = r.Lo, r.Hi
			} else {
				var err error
				if lo, err = intArg("random::int", args[0]); err != nil {
					return Nil, err
				}
				if hi, err = intArg("random::int", args[1]); err != nil {
					return Nil, err
				}
			}
			if hi <= lo {
				return Nil, errorf("random::int() got an empty range %d..%d", lo, hi)
			}
			return Int(lo + vm.rng.Int64N(hi-lo)), nil
		}),
		"float": fnValue("random::float", func(vm *VM, args []Value, kw []Kwarg) (Value, error) {
			if err := arity("random::float", args, 0, 0); err != nil {
				return Nil, err
			}
			return Float(vm.rng.Float64()), nil
		}),
		"choice": fnValue("random::choice", func(vm *VM, args []Value, kw []Kwarg) (Value, error) {
			if err := arity("random::choice", args, 1, 1); err != nil {
				return Nil, err
			}
			l, ok := args[0].O.(*ListObj)
			if !ok || len(l.Items) == 0 {
				return Nil, errorf("random::choice() needs a non-empty list")
			}
			return l.Items[vm.rng.IntN(len(l.Items))], nil
		}),
		"shuffle": fnValue("random::shuffle", func(vm *VM, args []Value, kw []Kwarg) (Value, error) {
			if err := arity("random::shuffle", args, 1, 1); err != nil {
				return Nil, err
			}
			l, ok := args[0].O.(*ListObj)
			if !ok {
				return Nil, errorf("random::shuffle() needs a list, not %s", TypeName(args[0]))
			}
			vm.rng.Shuffle(len(l.Items), func(i, j int) { l.Items[i], l.Items[j] = l.Items[j], l.Items[i] })
			return Nil, nil
		}),
		// seed(n) makes the following random numbers repeatable.
		"seed": fnValue("random::seed", func(vm *VM, args []Value, kw []Kwarg) (Value, error) {
			if err := arity("random::seed", args, 1, 1); err != nil {
				return Nil, err
			}
			n, err := intArg("random::seed", args[0])
			if err != nil {
				return Nil, err
			}
			vm.rng = rand.New(rand.NewPCG(uint64(n), 0x5e91))
			return Nil, nil
		}),
	}
}

// ---------------------------------------------------------------- math, time

func floatFn(name string, f func(float64) float64) Value {
	return fnValue(name, func(vm *VM, args []Value, kw []Kwarg) (Value, error) {
		if err := arity(name, args, 1, 1); err != nil {
			return Nil, err
		}
		x, err := numArg(name, args[0])
		if err != nil {
			return Nil, err
		}
		return Float(f(x)), nil
	})
}

func roundingFn(name string, f func(float64) float64) Value {
	return fnValue(name, func(vm *VM, args []Value, kw []Kwarg) (Value, error) {
		if err := arity(name, args, 1, 1); err != nil {
			return Nil, err
		}
		if args[0].K == KInt {
			return args[0], nil
		}
		x, err := numArg(name, args[0])
		if err != nil {
			return Nil, err
		}
		r := f(x)
		if math.IsNaN(r) || r < math.MinInt64 || r >= math.MaxInt64 {
			return Nil, errorf("%s(%s) does not fit in an int", name, FormatFloat(x))
		}
		return Int(int64(r)), nil
	})
}

func mathModule(vm *VM) map[string]Value {
	return map[string]Value{
		"pi":    Float(math.Pi),
		"e":     Float(math.E),
		"inf":   Float(math.Inf(1)),
		"sqrt":  floatFn("math::sqrt", math.Sqrt),
		"sin":   floatFn("math::sin", math.Sin),
		"cos":   floatFn("math::cos", math.Cos),
		"tan":   floatFn("math::tan", math.Tan),
		"log":   floatFn("math::log", math.Log),
		"exp":   floatFn("math::exp", math.Exp),
		"floor": roundingFn("math::floor", math.Floor),
		"ceil":  roundingFn("math::ceil", math.Ceil),
		"round": roundingFn("math::round", math.Round),
		"pow": fnValue("math::pow", func(vm *VM, args []Value, kw []Kwarg) (Value, error) {
			if err := arity("math::pow", args, 2, 2); err != nil {
				return Nil, err
			}
			x, err := numArg("math::pow", args[0])
			if err != nil {
				return Nil, err
			}
			y, err := numArg("math::pow", args[1])
			if err != nil {
				return Nil, err
			}
			return Float(math.Pow(x, y)), nil
		}),
	}
}

func timeModule(vm *VM) map[string]Value {
	return map[string]Value{
		// now() is the time in seconds, as a float.
		"now": fnValue("time::now", func(vm *VM, args []Value, kw []Kwarg) (Value, error) {
			if err := arity("time::now", args, 0, 0); err != nil {
				return Nil, err
			}
			return Float(float64(time.Now().UnixNano()) / 1e9), nil
		}),
		"sleep": fnValue("time::sleep", func(vm *VM, args []Value, kw []Kwarg) (Value, error) {
			if err := arity("time::sleep", args, 1, 1); err != nil {
				return Nil, err
			}
			ms, err := numArg("time::sleep", args[0])
			if err != nil {
				return Nil, err
			}
			vm.Out.Flush()
			time.Sleep(time.Duration(ms * float64(time.Millisecond)))
			return Nil, nil
		}),
	}
}
