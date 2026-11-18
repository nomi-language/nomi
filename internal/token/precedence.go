package token

// The binding levels of Nomi's operators, loosest first (spec §8
// *Operators and precedence*). A higher level binds tighter. Level 1 is
// any expression.
//
// This is the one precedence table. The parser reads it through
// InfixPrecedence and OperandPrecedence, the formatter decides parentheses
// with OperandNeedsParens, and TestPrecedenceTablesMatch (internal/docscheck)
// holds tree-sitter-nomi/grammar.js and the spec's table to it.
const (
	PrecOr             = 2  // or
	PrecAnd            = 3  // and
	PrecEquality       = 4  // == != is in
	PrecOrdering       = 5  // < > <= >=
	PrecPipe           = 6  // |> as
	PrecRange          = 7  // .. ..=
	PrecAdditive       = 8  // + - ||| ^^^
	PrecMultiplicative = 9  // * / % &&& <<< >>>
	PrecPrefix         = 10 // prefix - and ~~~
	PrecCall           = 11 // a call's `(`
	PrecField          = 12 // field access
)

// NotOperandPrecedence is the loosest level prefix `not`'s operand holds:
// `==`'s, so `not a == b` is `not (a == b)`, `not x |> f()` is
// `not (x |> f())`, and `not a and b` is `(not a) and b`.
const NotOperandPrecedence = PrecEquality

// TryOperandPrecedence is the loosest level the operand of a capture's
// `&` and of a `then` or `tap` stage holds: calls and field access attach
// to it, and every binary operator stays outside it.
const TryOperandPrecedence = PrecPrefix

// Assoc is how an operator reads beside another of its level.
type Assoc int

const (
	// LeftAssoc: `a - b - c` is `(a - b) - c`.
	LeftAssoc Assoc = iota
	// NonAssoc: the operator does not chain. `is` and `in` chain with no
	// comparison (`a == b is P` is an error), and `as` with no `as`.
	NonAssoc
)

// Operator is one infix operator.
type Operator struct {
	// Type is the operator's token: ILLEGAL for `>>>`, which the lexer
	// leaves as three GT tokens and the parser joins.
	Type TokenType
	// Text is the operator as written, which ast.Binary.Op holds.
	Text  string
	Prec  int
	Assoc Assoc
}

var infixOperators = []Operator{
	{OR, "or", PrecOr, LeftAssoc},
	{AND, "and", PrecAnd, LeftAssoc},
	{EQEQ, "==", PrecEquality, LeftAssoc},
	{BANGEQ, "!=", PrecEquality, LeftAssoc},
	{IS, "is", PrecEquality, NonAssoc},
	{IN, "in", PrecEquality, NonAssoc},
	{LT, "<", PrecOrdering, LeftAssoc},
	{GT, ">", PrecOrdering, LeftAssoc},
	{LTEQ, "<=", PrecOrdering, LeftAssoc},
	{GTEQ, ">=", PrecOrdering, LeftAssoc},
	// `as` shares `|>`'s level and its left-to-right reading, so
	// `xs |> Iter.to_list() as List<Int>` ascribes the whole pipeline, and
	// a comparison's operand holds it: `a == b as T` is `a == (b as T)`.
	{PIPE, "|>", PrecPipe, LeftAssoc},
	{AS, "as", PrecPipe, NonAssoc},
	// Ranges sit between comparison and arithmetic, so `1+2..5+6` is
	// `(1+2)..(5+6)` and `1..5 == 1..5` is `(1..5) == (1..5)`.
	{DOTDOT, "..", PrecRange, LeftAssoc},
	{DOTDOTEQ, "..=", PrecRange, LeftAssoc},
	// The bitwise operators sit where Go puts them: `&&&` and the shifts
	// with `*`, `|||` and `^^^` with `+`.
	{PLUS, "+", PrecAdditive, LeftAssoc},
	{MINUS, "-", PrecAdditive, LeftAssoc},
	{BIT_OR, "|||", PrecAdditive, LeftAssoc},
	{BIT_XOR, "^^^", PrecAdditive, LeftAssoc},
	{STAR, "*", PrecMultiplicative, LeftAssoc},
	{SLASH, "/", PrecMultiplicative, LeftAssoc},
	{PERCENT, "%", PrecMultiplicative, LeftAssoc},
	{BIT_AND, "&&&", PrecMultiplicative, LeftAssoc},
	{SHL, "<<<", PrecMultiplicative, LeftAssoc},
	{ILLEGAL, ">>>", PrecMultiplicative, LeftAssoc},
}

