// Package parser builds an AST from SEPL source.
//
// Statements and declarations are parsed by recursive descent, expressions
// by precedence climbing (Pratt). Precedence, lowest first:
//
//	or
//	and
//	not x
//	== != < <= > >= in, not in   (comparisons do not chain)
//	..                           (range: 0..n + 1 is 0..(n + 1))
//	+ -
//	* / // %
//	-x
//	x(...)  x[i]  x.name  x::name  x?  name!(...)
//
// A statement ends at NEWLINE, or right after an indented block (its DEDENT),
// so "let x = if c:" with indented branches needs no extra line ending.
package parser

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/Username59138/sepl/ast"
	"github.com/Username59138/sepl/lexer"
	"github.com/Username59138/sepl/token"
)

// Error is a syntax error.
type Error struct {
	Pos token.Pos
	Msg string
}

func (e *Error) Error() string { return e.Pos.String() + ": " + e.Msg }

// ErrorList is every syntax error found, sorted by position.
type ErrorList []*Error

func (l ErrorList) Error() string {
	switch len(l) {
	case 0:
		return "no errors"
	case 1:
		return l[0].Error()
	}
	return fmt.Sprintf("%s (and %d more errors)", l[0], len(l)-1)
}

const maxErrors = 10

// Parse parses a whole file. On syntax errors it returns the partial tree and
// an ErrorList.
func Parse(src string) (*ast.Program, error) {
	p := newParser(lexer.New(src), &errors{})
	prog := p.parseProgram()
	if len(p.errs.list) > 0 {
		list := p.errs.list
		sort.SliceStable(list, func(i, j int) bool { return before(list[i].Pos, list[j].Pos) })
		return prog, list
	}
	return prog, nil
}

// ParseExpr parses a single expression, such as one line typed in a REPL.
func ParseExpr(src string) (ast.Expr, error) {
	p := newParser(lexer.New(src), &errors{})
	var x ast.Expr
	p.guard(func() {
		x = p.parseExpr(precLowest)
		if p.tok.Type == token.NEWLINE {
			p.next()
		}
		if p.tok.Type != token.EOF {
			p.fail(p.tok.Pos, "unexpected "+p.describe(p.tok)+" after the expression")
		}
	})
	if len(p.errs.list) > 0 {
		return x, p.errs.list
	}
	return x, nil
}

func before(a, b token.Pos) bool {
	return a.Line < b.Line || a.Line == b.Line && a.Col < b.Col
}

// errors is shared by a parser and the sub-parsers of its f-strings.
type errors struct {
	list ErrorList
}

func (e *errors) full() bool { return len(e.list) >= maxErrors }

// bailout unwinds the parser to the nearest guard after an error.
type bailout struct{}

type parser struct {
	lx   *lexer.Lexer
	tok  token.Token // current token
	peek token.Token // next token
	prev token.Token // the previous token

	errs     *errors
	fstring  bool // parsing an expression inside an f-string
	lexDead  bool // the lexer failed: no tokens after lexErrAt
	lexErrAt token.Pos
}

func newParser(lx *lexer.Lexer, errs *errors) *parser {
	p := &parser{lx: lx, errs: errs}
	p.tok = p.lex()
	p.peek = p.lex()
	p.prev = token.Token{Type: token.NEWLINE}
	return p
}

func (p *parser) lex() token.Token {
	if p.lexDead {
		return token.Token{Type: token.EOF, Pos: p.lexErrAt}
	}
	t := p.lx.Next()
	if t.Type == token.ILLEGAL {
		p.lexDead = true
		p.lexErrAt = t.Pos
		p.add(t.Pos, t.Literal)
		return token.Token{Type: token.EOF, Pos: t.Pos}
	}
	return t
}

func (p *parser) next() {
	p.prev = p.tok
	p.tok = p.peek
	p.peek = p.lex()
}

func (p *parser) add(pos token.Pos, msg string) {
	if p.errs.full() {
		return
	}
	if n := len(p.errs.list); n > 0 && p.errs.list[n-1].Pos == pos {
		return // one error per position is enough
	}
	p.errs.list = append(p.errs.list, &Error{Pos: pos, Msg: msg})
}

// fail records an error and unwinds to the nearest guard.
func (p *parser) fail(pos token.Pos, msg string) {
	// After a lexer error the token stream is cut short, so errors from
	// that point on are just echoes of it.
	// Hitting the end of the cut stream is never a new error either.
	cutShort := p.lexDead && p.tok.Type == token.EOF && p.tok.Pos == p.lexErrAt
	if !cutShort && (!p.lexDead || before(pos, p.lexErrAt)) {
		p.add(pos, msg)
	}
	panic(bailout{})
}

