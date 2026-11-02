package ir

import "strconv"

// THE `todo` TRAP: what a `todo` expression lowers to.
//
// `todo` is Nomi's placeholder for code not yet written. It produces no value:
// reaching it stops the program with rt.TodoError's text, which names the
// source file and line and the reason when the programmer gave one. The text
// is rt's, as the no-match trap's is (nomatch.go), so no consumer owns the
// message.
//
// IT HAS A DESTINATION, unlike NoMatch, and that is what lets the builder put
// one anywhere an expression goes. The checker gives `todo` the type its
// position expects, so to the code around it a `todo` is an operand of that
// type: `Header{size: todo}` needs a temporary of kind Int to construct with.
// The destination is that temporary. It is typed like any other, and never
// written, because the instruction always faults first. A call to a function
// that never returns has the same shape: a destination the graph reads after
// it and no execution reaches.
//
// So it is NOT `RuleNoMatchIsLast`'s trap. Code after a `todo` in its block is
// the code the expression sits in, which the programmer wrote and the graph
// keeps; a consumer that cares that it never runs asks `Faults()`.
type Todo struct {
	pos    Pos
	dst    Temp
	reason string
}

// NewTodo is the trap a `todo` takes. pos is the `todo` keyword's position,
// whose file and line the report names; reason is the literal's text, or ""
// for a bare `todo`.
func NewTodo(pos Pos, dst Temp, reason string) *Todo {
	requirePos(pos, "NewTodo")
	return &Todo{pos: pos, dst: dst, reason: reason}
}

// Reason is the programmer's reason, or "" for a bare `todo`.
func (n *Todo) Reason() string { return n.reason }

// Faults is FaultTodo, always.
func (n *Todo) Faults() Faults { return FaultTodo }

func (n *Todo) Pos() Pos                     { return n.pos }
func (n *Todo) Dst() Temp                    { return n.dst }
func (n *Todo) AppendUses(dst []Temp) []Temp { return dst }

func (n *Todo) String() string {
	s := n.dst.String() + " = todo"
	if n.reason != "" {
		s += " " + strconv.Quote(n.reason)
	}
	return s
}
func (n *Todo) irNode()  {}
func (n *Todo) irInstr() {}
