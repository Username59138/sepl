// Package compiler turns a parsed SEPL program into bytecode for the VM.
//
// The compiler tracks the exact stack height at every instruction. A local
// variable is simply the stack slot its value was pushed into, so locals work
// the same at statement level and inside expressions (for example an if
// expression whose block declares a variable while operands sit below it).
package compiler

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Username59138/sepl/ast"
	"github.com/Username59138/sepl/token"
	"github.com/Username59138/sepl/vm"
)

// Error is a compile error.
type Error struct {
	Pos token.Pos
	Msg string
}

func (e *Error) Error() string { return e.Pos.String() + ": " + e.Msg }

// ErrorList is every compile error found, sorted by position.
type ErrorList []*Error

func (l ErrorList) Error() string {
	if len(l) == 1 {
		return l[0].Error()
	}
	return fmt.Sprintf("%s (and %d more errors)", l[0], len(l)-1)
}

// Globals is the table of global names of one module (a file, or every line
// typed into the REPL), so later code sees earlier globals. The values live
// in Env, which the compiled functions of the module point to.
type Globals struct {
	Env     *vm.Env
	index   map[string]int
	consts  map[string]bool
	typed   map[string]bool // globals with a fixed type
	own     map[string]bool // declared by the module's code (not given to it)
	prelude map[string]bool
	// REPL allows declaring the same global again.
	REPL bool
}

func NewGlobals(module string) *Globals {
	return &Globals{
		Env:     vm.NewEnv(module),
		index:   map[string]int{},
		consts:  map[string]bool{},
		typed:   map[string]bool{},
		own:     map[string]bool{},
		prelude: map[string]bool{},
	}
}

// Names lists the globals by index.
func (g *Globals) Names() []string { return g.Env.Names }

// Define gives the module a global with a value, such as a standard trait.
func (g *Globals) Define(name string, v vm.Value) {
	i := g.declare(name, true)
	g.Env.Set(i, v)
	g.prelude[name] = true
}

// Exports returns the public globals the module's own code defined: not
// hidden ones and not names starting with _.
func (g *Globals) Exports() map[string]vm.Value {
	out := map[string]vm.Value{}
	for i, name := range g.Env.Names {
		if !g.own[name] || strings.HasPrefix(name, "_") || strings.HasPrefix(name, hiddenPrefix) {
			continue
		}
		if v, ok := g.Env.Get(i); ok {
			out[name] = v
		}
	}
	return out
}

// hiddenPrefix starts the names of compiler-made variables, such as the
// one that holds the type of a typed variable.
const hiddenPrefix = "\x00"

func typeVar(name string) string { return hiddenPrefix + "type " + name }

// Lookup returns the index of a global.
func (g *Globals) Lookup(name string) (int, bool) {
	i, ok := g.index[name]
	return i, ok
}

func (g *Globals) declare(name string, isConst bool) int {
	i, ok := g.index[name]
	if !ok {
		i = g.Env.Add(name)
		g.index[name] = i
	}
	g.consts[name] = isConst
	return i
}

var builtinIndex = func() map[string]int {
	m := map[string]int{}
	for i, n := range vm.BuiltinNames() {
		m[n] = i
	}
	return m
}()

type local struct {
	name     string
	slot     int
	depth    int
	captured bool
	isConst  bool
	typeName string // the hidden variable holding its type, if it has one
}

type upvalRef struct {
	name    string
	index   int
	local   bool
	isConst bool
}

type loopInfo struct {
	start   int   // continue jumps here; -1: to the end of the body (conts)
	contSp  int   // stack height at continue
	breakSp int   // stack height at the exit
	breaks  []int // jumps to patch to the exit
	conts   []int // jumps to patch to the end of the body
}

// funcState is the compiler state of one function being compiled.
type funcState struct {
	parent   *funcState
	proto    *vm.Proto
	locals   []local
	upvals   []upvalRef
	depth    int // block depth; 0 is the top of the function
	sp       int // stack height, relative to the frame base
	loops    []*loopInfo
	isScript bool
	consts   map[any]int
	retSlot  int // local holding the declared result type, or -1
}

type compiler struct {
	g       *Globals
	f       *funcState
	file    string
	errs    ErrorList
	defined map[string]bool // globals declared by this compilation
}

// Compile compiles a program into the top-level function of a script. With
// replValue set, a final expression statement becomes the script's result.
func Compile(prog *ast.Program, g *Globals, file string, replValue bool) (*vm.Proto, error) {
	c := &compiler{g: g, file: file, defined: map[string]bool{}}
	c.f = &funcState{
		proto:    &vm.Proto{Name: "<script>", File: file, At: token.Pos{Line: 1, Col: 1}, Env: g.Env},
		isScript: true,
		consts:   map[any]int{},
		retSlot:  -1,
	}

	// Declare every top-level name first, so functions can use globals that
	// are defined further down the file.
	for _, s := range prog.Stmts {
		switch s := s.(type) {
		case *ast.LetStmt:
			g.declare(s.Name, false)
			g.typed[s.Name] = s.Type != nil || s.Infer
			if g.typed[s.Name] {
				g.declare(typeVar(s.Name), true)
			}
		case *ast.ConstStmt:
			g.declare(s.Name, true)
		case *ast.FnDecl:
			g.declare(s.Name, false)
		case *ast.StructDecl:
			g.declare(s.Name, true)
		case *ast.EnumDecl:
			g.declare(s.Name, true)
		case *ast.ImportStmt:
			g.declare(importName(s), true)
		}
	}

	for i, s := range prog.Stmts {
		if es, ok := s.(*ast.ExprStmt); ok && replValue && i == len(prog.Stmts)-1 {
			c.expr(es.X)
			c.emit(vm.OpReturn, 0, es.Pos(), -1)
			continue
		}
		c.stmt(s)
	}
	c.emit(vm.OpReturnNil, 0, token.Pos{}, 0)

	if len(c.errs) > 0 {
		sort.SliceStable(c.errs, func(i, j int) bool {
			a, b := c.errs[i].Pos, c.errs[j].Pos
			return a.Line < b.Line || a.Line == b.Line && a.Col < b.Col
		})
		return nil, c.errs
	}
	return c.f.proto, nil
}

func (c *compiler) errorf(pos token.Pos, format string, args ...any) {
	c.errs = append(c.errs, &Error{Pos: pos, Msg: fmt.Sprintf(format, args...)})
}

// ---------------------------------------------------------------- emitting