// guard runs f; after a syntax error it skips to the next statement.
// It reports whether f finished without an error.
func (p *parser) guard(f func()) (ok bool) {
	defer func() {
		if r := recover(); r != nil {
			if _, isBailout := r.(bailout); !isBailout || p.errs.full() {
				panic(r)
			}
			p.sync()
			ok = false
		}
	}()
	f()
	return true
}

// sync skips the rest of a broken statement, including an indented block
// that belongs to it, and stops at the start of the next statement or at the
// end of the enclosing block.
func (p *parser) sync() {
	depth := 0
	for {
		switch p.tok.Type {
		case token.EOF:
			return
		case token.INDENT:
			depth++
		case token.DEDENT:
			if depth == 0 {
				return
			}
			depth--
			if depth == 0 {
				p.next()
				if p.tok.Type == token.ELSE {
					continue // skip the else branch of a broken if too
				}
				return
			}
		case token.NEWLINE:
			if depth == 0 {
				p.next()
				if p.tok.Type == token.INDENT {
					continue // the body of a broken block header
				}
				return
			}
		}
		p.next()
	}
}

// describe names a token for error messages.
func (p *parser) describe(t token.Token) string {
	if p.fstring && (t.Type == token.NEWLINE || t.Type == token.EOF) {
		return "the end of the f-string expression"
	}
	return describe(t)
}

func describe(t token.Token) string {
	switch t.Type {
	case token.IDENT:
		return fmt.Sprintf("name '%s'", t.Literal)
	case token.INT, token.FLOAT:
		return "number " + t.Literal
	case token.STRING:
		return "a string"
	case token.FSTRING:
		return "an f-string"
	case token.NEWLINE:
		return "end of line"
	case token.INDENT:
		return "an indented line"
	case token.DEDENT:
		return "end of block"
	case token.EOF:
		return "end of file"
	}
	return "'" + t.Type.String() + "'"
}

func (p *parser) expect(t token.Type, what string) token.Token {
	if p.tok.Type != t {
		p.fail(p.tok.Pos, "expected "+what+", found "+p.describe(p.tok))
	}
	tok := p.tok
	p.next()
	return tok
}

func (p *parser) ident(what string) (string, token.Pos) {
	t := p.expect(token.IDENT, what)
	return t.Literal, t.Pos
}

// isWord reports whether the current token is the contextual word w
// (add, to, is): these are plain identifiers to the lexer.
func (p *parser) isWord(w string) bool {
	return p.tok.Type == token.IDENT && p.tok.Literal == w
}

// endStmt finishes a statement: either it ended with an indented block or a
// NEWLINE must follow.
func (p *parser) endStmt() {
	if p.prev.Type == token.DEDENT {
		return
	}
	switch p.tok.Type {
	case token.NEWLINE:
		p.next()
	case token.EOF, token.DEDENT:
	default:
		p.fail(p.tok.Pos, "expected end of line, found "+p.describe(p.tok))
	}
}

// ---------------------------------------------------------------- program, blocks

func (p *parser) parseProgram() *ast.Program {
	prog := &ast.Program{}
	defer func() {
		// Too many errors: stop where we are.
		if r := recover(); r != nil {
			if _, ok := r.(bailout); !ok {
				panic(r)
			}
		}
	}()
	for p.tok.Type != token.EOF {
		if s := p.stmt(); s != nil {
			prog.Stmts = append(prog.Stmts, s)
		}
	}
	return prog
}

// stmt parses one statement, recovering from errors.
func (p *parser) stmt() (s ast.Stmt) {
	p.guard(func() { s = p.parseStmt() })
	return s
}

// parseBlock parses ':' followed by an indented block or by one statement on
// the same line. after names the construct for the "expected ':'" message.
func (p *parser) parseBlock(after string) *ast.Block {
	if p.tok.Type != token.COLON {
		p.fail(p.tok.Pos, "expected ':' after "+after+", found "+p.describe(p.tok))
	}
	b := &ast.Block{Colon: p.tok.Pos}
	p.next()
	if p.tok.Type != token.NEWLINE {
		b.Inline = true
		b.Stmts = []ast.Stmt{p.parseSimpleStmt()}
		return b
	}
	p.next()
	if p.tok.Type != token.INDENT {
		p.fail(p.tok.Pos, "expected an indented block after ':'")
	}
	p.next()
	for p.tok.Type != token.DEDENT && p.tok.Type != token.EOF {
		if s := p.stmt(); s != nil {
			b.Stmts = append(b.Stmts, s)
		}
	}
	if p.tok.Type == token.DEDENT {
		p.next()
	}
	return b
}

