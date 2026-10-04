package ir

// The `logic` class: short-circuit `and` and `or`.
//
// This class has no instruction of its own, and that is the finding rather than
// an omission. `and` and `or` are a CONTROL-FLOW SHAPE — a Branch, a block for
// the right operand, and a Copy into one destination — and they are the reason
// this package has basic blocks and terminators at all. Go's `&&` and `||`
// short-circuit too, but only over EXPRESSIONS, and a Nomi right operand can
// need statements, so the branch has to be explicit. `native.go`'s `logical`
// says exactly that in its own comment and then builds the branch form
// UNCONDITIONALLY: a slot, then `if test { slot = right }`. So the shape below
// is not an approximation of what the builder does; it is what the builder
// does.
//
// Two things make the right operand a real block rather than an expression,
// both visible in `logical`:
//
//   - the right operand's own lowering may emit statements;
//   - inside an assertion, the operand recording emits statements too, and its
//     rule is asymmetric — `or` records its left operand always, `and` records
//     its left only when the left DECIDED, because a true left explains nothing
//     about why a conjunction failed.
//
// The canonical shape, which producer and consumer must agree on because a divergence
// here is a divergence in what gets evaluated:
//
//	b_entry:  d = lhs                    ; the answer if the left decides
//	          branch lhs ? b_rhs : b_join    (`and`)
//	          branch lhs ? b_join : b_rhs    (`or`)
//	b_rhs:    <the right operand's statements>
//	          d = rhs
//	          jump b_join
//	b_join:   ; d holds the answer
//
// `a and b` evaluates to `b` itself rather than to a normalized sentinel, which
// on a Bool is the same value — so the Copy is the whole of that rule.
//
// BeginShortCircuit builds the skeleton and Finish closes it, so neither
// consumer can forget the Copy or the Jump. That is the only reason a builder
// exists in a package with no lowering: the shape is shared, and a shape each
// consumer reconstructs by hand is a shape they will eventually reconstruct
// differently.
type LogicOp uint8

const (
	// LogicAnd is `and`: the right operand runs only when the left is true.
	LogicAnd LogicOp = iota + 1
	// LogicOr is `or`: the right operand runs only when the left is false.
	LogicOr
)

func (o LogicOp) String() string {
	if o == LogicOr {
		return "or"
	}
	return "and"
}

// ShortCircuit is a short-circuit `and` / `or` under construction.
type ShortCircuit struct {
	op   LogicOp
	dst  Temp
	rhs  *Block
	join *Block
	done bool
}

// BeginShortCircuit wires the entry side of a short-circuit operation.
//
// It appends `dst = lhs` to entry, terminates entry with a Branch on lhs, and
// creates the right-operand block and the join block. The caller lowers the
// right operand into the block Rhs returns and then calls Finish.
//
// It takes a Region and not a Func. The arena is used for NewBlock and
// nothing else: a short-circuit needs a block namespace, not a function. The
// builder reaches `and` five frames below any declaration, so requiring a
// Func here would require a position no producer has; see Region's header.
//
//   - opPos is the OPERATOR's position. It is the position of the branch
//     test.
//   - lhsPos is the left operand's own position, for the Copy that makes the
//     left value the answer when the left decides.
//   - rhsPos is the right operand's own position, which the right block
//     carries so nothing in it has to inherit.
func BeginShortCircuit(r *Region, entry *Block, op LogicOp, opPos, lhsPos, rhsPos Pos,
	dst, lhs Temp) *ShortCircuit {
	if r == nil || entry == nil {
		panic("ir: BeginShortCircuit: needs a block region and an entry block")
	}
	if op != LogicAnd && op != LogicOr {
		panic("ir: BeginShortCircuit: unknown logical operator")
	}
	entry.Append(NewCopy(lhsPos, dst, lhs))
	rhs := r.NewBlock(rhsPos, op.String()+".rhs")
	join := r.NewBlock(opPos, op.String()+".join")
	if op == LogicAnd {
		entry.SetTerm(NewBranch(opPos, lhs, rhs.ID(), join.ID()))
	} else {
		entry.SetTerm(NewBranch(opPos, lhs, join.ID(), rhs.ID()))
	}
	return &ShortCircuit{op: op, dst: dst, rhs: rhs, join: join}
}

// Op is the operator.
func (s *ShortCircuit) Op() LogicOp { return s.op }

// Rhs is the block the right operand lowers into. It is not yet terminated.
func (s *ShortCircuit) Rhs() *Block { return s.rhs }

// Finish copies the right operand's value into the destination and jumps to the
// join block, which it returns. end is the active exit after lowering the
// right operand, which may itself branch. valPos is the operand's position.
func (s *ShortCircuit) Finish(end *Block, valPos Pos, val Temp) *Block {
	if s.done {
		panic("ir: ShortCircuit.Finish called twice")
	}
	if end == nil || end.Term() != nil {
		panic("ir: ShortCircuit.Finish needs an unterminated operand exit")
	}
	s.done = true
	end.Append(NewCopy(valPos, s.dst, val))
	end.SetTerm(NewJump(valPos, s.join.ID()))
	return s.join
}