func (c *compiler) adjust(delta int) {
	f := c.f
	f.sp += delta
	if f.sp < 0 {
		panic(fmt.Sprintf("compiler: stack height below zero in %s", f.proto.Name))
	}
	if f.sp > f.proto.MaxStack {
		f.proto.MaxStack = f.sp
	}
}

// emit appends an instruction and changes the tracked stack height by delta.
// It returns the instruction's index.
func (c *compiler) emit(op vm.Op, a int, pos token.Pos, delta int) int {
	if a < 0 || a > vm.MaxOperand {
		c.errorf(pos, "the function is too large")
		a = 0
	}
	p := c.f.proto
	p.Code = append(p.Code, vm.Encode(op, a))
	p.Pos = append(p.Pos, pos)
	c.adjust(delta)
	return len(p.Code) - 1
}

// word appends an extra operand word to the last instruction.
func (c *compiler) word(w int, pos token.Pos) {
	p := c.f.proto
	p.Code = append(p.Code, uint32(w))
	p.Pos = append(p.Pos, pos)
}

func (c *compiler) here() int { return len(c.f.proto.Code) }

// patch makes the jump at index at go to the current position.
func (c *compiler) patch(at int) {
	code := c.f.proto.Code
	code[at] = code[at]&0xff | uint32(len(code))<<8
}

func (c *compiler) constant(v vm.Value, key any) int {
	if key != nil {
		if i, ok := c.f.consts[key]; ok {
			return i
		}
	}
	p := c.f.proto
	p.Consts = append(p.Consts, v)
	i := len(p.Consts) - 1
	if key != nil {
		c.f.consts[key] = i
	}
	return i
}

type strKey string
type methodKey string

func (c *compiler) strConst(s string) int { return c.constant(vm.Str(s), strKey(s)) }

// ---------------------------------------------------------------- scopes

func (c *compiler) beginScope() { c.f.depth++ }

// endScope removes the scope's locals. With keepTop, the value on top of the
// stack (the block's value) stays.
func (c *compiler) endScope(pos token.Pos, keepTop bool) {
	f := c.f
	n, captured := 0, false
	for len(f.locals) > 0 && f.locals[len(f.locals)-1].depth == f.depth {
		l := f.locals[len(f.locals)-1]
		captured = captured || l.captured
		f.locals = f.locals[:len(f.locals)-1]
		n++
	}
	f.depth--
	if n == 0 {
		return
	}
	switch {
	case keepTop && captured:
		c.emit(vm.OpCloseSlide, n, pos, -n)
	case keepTop:
		c.emit(vm.OpSlide, n, pos, -n)
	case captured:
		c.emit(vm.OpCloseN, n, pos, -n)
	case n == 1:
		c.emit(vm.OpPop, 0, pos, -1)
	default:
		c.emit(vm.OpPopN, n, pos, -n)
	}
}

// addLocal declares a local variable living in the given stack slot.
func (c *compiler) addLocal(name string, slot int, pos token.Pos, isConst bool) {
	f := c.f
	for i := len(f.locals) - 1; i >= 0 && f.locals[i].depth == f.depth; i-- {
		if f.locals[i].name == name && name != "_" && name != "" {
			c.errorf(pos, "'%s' is already declared in this block", name)
			break
		}
	}
	f.locals = append(f.locals, local{name: name, slot: slot, depth: f.depth, isConst: isConst})
}

// atGlobalLevel reports whether a declaration here creates a global.
func (c *compiler) atGlobalLevel() bool { return c.f.isScript && c.f.depth == 0 }

func (c *compiler) defineGlobal(name string, pos token.Pos, isConst bool) int {
	if c.defined[name] && !c.g.REPL {
		c.errorf(pos, "'%s' is already declared", name)
	}
	c.defined[name] = true
	c.g.own[name] = true
	return c.g.declare(name, isConst)
}

// unwindTo emits code that drops the stack down to height sp (for break,
// continue), without changing the tracked height of the code that follows.
func (c *compiler) unwindTo(sp int, pos token.Pos) {
	f := c.f
	n := f.sp - sp
	if n <= 0 {
		return
	}
	captured := false
	for _, l := range f.locals {
		if l.slot >= sp && l.captured {
			captured = true
		}
	}
	if captured {
		c.emit(vm.OpCloseN, n, pos, 0)
	} else {
		c.emit(vm.OpPopN, n, pos, 0)
	}
}

// ---------------------------------------------------------------- names

type varKind int

const (
	varNone varKind = iota
	varLocal
	varUpval
	varGlobal
	varBuiltin
)

func findLocal(f *funcState, name string) int {
	for i := len(f.locals) - 1; i >= 0; i-- {
		if f.locals[i].name == name {
			return i
		}
	}
	return -1
}

func addUpval(f *funcState, name string, index int, isLocal, isConst bool) int {
	for i, u := range f.upvals {
		if u.index == index && u.local == isLocal {
			return i
		}
	}
	f.upvals = append(f.upvals, upvalRef{name: name, index: index, local: isLocal, isConst: isConst})
	return len(f.upvals) - 1
}

// resolveUpval finds name in the enclosing functions of f.
func resolveUpval(f *funcState, name string) (int, bool) {
	if f.parent == nil {
		return -1, false
	}
	if i := findLocal(f.parent, name); i >= 0 {
		l := &f.parent.locals[i]
		// Globals of the script are not locals, so this only captures
		// variables declared in blocks or functions.
		l.captured = true
		return addUpval(f, name, l.slot, true, l.isConst), l.isConst
	}
	if i, isConst := resolveUpval(f.parent, name); i >= 0 {
		return addUpval(f, name, i, false, isConst), isConst
	}
	return -1, false
}

// resolve says where a name lives.
func (c *compiler) resolve(name string) (varKind, int, bool) {
	if i := findLocal(c.f, name); i >= 0 {
		l := c.f.locals[i]
		return varLocal, l.slot, l.isConst
	}
	if i, isConst := resolveUpval(c.f, name); i >= 0 {
		return varUpval, i, isConst
	}
	if i, ok := c.g.Lookup(name); ok {
		return varGlobal, i, c.g.consts[name]
	}
	if i, ok := builtinIndex[name]; ok {
		return varBuiltin, i, true
	}
	return varNone, 0, false
}

func (c *compiler) loadName(name string, pos token.Pos) {
	kind, i, _ := c.resolve(name)
	switch kind {
	case varLocal:
		c.emit(vm.OpGetLocal, i, pos, 1)
	case varUpval:
		c.emit(vm.OpGetUpval, i, pos, 1)
	case varGlobal:
		c.emit(vm.OpGetGlobal, i, pos, 1)
	case varBuiltin:
		c.emit(vm.OpGetBuiltin, i, pos, 1)
	default:
		c.errorf(pos, "undefined name '%s'", name)
		c.emit(vm.OpNil, 0, pos, 1)
	}
}

