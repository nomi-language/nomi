package ir

// THE OPERAND SHAPE RULE: the whole of what this representation can say about
// an operand's shape.
//
// # WHAT IT IS FOR
//
// `internal/vm/vm.go` carries 43 error constructions and 28 of them are
// conditions no rule in lint.go could express. ELEVEN of the 28 are
// operand-type mismatches — `branches on %T, which is not a Bool`, `an Int
// operation's left operand is %T`, `reads a field off %T, not a struct` — and
// each is checked for the first time when a value arrives in a register. This
// rule moves the derivable part of that to the gate.
//
// # WHAT A SHAPE IS, AND WHY IT IS NOT `*Type`
//
// `Slot` and `Cell` carry an `*Type`, so the obvious reading is that an
// operand-bearing instruction should carry one too. It would not help, and
// the reason is in table.go's own rules.
//
// A `*Type` is a NAME, a `TypeForm` and a table pointer. `TypeForm` has two
// members, concrete and existential, and table.go says why: "The only thing
// the relation below needs to know is whether a value of this type carries
// its own concrete type or has had it erased." Nothing in a `*Type` says
// Int, and the name cannot be read as one — "Comparing types by printed name
// is a defect this repository has already had: two same-named `Error`
// declarations compared equal and produced the uninterpretable diagnostic
// `expected Error, got Error`. Name is for display."
//
// The eleven conditions are not type questions. They are SHAPE questions:
// is this a Bool, is this a struct, is this a distinct. A `ValShape` is that
// and nothing more.
//
// Most of the shape is derived from fields every node already carries, in
// the same position as `Arith.Shape()`, `Block.CanFault()` and
// `Assert.Refuted()`. Two facts cannot be derived and are stored:
// `ir.Param.Shape` and `ir.Proj.Shape`.
//
// # What the rule answers, and the two facts it is stored in
//
// It reports a derived contradiction and says nothing where the derivation
// cannot answer. Without stored facts, answers come off an `ir.Const`, an
// `ir.Arith`, an `ir.Make` or a Copy/Bind chain. Two absences remain:
//
//	`ir.Param.Shape`  A parameter's temporary is written by nothing, so no
//	                  derivation over a body can say what arrives. This covers
//	                  a `*Ref` at `RefLocal` resolving to a parameter and a
//	                  parameter read directly.
//	`ir.Proj.Shape`   A projection's RESULT. Deriving it from the subject's own
//	                  `ir.Make` plus the selector would not work, because the
//	                  subject is almost always a parameter or another
//	                  projection and the function constructs nothing to read
//	                  the component off.
//
// So the rule inspects every position its conditions occupy, with no
// contradictions in production, and its failing side is a plant, which is
// `RuleLocalDeclared`'s footing exactly. An `*Type` on `Param` would not have
// answered these positions, for the same reason `Slot`'s type does not.
//
// In the retained population every `RefLocal` names a parameter and none
// names a `Bind`. `Bind` is built and executed and nothing reads one by NAME,
// because a body's read of a bound name is the Bind's destination temporary.
// So `RuleLocalDeclared`'s declaration set, "a parameter or one of its own
// Bindings", has no production member in its second half.
//
// # Why this rule is not refused for a zero population
//
// lint.go refuses a rule for an absent property, because a rule that cannot
// fire is worse than no lint. This rule's property is present at every
// position and correct at all of them, which is the same shape
// `RuleLocalDeclared` records for itself: "the passing side is production and
// the failing side is a plant."
//
// It is the only fence for one wrong answer: `irArithKind` answering
// `FloatArith()` where the kind is Int wraps where Nomi traps, a wrong answer
// with no error.
//
// The overflow DISCIPLINE is not a shape. `IntArith(OverflowWraps)` where the
// source wrote a checked `+` is DomainInt over two Ints, every operand type
// agrees, and MaxInt64 + 1 answers MinInt64. This rule cannot be extended to
// catch that, because the discipline is not a property of any operand.

import "strconv"

// ValShape is the coarse value shape an operand position requires.
//
// DERIVED, NEVER STORED, which is the property that makes this rule cost no
// construction site. It is also why the set is coarse: it holds exactly the
// distinctions the eleven VM conditions turn on, and adding a member would
// mean some node newly answers one, not that a producer newly states one.
type ValShape uint8

const (
	// ValUnknown is "the graph does not say", and it is the answer at 185
	// of the 331 positions in the retained population. The rule is silent
	// here rather than guessing.
	ValUnknown ValShape = iota
	ValUnit
	ValBool
	ValInt
	ValFloat
	ValDecimal
	ValString
	ValStruct
	ValTuple
	ValVariant
	ValDistinct
	ValContainer
	ValFunc
)

func (s ValShape) String() string {
	switch s {
	case ValUnit:
		return "Unit"
	case ValBool:
		return "Bool"
	case ValInt:
		return "Int"
	case ValFloat:
		return "Float"
	case ValDecimal:
		return "Decimal"
	case ValString:
		return "String"
	case ValStruct:
		return "a struct"
	case ValTuple:
		return "a tuple"
	case ValVariant:
		return "a variant"
	case ValDistinct:
		return "a distinct type"
	case ValContainer:
		return "a container"
	case ValFunc:
		return "a function value"
	}
	return "unknown"
}

