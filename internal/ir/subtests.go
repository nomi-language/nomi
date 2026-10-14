package ir

import "strconv"

// THE `subtests` STATEMENT: run one row function per element of a sequence,
// collecting each row's failed assertion, then fail once.
//
// `subtests rows, pattern { body }` lowers to the rows as a sequence (an
// `IterView` or any sequence-valued temporary) and the body as a function
// value of one parameter, the row, answering Unit. The body is a function
// because a row is the unit a failed assertion ends: the row's activation is
// a test body's (its assertions leave it as a report), and this instruction is
// what catches each report, records it with the row rendered as a `values:`
// row renders a value, and runs the next row.
//
// THE DELIVERY IS AN ASSERTION'S. After the last row, when any row failed,
// the combined failure (rt.SubtestsFailure) leaves the activation exactly as
// one failed `assert` does: a test body's case fails with the report, and an
// ordinary `fn` returns `Err(AssertionFailure)`. That is the consumer's, as
// it is for Assert.
//
// No destination: the statement's value is Unit.
//
// A row that faults is not caught: the fault leaves the row and this
// instruction as it would leave any call.
type Subtests struct {
	pos  Pos
	rows Temp
	row  Temp
	text string
}

// NewSubtests runs row once per element of rows. text is the rows expression
// as `format.RenderNode` renders it, which the combined failure carries as
// its expression.
func NewSubtests(pos Pos, rows, row Temp, text string) *Subtests {
	requirePos(pos, "ir.NewSubtests")
	if rows == NoTemp || row == NoTemp {
		panic("ir.NewSubtests: a subtests statement needs its rows and its row function")
	}
	if text == "" {
		panic("ir.NewSubtests: the combined failure names its rows and these have no text")
	}
	return &Subtests{pos: pos, rows: rows, row: row, text: text}
}

// Rows is the sequence of rows.
func (n *Subtests) Rows() Temp { return n.rows }

// Row is the function value one row runs.
func (n *Subtests) Row() Temp { return n.row }

// Text is the rows expression as written.
func (n *Subtests) Text() string { return n.text }

func (n *Subtests) Pos() Pos  { return n.pos }
func (n *Subtests) Dst() Temp { return NoTemp }
func (n *Subtests) AppendUses(dst []Temp) []Temp {
	return append(dst, n.rows, n.row)
}

func (n *Subtests) String() string {
	return "subtests " + n.rows.String() + ", " + n.row.String() + " " + strconv.Quote(n.text)
}
func (n *Subtests) irNode()  {}
func (n *Subtests) irInstr() {}
