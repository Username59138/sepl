package vm

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// The standard library is small on purpose: what scripts and small programs
// need, nothing more. Things that can fail for outside reasons (a missing
// file, a network error, bad JSON) return result; programmer mistakes (a bad
// regex, a wrong argument type) are runtime errors.

// SetStdTypes gives the VM the result and option enums of the prelude, so
// library functions can return them.
func (vm *VM) SetStdTypes(result, option Value) {
	vm.resultType, _ = result.O.(*TypeObj)
	vm.optionType, _ = option.O.(*TypeObj)
}

func (vm *VM) variant(t *TypeObj, name string, fields ...Value) Value {
	if t == nil {
		panic("sepl: the standard types are not set")
	}
	v := t.variants[name]
	if len(fields) == 0 && !v.Parens {
		val, _ := t.variantValue(name)
		return val
	}
	return Obj(&EnumValue{Type: t, Variant: v, Fields: fields})
}

func (vm *VM) ok(v Value) Value           { return vm.variant(vm.resultType, "ok", v) }
func (vm *VM) errResult(msg string) Value { return vm.variant(vm.resultType, "err", Str(msg)) }
func (vm *VM) some(v Value) Value         { return vm.variant(vm.optionType, "some", v) }
func (vm *VM) none() Value                { return vm.variant(vm.optionType, "none") }

// fn defines a module function with a fixed number of arguments (lo to hi)
// and the named arguments it accepts.
func fn(name string, lo, hi int, named []string, f func(vm *VM, a []Value, kw map[string]Value) (Value, error)) Value {
	return fnValue(name, func(vm *VM, args []Value, kw []Kwarg) (Value, error) {
		if err := arity(name, args, lo, hi); err != nil {
			return Nil, err
		}
		var m map[string]Value
		for _, k := range kw {
			if indexOf(named, k.Name) < 0 {
				return Nil, errorf("%s() has no parameter named '%s'", name, k.Name)
			}
			if m == nil {
				m = map[string]Value{}
			}
			m[k.Name] = k.Value
		}
		return f(vm, args, m)
	})
}

func strArgs(fn string, args []Value) ([]string, error) {
	out := make([]string, len(args))
	for i, a := range args {
		s, ok := a.AsStr()
		if !ok {
			return nil, errorf("%s() needs str arguments, not %s", fn, TypeName(a))
		}
		out[i] = s
	}
	return out, nil
}

// ---------------------------------------------------------------- fs