// shapeOfTemp is the shape of the value t holds, derived from every
// instruction that writes it, and from the parameter declaration if t is one.
//
// EVERY WRITER AND NOT `Func.Def`, which answers the FIRST. This package's
// header says the graph is NOT SSA — "a temporary may be assigned by more
// than one instruction, because that is what the two arms of a branch writing
// one destination requires" — so a derivation from the first writer alone
// would be a claim about one arm. Two writers that disagree answer unknown,
// which is what keeps a legal diamond from being reported.
//
// A PARAMETER'S DECLARATION IS ONE MORE WRITER, and it is merged by the same
// disagreement rule rather than short-circuited. Nothing in a body writes a
// parameter's temporary today, so the merge is a one-member set in every graph
// the producer builds — but a `Copy` into a parameter's temporary is a legal
// graph (`irtail.go`'s self-recursive hop rebinds parameters), and treating
// the declaration as authoritative there would describe the entry and report
// the hop.
func shapeOfTemp(f *Func, t Temp, depth int) ValShape {
	return newShapes(f).ofTemp(t, depth)
}

// shapes answers shapeOfTemp and shapeWritten over one function, with its
// instructions indexed by destination. Finding a temporary's writers by
// scanning every instruction made lintOperandShapes quadratic in the size
// of a function: a 20000-term string concatenation spent most of half a
// minute here.
type shapes struct {
	f       *Func
	writers map[Temp][]Instr
	// visited counts the instructions read to build writers.
	visited int
}

func newShapes(f *Func) *shapes { return &shapes{f: f} }

func (s *shapes) writersOf(t Temp) []Instr {
	if s.writers == nil {
		s.writers = map[Temp][]Instr{}
		for _, b := range s.f.Blocks() {
			for _, in := range b.Instrs() {
				s.visited++
				if d := in.Dst(); d != NoTemp {
					s.writers[d] = append(s.writers[d], in)
				}
			}
		}
	}
	return s.writers[t]
}

func (s *shapes) ofTemp(t Temp, depth int) ValShape {
	f := s.f
	if t == NoTemp || depth > 16 {
		return ValUnknown
	}
	out := ValUnknown
	for _, p := range f.Params() {
		if p.Temp != t {
			continue
		}
		if p.Shape == ValUnknown {
			// The producer declined to state one. ir.Param.Shape makes that an
			// answer rather than an absence.
			return ValUnknown
		}
		out = p.Shape
	}
	for _, in := range s.writersOf(t) {
		w := s.written(in, depth)
		if w == ValUnknown {
			return ValUnknown
		}
		if out != ValUnknown && out != w {
			// Two arms writing two shapes into one destination, or a
			// writer disagreeing with the parameter's declared shape. The
			// rule declines rather than picking one.
			return ValUnknown
		}
		out = w
	}
	return out
}

// shapeWritten is the shape one instruction's destination holds.
func shapeWritten(f *Func, in Instr, depth int) ValShape {
	return newShapes(f).written(in, depth)
}

func (s *shapes) written(in Instr, depth int) ValShape {
	f := s.f
	switch n := in.(type) {
	case *Not:
		return ValBool
	case *Compare:
		// A comparison's answer is a Bool whatever its operands are, which is
		// why the node states its OPERANDS' shape and not its destination's.
		return ValBool
	case *Const:
		switch n.Kind() {
		case ConstUnit:
			return ValUnit
		case ConstBool:
			return ValBool
		case ConstInt:
			return ValInt
		case ConstFloat:
			return ValFloat
		case ConstDecimal:
			return ValDecimal
		case ConstString:
			return ValString
		case ConstMarker:
			return ValDistinct
		case ConstEmptyList, ConstEmptySet, ConstEmptyVector:
			return ValContainer
		}
		return ValUnknown
	case *Arith:
		switch n.Domain() {
		case DomainInt:
			return ValInt
		case DomainFloat:
			return ValFloat
		case DomainDecimal:
			return ValDecimal
		}
		return ValUnknown
	case *Concat:
		return ValString
	case *Render:
		// Every RenderKind answers text. render.go's disagreements are
		// about WHICH text, not about the shape.
		return ValString
	case *Match:
		return ValBool
	case *Make:
		switch n.Kind() {
		case MakeStruct, MakeRecord, MakeUpdate:
			return ValStruct
		case MakeVariant:
			return ValVariant
		case MakeDistinct:
			return ValDistinct
		case MakeTuple:
			return ValTuple
		case MakeList, MakeSet, MakeVector, MakeMap, MakeRange:
			return ValContainer
		}
		return ValUnknown
	case *Proj:
		// The RESULT of a projection, which is the one fact about a
		// read that no derivation over this graph can supply: the component's
		// type is nowhere in the representation and the subject is almost
		// always a parameter or another projection, so there is no `ir.Make`
		// in the function to read it off. See `Proj.Shape`.
		//
		// NO ARM FOR THE KIND, deliberately. Only `ProjSuffix`'s result is a
		// function of the kind — a List's suffix is a List — and that one is
		// checked at construction instead, where a producer bug is a panic
		// rather than a silent second opinion about the same node.
		return n.Shape()
	case *Copy:
		return s.ofTemp(n.Src(), depth+1)
	case *Bind:
		return s.ofTemp(n.Src(), depth+1)
	case *FuncValue:
		return ValFunc
	case *Ref:
		if n.Kind() == RefFunc {
			return ValFunc
		}
		if n.Kind() == RefLocal {
			// A parameter, resolved by declaration identity: the shape the
			// declaration records, with nothing inferred.
			//
			// The `Bind` half is deliberately absent. The producer resolves a
			// bound name to the Bind's destination temporary at lowering time,
			// so no `RefLocal` is emitted for one, and an arm for it would
			// have no member. See this file's header.
			for _, p := range f.Params() {
				if p.Sym == n.Sym() {
					return p.Shape
				}
			}
			return ValUnknown
		}
		// A cell's and an app field's *Symbol carry no type.
		return ValUnknown
	}
	return ValUnknown
}

