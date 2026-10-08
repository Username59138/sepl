// Package ast defines the syntax tree of SEPL programs.
package ast

import "github.com/Username59138/sepl/token"

// Node is any node of the tree.
type Node interface {
	Pos() token.Pos
}

// Expr is an expression.
type Expr interface {
	Node
	exprNode()
}

// Stmt is a statement or a declaration.
type Stmt interface {
	Node
	stmtNode()
}

// Pattern is a pattern in a match arm.
type Pattern interface {
	Node
	patternNode()
}

// Program is a whole source file.
type Program struct {
	Stmts []Stmt
}

func (p *Program) Pos() token.Pos { return token.Pos{Line: 1, Col: 1} }

// Block is an indented block, or a single statement written after ':' on the
// same line (Inline). Its value, when used as an expression, is the value of
// its last statement.
type Block struct {
	Colon  token.Pos
	Stmts  []Stmt
	Inline bool
}

func (b *Block) Pos() token.Pos { return b.Colon }

// TypeExpr is a type annotation: int, point, net::request. In variables,
// parameters, results and fields it can also be an expression that gives a
// type, such as type(x); then Expr is set and Path holds its leading name.
type TypeExpr struct {
	At   token.Pos
	Path []string
	Expr Expr
}

func (t *TypeExpr) Pos() token.Pos { return t.At }

// Param is a function parameter or an enum variant field.
type Param struct {
	At       token.Pos
	Name     string
	Type     *TypeExpr // nil: any type
	Variadic bool      // ...name
}

func (p *Param) Pos() token.Pos { return p.At }

// ---------------------------------------------------------------- expressions

type (
	// Ident is a name: x, self, _.
	Ident struct {
		At   token.Pos
		Name string
	}

	// IntLit is an integer literal.
	IntLit struct {
		At    token.Pos
		Raw   string
		Value int64
	}

	// FloatLit is a float literal.
	FloatLit struct {
		At    token.Pos
		Raw   string
		Value float64
	}

	// StringLit is a string literal (escapes already decoded).
	StringLit struct {
		At    token.Pos
		Value string
	}

	// FString is f"text {expr} text".
	FString struct {
		At    token.Pos
		Parts []FStringPart
	}

	// BoolLit is true or false.
	BoolLit struct {
		At    token.Pos
		Value bool
	}

	// NilLit is nil.
	NilLit struct {
		At token.Pos
	}

	// ListLit is [a, b, c].
	ListLit struct {
		At    token.Pos
		Elems []Expr
	}

	// MapLit is {k: v, ...}.
	MapLit struct {
		At      token.Pos
		Entries []MapEntry
	}

	// Paren is (x). Kept so that tools can reproduce the source.
	Paren struct {
		At token.Pos
		X  Expr
	}

	// Unary is -x or not x.
	Unary struct {
		At token.Pos
		Op token.Type // MINUS or NOT
		X  Expr
	}

	// Binary is x op y. "x not in y" is Op == IN with Negate set.
	Binary struct {
		At     token.Pos // position of the operator
		Op     token.Type
		Negate bool
		X, Y   Expr
	}

	// Call is f(args). Named arguments have Name set: point(x: 1, y: 2).
	Call struct {
		At   token.Pos // position of '('
		Fn   Expr
		Args []Arg
	}

	// Index is x[i].
	Index struct {
		At    token.Pos // position of '['
		X     Expr
		Index Expr
	}

	// Member is x.name.
	Member struct {
		At   token.Pos // position of the name
		X    Expr
		Name string
	}

	// Scope is x::name: a module member, a type-level field, an enum variant.
	Scope struct {
		At   token.Pos // position of the name
		X    Expr
		Name string
	}

	// Try is x?.
	Try struct {
		At token.Pos // position of '?'
		X  Expr
	}

	// MacroCall is name!(args).
	MacroCall struct {
		At   token.Pos
		Name string
		Args []Expr
	}

	// IfExpr is if/else, used as a statement or as an expression.
	// Else is nil, a *Block, or an *IfExpr (else if).
	IfExpr struct {
		At   token.Pos
		Cond Expr
		Then *Block
		Else Node
	}

	// MatchExpr is match, used as a statement or as an expression.
	MatchExpr struct {
		At      token.Pos
		Subject Expr
		Arms    []*MatchArm
	}

	// FnLit is an anonymous function: fn(x): x * 2, or fn(x) with an
	// indented body. The parser turns an inline expression body into a
	// return, so fn(x): x * 2 gives back x * 2.
	FnLit struct {
		At     token.Pos
		Params []*Param
		Result *TypeExpr
		Body   *Block
	}
)

// FStringPart is literal text (X == nil) or an embedded expression.
type FStringPart struct {
	Text string
	X    Expr
}