// indented parses ':' NEWLINE INDENT item... DEDENT for bodies that must be
// indented (struct, impl, enum, match).
func (p *parser) indented(after string, item func()) {
	p.expect(token.COLON, "':' after "+after)
	if p.tok.Type != token.NEWLINE {
		p.fail(p.tok.Pos, "the body of "+after+" must start on a new indented line")
	}
	p.next()
	if p.tok.Type != token.INDENT {
		p.fail(p.tok.Pos, "expected an indented block after ':'")
	}
	p.next()
	for p.tok.Type != token.DEDENT && p.tok.Type != token.EOF {
		p.guard(item)
	}
	if p.tok.Type == token.DEDENT {
		p.next()
	}
}

// ---------------------------------------------------------------- statements

func (p *parser) parseStmt() ast.Stmt {
	var s ast.Stmt
	switch p.tok.Type {
	case token.FN:
		if p.peek.Type == token.LPAREN { // fn(...) without a name is an expression
			s = p.parseSimpleStmt()
			break
		}
		s = p.parseFn()
	case token.STRUCT:
		s = p.parseStruct()
	case token.IMPL:
		s = p.parseImpl()
	case token.ENUM:
		s = p.parseEnum()
	case token.IMPORT:
		s = p.parseImport()
	case token.MACRO:
		s = p.parseMacro()
	case token.WHILE:
		s = p.parseWhile()
	case token.FOR:
		s = p.parseFor()
	case token.INDENT:
		p.fail(p.tok.Pos, "unexpected indentation")
	case token.ELSE:
		p.fail(p.tok.Pos, "'else' without a matching 'if'")
	case token.IDENT:
		if p.tok.Literal == "add" && (p.peek.Type == token.STRUCT || p.peek.Type == token.IMPL) {
			s = p.parseAdd()
			break
		}
		s = p.parseSimpleStmt()
	default:
		s = p.parseSimpleStmt()
	}
	p.endStmt()
	return s
}

// parseSimpleStmt parses a statement that may also follow ':' on one line.
func (p *parser) parseSimpleStmt() ast.Stmt {
	if p.isWord("add") && (p.peek.Type == token.STRUCT || p.peek.Type == token.IMPL) {
		return p.parseAdd() // also after ':' on one line: if debug: add impl verbose to logger
	}
	switch p.tok.Type {
	case token.LET:
		return p.parseLet()
	case token.CONST:
		return p.parseConst()
	case token.RETURN:
		pos := p.tok.Pos
		p.next()
		s := &ast.ReturnStmt{At: pos}
		switch p.tok.Type {
		case token.NEWLINE, token.EOF, token.DEDENT, token.ELSE:
		default:
			s.Value = p.parseExpr(precLowest)
		}
		return s
	case token.BREAK:
		s := &ast.BreakStmt{At: p.tok.Pos}
		p.next()
		return s
	case token.CONTINUE:
		s := &ast.ContinueStmt{At: p.tok.Pos}
		p.next()
		return s
	case token.FN:
		if p.peek.Type != token.LPAREN { // fn(...) is an anonymous function
			p.fail(p.tok.Pos, "'fn' must start on its own line")
		}
	case token.STRUCT, token.IMPL, token.ENUM, token.IMPORT, token.MACRO, token.WHILE, token.FOR:
		p.fail(p.tok.Pos, "'"+p.tok.Type.String()+"' must start on its own line")
	}

	x := p.parseExpr(precLowest)
	switch op := p.tok; op.Type {
	case token.ASSIGN, token.PLUS_ASSIGN, token.MINUS_ASSIGN, token.STAR_ASSIGN,
		token.SLASH_ASSIGN, token.SLASH_SLASH_ASSIGN, token.PERCENT_ASSIGN:
		switch x.(type) {
		case *ast.Ident, *ast.Member, *ast.Index:
		case *ast.Scope:
			p.fail(op.Pos, fmt.Sprintf("cannot assign to %s: '::' names constants, methods and module members, which never change", ast.String(x)))
		default:
			p.fail(op.Pos, "cannot assign to this expression; only to a variable, a field or an element")
		}
		p.next()
		return &ast.AssignStmt{At: op.Pos, Op: op.Type, Target: x, Value: p.parseExpr(precLowest)}
	case token.WALRUS:
		if id, ok := x.(*ast.Ident); ok {
			p.fail(op.Pos, fmt.Sprintf("':=' is only used with let: write 'let %s := ...'", id.Name))
		}
		p.fail(op.Pos, "unexpected ':='")
	}
	return &ast.ExprStmt{X: x}
}