// lintOperandShapes applies RuleOperandShape.
//
// Ten positions: every operand-type check the VM makes except the `io.print`
// operand. That one is not a position in this graph at all:
// `vm.go` checks it inside a HOST FUNCTION, reached through the machine's own
// `m.hosts` binding, and which Go function a `Crosses()` call lands on is the
// consumer's answer rather than a fact the representation states.
func lintOperandShapes(f *Func, vs *[]Violation) {
	sh := newShapes(f)
	report := func(pos Pos, what, position string, got, want ValShape) {
		if got == ValUnknown || got == want {
			return
		}
		*vs = append(*vs, Violation{Rule: RuleOperandShape, Pos: pos, What: what,
			Why: position + " is " + got.String() + ", not " + want.String()})
	}
	for _, b := range f.Blocks() {
		for i, in := range b.Instrs() {
			what := b.ID().String() + " instr " + strconv.Itoa(i)
			switch n := in.(type) {
			case *Arith:
				var want ValShape
				switch n.Domain() {
				case DomainInt:
					want = ValInt
				case DomainFloat:
					want = ValFloat
				case DomainDecimal:
					want = ValDecimal
				default:
					continue
				}
				// `want.String()` rather than `Domain.String()`, which is
				// lower case: the VM's own text for this condition reads "an
				// Int operation's left operand is %T", and two checks
				// naming one condition two ways would drift.
				side := "the left operand of this " + want.String() + " operation"
				report(n.Pos(), what, side, sh.ofTemp(n.Lhs(), 0), want)
				if n.Rhs() != NoTemp {
					side = "the right operand of this " + want.String() + " operation"
					report(n.Pos(), what, side, sh.ofTemp(n.Rhs(), 0), want)
				}
			case *Not:
				report(n.Pos(), what, "Boolean negation operand", sh.ofTemp(n.Val(), 0), ValBool)
			case *Compare:
				// BOTH OPERANDS AGAINST THE NODE'S OWN DECLARED SHAPE, which
				// is the one rule a comparison has that the constructor
				// cannot check: `NewCompare` sees two temporaries and the
				// shape they are claimed to hold, and whether the graph
				// AGREES is a fact about every instruction that writes them.
				// A mixed-type comparison is a refusal in the producer
				// (`mixed-type operator`), so a graph holding one is a
				// producer bug rather than a program.
				side := "the left operand of this " + n.Op().Symbol() + " comparison"
				report(n.Pos(), what, side, sh.ofTemp(n.Lhs(), 0), n.Shape())
				if !n.Ranked() {
					side = "the right operand of this " + n.Op().Symbol() + " comparison"
					report(n.Pos(), what, side, sh.ofTemp(n.Rhs(), 0), n.Shape())
				}
			case *Concat:
				for p := range n.NumParts() {
					report(n.Pos(), what, "concat part "+strconv.Itoa(p),
						sh.ofTemp(n.Part(p), 0), ValString)
				}
			case *Proj:
				want := ValUnknown
				switch n.Kind() {
				case ProjField, ProjRecordField:
					want = ValStruct
				case ProjSlot:
					want = ValTuple
				case ProjPayload, ProjEnumField:
					want = ValVariant
				case ProjInner:
					want = ValDistinct
				case ProjElem, ProjSuffix:
					want = ValContainer
				}
				if want == ValUnknown {
					continue
				}
				report(n.Pos(), what, "the subject of this "+n.Kind().String()+" projection",
					sh.ofTemp(n.Subject(), 0), want)
			}
		}
		if br, isBranch := b.Term().(*Branch); isBranch {
			report(br.Pos(), b.ID().String()+" terminator", "the branched condition",
				sh.ofTemp(br.Cond(), 0), ValBool)
		}
	}
}
