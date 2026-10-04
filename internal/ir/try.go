package ir

import "strconv"

// The `try` class: Nomi's error propagation, which is a non-local exit.
//
// It pairs with `assert`. Both are exits that leave a construct early, and
// both carry a rendered source text for the same reason: whoever reports the
// exit prints where it left from. `Try.Text` is the same field as
// `Assert.Text` (point 3 of ir.go's package header), which shows that the
// rule is about non-local exit and not about assertions.
//
// # The class contributes one node, and the other two are already here
//
// A `try` is three things:
//
//	refute      is the operand the unwind variant?   ir.MatchVariant (match.go)
//	exit        leave, carrying it                   Try, here
//	navigate    read the unwrap variant's payload    ir.Proj (proj.go)
//
// So `try` decomposes exactly as a pattern does: a pattern is a sequence, not
// a node, and that holds for a construct that is not a pattern.
// `internal/irbuild`'s irTry builds the `ir.MatchVariant` through the same
// constructor a `case` arm uses and takes only the condition from it, because
// where the false answer goes is the consumer's, as match.go says.
//
// # The boundary is not on this node
//
// `try` unwinds to the enclosing activation: a lambda is one, so a `try` inside
// a lambda leaves the lambda and not the enclosing `fn`. There are four boundaries — a `fn`, a lambda, a `concurrent` block and a
// test body — and they have three different exits, so it is tempting to put the
// answer on the node.
//
// It does not go here, because the consumer reads no field for it. The
// boundary is the innermost activation, which is the node's POSITION
// in the program, and a linear IR inside a Region already expresses that; a
// field would be a second statement of it and able to disagree.
//
// What the boundary does decide is the SHAPE of the exit — a value rebuilt at
// the boundary's own result type, a report for a test body, nothing at all for
// a block — and that is delivery, which belongs to the consumer. irbuild
// reads the classification back off the CHECKER's hover symbol rather than
// re-deriving it, because its own two flags get one of the four wrong.
//
// So: no boundary field, no predicate, and the class adds one node.

// Try is the point at which control may leave, carrying an error.
type Try struct {
	pos  Pos
	src  Temp
	text string
}

// NewTry propagates out of src when src holds the unwind variant.
//
// text is `try E` — or the whole pipe, for `x |> try f()` — as
// `format.RenderNode` renders it. The text is a field for the same reason
// `Assert.Text` is, and the reason is stronger here because the two SPELLINGS of one `try` render
// differently: the pipe form renders the `*ast.Binary` and the prefix form the
// `*ast.TryOp`, so irbuild is handed the node to render separately from the node
// carrying the position. A field the producer fills is where that choice is
// recorded once.
//
// REQUIRED even though only ONE of the four boundaries prints it. A test body's
// report shows the `try` that ended the case; every other boundary discards the
// text. Making it optional would make the node's contents depend on a
// consumer's delivery, the coupling the fault/delivery division prevents, and
// a producer cannot know which boundary it is in without asking the question
// this node deliberately does not carry.
//
// NO DESTINATION. The unwrapped value is an `ir.Proj` of the unwrap variant's
// payload, so the read is its own node and this one writes nothing: a `try`
// reads its payload with the same projection an ordinary `case Ok(v)` arm
// uses.
func NewTry(pos Pos, src Temp, text string) *Try {
	requirePos(pos, "ir.NewTry")
	if src == NoTemp {
		panic("ir.NewTry: a try with no operand propagates nothing")
	}
	if text == "" {
		panic("ir.NewTry: a propagating try is reported by its source text and this one has none")
	}
	return &Try{pos: pos, src: src, text: text}
}

// Src is the `Result` or `Maybe` this try unwraps.
func (t *Try) Src() Temp { return t.src }

// Text is how the try was written, as the report prints it.
func (t *Try) Text() string { return t.text }

func (t *Try) Pos() Pos                     { return t.pos }
func (t *Try) Dst() Temp                    { return NoTemp }
func (t *Try) AppendUses(dst []Temp) []Temp { return append(dst, t.src) }
func (t *Try) String() string {
	return "try " + t.src.String() + " " + strconv.Quote(t.text)
}
func (t *Try) irNode()  {}
func (t *Try) irInstr() {}