func (p *parser) parseLet() ast.Stmt {
	pos := p.expect(token.LET, "'let'").Pos
	name, _ := p.ident("a variable name after 'let'")
	s := &ast.LetStmt{At: pos, Name: name}
	if p.tok.Type == token.IDENT {
		s.Type = p.parseAnnotation()
	}
	switch p.tok.Type {
	case token.ASSIGN:
		p.next()
		s.Value = p.parseExpr(precLowest)
	case token.WALRUS:
		if s.Type != nil {
			p.fail(p.tok.Pos, "':=' infers the type: drop the type or use '='")
		}
		p.next()
		s.Value = p.parseExpr(precLowest)
		s.Infer = true
	}
	return s
}

func (p *parser) parseConst() ast.Stmt {
	pos := p.expect(token.CONST, "'const'").Pos
	name, _ := p.ident("a constant name after 'const'")
	s := &ast.ConstStmt{At: pos, Name: name}
	if p.tok.Type == token.IDENT {
		s.Type = p.parseAnnotation()
	}
	if p.tok.Type == token.WALRUS {
		p.fail(p.tok.Pos, "a const never changes, so write 'const "+name+" = ...'")
	}
	if p.tok.Type == token.ASSIGN {
		p.next()
		s.Value = p.parseExpr(precLowest)
	}
	return s
}

func (p *parser) parseWhile() ast.Stmt {
	pos := p.expect(token.WHILE, "'while'").Pos
	cond := p.parseExpr(precLowest)
	return &ast.WhileStmt{At: pos, Cond: cond, Body: p.parseBlock("the while condition")}
}

func (p *parser) parseFor() ast.Stmt {
	pos := p.expect(token.FOR, "'for'").Pos
	name, _ := p.ident("a loop variable after 'for'")
	p.expect(token.IN, "'in' after the loop variable")
	iter := p.parseExpr(precLowest)
	return &ast.ForStmt{At: pos, Var: name, Iter: iter, Body: p.parseBlock("the for loop")}
}

// ---------------------------------------------------------------- declarations

func (p *parser) parseType() *ast.TypeExpr {
	name, pos := p.ident("a type name")
	t := &ast.TypeExpr{At: pos, Path: []string{name}}
	for p.tok.Type == token.COLON_COLON {
		p.next()
		part, _ := p.ident("a name after '::'")
		t.Path = append(t.Path, part)
	}
	return t
}

// parseAnnotation parses a type wherever one is expected (annotations, impl,
// add, is): a type name, or an expression that gives a type, such as
// type(x) or types[0].
func (p *parser) parseAnnotation() *ast.TypeExpr {
	t := p.parseType()
	switch p.tok.Type {
	case token.LPAREN, token.LBRACKET, token.DOT:
		var x ast.Expr = &ast.Ident{At: t.At, Name: t.Path[0]}
		for _, part := range t.Path[1:] {
			x = &ast.Scope{At: t.At, X: x, Name: part}
		}
		t.Expr = p.parsePostfix(x)
	}
	return t
}

func (p *parser) parseTypeList() []*ast.TypeExpr {
	list := []*ast.TypeExpr{p.parseAnnotation()}
	for p.tok.Type == token.COMMA {
		p.next()
		list = append(list, p.parseAnnotation())
	}
	return list
}

// parseParams parses (a, b int, ...rest). Variadic parameters are allowed
// only when variadic is true, and only last.
func (p *parser) parseParams(variadic bool) []*ast.Param {
	p.expect(token.LPAREN, "'('")
	var list []*ast.Param
	for p.tok.Type != token.RPAREN {
		param := &ast.Param{At: p.tok.Pos}
		if p.tok.Type == token.ELLIPSIS {
			if !variadic {
				p.fail(p.tok.Pos, "'...' is only allowed in function parameters")
			}
			param.Variadic = true
			p.next()
		}
		param.Name, _ = p.ident("a parameter name")
		if p.tok.Type == token.IDENT {
			param.Type = p.parseAnnotation()
		}
		if len(list) > 0 && list[len(list)-1].Variadic {
			p.fail(list[len(list)-1].At, "the '...' parameter must be the last one")
		}
		list = append(list, param)
		if p.tok.Type != token.COMMA {
			break
		}
		p.next()
	}
	p.expect(token.RPAREN, "',' or ')' in the parameter list")
	return list
}

func (p *parser) parseFn() *ast.FnDecl {
	pos := p.expect(token.FN, "'fn'").Pos
	name, _ := p.ident("a function name after 'fn'")
	fn := &ast.FnDecl{At: pos, Name: name, Params: p.parseParams(true)}
	if p.tok.Type == token.IDENT {
		fn.Result = p.parseAnnotation()
	}
	if p.tok.Type == token.COLON || p.tok.Type == token.NEWLINE && p.peek.Type == token.INDENT {
		fn.Body = p.parseBlock("the function signature")
	}
	// No body: a required method that every inheritor must implement.
	return fn
}

