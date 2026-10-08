package ast

import (
	"fmt"
	"strings"

	"github.com/Username59138/sepl/token"
)

// String renders a node as a one-line S-expression, e.g. (let x (+ 2 3)).
func String(n Node) string {
	var p printer
	p.node(n)
	return p.b.String()
}

// Pretty renders a node as an S-expression with one statement per line.
func Pretty(n Node) string {
	p := printer{pretty: true}
	p.node(n)
	return p.b.String()
}

type printer struct {
	b      strings.Builder
	pretty bool
	depth  int
}

func (p *printer) w(s string) { p.b.WriteString(s) }

// sep starts the next item of a statement list.
func (p *printer) sep() {
	if p.pretty {
		p.w("\n" + strings.Repeat("  ", p.depth))
	} else {
		p.w(" ")
	}
}

// stmts prints a list of statements under head, e.g. (do s1 s2).
func (p *printer) stmts(head string, list []Stmt) {
	p.w("(" + head)
	p.depth++
	for _, s := range list {
		p.sep()
		p.node(s)
	}
	p.depth--
	p.w(")")
}

func (p *printer) block(b *Block) {
	if b == nil {
		p.w("(do)")
		return
	}
	p.stmts("do", b.Stmts)
}

func (p *printer) exprs(list []Expr) {
	for _, x := range list {
		p.w(" ")
		p.node(x)
	}
}

func typeString(t *TypeExpr) string {
	if t == nil {
		return ""
	}
	return strings.Join(t.Path, "::")
}

func paramString(x *Param) string {
	s := x.Name
	if x.Variadic {
		s = "..." + s
	}
	if x.Type != nil {
		s += ":" + typeString(x.Type)
	}
	return s
}

func paramsString(list []*Param) string {
	parts := make([]string, len(list))
	for i, x := range list {
		parts[i] = paramString(x)
	}
	return "(" + strings.Join(parts, " ") + ")"
}

func typesString(list []*TypeExpr) string {
	parts := make([]string, len(list))
	for i, t := range list {
		parts[i] = typeString(t)
	}
	return strings.Join(parts, " ")
}

// binding prints the common "name [type] [= value]" tail of let/const/fields.
func (p *printer) binding(name string, t *TypeExpr, value Expr, infer bool) {
	p.w(" " + name)
	if t != nil {
		p.w(" " + typeString(t))
	}
	if value != nil {
		if infer {
			p.w(" := ")
		} else {
			p.w(" = ")
		}
		p.node(value)
	}
}