// typeCompanion returns the name of the hidden variable that holds the type
// of a typed variable, or "" if the variable has no fixed type.
func (c *compiler) typeCompanion(name string) string {
	for f := c.f; f != nil; f = f.parent {
		if i := findLocal(f, name); i >= 0 {
			return f.locals[i].typeName
		}
	}
	if _, ok := c.g.Lookup(name); ok && c.g.typed[name] {
		return typeVar(name)
	}
	return ""
}

// checkTop checks the value on top of the stack against the type held by the
// variable typeName.
func (c *compiler) checkTop(typeName, ctx string, pos token.Pos) {
	c.loadName(typeName, pos)
	c.emit(vm.OpCheckType, c.strConst(ctx), pos, -1)
}

// storeName pops the top of the stack into a variable.
func (c *compiler) storeName(name string, pos token.Pos) {
	kind, i, isConst := c.resolve(name)
	if isConst && kind != varBuiltin && kind != varNone {
		c.errorf(pos, "cannot assign to '%s': it is a const", name)
	}
	if tn := c.typeCompanion(name); tn != "" && kind != varNone && kind != varBuiltin {
		c.checkTop(tn, name, pos)
		kind, i, _ = c.resolve(name) // loading the type may have added an upvalue
	}
	switch kind {
	case varLocal:
		c.emit(vm.OpSetLocal, i, pos, -1)
	case varUpval:
		c.emit(vm.OpSetUpval, i, pos, -1)
	case varGlobal:
		c.emit(vm.OpSetGlobal, i, pos, -1)
	case varBuiltin:
		c.errorf(pos, "cannot assign to the builtin '%s'; declare your own with 'let %s = ...'", name, name)
		c.emit(vm.OpPop, 0, pos, -1)
	default:
		c.errorf(pos, "undefined variable '%s' (declare it with 'let %s = ...')", name, name)
		c.emit(vm.OpPop, 0, pos, -1)
	}
}

// ---------------------------------------------------------------- statements

func (c *compiler) stmt(s ast.Stmt) {
	switch s := s.(type) {
	case *ast.ExprStmt:
		switch x := s.X.(type) {
		case *ast.IfExpr:
			c.ifExpr(x, false)
			return
		case *ast.MatchExpr:
			c.match(x, false)
			return
		}
		c.expr(s.X)
		c.emit(vm.OpPop, 0, s.Pos(), -1)

	case *ast.LetStmt:
		if s.Value != nil {
			c.expr(s.Value)
		} else {
			c.emit(vm.OpNil, 0, s.At, 1)
		}
		switch {
		case s.Type != nil:
			c.typeRef(s.Type)
			c.emit(vm.OpCheckKeep, c.strConst(s.Name), s.At, 0)
		case s.Infer:
			c.emit(vm.OpTypeOf, c.strConst(s.Name), s.At, 1)
		default:
			c.declare(s.Name, s.At, false)
			return
		}
		c.declareTyped(s.Name, s.At)

	case *ast.ConstStmt:
		if s.Value == nil {
			c.errorf(s.At, "const '%s' needs a value: const %s = ...", s.Name, s.Name)
			c.emit(vm.OpNil, 0, s.At, 1)
		} else {
			c.expr(s.Value)
		}
		if s.Type != nil {
			c.typeRef(s.Type)
			c.emit(vm.OpCheckType, c.strConst(s.Name), s.At, -1)
		}
		c.declare(s.Name, s.At, true)

	case *ast.AssignStmt:
		c.assign(s)

	case *ast.ReturnStmt:
		if c.f.isScript {
			c.errorf(s.At, "'return' outside a function")
		}
		if s.Value != nil {
			c.expr(s.Value)
			c.checkResult(s.At)
			c.emit(vm.OpReturn, 0, s.At, -1)
		} else {
			c.emit(vm.OpReturnNil, 0, s.At, 0)
		}

	case *ast.BreakStmt:
		if len(c.f.loops) == 0 {
			c.errorf(s.At, "'break' outside a loop")
			return
		}
		l := c.f.loops[len(c.f.loops)-1]
		c.unwindTo(l.breakSp, s.At)
		l.breaks = append(l.breaks, c.emit(vm.OpJump, 0, s.At, 0))

	case *ast.ContinueStmt:
		if len(c.f.loops) == 0 {
			c.errorf(s.At, "'continue' outside a loop")
			return
		}
		l := c.f.loops[len(c.f.loops)-1]
		c.unwindTo(l.contSp, s.At)
		if l.start >= 0 {
			c.emit(vm.OpJump, l.start, s.At, 0)
		} else {
			l.conts = append(l.conts, c.emit(vm.OpJump, 0, s.At, 0))
		}

	case *ast.WhileStmt:
		c.whileStmt(s)
	case *ast.ForStmt:
		c.forStmt(s)
	case *ast.FnDecl:
		c.fnDecl(s)

	case *ast.StructDecl:
		c.structDecl(s)
	case *ast.ImplDecl:
		c.typeRef(s.Type)
		desc, n := c.methods(s.Methods, s.Type.String())
		c.emit(vm.OpImpl, c.constant(vm.Obj(desc), nil), s.At, -1-n)
	case *ast.AddDecl:
		c.typeRef(s.Source)
		c.typeRef(s.Target)
		if s.Kind == token.STRUCT {
			c.emit(vm.OpAddStruct, 0, s.At, -2)
			return
		}
		desc, n := c.methods(s.Methods, s.Target.String())
		c.emit(vm.OpAddImpl, c.constant(vm.Obj(desc), nil), s.At, -2-n)
	case *ast.EnumDecl:
		c.enumDecl(s)
	case *ast.ImportStmt:
		name := strings.Join(s.Path, "::")
		c.emit(vm.OpImport, c.strConst(name), s.At, 1)
		c.declare(importName(s), s.At, true)
	case *ast.MacroDecl:
		c.errorf(s.At, "macros are not supported yet")
	default:
		c.errorf(s.Pos(), "internal error: unknown statement %T", s)
	}
}