// parseFnLit parses an anonymous function. With the body on the same line
// (fn(x): x * 2) an expression body is the result; an indented body works
// like the body of a named function and needs return.
func (p *parser) parseFnLit() ast.Expr {
	pos := p.expect(token.FN, "'fn'").Pos
	if p.tok.Type != token.LPAREN {
		p.fail(p.tok.Pos, "expected '(' after 'fn' (an anonymous function), found "+p.describe(p.tok))
	}
	fn := &ast.FnLit{At: pos, Params: p.parseParams(true)}
	if p.tok.Type == token.IDENT {
		fn.Result = p.parseAnnotation()
	}
	fn.Body = p.parseBlock("the parameters of fn")
	if fn.Body.Inline {
		if es, ok := fn.Body.Stmts[0].(*ast.ExprStmt); ok {
			fn.Body.Stmts[0] = &ast.ReturnStmt{At: es.X.Pos(), Value: es.X}
		}
	}
	return fn
}

func (p *parser) parseMacro() ast.Stmt {
	pos := p.expect(token.MACRO, "'macro'").Pos
	name, _ := p.ident("a macro name after 'macro'")
	params := p.parseParams(true)
	return &ast.MacroDecl{At: pos, Name: name, Params: params, Body: p.parseBlock("the macro signature")}
}

func (p *parser) parseStruct() ast.Stmt {
	pos := p.expect(token.STRUCT, "'struct'").Pos
	name, _ := p.ident("a struct name after 'struct'")
	s := &ast.StructDecl{At: pos, Name: name}
	if p.isWord("is") {
		p.next()
		s.Parents = p.parseTypeList()
	}
	if p.tok.Type != token.COLON {
		return s // struct without fields: struct minus
	}
	p.indented("struct "+name, func() {
		f := &ast.Field{At: p.tok.Pos}
		switch p.tok.Type {
		case token.LET:
		case token.CONST:
			f.Const = true
		case token.FN:
			p.fail(p.tok.Pos, "methods go into 'impl "+name+":', not into the struct")
		default:
			p.fail(p.tok.Pos, "expected a field ('let' or 'const'), found "+p.describe(p.tok))
		}
		p.next()
		f.Name, _ = p.ident("a field name")
		if p.tok.Type == token.IDENT {
			f.Type = p.parseAnnotation()
		}
		switch p.tok.Type {
		case token.ASSIGN:
			p.next()
			f.Value = p.parseExpr(precLowest)
		case token.WALRUS:
			if f.Type != nil || f.Const {
				p.fail(p.tok.Pos, "':=' is only for 'let' fields without a type")
			}
			p.next()
			f.Value = p.parseExpr(precLowest)
			f.Infer = true
		}
		p.endStmt()
		s.Fields = append(s.Fields, f)
	})
	return s
}

// parseMethods parses the indented fn list of impl and add impl.
func (p *parser) parseMethods(after string) []*ast.FnDecl {
	var list []*ast.FnDecl
	p.indented(after, func() {
		if p.tok.Type != token.FN {
			p.fail(p.tok.Pos, "only methods ('fn') can go into "+after+", found "+p.describe(p.tok))
		}
		list = append(list, p.parseFn())
		p.endStmt()
	})
	return list
}

func (p *parser) parseImpl() ast.Stmt {
	pos := p.expect(token.IMPL, "'impl'").Pos
	t := p.parseAnnotation()
	return &ast.ImplDecl{At: pos, Type: t, Methods: p.parseMethods("impl " + t.String())}
}

func (p *parser) parseAdd() ast.Stmt {
	pos := p.tok.Pos
	p.next() // add
	d := &ast.AddDecl{At: pos, Kind: p.tok.Type}
	p.next() // struct | impl
	d.Source = p.parseAnnotation()
	if !p.isWord("to") {
		p.fail(p.tok.Pos, "expected 'to' after 'add "+d.Kind.String()+" "+d.Source.String()+"', found "+p.describe(p.tok))
	}
	p.next()
	d.Target = p.parseAnnotation()
	if p.tok.Type == token.COLON {
		if d.Kind == token.STRUCT {
			p.fail(p.tok.Pos, "'add struct' has no body; to override methods use 'add impl ... to ...:'")
		}
		d.Methods = p.parseMethods("add impl")
	}
	return d
}

