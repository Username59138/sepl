package vm

import (
	"fmt"
	"io"
	"strings"

	"github.com/Username59138/sepl/token"
)

// Op is an instruction opcode. An instruction is one uint32 word: the opcode
// in the low 8 bits and an operand A in the high 24 bits. A few instructions
// take extra operand words right after them (noted below).
type Op uint8

const (
	OpConst      Op = iota // push Consts[A]
	OpInt                  // push int A (signed 24-bit)
	OpNil                  // push nil
	OpTrue                 // push true
	OpFalse                // push false
	OpPop                  // pop 1
	OpPopN                 // pop A
	OpCloseN               // close upvalues of the top A slots, pop A
	OpSlide                // keep the top value, drop the A values under it
	OpCloseSlide           // same, closing upvalues of the dropped slots
	OpDup                  // push a copy of the top
	OpDup2                 // push copies of the top two
	OpGetLocal             // push slot A
	OpSetLocal             // pop into slot A
	OpGetUpval             // push upvalue A
	OpSetUpval             // pop into upvalue A
	OpGetGlobal            // push global A
	OpSetGlobal            // pop into global A (must be defined)
	OpDefGlobal            // pop into global A
	OpGetBuiltin           // push builtin A

	OpAdd
	OpSub
	OpMul
	OpDiv
	OpIDiv
	OpMod
	OpNeg
	OpNot
	OpToBool
	OpEq
	OpNe
	OpLt
	OpLe
	OpGt
	OpGe
	OpIn
	OpInRange // x lo hi -> lo <= x < hi

	OpJump        // jump to A
	OpJumpIfFalse // pop; jump to A if falsy
	OpJumpIfTrue  // pop; jump to A if truthy

	OpCall      // call with A positional args
	OpCallKw    // call with A args, the last ones named; +1 word: Consts index of KwNames
	OpInvoke    // method call, A args; +1 word: Consts index of the method name
	OpInvokeKw  // method call; +2 words: method name, KwNames
	OpReturn    // return the top value
	OpReturnNil // return nil
	OpClosure   // push a closure of the Proto in Consts[A]

	OpList     // build a list from the top A values
	OpMap      // build a map from the top A key/value pairs
	OpRange    // lo hi -> lo..hi
	OpIndex    // c i -> c[i]
	OpSetIndex // c i v -> (c[i] = v)
	OpGetField // x -> x.name, name = Consts[A]
	OpSetField // x v -> (x.name = v)
	OpBuildStr // join the to_str of the top A values

	OpIter     // replace the top value with an iterator over it
	OpForNext  // push the next item of the iterator in slot (+1 word), or jump to A when done
	OpForPrep  // check that the two range bounds on top are ints
	OpForRange // counter in slot (+1 word), limit in slot+1, loop variable in slot+2: set and step, or jump to A

	// Fused instructions: one dispatch instead of two or three.
	OpAddI      // top += A (signed 24-bit)
	OpSubI      // top -= A
	OpMulI      // top *= A
	OpModI      // top %= A
	OpJumpNotEq // pop 2; jump to A unless a == b
	OpJumpNotNe // pop 2; jump to A unless a != b
	OpJumpNotLt // pop 2; jump to A unless a < b
	OpJumpNotLe // pop 2; jump to A unless a <= b
	OpJumpNotGt // pop 2; jump to A unless a > b
	OpJumpNotGe // pop 2; jump to A unless a >= b
	OpIncLocal  // local[A & 0xfff] += (A >> 12) as a signed 12-bit int
	OpAddLocal  // local[A & 0xfff] += local[A >> 12]
	OpCloseFrom // close upvalues of slots >= A, without popping

	OpStruct    // build a struct type from Consts[A] (a StructDesc) and the values on the stack
	OpImpl      // type, method closures -> add the methods of Consts[A] (an ImplDesc)
	OpAddStruct // source target -> add the source's fields to the target
	OpAddImpl   // source target closures -> add the source's methods (and the overrides in Consts[A])
	OpScope     // x -> x::name, name = Consts[A]
	OpImport    // push the module named Consts[A]

	OpEnum         // build an enum type from Consts[A] (an EnumDesc); parent enums on the stack
	OpIsVariant    // x p -> is x a value of the variant p? A-1 is the number of sub-patterns (A=0: any)
	OpVariantField // x -> field A of the enum value x
	OpMatchEq      // x p -> x == p, where a variant constructor p matches its whole variant
	OpTry          // x -> x? : x.try() gives flow::next(v): go on at A with v; flow::exit(v): fall through (to return v)
	OpCheckType    // v t -> v (checked against type t; Consts[A] says what is checked)
	OpCheckKeep    // v t -> v t (checked)
	OpCheckEach    // list t -> list (every item checked)
	OpTypeOf       // v -> v t (the type that := fixes)
	OpCheckArg     // v t -> v, like CHECKTYPE, but an error is reported at the call
	OpCheckParam   // check parameter local[A & 0xfff] in place against the type in local[A >> 12]
	OpCheckResult  // check the top value against the result type in local[A]
)