// declareTyped binds a value and its type (the top two stack values) to a
// new variable whose assignments are checked against the type.
func (c *compiler) declareTyped(name string, pos token.Pos) {
	hidden := typeVar(name)
	if c.atGlobalLevel() {
		c.emit(vm.OpDefGlobal, c.g.declare(hidden, true), pos, -1)
		c.emit(vm.OpDefGlobal, c.defineGlobal(name, pos, false), pos, -1)
		c.g.typed[name] = true
		return
	}
	c.addLocal(name, c.f.sp-2, pos, false)
	c.f.locals[len(c.f.locals)-1].typeName = hidden
	c.addLocal(hidden, c.f.sp-1, pos, true)
}

// checkResult checks the value on top of the stack against the declared
// result type of the current function, if it has one.
func (c *compiler) checkResult(pos token.Pos) {
	if c.f.retSlot < 0 {
		return
	}
	c.emit(vm.OpCheckResult, c.f.retSlot, pos, 0)
}

// declare binds the value on top of the stack to a new variable.
func (c *compiler) declare(name string, pos token.Pos, isConst bool) {
	if c.atGlobalLevel() {
		c.emit(vm.OpDefGlobal, c.defineGlobal(name, pos, isConst), pos, -1)
		c.g.typed[name] = false
		return
	}
	c.addLocal(name, c.f.sp-1, pos, isConst)
}

var compoundOps = map[token.Type]vm.Op{
	token.PLUS_ASSIGN:        vm.OpAdd,
	token.MINUS_ASSIGN:       vm.OpSub,
	token.STAR_ASSIGN:        vm.OpMul,
	token.SLASH_ASSIGN:       vm.OpDiv,
	token.SLASH_SLASH_ASSIGN: vm.OpIDiv,
	token.PERCENT_ASSIGN:     vm.OpMod,
}

func (c *compiler) assign(s *ast.AssignStmt) {
	op, compound := compoundOps[s.Op]
	switch t := s.Target.(type) {
	case *ast.Ident:
		if c.incLocal(s, t) {
			return
		}
		if compound {
			c.loadName(t.Name, t.At)
			c.expr(s.Value)
			c.emit(op, 0, s.At, -1)
		} else {
			c.expr(s.Value)
		}
		c.storeName(t.Name, t.At)
	case *ast.Index:
		c.expr(t.X)
		c.expr(t.Index)
		if compound {
			c.emit(vm.OpDup2, 0, t.At, 2)
			c.emit(vm.OpIndex, 0, t.At, -1)
			c.expr(s.Value)
			c.emit(op, 0, s.At, -1)
		} else {
			c.expr(s.Value)
		}
		c.emit(vm.OpSetIndex, 0, s.At, -3)
	case *ast.Scope:
		c.errorf(t.At, "cannot assign to %s: '::' names constants, methods and module members, which never change", ast.String(t))
	case *ast.Member:
		name := c.strConst(t.Name)
		c.expr(t.X)
		if compound {
			c.emit(vm.OpDup, 0, t.At, 1)
			c.emit(vm.OpGetField, name, t.At, 0)
			c.expr(s.Value)
			c.emit(op, 0, s.At, -1)
		} else {
			c.expr(s.Value)
		}
		c.emit(vm.OpSetField, name, t.At, -2)
	default:
		c.errorf(s.At, "cannot assign to this expression")
	}
}

// incLocal compiles "x += k", "x -= k" and "x += y" on local variables
// into a single instruction. It reports whether it did.
func (c *compiler) incLocal(s *ast.AssignStmt, t *ast.Ident) bool {
	if s.Op != token.PLUS_ASSIGN && s.Op != token.MINUS_ASSIGN {
		return false
	}
	kind, slot, isConst := c.resolve(t.Name)
	if kind != varLocal || isConst || slot > 0xfff || c.typeCompanion(t.Name) != "" {
		return false
	}
	if k, ok := smallInt(s.Value); ok {
		if s.Op == token.MINUS_ASSIGN {
			k = -k
		}
		if k >= -2048 && k < 2048 {
			c.raw(vm.EncodePair(vm.OpIncLocal, slot, int(k)), s.At, 0)
			return true
		}
		return false
	}
	if id, ok := s.Value.(*ast.Ident); ok && s.Op == token.PLUS_ASSIGN {
		if k2, slot2, _ := c.resolve(id.Name); k2 == varLocal && slot2 <= 0xfff {
			c.raw(vm.EncodePair(vm.OpAddLocal, slot, slot2), s.At, 0)
			return true
		}
	}
	return false
}

// smallInt returns the value of an int literal (or -literal) that fits a
// 24-bit operand.
func smallInt(e ast.Expr) (int64, bool) {
	switch x := e.(type) {
	case *ast.Paren:
		return smallInt(x.X)
	case *ast.IntLit:
		return x.Value, vm.FitsInt(x.Value)
	case *ast.Unary:
		if lit, ok := x.X.(*ast.IntLit); ok && x.Op == token.MINUS {
			return -lit.Value, vm.FitsInt(-lit.Value)
		}
	}
	return 0, false
}

// raw appends a ready-made instruction word.
func (c *compiler) raw(w uint32, pos token.Pos, delta int) int {
	p := c.f.proto
	p.Code = append(p.Code, w)
	p.Pos = append(p.Pos, pos)
	c.adjust(delta)
	return len(p.Code) - 1
}

var fusedJumps = map[token.Type]vm.Op{
	token.EQ:     vm.OpJumpNotEq,
	token.NOT_EQ: vm.OpJumpNotNe,
	token.LT:     vm.OpJumpNotLt,
	token.LT_EQ:  vm.OpJumpNotLe,
	token.GT:     vm.OpJumpNotGt,
	token.GT_EQ:  vm.OpJumpNotGe,
}

// condJumps compiles a condition and returns the jumps (to be patched) that
// are taken when it is false. Comparisons and 'and' jump directly, without
// building a bool first.
func (c *compiler) condJumps(cond ast.Expr) []int {
	switch x := cond.(type) {
	case *ast.Paren:
		return c.condJumps(x.X)
	case *ast.Binary:
		if x.Op == token.AND {
			return append(c.condJumps(x.X), c.condJumps(x.Y)...)
		}
		if op, ok := fusedJumps[x.Op]; ok {
			c.expr(x.X)
			c.expr(x.Y)
			return []int{c.emit(op, 0, x.At, -2)}
		}
	}
	c.expr(cond)
	return []int{c.emit(vm.OpJumpIfFalse, 0, cond.Pos(), -1)}
}

func (c *compiler) pushLoop(start, contSp, breakSp int) *loopInfo {
	l := &loopInfo{start: start, contSp: contSp, breakSp: breakSp}
	c.f.loops = append(c.f.loops, l)
	return l
}

