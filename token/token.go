// Package token defines the lexical tokens of SEPL.
package token

import "fmt"

// Type is the kind of a token.
type Type uint8

const (
	ILLEGAL Type = iota // lexical error; Literal holds the message
	EOF
	NEWLINE // end of a statement
	INDENT  // start of an indented block
	DEDENT  // end of an indented block

	IDENT
	INT
	FLOAT
	STRING  // Literal is the decoded value
	FSTRING // Literal is the raw text between the quotes; see lexer.SplitFString

	PLUS        // +
	MINUS       // -
	STAR        // *
	SLASH       // /
	SLASH_SLASH // //
	PERCENT     // %

	ASSIGN             // =
	WALRUS             // :=
	PLUS_ASSIGN        // +=
	MINUS_ASSIGN       // -=
	STAR_ASSIGN        // *=
	SLASH_ASSIGN       // /=
	SLASH_SLASH_ASSIGN // //=
	PERCENT_ASSIGN     // %=

	EQ     // ==
	NOT_EQ // !=
	LT     // <
	LT_EQ  // <=
	GT     // >
	GT_EQ  // >=

	BANG        // !   (macro call)
	QUESTION    // ?   (try)
	DOT         // .
	DOT_DOT     // ..  (range)
	ELLIPSIS    // ... (variadic)
	COMMA       // ,
	COLON       // :
	COLON_COLON // ::

	LPAREN   // (
	RPAREN   // )
	LBRACKET // [
	RBRACKET // ]
	LBRACE   // {
	RBRACE   // }

	keywordBeg
	LET
	CONST
	FN
	RETURN
	IF
	ELSE
	WHILE
	FOR
	IN
	BREAK
	CONTINUE
	MATCH
	STRUCT
	IMPL
	ENUM
	IMPORT
	AS
	AND
	OR
	NOT
	TRUE
	FALSE
	NIL
	MACRO
	keywordEnd
)

// Contextual words such as add, to, is, quote and self are not keywords:
// the lexer returns them as IDENT and the parser recognizes them by position,
// so names like math::add keep working.

var names = [...]string{
	ILLEGAL: "ILLEGAL",
	EOF:     "EOF",
	NEWLINE: "NEWLINE",
	INDENT:  "INDENT",
	DEDENT:  "DEDENT",

	IDENT:   "IDENT",
	INT:     "INT",
	FLOAT:   "FLOAT",
	STRING:  "STRING",
	FSTRING: "FSTRING",

	PLUS:        "+",
	MINUS:       "-",
	STAR:        "*",
	SLASH:       "/",
	SLASH_SLASH: "//",
	PERCENT:     "%",

	ASSIGN:             "=",
	WALRUS:             ":=",
	PLUS_ASSIGN:        "+=",
	MINUS_ASSIGN:       "-=",
	STAR_ASSIGN:        "*=",
	SLASH_ASSIGN:       "/=",
	SLASH_SLASH_ASSIGN: "//=",
	PERCENT_ASSIGN:     "%=",

	EQ:     "==",
	NOT_EQ: "!=",
	LT:     "<",
	LT_EQ:  "<=",
	GT:     ">",
	GT_EQ:  ">=",

	BANG:        "!",
	QUESTION:    "?",
	DOT:         ".",
	DOT_DOT:     "..",
	ELLIPSIS:    "...",
	COMMA:       ",",
	COLON:       ":",
	COLON_COLON: "::",

	LPAREN:   "(",
	RPAREN:   ")",
	LBRACKET: "[",
	RBRACKET: "]",
	LBRACE:   "{",
	RBRACE:   "}",

	LET:      "let",
	CONST:    "const",
	FN:       "fn",
	RETURN:   "return",
	IF:       "if",
	ELSE:     "else",
	WHILE:    "while",
	FOR:      "for",
	IN:       "in",
	BREAK:    "break",
	CONTINUE: "continue",
	MATCH:    "match",
	STRUCT:   "struct",
	IMPL:     "impl",
	ENUM:     "enum",
	IMPORT:   "import",
	AS:       "as",
	AND:      "and",
	OR:       "or",
	NOT:      "not",
	TRUE:     "true",
	FALSE:    "false",
	NIL:      "nil",
	MACRO:    "macro",
}

func (t Type) String() string {
	if int(t) < len(names) && names[t] != "" {
		return names[t]
	}
	return fmt.Sprintf("Type(%d)", uint8(t))
}

// IsKeyword reports whether t is a reserved word.
func (t Type) IsKeyword() bool { return keywordBeg < t && t < keywordEnd }

var keywords = func() map[string]Type {
	m := make(map[string]Type, keywordEnd-keywordBeg)
	for t := keywordBeg + 1; t < keywordEnd; t++ {
		m[names[t]] = t
	}
	return m
}()

// Lookup returns the keyword type for ident, or IDENT.
func Lookup(ident string) Type {
	// Keywords are 2 to 8 lowercase ASCII letters: skip the map otherwise.
	if len(ident) < 2 || len(ident) > 8 || ident[0] < 'a' || ident[0] > 'z' {
		return IDENT
	}
	if t, ok := keywords[ident]; ok {
		return t
	}
	return IDENT
}

// Pos is a position in the source: 1-based line and column (in characters).
type Pos struct {
	Line int
	Col  int
}

func (p Pos) String() string { return fmt.Sprintf("%d:%d", p.Line, p.Col) }

// Token is a single lexical token.
type Token struct {
	Type    Type
	Literal string
	Pos     Pos
}

func (t Token) String() string {
	switch t.Type {
	case IDENT, INT, FLOAT:
		return fmt.Sprintf("%s(%s)", t.Type, t.Literal)
	case STRING, FSTRING:
		return fmt.Sprintf("%s(%q)", t.Type, t.Literal)
	case ILLEGAL:
		return "ILLEGAL(" + t.Literal + ")"
	}
	return t.Type.String()
}