var opNames = [...]string{
	OpConst: "CONST", OpInt: "INT", OpNil: "NIL", OpTrue: "TRUE", OpFalse: "FALSE",
	OpPop: "POP", OpPopN: "POPN", OpCloseN: "CLOSEN", OpSlide: "SLIDE", OpCloseSlide: "CLOSESLIDE",
	OpDup: "DUP", OpDup2: "DUP2",
	OpGetLocal: "GETLOCAL", OpSetLocal: "SETLOCAL", OpGetUpval: "GETUPVAL", OpSetUpval: "SETUPVAL",
	OpGetGlobal: "GETGLOBAL", OpSetGlobal: "SETGLOBAL", OpDefGlobal: "DEFGLOBAL", OpGetBuiltin: "GETBUILTIN",
	OpAdd: "ADD", OpSub: "SUB", OpMul: "MUL", OpDiv: "DIV", OpIDiv: "IDIV", OpMod: "MOD",
	OpNeg: "NEG", OpNot: "NOT", OpToBool: "TOBOOL",
	OpEq: "EQ", OpNe: "NE", OpLt: "LT", OpLe: "LE", OpGt: "GT", OpGe: "GE", OpIn: "IN", OpInRange: "INRANGE",
	OpJump: "JUMP", OpJumpIfFalse: "JUMPIFFALSE", OpJumpIfTrue: "JUMPIFTRUE",
	OpCall: "CALL", OpCallKw: "CALLKW", OpInvoke: "INVOKE", OpInvokeKw: "INVOKEKW",
	OpReturn: "RETURN", OpReturnNil: "RETURNNIL", OpClosure: "CLOSURE",
	OpList: "LIST", OpMap: "MAP", OpRange: "RANGE", OpIndex: "INDEX", OpSetIndex: "SETINDEX",
	OpGetField: "GETFIELD", OpSetField: "SETFIELD", OpBuildStr: "BUILDSTR",
	OpIter: "ITER", OpForNext: "FORNEXT", OpForPrep: "FORPREP", OpForRange: "FORRANGE",
	OpAddI: "ADDI", OpSubI: "SUBI", OpMulI: "MULI", OpModI: "MODI",
	OpJumpNotEq: "JUMPNOTEQ", OpJumpNotNe: "JUMPNOTNE", OpJumpNotLt: "JUMPNOTLT",
	OpJumpNotLe: "JUMPNOTLE", OpJumpNotGt: "JUMPNOTGT", OpJumpNotGe: "JUMPNOTGE",
	OpIncLocal: "INCLOCAL", OpAddLocal: "ADDLOCAL", OpCloseFrom: "CLOSEFROM",
	OpStruct: "STRUCT", OpImpl: "IMPL", OpAddStruct: "ADDSTRUCT", OpAddImpl: "ADDIMPL",
	OpScope: "SCOPE", OpImport: "IMPORT",
	OpEnum: "ENUM", OpIsVariant: "ISVARIANT", OpVariantField: "VARIANTFIELD", OpMatchEq: "MATCHEQ", OpTry: "TRY",
	OpCheckType: "CHECKTYPE", OpCheckKeep: "CHECKKEEP", OpCheckEach: "CHECKEACH", OpTypeOf: "TYPEOF", OpCheckArg: "CHECKARG",
	OpCheckParam: "CHECKPARAM", OpCheckResult: "CHECKRESULT",
}

func (op Op) String() string {
	if int(op) < len(opNames) && opNames[op] != "" {
		return opNames[op]
	}
	return fmt.Sprintf("OP%d", op)
}

// Symbol is the source operator of an arithmetic opcode, for error messages.
func (op Op) Symbol() string {
	switch op {
	case OpAdd:
		return "+"
	case OpSub:
		return "-"
	case OpMul:
		return "*"
	case OpDiv:
		return "/"
	case OpIDiv:
		return "//"
	case OpMod, OpModI:
		return "%"
	case OpAddI:
		return "+"
	case OpSubI:
		return "-"
	case OpMulI:
		return "*"
	case OpLt:
		return "<"
	case OpLe:
		return "<="
	case OpGt:
		return ">"
	case OpGe:
		return ">="
	}
	return op.String()
}

// ExtraWords is how many operand words follow the instruction.
func (op Op) ExtraWords() int {
	switch op {
	case OpCallKw, OpInvoke, OpForNext, OpForRange:
		return 1
	case OpInvokeKw:
		return 2
	}
	return 0
}

// MaxOperand is the largest value of operand A.
const MaxOperand = 1<<24 - 1

// Encode builds an instruction word.
func Encode(op Op, a int) uint32 { return uint32(op) | uint32(a)<<8 }

// EncodeInt builds an OpInt word for a signed 24-bit value.
func EncodeInt(v int) uint32 { return uint32(OpInt) | uint32(int32(v)<<8) }

// EncodeSigned builds a word whose operand is a signed 24-bit value.
func EncodeSigned(op Op, v int) uint32 { return uint32(op) | uint32(int32(v)<<8) }