func (c *compiler) popLoop() {
	l := c.f.loops[len(c.f.loops)-1]
	c.f.loops = c.f.loops[:len(c.f.loops)-1]
	for _, j := range l.breaks {
		c.patch(j)
	}
}

func (c *compiler) whileStmt(s *ast.WhileStmt) {
	start := c.here()
	sp := c.f.sp
	exits := c.condJumps(s.Cond)
	c.pushLoop(start, sp, sp)
	c.block(s.Body, false)
	c.emit(vm.OpJump, start, s.At, 0)
	for _, j := range exits {
		c.patch(j)
	}
	c.popLoop()
}

func (c *compiler) forStmt(s *ast.ForStmt) {
	// The loop keeps hidden slots (the iterator, or a counter and a limit)
	// and a fixed slot for the loop variable, which FORNEXT/FORRANGE
	// overwrite on every iteration.
	var next, hidden int
	if r, ok := s.Iter.(*ast.Binary); ok && r.Op == token.DOT_DOT {
		// for i in lo..hi: count in place, no range object.
		c.expr(r.X)
		c.expr(r.Y)
		c.emit(vm.OpForPrep, 0, r.At, 0)
		c.emit(vm.OpNil, 0, s.At, 1)
		hidden = 3
		next = c.emit(vm.OpForRange, 0, s.At, 0)
		c.word(c.f.sp-3, s.At)
	} else {
		c.expr(s.Iter)
		c.emit(vm.OpIter, 0, s.Iter.Pos(), 0)
		c.emit(vm.OpNil, 0, s.At, 1)
		hidden = 2
		next = c.emit(vm.OpForNext, 0, s.At, 0)
		c.word(c.f.sp-2, s.At)
	}
	varSlot := c.f.sp - 1
	sp := c.f.sp
	loop := c.pushLoop(-1, sp, sp)
	c.beginScope()
	c.addLocal(s.Var, varSlot, s.At, false)
	c.block(s.Body, false)

	// End of an iteration; 'continue' lands here.
	for _, j := range loop.conts {
		c.patch(j)
	}
	captured := c.f.locals[len(c.f.locals)-1].captured
	if captured {
		// Closures made in this iteration keep this iteration's value.
		c.emit(vm.OpCloseFrom, varSlot, s.At, 0)
	}
	c.f.locals = c.f.locals[:len(c.f.locals)-1]
	c.f.depth--
	c.emit(vm.OpJump, next, s.At, 0)

	c.patch(next)
	c.popLoop()
	if captured {
		c.emit(vm.OpCloseN, hidden, s.At, -hidden)
	} else {
		c.emit(vm.OpPopN, hidden, s.At, -hidden)
	}
}

// ---------------------------------------------------------------- functions

func (c *compiler) fnDecl(d *ast.FnDecl) {
	if d.Body == nil {
		c.errorf(d.At, "a function without a body is only allowed inside impl (as a required method)")
		return
	}
	if c.atGlobalLevel() {
		idx := c.defineGlobal(d.Name, d.At, false)
		c.g.typed[d.Name] = false
		c.function(d.Name, d.Params, d.Result, d.Body, d.At)
		c.emit(vm.OpDefGlobal, idx, d.At, -1)
		return
	}
	// Declare the local first so the function can call itself.
	c.addLocal(d.Name, c.f.sp, d.At, false)
	c.function(d.Name, d.Params, d.Result, d.Body, d.At)
}

// function compiles a function body and emits code that pushes its closure.
func (c *compiler) function(name string, params []*ast.Param, result *ast.TypeExpr, body *ast.Block, pos token.Pos) {
	p := &vm.Proto{Name: name, File: c.file, At: pos, NParams: len(params), Env: c.g.Env}
	f := &funcState{parent: c.f, proto: p, consts: map[any]int{}, retSlot: -1}
	for i, prm := range params {
		for _, l := range f.locals {
			if l.name == prm.Name {
				c.errorf(prm.At, "duplicate parameter '%s'", prm.Name)
			}
		}
		f.locals = append(f.locals, local{name: prm.Name, slot: i})
		p.ParamNames = append(p.ParamNames, prm.Name)
		if prm.Variadic {
			p.Variadic = true
		}
	}
	f.sp = len(params)
	p.MaxStack = f.sp

	c.f = f
	f.depth = 1
	// Check typed arguments on entry, and keep their types for assignments.
	for i, prm := range params {
		if prm.Type == nil {
			continue
		}
		hidden := typeVar(prm.Name)
		c.typeRef(prm.Type)
		typeSlot := f.sp - 1
		c.addLocal(hidden, typeSlot, prm.At, true)
		if !prm.Variadic && i <= 0xfff && typeSlot <= 0xfff {
			c.raw(vm.EncodePair(vm.OpCheckParam, i, typeSlot), prm.At, 0)
			f.locals[i].typeName = hidden
			continue
		}
		c.emit(vm.OpGetLocal, i, prm.At, 1)
		c.emit(vm.OpGetLocal, typeSlot, prm.At, 1)
		ctx := c.strConst(fmt.Sprintf("argument '%s' of %s()", prm.Name, name))
		if prm.Variadic {
			c.emit(vm.OpCheckEach, ctx, prm.At, -1)
		} else {
			c.emit(vm.OpCheckArg, ctx, prm.At, -1)
			f.locals[i].typeName = hidden
		}
		c.emit(vm.OpSetLocal, i, prm.At, -1)
	}
	if result != nil {
		c.typeRef(result)
		f.retSlot = f.sp - 1
		c.addLocal(hiddenPrefix+"result", f.retSlot, result.At, true)
	}
	for _, s := range body.Stmts {
		c.stmt(s)
	}
	c.emit(vm.OpReturnNil, 0, pos, 0)
	c.f = f.parent

	for _, u := range f.upvals {
		p.Upvals = append(p.Upvals, vm.UpvalDesc{Local: u.local, Index: u.index, Name: u.name})
	}
	p.MaxStack += 2
	c.emit(vm.OpClosure, c.constant(vm.Obj(p), nil), pos, 1)
}

// ---------------------------------------------------------------- structs

func importName(s *ast.ImportStmt) string {
	if s.Alias != "" {
		return s.Alias
	}
	return s.Path[len(s.Path)-1]
}

// typeRef pushes the type named by a type annotation: point, net::request.
func (c *compiler) typeRef(t *ast.TypeExpr) {
	if t.Expr != nil { // let y type(x): computed when the annotation is checked
		c.expr(t.Expr)
		return
	}
	c.loadName(t.Path[0], t.At)
	for _, part := range t.Path[1:] {
		c.emit(vm.OpScope, c.strConst(part), t.At, 0)
	}
}

