// Package interp ties the parser, the compiler and the VM together: it runs
// files and REPL input and reports errors with their source lines.
package interp

import (
	_ "embed"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Username59138/sepl/compiler"
	"github.com/Username59138/sepl/diag"
	"github.com/Username59138/sepl/parser"
	"github.com/Username59138/sepl/token"
	"github.com/Username59138/sepl/vm"
)

// Error is any error from running SEPL code, ready to show to a person.
type Error struct {
	Kind  string // "syntax", "compile" or "runtime"
	File  string
	Src   string
	Pos   token.Pos
	Msg   string
	Trace []vm.TraceEntry // runtime errors: the calls that led here
	Extra []*Error        // further syntax or compile errors
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s:%s: %s", e.File, e.Pos, e.Msg)
}

// Format renders the error (and any further ones) with source lines.
func (e *Error) Format() string {
	var b strings.Builder
	for _, x := range append([]*Error{e}, e.Extra...) {
		label := "error"
		if x.Kind == "runtime" {
			label = "runtime error"
		}
		b.WriteString(diag.FormatLabel(x.File, x.Src, x.Pos, label, x.Msg))
	}
	if n := len(e.Extra) + 1; n > 1 {
		fmt.Fprintf(&b, "%d errors\n", n)
	}
	if len(e.Trace) > 1 {
		b.WriteString("called from:\n")
		for _, t := range e.Trace[1:] {
			fmt.Fprintf(&b, "  %s:%s in %s\n", t.File, t.Pos, t.Func)
		}
	}
	return b.String()
}

// Session runs a program and the modules it imports, or a whole REPL. Its
// Globals belong to the main program (or the REPL).
type Session struct {
	Globals *compiler.Globals
	VM      *vm.VM
	srcs    map[string]string // sources by file name, for error messages

	prelude *compiler.Globals        // the standard traits, given to every module
	modules map[string]*vm.ModuleObj // loaded modules by absolute path
	loading []string                 // modules being loaded, to catch import cycles
	mainAbs string                   // absolute path of the main file
}

//go:embed prelude.sepl
var preludeSrc string

// NewSession creates a session that prints to out and reads input from in
// (nil means the terminal). It starts with the standard traits defined.
func NewSession(out io.Writer, in io.Reader) *Session {
	s := &Session{
		VM:      vm.New(out, in),
		srcs:    map[string]string{},
		prelude: compiler.NewGlobals("<prelude>"),
		modules: map[string]*vm.ModuleObj{},
	}
	proto, err := s.compile(s.prelude, "<prelude>", preludeSrc, false)
	if err == nil {
		_, err = s.run(proto)
	}
	if err != nil {
		panic("sepl: the prelude does not run: " + err.Error())
	}
	s.Globals = s.newGlobals("main")
	s.VM.Importer = s.importModule
	return s
}

// newGlobals makes the globals of a module, with the standard traits in it.
func (s *Session) newGlobals(module string) *compiler.Globals {
	g := compiler.NewGlobals(module)
	for name, v := range s.prelude.Exports() {
		g.Define(name, v)
	}
	return g
}