// MapEntry is one key: value pair.
type MapEntry struct {
	Key, Value Expr
}

// Arg is a call argument; Name is "" for positional arguments.
type Arg struct {
	Name  string
	Value Expr
}

// MatchArm is "pattern, pattern: body".
type MatchArm struct {
	At       token.Pos
	Patterns []Pattern
	Body     *Block
}

func (a *MatchArm) Pos() token.Pos { return a.At }

func (x *Ident) Pos() token.Pos     { return x.At }
func (x *IntLit) Pos() token.Pos    { return x.At }
func (x *FloatLit) Pos() token.Pos  { return x.At }
func (x *StringLit) Pos() token.Pos { return x.At }
func (x *FString) Pos() token.Pos   { return x.At }
func (x *BoolLit) Pos() token.Pos   { return x.At }
func (x *NilLit) Pos() token.Pos    { return x.At }
func (x *ListLit) Pos() token.Pos   { return x.At }
func (x *MapLit) Pos() token.Pos    { return x.At }
func (x *Paren) Pos() token.Pos     { return x.At }
func (x *Unary) Pos() token.Pos     { return x.At }
func (x *Binary) Pos() token.Pos    { return x.At }
func (x *Call) Pos() token.Pos      { return x.At }
func (x *Index) Pos() token.Pos     { return x.At }
func (x *Member) Pos() token.Pos    { return x.At }
func (x *Scope) Pos() token.Pos     { return x.At }
func (x *Try) Pos() token.Pos       { return x.At }
func (x *MacroCall) Pos() token.Pos { return x.At }
func (x *IfExpr) Pos() token.Pos    { return x.At }
func (x *MatchExpr) Pos() token.Pos { return x.At }
func (x *FnLit) Pos() token.Pos     { return x.At }

func (*Ident) exprNode()     {}
func (*IntLit) exprNode()    {}
func (*FloatLit) exprNode()  {}
func (*StringLit) exprNode() {}
func (*FString) exprNode()   {}
func (*BoolLit) exprNode()   {}
func (*NilLit) exprNode()    {}
func (*ListLit) exprNode()   {}
func (*MapLit) exprNode()    {}
func (*Paren) exprNode()     {}
func (*Unary) exprNode()     {}
func (*Binary) exprNode()    {}
func (*Call) exprNode()      {}
func (*Index) exprNode()     {}
func (*Member) exprNode()    {}
func (*Scope) exprNode()     {}
func (*Try) exprNode()       {}
func (*MacroCall) exprNode() {}
func (*IfExpr) exprNode()    {}
func (*MatchExpr) exprNode() {}
func (*FnLit) exprNode()     {}

// ---------------------------------------------------------------- patterns

type (
	// WildcardPat is _.
	WildcardPat struct {
		At token.Pos
	}

	// ValuePat compares the subject with a value using ==: 1, "up", nil,
	// LIMIT, foo::MAX, shape::empty.
	ValuePat struct {
		X Expr
	}

	// RangePat is lo..hi.
	RangePat struct {
		Lo, Hi Expr
	}

	// VariantPat is an enum variant with data: shape::circle(r).
	VariantPat struct {
		Path Expr
		Args []Pattern
	}

	// BindPat binds a variant field to a new variable (inside VariantPat).
	BindPat struct {
		At   token.Pos
		Name string
	}
)

func (p *WildcardPat) Pos() token.Pos { return p.At }
func (p *ValuePat) Pos() token.Pos    { return p.X.Pos() }
func (p *RangePat) Pos() token.Pos    { return p.Lo.Pos() }
func (p *VariantPat) Pos() token.Pos  { return p.Path.Pos() }
func (p *BindPat) Pos() token.Pos     { return p.At }

func (*WildcardPat) patternNode() {}
func (*ValuePat) patternNode()    {}
func (*RangePat) patternNode()    {}
func (*VariantPat) patternNode()  {}
func (*BindPat) patternNode()     {}

// ---------------------------------------------------------------- statements