// literalValue returns the value of a constant expression (5, -2.5, "s", nil).
func literalValue(e ast.Expr) (vm.Value, bool) {
	switch x := e.(type) {
	case *ast.IntLit:
		return vm.Int(x.Value), true
	case *ast.FloatLit:
		return vm.Float(x.Value), true
	case *ast.StringLit:
		return vm.Str(x.Value), true
	case *ast.BoolLit:
		return vm.Bool(x.Value), true
	case *ast.NilLit:
		return vm.Nil, true
	case *ast.Paren:
		return literalValue(x.X)
	case *ast.Unary:
		if x.Op == token.MINUS {
			switch lit := x.X.(type) {
			case *ast.IntLit:
				return vm.Int(-lit.Value), true
			case *ast.FloatLit:
				return vm.Float(-lit.Value), true
			}
		}
	}
	return vm.Nil, false
}

func (c *compiler) structDecl(d *ast.StructDecl) {
	desc := &vm.StructDesc{Name: d.Name, NParents: len(d.Parents)}
	for _, p := range d.Parents {
		c.typeRef(p)
	}
	seen := map[string]bool{}
	for _, f := range d.Fields {
		if seen[f.Name] {
			c.errorf(f.At, "field '%s' is declared twice in struct %s", f.Name, d.Name)
		}
		seen[f.Name] = true
		fd := vm.FieldDesc{Name: f.Name}
		switch {
		case f.Const && f.Value != nil:
			fd.Mode = vm.FieldStatic
			c.expr(f.Value)
		case f.Const:
			fd.Mode = vm.FieldRequired
		case f.Value == nil:
			fd.Mode = vm.FieldNil
		default:
			if v, ok := literalValue(f.Value); ok {
				fd.Mode, fd.Literal = vm.FieldLiteral, v
			} else {
				// Computed again for every new object, so a default list is
				// never shared between objects.
				fd.Mode = vm.FieldThunk
				body := &ast.Block{Colon: f.At, Stmts: []ast.Stmt{&ast.ReturnStmt{At: f.Value.Pos(), Value: f.Value}}}
				c.function(d.Name+"."+f.Name, nil, nil, body, f.At)
			}
		}
		switch {
		case f.Type != nil:
			c.typeRef(f.Type)
			fd.Typed = true
		case f.Infer:
			fd.Infer = true
		}
		desc.Fields = append(desc.Fields, fd)
	}
	n := desc.Pushed()
	c.emit(vm.OpStruct, c.constant(vm.Obj(desc), nil), d.At, 1-n)
	c.declare(d.Name, d.At, true)
}

func (c *compiler) enumDecl(d *ast.EnumDecl) {
	desc := &vm.EnumDesc{Name: d.Name, NParents: len(d.Parents)}
	for _, p := range d.Parents {
		c.typeRef(p)
	}
	seen := map[string]bool{}
	for _, v := range d.Variants {
		if seen[v.Name] {
			c.errorf(v.At, "variant '%s' is declared twice in enum %s", v.Name, d.Name)
		}
		seen[v.Name] = true
		vd := vm.VariantDesc{Name: v.Name, Parens: v.Parens}
		for _, f := range v.Fields {
			if indexOfStr(vd.Fields, f.Name) >= 0 {
				c.errorf(f.At, "field '%s' is declared twice in %s::%s", f.Name, d.Name, v.Name)
			}
			vd.Fields = append(vd.Fields, f.Name)
			vd.Typed = append(vd.Typed, f.Type != nil)
			if f.Type != nil {
				c.typeRef(f.Type)
			}
		}
		desc.Variants = append(desc.Variants, vd)
	}
	c.emit(vm.OpEnum, c.constant(vm.Obj(desc), nil), d.At, 1-desc.Pushed())
	c.declare(d.Name, d.At, true)
}

func indexOfStr(list []string, s string) int {
	for i, x := range list {
		if x == s {
			return i
		}
	}
	return -1
}

// methods compiles the methods of impl or add impl; it pushes the closures
// of the ones with a body and returns the descriptor and their count.
func (c *compiler) methods(list []*ast.FnDecl, owner string) (*vm.ImplDesc, int) {
	desc := &vm.ImplDesc{}
	seen := map[string]bool{}
	n := 0
	for _, m := range list {
		if seen[m.Name] {
			c.errorf(m.At, "method '%s' is defined twice in this block", m.Name)
		}
		seen[m.Name] = true
		static := len(m.Params) == 0 || m.Params[0].Name != "self"
		desc.Methods = append(desc.Methods, vm.MethodDesc{Name: m.Name, Abstract: m.Body == nil, Static: static})
		if m.Body != nil {
			c.function(owner+"."+m.Name, m.Params, m.Result, m.Body, m.At)
			n++
		}
	}
	return desc, n
}

// ---------------------------------------------------------------- blocks

// block compiles b in its own scope. With value set it leaves the value of
// the block (its last expression, or nil) on the stack.
func (c *compiler) block(b *ast.Block, value bool) {
	c.beginScope()
	for i, s := range b.Stmts {
		if value && i == len(b.Stmts)-1 {
			if es, ok := s.(*ast.ExprStmt); ok {
				c.expr(es.X)
				break
			}
			c.stmt(s)
			c.emit(vm.OpNil, 0, s.Pos(), 1)
			break
		}
		c.stmt(s)
	}
	if value && len(b.Stmts) == 0 {
		c.emit(vm.OpNil, 0, b.Colon, 1)
	}
	c.endScope(b.Colon, value)
}

func (c *compiler) ifExpr(x *ast.IfExpr, value bool) {
	sp := c.f.sp
	jfs := c.condJumps(x.Cond)
	c.block(x.Then, value)
	if x.Else == nil && !value {
		for _, j := range jfs {
			c.patch(j)
		}
		return
	}
	end := c.emit(vm.OpJump, 0, x.At, 0)
	for _, j := range jfs {
		c.patch(j)
	}
	c.f.sp = sp
	switch e := x.Else.(type) {
	case nil:
		c.emit(vm.OpNil, 0, x.At, 1)
	case *ast.Block:
		c.block(e, value)
	case *ast.IfExpr:
		c.ifExpr(e, value)
	}
	c.patch(end)
}