// importModule loads "import name" for code in fromFile: name.sepl next to
// that file (net::http is net/http.sepl), otherwise a built-in module.
func (s *Session) importModule(name, fromFile string) (*vm.ModuleObj, error) {
	dir := "."
	if !strings.HasPrefix(fromFile, "<") {
		dir = filepath.Dir(fromFile)
	}
	path := filepath.Join(dir, filepath.FromSlash(strings.ReplaceAll(name, "::", "/"))+".sepl")
	if _, err := os.Stat(path); err != nil {
		if vm.HasModule(name) {
			return s.VM.BuiltinModule(name)
		}
		return nil, fmt.Errorf("module '%s' not found: there is no %s and no built-in module %s (built-in: %s)",
			name, path, name, strings.Join(vm.ModuleNames(), ", "))
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	if m, ok := s.modules[abs]; ok {
		return m, nil
	}
	if abs == s.mainAbs || contains(s.loading, abs) {
		chain := append(append([]string{}, s.loading...), abs)
		for i, c := range chain {
			chain[i] = filepath.Base(c)
		}
		return nil, fmt.Errorf("circular import: %s; move what both need into a third module", strings.Join(chain, " imports "))
	}
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	s.loading = append(s.loading, abs)
	defer func() { s.loading = s.loading[:len(s.loading)-1] }()

	g := s.newGlobals(name)
	proto, err := s.compile(g, path, string(src), false)
	if err != nil {
		return nil, err // a syntax or compile error, shown with the module's source
	}
	if _, err := s.VM.Call(vm.Obj(&vm.Closure{Proto: proto}), nil); err != nil {
		return nil, err
	}
	m := &vm.ModuleObj{Name: name, Members: g.Exports()}
	s.modules[abs] = m
	return m, nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// PassThrough marks errors that the VM hands back unchanged.
func (e *Error) PassThrough() {}

// Close restores the terminal if the program changed it (term::key).
func (s *Session) Close() { s.VM.Close() }

// Compile parses and compiles source code of the main program.
func (s *Session) Compile(file, src string, replValue bool) (*vm.Proto, error) {
	return s.compile(s.Globals, file, src, replValue)
}

func (s *Session) compile(g *compiler.Globals, file, src string, replValue bool) (*vm.Proto, error) {
	s.srcs[file] = src
	prog, err := parser.Parse(src)
	if err != nil {
		list := err.(parser.ErrorList)
		e := &Error{Kind: "syntax", File: file, Src: src, Pos: list[0].Pos, Msg: list[0].Msg}
		for _, x := range list[1:] {
			e.Extra = append(e.Extra, &Error{Kind: "syntax", File: file, Src: src, Pos: x.Pos, Msg: x.Msg})
		}
		return nil, e
	}
	proto, err := compiler.Compile(prog, g, file, replValue)
	if err != nil {
		list := err.(compiler.ErrorList)
		e := &Error{Kind: "compile", File: file, Src: src, Pos: list[0].Pos, Msg: list[0].Msg}
		for _, x := range list[1:] {
			e.Extra = append(e.Extra, &Error{Kind: "compile", File: file, Src: src, Pos: x.Pos, Msg: x.Msg})
		}
		return nil, e
	}
	return proto, nil
}

func (s *Session) wrap(err error) error {
	switch e := err.(type) {
	case nil:
		return nil
	case *vm.ExitError, *Error:
		return e
	case *vm.RuntimeError:
		out := &Error{Kind: "runtime", Msg: e.Msg, Trace: e.Trace}
		// Point at the user's code, not inside the prelude: an error in
		// unwrap() is reported where unwrap was called.
		for len(out.Trace) > 1 && out.Trace[0].File == "<prelude>" {
			out.Trace = out.Trace[1:]
		}
		if len(out.Trace) > 0 {
			out.File, out.Pos = out.Trace[0].File, out.Trace[0].Pos
			out.Src = s.srcs[out.File]
		}
		return out
	}
	return &Error{Kind: "runtime", Msg: err.Error()}
}

// run executes a compiled script.
func (s *Session) run(proto *vm.Proto) (vm.Value, error) {
	v, err := s.VM.Run(&vm.Closure{Proto: proto})
	s.VM.Out.Flush()
	return v, s.wrap(err)
}

// Eval runs one piece of REPL input and returns the value of its last
// expression (nil if it ended with a statement).
func (s *Session) Eval(src string) (vm.Value, error) {
	s.Globals.REPL = true
	proto, err := s.Compile("<repl>", src, true)
	if err != nil {
		return vm.Nil, err
	}
	return s.run(proto)
}

// RunFile runs a program: its top-level code, then main(args) if it has a
// main function. It returns the exit code: main's int result, the code given
// to exit(), 1 after an error, otherwise 0.
func (s *Session) RunFile(file, src string, args []string) (int, error) {
	if abs, err := filepath.Abs(file); err == nil {
		s.mainAbs = abs
	}
	proto, err := s.Compile(file, src, false)
	if err != nil {
		return 1, err
	}
	if _, err := s.run(proto); err != nil {
		return exitCode(err), err
	}

	idx, ok := s.Globals.Lookup("main")
	if !ok {
		return 0, nil
	}
	mainFn, ok := s.Globals.Env.Get(idx)
	if !ok {
		return 0, nil
	}
	cl, ok := mainFn.O.(*vm.Closure)
	if !ok {
		return 0, nil
	}
	var callArgs []vm.Value
	switch {
	case cl.Proto.NParams == 0:
	case cl.Proto.NParams == 1:
		items := make([]vm.Value, len(args))
		for i, a := range args {
			items[i] = vm.Str(a)
		}
		callArgs = []vm.Value{vm.NewList(items)}
	default:
		e := &Error{Kind: "runtime", File: file, Src: src, Pos: cl.Proto.At,
			Msg: "main must take no parameters or one (the list of command-line arguments)"}
		return 1, e
	}
	result, err := s.VM.CallTop(mainFn, callArgs)
	s.VM.Out.Flush()
	if err != nil {
		err = s.wrap(err)
		return exitCode(err), err
	}
	if result.K == vm.KInt {
		return int(result.AsInt()), nil
	}
	return 0, nil
}

func exitCode(err error) int {
	if e, ok := err.(*vm.ExitError); ok {
		return e.Code
	}
	return 1
}

// Disasm compiles a file and returns its bytecode listing.
func (s *Session) Disasm(file, src string) (string, error) {
	proto, err := s.Compile(file, src, false)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	proto.Disasm(&b)
	return b.String(), nil
}