func (p *printer) node(n Node) {
	switch n := n.(type) {
	case nil:
		p.w("<nil>")

	case *Program:
		for i, s := range n.Stmts {
			if i > 0 {
				p.w("\n")
			}
			p.node(s)
		}
	case *Block:
		p.block(n)

	// expressions
	case *Ident:
		p.w(n.Name)
	case *IntLit:
		p.w(n.Raw)
	case *FloatLit:
		p.w(n.Raw)
	case *StringLit:
		p.w(fmt.Sprintf("%q", n.Value))
	case *BoolLit:
		p.w(fmt.Sprint(n.Value))
	case *NilLit:
		p.w("nil")
	case *FString:
		p.w("(f")
		for _, part := range n.Parts {
			p.w(" ")
			if part.X != nil {
				p.node(part.X)
			} else {
				p.w(fmt.Sprintf("%q", part.Text))
			}
		}
		p.w(")")
	case *ListLit:
		p.w("(list")
		p.exprs(n.Elems)
		p.w(")")
	case *MapLit:
		p.w("(map")
		for _, e := range n.Entries {
			p.w(" (")
			p.node(e.Key)
			p.w(" ")
			p.node(e.Value)
			p.w(")")
		}
		p.w(")")
	case *Paren:
		p.node(n.X)
	case *Unary:
		p.w("(" + n.Op.String() + " ")
		p.node(n.X)
		p.w(")")
	case *Binary:
		op := n.Op.String()
		if n.Negate {
			op = "not " + op
		}
		p.w("(" + op + " ")
		p.node(n.X)
		p.w(" ")
		p.node(n.Y)
		p.w(")")
	case *Call:
		p.w("(call ")
		p.node(n.Fn)
		for _, a := range n.Args {
			p.w(" ")
			if a.Name != "" {
				p.w(a.Name + ": ")
			}
			p.node(a.Value)
		}
		p.w(")")
	case *Index:
		p.w("(index ")
		p.node(n.X)
		p.w(" ")
		p.node(n.Index)
		p.w(")")
	case *Member:
		p.node(n.X)
		p.w("." + n.Name)
	case *Scope:
		p.node(n.X)
		p.w("::" + n.Name)
	case *Try:
		p.w("(? ")
		p.node(n.X)
		p.w(")")
	case *MacroCall:
		p.w("(" + n.Name + "!")
		p.exprs(n.Args)
		p.w(")")
	case *IfExpr:
		p.w("(if ")
		p.node(n.Cond)
		p.w(" ")
		p.block(n.Then)
		if n.Else != nil {
			p.w(" ")
			p.node(n.Else)
		}
		p.w(")")
	case *MatchExpr:
		p.w("(match ")
		p.node(n.Subject)
		p.depth++
		for _, arm := range n.Arms {
			p.sep()
			p.w("(case")
			for _, pat := range arm.Patterns {
				p.w(" ")
				p.node(pat)
			}
			p.w(" ")
			p.block(arm.Body)
			p.w(")")
		}
		p.depth--
		p.w(")")

	// patterns
	case *WildcardPat:
		p.w("_")
	case *ValuePat:
		p.node(n.X)
	case *RangePat:
		p.node(n.Lo)
		p.w("..")
		p.node(n.Hi)
	case *VariantPat:
		p.node(n.Path)
		p.w("(")
		for i, a := range n.Args {
			if i > 0 {
				p.w(" ")
			}
			p.node(a)
		}
		p.w(")")
	case *BindPat:
		p.w(n.Name)

	// statements
	case *LetStmt:
		p.w("(let")
		p.binding(n.Name, n.Type, n.Value, n.Infer)
		p.w(")")
	case *ConstStmt:
		p.w("(const")
		p.binding(n.Name, n.Type, n.Value, false)
		p.w(")")
	case *AssignStmt:
		p.w("(" + n.Op.String() + " ")
		p.node(n.Target)
		p.w(" ")
		p.node(n.Value)
		p.w(")")
	case *ExprStmt:
		p.node(n.X)
	case *ReturnStmt:
		p.w("(return")
		if n.Value != nil {
			p.w(" ")
			p.node(n.Value)
		}
		p.w(")")
	case *BreakStmt:
		p.w("(break)")
	case *ContinueStmt:
		p.w("(continue)")
	case *WhileStmt:
		p.w("(while ")
		p.node(n.Cond)
		p.w(" ")
		p.block(n.Body)
		p.w(")")
	case *ForStmt:
		p.w("(for " + n.Var + " ")
		p.node(n.Iter)
		p.w(" ")
		p.block(n.Body)
		p.w(")")
	case *FnDecl:
		p.w("(fn " + n.Name + " " + paramsString(n.Params))
		if n.Result != nil {
			p.w(" -> " + typeString(n.Result))
		}
		if n.Body != nil {
			p.w(" ")
			p.block(n.Body)
		}
		p.w(")")
	case *MacroDecl:
		p.w("(macro " + n.Name + " " + paramsString(n.Params) + " ")
		p.block(n.Body)
		p.w(")")
	case *StructDecl:
		p.w("(struct " + n.Name)
		if len(n.Parents) > 0 {
			p.w(" is " + typesString(n.Parents))
		}
		p.depth++
		for _, f := range n.Fields {
			p.sep()
			p.node(f)
		}
		p.depth--
		p.w(")")
	case *Field:
		if n.Const {
			p.w("(const")
		} else {
			p.w("(let")
		}
		p.binding(n.Name, n.Type, n.Value, n.Infer)
		p.w(")")
	case *ImplDecl:
		p.w("(impl " + typeString(n.Type))
		p.methods(n.Methods)
		p.w(")")
	case *AddDecl:
		kind := "impl"
		if n.Kind == token.STRUCT {
			kind = "struct"
		}
		p.w("(add " + kind + " " + typeString(n.Source) + " to " + typeString(n.Target))
		p.methods(n.Methods)
		p.w(")")
	case *EnumDecl:
		p.w("(enum " + n.Name)
		if len(n.Parents) > 0 {
			p.w(" is " + typesString(n.Parents))
		}
		for _, v := range n.Variants {
			p.w(" ")
			p.node(v)
		}
		p.w(")")
	case *Variant:
		p.w(n.Name)
		if n.Parens {
			p.w(paramsString(n.Fields))
		}
	case *ImportStmt:
		p.w("(import " + strings.Join(n.Path, "::"))
		if n.Alias != "" {
			p.w(" as " + n.Alias)
		}
		p.w(")")
	case *TypeExpr:
		p.w(typeString(n))
	case *Param:
		p.w(paramString(n))
	default:
		p.w(fmt.Sprintf("<unknown %T>", n))
	}
}

func (p *printer) methods(list []*FnDecl) {
	p.depth++
	for _, m := range list {
		p.sep()
		p.node(m)
	}
	p.depth--
}