func (p *parser) parseEnum() ast.Stmt {
	pos := p.expect(token.ENUM, "'enum'").Pos
	name, _ := p.ident("an enum name after 'enum'")
	e := &ast.EnumDecl{At: pos, Name: name}
	if p.isWord("is") {
		p.next()
		e.Parents = p.parseTypeList()
	}
	p.indented("enum "+name, func() {
		vname, vpos := p.ident("a variant name")
		v := &ast.Variant{At: vpos, Name: vname}
		if p.tok.Type == token.LPAREN {
			v.Parens = true
			v.Fields = p.parseParams(false)
		}
		p.endStmt()
		e.Variants = append(e.Variants, v)
	})
	return e
}

func (p *parser) parseImport() ast.Stmt {
	pos := p.expect(token.IMPORT, "'import'").Pos
	first, _ := p.ident("a module name after 'import'")
	s := &ast.ImportStmt{At: pos, Path: []string{first}}
	for p.tok.Type == token.COLON_COLON {
		p.next()
		part, _ := p.ident("a module name after '::'")
		s.Path = append(s.Path, part)
	}
	if p.tok.Type == token.AS {
		p.next()
		s.Alias, _ = p.ident("a name after 'as'")
	}
	return s
}

// ---------------------------------------------------------------- expressions

const (
	precLowest = iota
	precOr
	precAnd
	precNot
	precCompare
	precRange
	precSum
	precProduct
)

func infixPrec(t token.Type) int {
	switch t {
	case token.OR:
		return precOr
	case token.AND:
		return precAnd
	case token.EQ, token.NOT_EQ, token.LT, token.LT_EQ, token.GT, token.GT_EQ, token.IN:
		return precCompare
	case token.DOT_DOT:
		return precRange
	case token.PLUS, token.MINUS:
		return precSum
	case token.STAR, token.SLASH, token.SLASH_SLASH, token.PERCENT:
		return precProduct
	}
	return precLowest
}

func isComparison(x ast.Expr) bool {
	b, ok := x.(*ast.Binary)
	return ok && infixPrec(b.Op) == precCompare
}

// parseExpr parses an expression whose operators all bind tighter than min.
func (p *parser) parseExpr(min int) ast.Expr {
	left := p.parseUnary()
	for {
		// An expression that ended with an indented block (if/match) cannot
		// continue: the next line is a new statement.
		if p.prev.Type == token.DEDENT {
			return left
		}
		op := p.tok
		prec := infixPrec(op.Type)
		notIn := op.Type == token.NOT && p.peek.Type == token.IN
		if notIn {
			prec = precCompare
		}
		if prec == precLowest || prec <= min {
			return left
		}
		if prec == precCompare && isComparison(left) {
			p.fail(op.Pos, "comparisons cannot be chained: use 'and', as in 'a < b and b < c'")
		}
		p.next()
		if notIn {
			p.next()
		}
		right := p.parseExpr(prec)
		b := &ast.Binary{At: op.Pos, Op: op.Type, X: left, Y: right}
		if notIn {
			b.Op, b.Negate = token.IN, true
		}
		left = b
	}
}

func (p *parser) parseUnary() ast.Expr {
	switch t := p.tok; t.Type {
	case token.MINUS:
		p.next()
		return &ast.Unary{At: t.Pos, Op: token.MINUS, X: p.parseExpr(precProduct)}
	case token.NOT:
		p.next()
		return &ast.Unary{At: t.Pos, Op: token.NOT, X: p.parseExpr(precNot)}
	}
	return p.parsePostfix(p.parsePrimary())
}

func (p *parser) parsePostfix(x ast.Expr) ast.Expr {
	for {
		if p.prev.Type == token.DEDENT {
			return x
		}
		switch t := p.tok; t.Type {
		case token.LPAREN:
			x = &ast.Call{At: t.Pos, Fn: x, Args: p.parseArgs()}
		case token.LBRACKET:
			p.next()
			idx := p.parseExpr(precLowest)
			p.expect(token.RBRACKET, "']'")
			x = &ast.Index{At: t.Pos, X: x, Index: idx}
		case token.DOT:
			p.next()
			name, pos := p.ident("a field or method name after '.'")
			x = &ast.Member{At: pos, X: x, Name: name}
		case token.COLON_COLON:
			p.next()
			name, pos := p.ident("a name after '::'")
			x = &ast.Scope{At: pos, X: x, Name: name}
		case token.QUESTION:
			p.next()
			x = &ast.Try{At: t.Pos, X: x}
		case token.BANG:
			id, ok := x.(*ast.Ident)
			if !ok || p.peek.Type != token.LPAREN {
				p.fail(t.Pos, "unexpected '!': macros are called as name!(...), and 'not' negates")
			}
			p.next()
			var args []ast.Expr
			for _, a := range p.parseArgs() {
				if a.Name != "" {
					p.fail(a.Value.Pos(), "macro arguments cannot be named")
				}
				args = append(args, a.Value)
			}
			x = &ast.MacroCall{At: id.At, Name: id.Name, Args: args}
		default:
			return x
		}
	}
}