type (
	// LetStmt is let name [type] [= value] or let name := value.
	LetStmt struct {
		At    token.Pos
		Name  string
		Type  *TypeExpr
		Value Expr
		Infer bool // := : the type is inferred from Value and fixed
	}

	// ConstStmt is const name [type] [= value].
	ConstStmt struct {
		At    token.Pos
		Name  string
		Type  *TypeExpr
		Value Expr
	}

	// AssignStmt is target = value, or a compound assignment (+=, -=, ...).
	AssignStmt struct {
		At     token.Pos // position of the operator
		Op     token.Type
		Target Expr
		Value  Expr
	}

	// ExprStmt is an expression used as a statement.
	ExprStmt struct {
		X Expr
	}

	// ReturnStmt is return [value].
	ReturnStmt struct {
		At    token.Pos
		Value Expr
	}

	BreakStmt struct {
		At token.Pos
	}

	ContinueStmt struct {
		At token.Pos
	}

	// WhileStmt is while cond: body.
	WhileStmt struct {
		At   token.Pos
		Cond Expr
		Body *Block
	}

	// ForStmt is for name in iter: body.
	ForStmt struct {
		At   token.Pos
		Var  string
		Iter Expr
		Body *Block
	}

	// FnDecl is a function or method. Body is nil for a required (abstract)
	// method: fn minus(self, x).
	FnDecl struct {
		At     token.Pos
		Name   string
		Params []*Param
		Result *TypeExpr
		Body   *Block
	}

	// StructDecl is struct name [is parents]: fields.
	StructDecl struct {
		At      token.Pos
		Name    string
		Parents []*TypeExpr
		Fields  []*Field
	}

	// ImplDecl is impl type: methods.
	ImplDecl struct {
		At      token.Pos
		Type    *TypeExpr
		Methods []*FnDecl
	}

	// AddDecl is add struct|impl source to target [: methods].
	AddDecl struct {
		At      token.Pos
		Kind    token.Type // STRUCT or IMPL
		Source  *TypeExpr
		Target  *TypeExpr
		Methods []*FnDecl
	}

	// AddToDecl is add to type: fields and methods written in place, without
	// a struct to take them from.
	AddToDecl struct {
		At      token.Pos
		Target  *TypeExpr
		Fields  []*Field
		Methods []*FnDecl
	}

	// EnumDecl is enum name [is parents]: variants.
	EnumDecl struct {
		At       token.Pos
		Name     string
		Parents  []*TypeExpr
		Variants []*Variant
	}

	// ImportStmt is import a::b [as c].
	ImportStmt struct {
		At    token.Pos
		Path  []string
		Alias string
	}

	// MacroDecl is macro name(params): body.
	MacroDecl struct {
		At     token.Pos
		Name   string
		Params []*Param
		Body   *Block
	}
)

// Field is a struct field: let x [type] [= default] or const X [type] [= value].
type Field struct {
	At    token.Pos
	Const bool
	Name  string
	Type  *TypeExpr
	Value Expr
	Infer bool
}

func (f *Field) Pos() token.Pos { return f.At }

// Variant is an enum variant: empty, circle(r), rect(w int, h int).
type Variant struct {
	At     token.Pos
	Name   string
	Fields []*Param
	Parens bool // written with (), even if empty
}

func (v *Variant) Pos() token.Pos { return v.At }

func (s *LetStmt) Pos() token.Pos      { return s.At }
func (s *ConstStmt) Pos() token.Pos    { return s.At }
func (s *AssignStmt) Pos() token.Pos   { return s.At }
func (s *ExprStmt) Pos() token.Pos     { return s.X.Pos() }
func (s *ReturnStmt) Pos() token.Pos   { return s.At }
func (s *BreakStmt) Pos() token.Pos    { return s.At }
func (s *ContinueStmt) Pos() token.Pos { return s.At }
func (s *WhileStmt) Pos() token.Pos    { return s.At }
func (s *ForStmt) Pos() token.Pos      { return s.At }
func (s *FnDecl) Pos() token.Pos       { return s.At }
func (s *StructDecl) Pos() token.Pos   { return s.At }
func (s *ImplDecl) Pos() token.Pos     { return s.At }
func (s *AddDecl) Pos() token.Pos      { return s.At }
func (s *AddToDecl) Pos() token.Pos    { return s.At }
func (s *EnumDecl) Pos() token.Pos     { return s.At }
func (s *ImportStmt) Pos() token.Pos   { return s.At }
func (s *MacroDecl) Pos() token.Pos    { return s.At }

func (*LetStmt) stmtNode()      {}
func (*ConstStmt) stmtNode()    {}
func (*AssignStmt) stmtNode()   {}
func (*ExprStmt) stmtNode()     {}
func (*ReturnStmt) stmtNode()   {}
func (*BreakStmt) stmtNode()    {}
func (*ContinueStmt) stmtNode() {}
func (*WhileStmt) stmtNode()    {}
func (*ForStmt) stmtNode()      {}
func (*FnDecl) stmtNode()       {}
func (*StructDecl) stmtNode()   {}
func (*ImplDecl) stmtNode()     {}
func (*AddDecl) stmtNode()      {}
func (*AddToDecl) stmtNode()    {}
func (*EnumDecl) stmtNode()     {}
func (*ImportStmt) stmtNode()   {}
func (*MacroDecl) stmtNode()    {}