func (c *compiler) match(x *ast.MatchExpr, value bool) {
	c.expr(x.Subject)
	subject := c.f.sp - 1
	sp := c.f.sp
	var ends []int
	for _, arm := range x.Arms {
		var fails []failJump
		if len(arm.Patterns) == 1 {
			// One pattern: it may bind variables, which live in the arm's scope.
			c.beginScope()
			fails = c.testPattern(arm.Patterns[0], subject, true)
			c.block(arm.Body, value)
			c.endScope(arm.At, value)
		} else {
			// Several patterns: try each in turn; none may bind variables.
			var toBody []int
			for _, pat := range arm.Patterns {
				start := c.f.sp
				fs := c.testPattern(pat, subject, false)
				if n := c.f.sp - start; n > 0 {
					c.emit(vm.OpPopN, n, pat.Pos(), -n)
				}
				toBody = append(toBody, c.emit(vm.OpJump, 0, pat.Pos(), 0))
				c.failStubs(fs, start, arm.At)
				c.f.sp = start
			}
			fails = []failJump{{at: c.emit(vm.OpJump, 0, arm.At, 0), sp: sp}}
			for _, j := range toBody {
				c.patch(j)
			}
			c.block(arm.Body, value)
		}
		ends = append(ends, c.emit(vm.OpJump, 0, arm.At, 0))
		// The pattern did not match: drop what the test left and try the next arm.
		c.failStubs(fails, sp, arm.At)
		c.f.sp = sp
	}
	if value {
		c.emit(vm.OpNil, 0, x.At, 1) // no arm matched
	}
	for _, j := range ends {
		c.patch(j)
	}
	if value {
		c.emit(vm.OpSlide, 1, x.At, -1)
	} else {
		c.emit(vm.OpPop, 0, x.At, -1)
	}
}

// failJump is a jump taken when a pattern does not match, and the stack
// height at that point.
type failJump struct {
	at, sp int
}

// failStubs lands the failed-match jumps: each pops what its test left on the
// stack (down to height base), then all continue after the stubs.
func (c *compiler) failStubs(fails []failJump, base int, pos token.Pos) {
	var done []int
	for i, f := range fails {
		if f.sp == base {
			continue
		}
		c.patch(f.at)
		c.f.sp = f.sp
		c.emit(vm.OpPopN, f.sp-base, pos, base-f.sp)
		if i < len(fails)-1 {
			done = append(done, c.emit(vm.OpJump, 0, pos, 0))
		}
	}
	for _, f := range fails {
		if f.sp == base {
			c.patch(f.at)
		}
	}
	for _, j := range done {
		c.patch(j)
	}
	c.f.sp = base
}

// testPattern emits a test of the value in slot against pat and returns the
// jumps taken when it does not match. Values taken out of enum variants stay
// on the stack; with bind set they become (possibly unnamed) locals of the
// current scope, and plain names in the pattern become variables.
func (c *compiler) testPattern(pat ast.Pattern, slot int, bind bool) []failJump {
	switch p := pat.(type) {
	case *ast.WildcardPat:
		return nil
	case *ast.BindPat:
		c.errorf(p.At, "internal error: unexpected variable pattern")
		return nil
	case *ast.ValuePat:
		c.emit(vm.OpGetLocal, slot, p.Pos(), 1)
		c.expr(p.X)
		c.emit(vm.OpMatchEq, 0, p.Pos(), -1)
		return []failJump{{at: c.emit(vm.OpJumpIfFalse, 0, p.Pos(), -1), sp: c.f.sp}}
	case *ast.RangePat:
		c.emit(vm.OpGetLocal, slot, p.Pos(), 1)
		c.expr(p.Lo)
		c.expr(p.Hi)
		c.emit(vm.OpInRange, 0, p.Pos(), -2)
		return []failJump{{at: c.emit(vm.OpJumpIfFalse, 0, p.Pos(), -1), sp: c.f.sp}}
	case *ast.VariantPat:
		c.emit(vm.OpGetLocal, slot, p.Pos(), 1)
		c.expr(p.Path)
		c.emit(vm.OpIsVariant, len(p.Args)+1, p.Pos(), -1)
		fails := []failJump{{at: c.emit(vm.OpJumpIfFalse, 0, p.Pos(), -1), sp: c.f.sp}}
		for i, sub := range p.Args {
			if _, ok := sub.(*ast.WildcardPat); ok {
				continue
			}
			c.emit(vm.OpGetLocal, slot, sub.Pos(), 1)
			c.emit(vm.OpVariantField, i, sub.Pos(), 0)
			field := c.f.sp - 1
			if b, ok := sub.(*ast.BindPat); ok {
				if !bind {
					c.errorf(b.At, "patterns joined with ',' cannot bind variables like '%s'", b.Name)
				}
				c.addLocal(b.Name, field, b.At, false)
				continue
			}
			if bind {
				c.addLocal("", field, sub.Pos(), false)
			}
			fails = append(fails, c.testPattern(sub, field, bind)...)
		}
		return fails
	}
	c.errorf(pat.Pos(), "internal error: unknown pattern %T", pat)
	return nil
}

// ---------------------------------------------------------------- expressions

var binaryOps = map[token.Type]vm.Op{
	token.PLUS:        vm.OpAdd,
	token.MINUS:       vm.OpSub,
	token.STAR:        vm.OpMul,
	token.SLASH:       vm.OpDiv,
	token.SLASH_SLASH: vm.OpIDiv,
	token.PERCENT:     vm.OpMod,
	token.EQ:          vm.OpEq,
	token.NOT_EQ:      vm.OpNe,
	token.LT:          vm.OpLt,
	token.LT_EQ:       vm.OpLe,
	token.GT:          vm.OpGt,
	token.GT_EQ:       vm.OpGe,
	token.IN:          vm.OpIn,
	token.DOT_DOT:     vm.OpRange,
}

type floatKey float64

func (c *compiler) intConst(v int64, pos token.Pos) {
	if vm.FitsInt(v) {
		p := c.f.proto
		p.Code = append(p.Code, vm.EncodeInt(int(v)))
		p.Pos = append(p.Pos, pos)
		c.adjust(1)
		return
	}
	c.emit(vm.OpConst, c.constant(vm.Int(v), v), pos, 1)
}