// parseArgs parses (a, b, name: value).
func (p *parser) parseArgs() []ast.Arg {
	p.expect(token.LPAREN, "'('")
	var args []ast.Arg
	named := false
	for p.tok.Type != token.RPAREN {
		var a ast.Arg
		if p.tok.Type == token.IDENT && p.peek.Type == token.COLON {
			a.Name = p.tok.Literal
			p.next()
			p.next()
			named = true
		} else if named {
			p.fail(p.tok.Pos, "positional arguments must come before named ones")
		}
		a.Value = p.parseExpr(precLowest)
		args = append(args, a)
		if p.tok.Type != token.COMMA {
			break
		}
		p.next()
	}
	p.expect(token.RPAREN, "',' or ')' in the argument list")
	return args
}

func (p *parser) parsePrimary() ast.Expr {
	t := p.tok
	switch t.Type {
	case token.IDENT:
		p.next()
		return &ast.Ident{At: t.Pos, Name: t.Literal}
	case token.INT:
		p.next()
		v, err := strconv.ParseInt(t.Literal, 0, 64)
		if err != nil {
			p.fail(t.Pos, "invalid integer "+t.Literal)
		}
		return &ast.IntLit{At: t.Pos, Raw: t.Literal, Value: v}
	case token.FLOAT:
		p.next()
		v, err := strconv.ParseFloat(t.Literal, 64)
		if err != nil {
			p.fail(t.Pos, "invalid number "+t.Literal)
		}
		return &ast.FloatLit{At: t.Pos, Raw: t.Literal, Value: v}
	case token.STRING:
		p.next()
		return &ast.StringLit{At: t.Pos, Value: t.Literal}
	case token.FSTRING:
		p.next()
		return p.parseFString(t)
	case token.TRUE, token.FALSE:
		p.next()
		return &ast.BoolLit{At: t.Pos, Value: t.Type == token.TRUE}
	case token.NIL:
		p.next()
		return &ast.NilLit{At: t.Pos}
	case token.LPAREN:
		p.next()
		x := p.parseExpr(precLowest)
		p.expect(token.RPAREN, "')'")
		return &ast.Paren{At: t.Pos, X: x}
	case token.LBRACKET:
		p.next()
		l := &ast.ListLit{At: t.Pos}
		for p.tok.Type != token.RBRACKET {
			l.Elems = append(l.Elems, p.parseExpr(precLowest))
			if p.tok.Type != token.COMMA {
				break
			}
			p.next()
		}
		p.expect(token.RBRACKET, "',' or ']' in the list")
		return l
	case token.LBRACE:
		p.next()
		m := &ast.MapLit{At: t.Pos}
		for p.tok.Type != token.RBRACE {
			k := p.parseExpr(precLowest)
			p.expect(token.COLON, "':' between a map key and its value")
			v := p.parseExpr(precLowest)
			m.Entries = append(m.Entries, ast.MapEntry{Key: k, Value: v})
			if p.tok.Type != token.COMMA {
				break
			}
			p.next()
		}
		p.expect(token.RBRACE, "',' or '}' in the map")
		return m
	case token.IF:
		return p.parseIf()
	case token.MATCH:
		return p.parseMatch()
	case token.FN:
		return p.parseFnLit()
	}
	// "a +" at the end of a line continues on the next line, so the real
	// mistake is the dangling operator, not what follows it.
	if infixPrec(p.prev.Type) != precLowest && t.Pos.Line > p.prev.Pos.Line && !p.fstring {
		p.fail(p.prev.Pos, fmt.Sprintf("incomplete expression: the line ends with '%s'", p.prev.Type))
	}
	p.fail(t.Pos, "expected an expression, found "+p.describe(t))
	return nil
}

func (p *parser) parseFString(t token.Token) ast.Expr {
	parts, err := lexer.SplitFString(t.Literal)
	if err != nil { // the lexer already validated it
		p.fail(t.Pos, err.Error())
	}
	fs := &ast.FString{At: t.Pos}
	for _, part := range parts {
		if !part.IsExpr {
			fs.Parts = append(fs.Parts, ast.FStringPart{Text: part.Text})
			continue
		}
		src := strings.TrimLeft(part.Expr, " \t")
		pos := token.Pos{
			Line: t.Pos.Line,
			Col:  t.Pos.Col + 2 + len([]rune(t.Literal[:part.Offset])) + len(part.Expr) - len(src),
		}
		sub := newParser(lexer.NewAt(src, pos), p.errs)
		sub.fstring = true
		x := sub.parseExpr(precLowest)
		if sub.tok.Type != token.NEWLINE && sub.tok.Type != token.EOF {
			sub.fail(sub.tok.Pos, "unexpected "+sub.describe(sub.tok)+" in an f-string expression")
		}
		if sub.lexDead {
			panic(bailout{}) // the lexer error is already recorded
		}
		fs.Parts = append(fs.Parts, ast.FStringPart{X: x})
	}
	return fs
}