func fsModule(vm *VM) map[string]Value {
	return map[string]Value{
		// read(path): result::ok(text) or result::err(msg)
		"read": fn("fs::read", 1, 1, nil, func(vm *VM, a []Value, _ map[string]Value) (Value, error) {
			s, err := strArgs("fs::read", a)
			if err != nil {
				return Nil, err
			}
			data, e := os.ReadFile(s[0])
			if e != nil {
				return vm.errResult(e.Error()), nil
			}
			return vm.ok(Str(string(data))), nil
		}),
		// write(path, text) replaces the file; append(path, text) adds to it.
		"write": fn("fs::write", 2, 2, nil, func(vm *VM, a []Value, _ map[string]Value) (Value, error) {
			s, err := strArgs("fs::write", a)
			if err != nil {
				return Nil, err
			}
			if e := os.WriteFile(s[0], []byte(s[1]), 0o644); e != nil {
				return vm.errResult(e.Error()), nil
			}
			return vm.ok(Nil), nil
		}),
		"append": fn("fs::append", 2, 2, nil, func(vm *VM, a []Value, _ map[string]Value) (Value, error) {
			s, err := strArgs("fs::append", a)
			if err != nil {
				return Nil, err
			}
			f, e := os.OpenFile(s[0], os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			if e == nil {
				_, e = f.WriteString(s[1])
				if cerr := f.Close(); e == nil {
					e = cerr
				}
			}
			if e != nil {
				return vm.errResult(e.Error()), nil
			}
			return vm.ok(Nil), nil
		}),
		"exists": fn("fs::exists", 1, 1, nil, func(vm *VM, a []Value, _ map[string]Value) (Value, error) {
			s, err := strArgs("fs::exists", a)
			if err != nil {
				return Nil, err
			}
			_, e := os.Stat(s[0])
			return Bool(e == nil), nil
		}),
		"is_dir": fn("fs::is_dir", 1, 1, nil, func(vm *VM, a []Value, _ map[string]Value) (Value, error) {
			s, err := strArgs("fs::is_dir", a)
			if err != nil {
				return Nil, err
			}
			fi, e := os.Stat(s[0])
			return Bool(e == nil && fi.IsDir()), nil
		}),
		// list(dir): result::ok(sorted names) or result::err(msg)
		"list": fn("fs::list", 1, 1, nil, func(vm *VM, a []Value, _ map[string]Value) (Value, error) {
			s, err := strArgs("fs::list", a)
			if err != nil {
				return Nil, err
			}
			entries, e := os.ReadDir(s[0])
			if e != nil {
				return vm.errResult(e.Error()), nil
			}
			names := make([]string, len(entries))
			for i, en := range entries {
				names[i] = en.Name()
			}
			sort.Strings(names)
			items := make([]Value, len(names))
			for i, n := range names {
				items[i] = Str(n)
			}
			return vm.ok(NewList(items)), nil
		}),
		// mkdir(path) creates the directory and any missing parents.
		"mkdir": fn("fs::mkdir", 1, 1, nil, func(vm *VM, a []Value, _ map[string]Value) (Value, error) {
			s, err := strArgs("fs::mkdir", a)
			if err != nil {
				return Nil, err
			}
			if e := os.MkdirAll(s[0], 0o755); e != nil {
				return vm.errResult(e.Error()), nil
			}
			return vm.ok(Nil), nil
		}),
		// remove(path) removes a file or an empty directory.
		"remove": fn("fs::remove", 1, 1, nil, func(vm *VM, a []Value, _ map[string]Value) (Value, error) {
			s, err := strArgs("fs::remove", a)
			if err != nil {
				return Nil, err
			}
			if e := os.Remove(s[0]); e != nil {
				return vm.errResult(e.Error()), nil
			}
			return vm.ok(Nil), nil
		}),
		// join("a", "b", "c.txt") builds a path with the system's separator.
		"join": fn("fs::join", 1, 1<<16, nil, func(vm *VM, a []Value, _ map[string]Value) (Value, error) {
			s, err := strArgs("fs::join", a)
			if err != nil {
				return Nil, err
			}
			return Str(filepath.Join(s...)), nil
		}),
	}
}

// ---------------------------------------------------------------- os

func osModule(vm *VM) map[string]Value {
	return map[string]Value{
		// "linux", "darwin", "windows", ...
		"platform": Str(runtime.GOOS),
		// env(name): option::some(value) or option::none
		"env": fn("os::env", 1, 1, nil, func(vm *VM, a []Value, _ map[string]Value) (Value, error) {
			s, err := strArgs("os::env", a)
			if err != nil {
				return Nil, err
			}
			if v, ok := os.LookupEnv(s[0]); ok {
				return vm.some(Str(v)), nil
			}
			return vm.none(), nil
		}),
		"cwd": fn("os::cwd", 0, 0, nil, func(vm *VM, a []Value, _ map[string]Value) (Value, error) {
			d, e := os.Getwd()
			if e != nil {
				return Nil, opError(e.Error())
			}
			return Str(d), nil
		}),
		// run("git", "status"): result::ok(output) if the command succeeds,
		// otherwise result::err with its exit status and output.
		"run": fn("os::run", 1, 1<<16, nil, func(vm *VM, a []Value, _ map[string]Value) (Value, error) {
			s, err := strArgs("os::run", a)
			if err != nil {
				return Nil, err
			}
			vm.Out.Flush()
			out, e := exec.Command(s[0], s[1:]...).CombinedOutput()
			if e != nil {
				msg := e.Error()
				if text := strings.TrimSpace(string(out)); text != "" {
					msg += ": " + text
				}
				return vm.errResult(msg), nil
			}
			return vm.ok(Str(string(out))), nil
		}),
	}
}