// EncodePair builds a word with two 12-bit operands; the second may be signed.
func EncodePair(op Op, lo, hi int) uint32 {
	return uint32(op) | uint32(lo&0xfff)<<8 | uint32(int32(hi)<<20)
}

// FitsInt reports whether v fits the OpInt operand.
func FitsInt(v int64) bool { return v >= -1<<23 && v < 1<<23 }

// Proto is a compiled function.
type Proto struct {
	Name       string
	File       string
	At         token.Pos
	NParams    int
	Variadic   bool // the last parameter collects extra arguments
	ParamNames []string
	Code       []uint32
	Pos        []token.Pos // source position of each code word
	Consts     []Value
	Upvals     []UpvalDesc
	MaxStack   int
	Env        *Env // the globals of the module this function belongs to
}

func (*Proto) TypeName() string { return "proto" }

// UpvalDesc says where a closure gets a captured variable from: a local slot
// of the enclosing function, or one of the enclosing function's upvalues.
type UpvalDesc struct {
	Local bool
	Index int
	Name  string
}

// Disasm writes a readable listing of p and the functions inside it.
func (p *Proto) Disasm(w io.Writer) {
	fmt.Fprintf(w, "fn %s (params %d, stack %d, upvalues %d)\n", p.Name, p.NParams, p.MaxStack, len(p.Upvals))
	var inner []*Proto
	for ip := 0; ip < len(p.Code); {
		ins := p.Code[ip]
		op := Op(ins)
		a := int(ins >> 8)
		fmt.Fprintf(w, "  %4d %5s  %-12s", ip, p.Pos[ip], op)
		switch op {
		case OpInt, OpAddI, OpSubI, OpMulI, OpModI:
			fmt.Fprintf(w, "%d", int32(ins)>>8)
		case OpIncLocal:
			fmt.Fprintf(w, "slot %d += %d", a&0xfff, int32(ins)>>20)
		case OpAddLocal:
			fmt.Fprintf(w, "slot %d += slot %d", a&0xfff, a>>12)
		case OpCheckParam:
			fmt.Fprintf(w, "slot %d is a slot %d", a&0xfff, a>>12)
		case OpConst:
			fmt.Fprintf(w, "%d  ; %s", a, constString(p.Consts[a]))
		case OpClosure:
			fp := p.Consts[a].O.(*Proto)
			inner = append(inner, fp)
			fmt.Fprintf(w, "%d  ; fn %s", a, fp.Name)
		case OpGetField, OpSetField:
			fmt.Fprintf(w, "%d  ; .%s", a, ToStr(p.Consts[a]))
		case OpScope:
			fmt.Fprintf(w, "%d  ; ::%s", a, ToStr(p.Consts[a]))
		case OpImport, OpCheckType, OpCheckKeep, OpCheckEach, OpTypeOf, OpCheckArg:
			fmt.Fprintf(w, "%d  ; %s", a, ToStr(p.Consts[a]))
		case OpStruct:
			fmt.Fprintf(w, "%d  ; struct %s", a, p.Consts[a].O.(*StructDesc).Name)
		case OpEnum:
			fmt.Fprintf(w, "%d  ; enum %s", a, p.Consts[a].O.(*EnumDesc).Name)
		case OpImpl, OpAddImpl:
			var names []string
			for _, m := range p.Consts[a].O.(*ImplDesc).Methods {
				names = append(names, m.Name)
			}
			fmt.Fprintf(w, "%d  ; %s", a, strings.Join(names, " "))
		case OpCallKw:
			fmt.Fprintf(w, "%d  ; %v", a, p.Consts[p.Code[ip+1]].O)
		case OpInvoke:
			fmt.Fprintf(w, "%d  ; .%s", a, p.Consts[p.Code[ip+1]].O.(*MethodRef).Name)
		case OpInvokeKw:
			fmt.Fprintf(w, "%d  ; .%s %v", a, p.Consts[p.Code[ip+1]].O.(*MethodRef).Name, p.Consts[p.Code[ip+2]].O)
		case OpForNext, OpForRange:
			fmt.Fprintf(w, "%d  ; slot %d", a, p.Code[ip+1])
		case OpNil, OpTrue, OpFalse, OpPop, OpDup, OpDup2, OpReturn, OpReturnNil,
			OpAdd, OpSub, OpMul, OpDiv, OpIDiv, OpMod, OpNeg, OpNot, OpToBool,
			OpEq, OpNe, OpLt, OpLe, OpGt, OpGe, OpIn, OpInRange, OpRange,
			OpIndex, OpSetIndex, OpIter, OpForPrep, OpAddStruct, OpMatchEq:
		default:
			fmt.Fprintf(w, "%d", a)
		}
		fmt.Fprintln(w)
		ip += 1 + op.ExtraWords()
	}
	for _, fp := range inner {
		fmt.Fprintln(w)
		fp.Disasm(w)
	}
}

func constString(v Value) string {
	s := Repr(v)
	if len(s) > 40 {
		s = s[:37] + "..."
	}
	return strings.ReplaceAll(s, "\n", `\n`)
}