var (
	operatorByType = map[TokenType]*Operator{}
	operatorByText = map[string]*Operator{}
)

func init() {
	for i := range infixOperators {
		op := &infixOperators[i]
		if op.Type != ILLEGAL {
			operatorByType[op.Type] = op
		}
		operatorByText[op.Text] = op
	}
}

// InfixOperators returns the infix operators, loosest first.
func InfixOperators() []Operator {
	return append([]Operator(nil), infixOperators...)
}

// InfixPrecedence is the level of the infix operator tt starts, a call's
// `(` and a field access's `.` included, or 0 when tt starts none.
func InfixPrecedence(tt TokenType) int {
	switch tt {
	case LPAREN:
		return PrecCall
	case DOT:
		return PrecField
	}
	if op := operatorByType[tt]; op != nil {
		return op.Prec
	}
	return 0
}

// Precedence is the level of the infix operator written op, or 0 when op
// is none.
func Precedence(op string) int {
	if o := operatorByText[op]; o != nil {
		return o.Prec
	}
	return 0
}

// Side is where an operand stands beside its operator.
type Side int

const (
	// LeftOperand is an infix operator's left operand.
	LeftOperand Side = iota
	// RightOperand is an infix operator's right operand.
	RightOperand
	// PrefixOperand is the operand of prefix `-`, `~~~` or `not`.
	PrefixOperand
)

// OperandPrecedence is the loosest level the operand on side of the
// operator written op holds without parentheses: the level the parser
// parses that operand at.
//
//   - A prefix `-` or `~~~` holds PrecPrefix, and `not`
//     NotOperandPrecedence.
//   - `is` and `in` chain with no comparison, so their operands hold only
//     what binds tighter than every comparison.
//   - A left operand holds its operator's own level, since operators of
//     one level read left to right, and a right operand the next one.
func OperandPrecedence(op string, side Side) int {
	if side == PrefixOperand {
		if op == "not" {
			return NotOperandPrecedence
		}
		return PrecPrefix
	}
	o := operatorByText[op]
	if o == nil {
		return 0
	}
	if o.Assoc == NonAssoc && o.Prec <= PrecOrdering {
		return PrecOrdering + 1
	}
	if side == RightOperand {
		return o.Prec + 1
	}
	return o.Prec
}

// OperandNeedsParens reports whether an operand whose own outermost
// operator is child (an infix operator as written, or "not") must be
// written in parentheses on side of the operator parent (an infix
// operator, or a prefix one with PrefixOperand): whether the parser reads
// the operand differently, or refuses it, without them.
// TestOperandNeedsParensIsTheParsersAnswer (internal/parser) holds it to
// the parser for every operator, side and operand.
//
// An operand whose outermost operator is none of those (a call, a
// literal, a prefix `-`) needs none for precedence.
func OperandNeedsParens(parent string, side Side, child string) bool {
	need := OperandPrecedence(parent, side)
	var p *Operator
	if side != PrefixOperand {
		p = operatorByText[parent]
	}
	c := operatorByText[child]
	switch {
	case child == "not":
		// `not` takes everything to its right down to `==`, so it stands
		// bare only where nothing that tight follows it: as a right or
		// prefix operand parsed that loosely, or to the left of `and` or
		// `or`.
		if side == LeftOperand {
			return p != nil && p.Prec >= NotOperandPrecedence
		}
		return need > NotOperandPrecedence
	case c == nil:
		return false
	case c.Assoc == NonAssoc && c.Prec <= PrecOrdering:
		// `is` and `in` chain with no comparison, on either side of one.
		return c.Prec < need || p != nil && p.Prec >= PrecEquality && p.Prec <= PrecOrdering
	case child == "as" && side == LeftOperand:
		// `as` ends with its type, so whatever follows applies to the
		// whole ascription, as it would in parentheses: `b as T + 1` is
		// `(b as T) + 1`. Two follow no type: another `as`, which does not
		// chain, and `<`, which would open the type's arguments.
		return parent == "as" || parent == "<"
	}
	return c.Prec < need
}
