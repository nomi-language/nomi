package ir

import "strconv"

// String concatenation, N-ary.
//
// `ir.Domain` has four members (Int, Float, Decimal, Named) and String is
// not one, which `internal/irbuild/irarith.go`'s `irArithKind` states as a
// construction rather than an omission: `"a" + "b"` is concatenation, not
// arithmetic. `arith()`'s String arm and the interpolation join are the same
// operation, and this node is it.
//
// The node records the operation and leaves its delivery to the consumer,
// the division point 1 of ir.go's package header draws for faults. What is
// here is "these N Strings, in this order, become one String". Whether that
// is a builder, a right-nested `+`, a `strings.Join` or a rope is the
// consumer's choice.
//
// It is N-ary rather than binary, for three reasons.
//
//  1. IT SUBSUMES BOTH SITES. `"a" + b` is a Concat of two and `"x${y}z"` is a
//     Concat of three, and neither producer has to know which the other built.
//     A binary node would make an interpolation of k holes into 2k-1 nodes
//     whose tree SHAPE — left-nested or right-nested — is a fact no source
//     wrote and producer and consumer would have to agree about.
//  2. IT IS WHAT THE PRODUCER ALREADY DOES. `gen.interp` joins every part at
//     once and builds no tree; a binary node would have been the one shape
//     with no producer.
//  3. A BINARY NODE PUTS AN ASSOCIATIVITY CLAIM IN THE REPRESENTATION THAT
//     THE LANGUAGE DOES NOT MAKE. Nomi's `+` on String is parsed left-
//     associative, so `a + b + c` is `(a + b) + c` and the builder keeps the
//     nesting; but an INTERPOLATION has no operator and no grouping at all,
//     and a producer forced to invent one would be recording a fact about its
//     own loop. An N-ary join records the ORDER, which is the only thing the
//     source says.
//
// CONCATENATION CANNOT FAULT, which is why this node has no `Faults` and why
// it is not in `arith`. Every operand is already a String, Nomi's String is
// immutable, and there is no operand pair with no answer — unlike Int `/`,
// Float `%` and Decimal `%`, which is the whole reason `Domain` and `Faults`
// exist. Putting String in `Domain` would have given twelve faulting
// operations a thirteenth domain that faults in none of them and a `Shape`
// enum an eighth member that is not an arithmetic shape.
//
// WHY NOT `Render`. A Render turns ONE value of any type into a String under
// one of three disciplines; a Concat joins N values that are ALREADY Strings
// and chooses nothing. `"x = ${n}"` is both, in that order: a Render of `n`
// under the Display discipline, then a Concat of the text runs and the
// rendered hole. Collapsing them would put the rendering discipline — the
// disagreements render.go lists — inside a join that has no value to
// render.
//
// TWO OPERANDS MINIMUM. A join of one is that one operand, and both producers
// already answer that way without building anything: `gen.interp` returns
// `parts[0]` for a single part. A one-operand Concat would be a node a
// consumer must recognise as the identity, a node that carries no
// information. A join of ZERO is the empty String, which
// is `ir.NewString(pos, dst, "")` and already has a node.

// Concat joins N Strings, in order, into one.
type Concat struct {
	pos   Pos
	dst   Temp
	parts []Temp
}

// NewConcat joins parts into dst. pos is the JOIN's own position: the
// operator for `a + b`, the interpolation for `"a${b}"`.
func NewConcat(pos Pos, dst Temp, parts ...Temp) *Concat {
	requirePos(pos, "NewConcat")
	if dst == NoTemp {
		panic("ir: NewConcat: a join nobody reads is dead; do not build it")
	}
	if len(parts) < 2 {
		panic("ir: NewConcat: a join of " + strconv.Itoa(len(parts)) +
			" is not a join; one part IS the answer and zero parts is the empty String constant")
	}
	for i, p := range parts {
		if p == NoTemp {
			panic("ir: NewConcat: part " + strconv.Itoa(i) + " is absent")
		}
	}
	return &Concat{pos: pos, dst: dst, parts: append([]Temp(nil), parts...)}
}

// NumParts is how many Strings are joined.
func (c *Concat) NumParts() int { return len(c.parts) }

// Part is operand n, in source order.
func (c *Concat) Part(n int) Temp { return c.parts[n] }

func (c *Concat) Pos() Pos  { return c.pos }
func (c *Concat) Dst() Temp { return c.dst }

func (c *Concat) AppendUses(dst []Temp) []Temp { return append(dst, c.parts...) }

func (c *Concat) String() string {
	s := c.dst.String() + " = concat"
	for _, p := range c.parts {
		s += " " + p.String()
	}
	return s
}

func (c *Concat) irNode()  {}
func (c *Concat) irInstr() {}