func (p *parser) parseIf() ast.Expr {
	pos := p.expect(token.IF, "'if'").Pos
	cond := p.parseExpr(precLowest)
	x := &ast.IfExpr{At: pos, Cond: cond, Then: p.parseBlock("the if condition")}
	// Allow "if c: a" with "else: b" on the next line.
	if x.Then.Inline && p.tok.Type == token.NEWLINE && p.peek.Type == token.ELSE {
		p.next()
	}
	if p.tok.Type == token.ELSE {
		p.next()
		if p.tok.Type == token.IF {
			x.Else = p.parseIf()
		} else {
			x.Else = p.parseBlock("else")
		}
	}
	return x
}

func (p *parser) parseMatch() ast.Expr {
	pos := p.expect(token.MATCH, "'match'").Pos
	m := &ast.MatchExpr{At: pos, Subject: p.parseExpr(precLowest)}
	p.indented("the match value", func() {
		arm := &ast.MatchArm{At: p.tok.Pos}
		arm.Patterns = append(arm.Patterns, p.parsePattern())
		for p.tok.Type == token.COMMA {
			p.next()
			arm.Patterns = append(arm.Patterns, p.parsePattern())
		}
		arm.Body = p.parseBlock("the match pattern")
		p.endStmt()
		m.Arms = append(m.Arms, arm)
	})
	return m
}

// ---------------------------------------------------------------- patterns

func (p *parser) parsePattern() ast.Pattern {
	if p.isWord("_") {
		pos := p.tok.Pos
		p.next()
		return &ast.WildcardPat{At: pos}
	}
	x := p.parsePatternValue()
	if p.tok.Type == token.LPAREN {
		switch x.(type) {
		case *ast.Ident, *ast.Scope:
		default:
			p.fail(p.tok.Pos, "only an enum variant can take '(...)' in a pattern")
		}
		return &ast.VariantPat{Path: x, Args: p.parseSubPatterns()}
	}
	if p.tok.Type == token.DOT_DOT {
		p.next()
		return &ast.RangePat{Lo: x, Hi: p.parsePatternValue()}
	}
	return &ast.ValuePat{X: x}
}

// parsePatternValue parses a literal, -number, name or a::b path.
func (p *parser) parsePatternValue() ast.Expr {
	t := p.tok
	switch t.Type {
	case token.INT, token.FLOAT, token.STRING, token.TRUE, token.FALSE, token.NIL:
		return p.parsePrimary()
	case token.MINUS:
		p.next()
		if p.tok.Type != token.INT && p.tok.Type != token.FLOAT {
			p.fail(p.tok.Pos, "expected a number after '-' in a pattern")
		}
		return &ast.Unary{At: t.Pos, Op: token.MINUS, X: p.parsePrimary()}
	case token.IDENT:
		p.next()
		var x ast.Expr = &ast.Ident{At: t.Pos, Name: t.Literal}
		for p.tok.Type == token.COLON_COLON {
			p.next()
			name, pos := p.ident("a name after '::'")
			x = &ast.Scope{At: pos, X: x, Name: name}
		}
		return x
	case token.FSTRING:
		p.fail(t.Pos, "f-strings cannot be used as patterns")
	}
	p.fail(t.Pos, "expected a pattern (a value, a range, _ or an enum variant), found "+describe(t))
	return nil
}

// parseSubPatterns parses the (a, b) of a variant pattern: plain names bind
// the variant's fields to variables.
func (p *parser) parseSubPatterns() []ast.Pattern {
	p.expect(token.LPAREN, "'('")
	var list []ast.Pattern
	for p.tok.Type != token.RPAREN {
		if p.tok.Type == token.IDENT && p.tok.Literal != "_" &&
			p.peek.Type != token.COLON_COLON && p.peek.Type != token.LPAREN {
			list = append(list, &ast.BindPat{At: p.tok.Pos, Name: p.tok.Literal})
			p.next()
		} else {
			list = append(list, p.parsePattern())
		}
		if p.tok.Type != token.COMMA {
			break
		}
		p.next()
	}
	p.expect(token.RPAREN, "',' or ')' in the pattern")
	return list
}