func (c *compiler) expr(e ast.Expr) {
	switch x := e.(type) {
	case *ast.Ident:
		c.loadName(x.Name, x.At)
	case *ast.IntLit:
		c.intConst(x.Value, x.At)
	case *ast.FloatLit:
		c.emit(vm.OpConst, c.constant(vm.Float(x.Value), floatKey(x.Value)), x.At, 1)
	case *ast.StringLit:
		c.emit(vm.OpConst, c.strConst(x.Value), x.At, 1)
	case *ast.BoolLit:
		if x.Value {
			c.emit(vm.OpTrue, 0, x.At, 1)
		} else {
			c.emit(vm.OpFalse, 0, x.At, 1)
		}
	case *ast.NilLit:
		c.emit(vm.OpNil, 0, x.At, 1)
	case *ast.FString:
		for _, part := range x.Parts {
			if part.X != nil {
				c.expr(part.X)
			} else {
				c.emit(vm.OpConst, c.strConst(part.Text), x.At, 1)
			}
		}
		if len(x.Parts) == 0 {
			c.emit(vm.OpConst, c.strConst(""), x.At, 1)
		} else if len(x.Parts) > 1 || x.Parts[0].X != nil {
			c.emit(vm.OpBuildStr, len(x.Parts), x.At, 1-len(x.Parts))
		}
	case *ast.ListLit:
		for _, el := range x.Elems {
			c.expr(el)
		}
		c.emit(vm.OpList, len(x.Elems), x.At, 1-len(x.Elems))
	case *ast.MapLit:
		for _, en := range x.Entries {
			c.expr(en.Key)
			c.expr(en.Value)
		}
		c.emit(vm.OpMap, len(x.Entries), x.At, 1-2*len(x.Entries))
	case *ast.Paren:
		c.expr(x.X)
	case *ast.Unary:
		if x.Op == token.MINUS {
			// Fold -5 and -2.5 into constants.
			switch lit := x.X.(type) {
			case *ast.IntLit:
				c.intConst(-lit.Value, x.At)
				return
			case *ast.FloatLit:
				c.emit(vm.OpConst, c.constant(vm.Float(-lit.Value), floatKey(-lit.Value)), x.At, 1)
				return
			}
			c.expr(x.X)
			c.emit(vm.OpNeg, 0, x.At, 0)
			return
		}
		c.expr(x.X)
		c.emit(vm.OpNot, 0, x.At, 0)
	case *ast.Binary:
		c.binary(x)
	case *ast.Call:
		c.call(x)
	case *ast.Index:
		c.expr(x.X)
		c.expr(x.Index)
		c.emit(vm.OpIndex, 0, x.At, -1)
	case *ast.Member:
		c.expr(x.X)
		c.emit(vm.OpGetField, c.strConst(x.Name), x.At, 0)
	case *ast.IfExpr:
		c.ifExpr(x, true)
	case *ast.MatchExpr:
		c.match(x, true)
	case *ast.FnLit:
		c.function("<fn>", x.Params, x.Result, x.Body, x.At)
	case *ast.Scope:
		c.expr(x.X)
		c.emit(vm.OpScope, c.strConst(x.Name), x.At, 0)
	case *ast.Try:
		if c.f.isScript {
			c.errorf(x.At, "'?' can only be used inside a function (it may return from it)")
		}
		c.expr(x.X)
		// TRY jumps over the return code when the value goes on.
		try := c.emit(vm.OpTry, 0, x.At, 0)
		sp := c.f.sp
		c.checkResult(x.At)
		c.emit(vm.OpReturn, 0, x.At, -1)
		c.f.sp = sp
		c.patch(try)
	case *ast.MacroCall:
		c.errorf(x.At, "macros are not supported yet")
		c.emit(vm.OpNil, 0, x.At, 1)
	default:
		c.errorf(e.Pos(), "internal error: unknown expression %T", e)
		c.emit(vm.OpNil, 0, e.Pos(), 1)
	}
}

func (c *compiler) binary(x *ast.Binary) {
	switch x.Op {
	case token.AND, token.OR:
		// a and b: b's truth if a is true, else false. Always a bool.
		sp := c.f.sp
		c.expr(x.X)
		jumpOp, short := vm.OpJumpIfFalse, vm.OpFalse
		if x.Op == token.OR {
			jumpOp, short = vm.OpJumpIfTrue, vm.OpTrue
		}
		j := c.emit(jumpOp, 0, x.At, -1)
		c.expr(x.Y)
		c.emit(vm.OpToBool, 0, x.At, 0)
		end := c.emit(vm.OpJump, 0, x.At, 0)
		c.patch(j)
		c.f.sp = sp
		c.emit(short, 0, x.At, 1)
		c.patch(end)
		return
	}
	// x + 1, n - 2, i % 3: the constant goes into the instruction.
	if k, ok := smallInt(x.Y); ok {
		var op vm.Op
		found := true
		switch x.Op {
		case token.PLUS:
			op = vm.OpAddI
		case token.MINUS:
			op = vm.OpSubI
		case token.STAR:
			op = vm.OpMulI
		case token.PERCENT:
			op = vm.OpModI
		default:
			found = false
		}
		if found {
			c.expr(x.X)
			c.raw(vm.EncodeSigned(op, int(k)), x.At, 0)
			return
		}
	}
	op, ok := binaryOps[x.Op]
	if !ok {
		c.errorf(x.At, "internal error: unknown operator %s", x.Op)
		c.emit(vm.OpNil, 0, x.At, 1)
		return
	}
	c.expr(x.X)
	c.expr(x.Y)
	c.emit(op, 0, x.At, -1)
	if x.Negate {
		c.emit(vm.OpNot, 0, x.At, 0)
	}
}

// args pushes call arguments and returns the names of the named ones (which
// the parser guarantees come last).
func (c *compiler) args(args []ast.Arg) vm.KwNames {
	var names vm.KwNames
	for _, a := range args {
		c.expr(a.Value)
		if a.Name != "" {
			for _, n := range names {
				if n == a.Name {
					c.errorf(a.Value.Pos(), "argument '%s' is given twice", a.Name)
				}
			}
			names = append(names, a.Name)
		}
	}
	return names
}

func (c *compiler) call(x *ast.Call) {
	argc := len(x.Args)
	if m, ok := x.Fn.(*ast.Member); ok {
		c.expr(m.X)
		names := c.args(x.Args)
		name := c.constant(vm.Obj(vm.NewMethodRef(m.Name)), methodKey(m.Name))
		if names == nil {
			c.emit(vm.OpInvoke, argc, x.At, -argc)
			c.word(name, x.At)
		} else {
			c.emit(vm.OpInvokeKw, argc, x.At, -argc)
			c.word(name, x.At)
			c.word(c.constant(vm.Obj(names), nil), x.At)
		}
		return
	}
	c.expr(x.Fn)
	names := c.args(x.Args)
	if names == nil {
		c.emit(vm.OpCall, argc, x.At, -argc)
		return
	}
	c.emit(vm.OpCallKw, argc, x.At, -argc)
	c.word(c.constant(vm.Obj(names), nil), x.At)
}
