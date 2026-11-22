package ir

import "strconv"

// The `call` class: one instruction for a call, whatever the callee turned out
// to be.
//
// # WHAT IS BELOW THE INSTRUCTION SET IS THE CALLING CONVENTION
//
// The iteration push protocol is below the instruction set: a pull protocol
// would emit the same four instructions. The analogous answer here is larger,
// and it is the reason this class is 65 builder sites and one node.
//
// A call instruction is `dst = call callee(a0…an)`. EVERYTHING THAT MAKES THAT
// OPERAND VECTOR is below it:
//
//   - NAMED ARGUMENTS. A name is a slot assignment. `f(b: 2, a: 1)` and
//     `f(1, 2)` are the same instruction.
//   - DEFAULTS. A parameter the call omits is discharged into an operand
//     before the call, so a call to a 3-parameter function always has three
//     operands.
//   - EVALUATION ORDER. `internal/irbuild`'s own resolver drains positionals
//     before named ones, so a named argument written first is evaluated last.
//     The order is in the OPERAND SEQUENCE, not in a field.
//   - COERCION. `embeds` widening and erasure into an existential happen at
//     the argument, producing a different temporary.
//   - ARITY, TYPE-ARGUMENT SOLVING, MONOMORPHIZATION, OVERLOAD SELECTION.
//     All of it has answered by the time there is an instruction: the callee
//     is one identity and the operands are one flat vector.
//   - THE FRAME. The runtime frame is the consumer's, and it is not here.
//
// So a language that passed arguments by name, or right to left, or lazily,
// would produce the same instruction, the same shape of answer as push against
// pull in iteration. What is genuinely IN the instruction is the
// callee's IDENTITY, the operand vector, whether the call is in TAIL POSITION,
// and the POSITION.
//
// # THE CALLEE'S IDENTITY IS TABLE KNOWLEDGE; THE REFERENCE TO IT IS IR
// KNOWLEDGE
//
// A direct call names a `*Symbol` interned in the `Table` against the
// producer's own identity token, so two calls to one declaration name ONE
// symbol and two same-named declarations in two modules name two. That is
// `Table.Symbol`'s contract, which `once` cells use too.
//
// A `*Symbol`, deliberately: a Symbol is "exactly enough for an operand that
// only has to be named" (ir.go). SELECTION HAS ALREADY HAPPENED at a call —
// the checker recorded the function the call or the operator reaches, and
// the builder lowers that record — so the call only has to NAME its
// callee.
//
// # FOUR FORMS, ONE OF THEM WALK-ONLY, AND ONE POPULATION COLLAPSED
//
// A reader sizing this class names five populations: direct calls, host calls,
// interface dispatch, type-parameter-qualified dispatch, and calling a value.
// There are FOUR FORMS; host calls collapse into direct ones, as below.
//
//	DIRECT      the callee is a declaration known statically.
//	DISPATCHED  the callee is selected at run time from an interface's method
//	            table, keyed on the type identity carried by ONE OPERAND.
//	INDIRECT    the callee is an operand holding a function value.
//	BOUND       the callee is an interface's function for some type that
//	            satisfies a type parameter's bound, with no operand to select
//	            it. Only an abstract module holds one (below).
//
// A HOST CALL IS NOT A FORM. Whether a callee's body is Nomi or Go is a fact
// about the DECLARATION, not about the call: both are a direct call to a
// declaration known statically, with the same operands. Splitting them
// would put the callee's implementation language in the opcode. What a
// direct call does carry is whether control leaves the engine (Crosses, set
// by NewHostCall).
//
// A CALL THROUGH A TYPE PARAMETER'S BOUND RUNS NOWHERE. `T.from_sql(v)` for a
// bounded `T` names an interface's function, and which implementation runs is
// the type argument's, not an operand's: `from_sql` takes no value of `T`.
// `internal/ir` has no representation for a type argument, so no machine can
// run the call, and a program runs instances of its generic functions instead,
// where `T` is a type and the call is direct (internal/irbuild's
// genericmono.go). What the effect walk needs of a generic function for every
// type its callers may give it is its abstract body (internal/irbuild's
// abstract.go), and there such a call is the BOUND form: "the function
// `callee` of its interface, for some type", which the walk answers as it
// answers a dispatch, by every implementation the program retains. So it is a
// form of its own and not a dispatch on a made-up operand. Only a module
// marked abstract (Module.SetAbstract) holds one: lint refuses it anywhere
// else (RuleBoundCallAbstract), the encoder writes none, the linker defines
// no abstract module, and the VM's compiler compiles one to a failure.
//
// # THE DISPATCH KEY IS AN OPERAND INDEX, AND IT IS NOT ZERO
//
// `interface Tagger { fn tag(label: String, target: self) -> String }` dispatches
// on argument ONE. The analyzer finds an impl's receiver by scanning for the
// `self`-typed parameter, and a dispatcher that hardcoded `args[0]` would agree
// with it only because every stdlib interface happens to declare `self` first.
// So `KeyAt` is a required argument to NewDispatchCall and
// out-of-range is a panic, rather than a field a producer may leave zero.
//
// No method declaration in std/ or tests/ has a non-zero
// receiver position, and `internal/irbuild/testdata/tagger.nomi` exists
// because nothing else in the repository has the shape. The field varies over
// the language and not over the corpus; it is the one thing about this node
// whose variation is fixture-only.
//
// # THE PREDICATE: TAIL POSITION
//
// `Tail` reports that this call is the last thing its function does. The IR
// does not invent it: `analysis/tail_position.go`'s `MarkTailCalls` computes it,
// `ast.Call.IsTailCall` carries it, and `internal/irbuild` reads it.
//
// The three-part test. NOT DERIVABLE FROM THE KIND: the same callee is called
// from both positions. NOT DERIVABLE FROM THE TEXT: a call renders the same
// whether the bit is set or not. VARIES OVER ITS POPULATION: the corpus has
// calls in both positions.
//
// AND IT CANNOT LIVE ON THE CALLEE. `fn a() -> Int { b() }` and
// `fn c() -> Int { b() + 1 }` call one declaration from two positions, so a bit
// on the callee would have to be both. It cannot live on the block either: a
// Nomi tail call is an EXPRESSION in tail position, and whether its value
// reaches a Return is a question about the path after it (ir.TailTransfers),
// not about the block's terminator.
//
// # `tail` IS A SEPARATE CLASS AND STAYS ONE
//
// What `call` takes is the FACT: it records tail position, and how a tail
// call runs is the consumer's (the VM replaces the caller's activation; see
// tail.go). That is the division ir.go's package header draws for a faulting
// Int operation.

// CallSite is whether a call is in tail position.
//
// A TYPED ENUM AND NOT A BOOL, for `NewIterSignalling`'s reason stated the
// other way round. That uses two constructors because a bool parameter is one
// somebody passes the wrong way round; here there are three forms, so two
// constructors each would be six. A named type with no
// usable zero value cannot be passed the wrong way round either — it will not
// compile against any other argument — and `CallSite(0)` is rejected at
// construction, so omitting it is a panic rather than a silent OrdinaryCall.
type CallSite uint8

const (
	// OrdinaryCall is a call whose result is used by something else.
	OrdinaryCall CallSite = iota + 1
	// TailCall is a call in tail position: its result is its function's.
	TailCall
)

func (s CallSite) String() string {
	switch s {
	case OrdinaryCall:
		return "call"
	case TailCall:
		return "tail call"
	}
	return "call?"
}

// CalleeForm is HOW a call's callee is determined. See the file comment for
// the two populations that are not forms.
type CalleeForm uint8

const (
	// CalleeDirect is a callee resolved to one declaration statically.
	CalleeDirect CalleeForm = iota + 1
	// CalleeDispatched is a callee selected at run time from an interface's
	// method table, keyed on the type identity one operand carries.
	CalleeDispatched
	// CalleeIndirect is a callee held in an operand as a function value.
	CalleeIndirect
	// CalleeBound is an interface's function for some type that satisfies a
	// type parameter's bound, with no operand to select the implementation:
	// a call in an abstract body, which only the effect walk reads.
	CalleeBound
)

func (f CalleeForm) String() string {
	switch f {
	case CalleeDirect:
		return "direct"
	case CalleeDispatched:
		return "dispatched"
	case CalleeIndirect:
		return "indirect"
	case CalleeBound:
		return "bound"
	}
	return "callee?"
}

// Call is one call over already-named operands.
type Call struct {
	pos    Pos
	dst    Temp
	form   CalleeForm
	callee *Symbol
	fn     Temp
	keyAt  int
	args   []Temp
	tail   bool
	// crosses records that this call leaves the Nomi engine and enters Go.
	// See NewHostCall.
	crosses bool
}

// NewCall is a call to a statically resolved declaration.
//
// pos is the CALL SITE's own position, because it becomes the caller frame's
// line in a trace.
func NewCall(pos Pos, dst Temp, site CallSite, callee *Symbol, args ...Temp) *Call {
	if callee == nil {
		panic("ir: NewCall: a direct call has no callee")
	}
	return newCall(pos, dst, site, CalleeDirect, callee, NoTemp, -1, args)
}

// NewHostCall is a direct call whose callee's body is Go: a std `host fn`
// or primitive, or a project's `host fn`, which a host table an embedding
// Go program supplies answers.
//
// THE MARKER IS ONE FIELD RATHER THAN A MECHANISM. The VM reads it when it
// compiles the calling function: a marked call becomes a host call bound to
// the adapter or intrinsic its callee's name lands on
// (internal/vm/hostcall.go), and it is never run as a tail transfer
// (internal/vm/tail.go).
//
// IT IS NOT A `CalleeForm`, AND THE FILE HEADER'S REFUSAL STANDS UNCHANGED.
// That refusal is about how a callee is DETERMINED — "A HOST CALL IS NOT A
// FORM. Whether a callee's body is Nomi or Go is a fact about the
// DECLARATION, not about the call" — and it is right: both are a direct call
// to a declaration known statically, with the same operands. This
// field answers a different question, and it is a question about the CALL
// rather than about the callee's implementation language: does control leave
// the engine executing this function. A `*Symbol` is "a name plus an
// identity and NOTHING ELSE" and cannot answer it, and a consumer cannot
// re-derive it from the operand vector.
//
// WHAT IT DOES NOT COVER, stated rather than implied. A DISPATCHED call
// landing on a host impl, and an INDIRECT call through a function value that
// holds one, both answer false: at neither is the callee known when the
// instruction is built, so a producer could only guess. Both are real
// crossings and both are unmarked today. That is the boundary of a marker
// preserved rather than a mechanism built, and closing it needs the callee
// index `table.go`'s Declare comment already names as dependent 2's.
func NewHostCall(pos Pos, dst Temp, site CallSite, callee *Symbol, args ...Temp) *Call {
	if callee == nil {
		panic("ir: NewHostCall: a direct call has no callee")
	}
	c := newCall(pos, dst, site, CalleeDirect, callee, NoTemp, -1, args)
	c.crosses = true
	return c
}

// Crosses reports whether this call leaves the Nomi engine and enters Go.
// See NewHostCall for what it does and does not cover.
func (c *Call) Crosses() bool { return c.crosses }

// NewDispatchCall is a call whose implementation is selected at run time from
// interface method `callee`'s table, keyed on the type identity operand keyAt
// carries.
//
// keyAt is required and out-of-range panics. See the file comment: the
// receiver is the first `self`-typed position the SIGNATURE declared and it is
// not always argument zero, and assuming it was once type-checked a program
// and dispatched it wrongly.
func NewDispatchCall(pos Pos, dst Temp, site CallSite, callee *Symbol, keyAt int, args ...Temp) *Call {
	if callee == nil {
		panic("ir: NewDispatchCall: a dispatched call names no interface method")
	}
	if keyAt < 0 || keyAt >= len(args) {
		panic("ir: NewDispatchCall: " + callee.Name() + " dispatches on operand " +
			strconv.Itoa(keyAt) + " of " + strconv.Itoa(len(args)))
	}
	return newCall(pos, dst, site, CalleeDispatched, callee, NoTemp, keyAt, args)
}

// NewBoundCall is a call of interface function `callee` for some type that
// satisfies a type parameter's bound, which no operand selects: what an
// abstract body calls through its type parameter's bound. See the file
// comment for why only an abstract module holds one.
func NewBoundCall(pos Pos, dst Temp, site CallSite, callee *Symbol, args ...Temp) *Call {
	if callee == nil {
		panic("ir: NewBoundCall: a call through a bound names no interface function")
	}
	return newCall(pos, dst, site, CalleeBound, callee, NoTemp, -1, args)
}

// NewIndirectCall is a call through a function value held in fn.
func NewIndirectCall(pos Pos, dst Temp, site CallSite, fn Temp, args ...Temp) *Call {
	if fn == NoTemp {
		panic("ir: NewIndirectCall: the callee operand is absent")
	}
	return newCall(pos, dst, site, CalleeIndirect, nil, fn, -1, args)
}

func newCall(pos Pos, dst Temp, site CallSite, form CalleeForm, callee *Symbol,
	fn Temp, keyAt int, args []Temp) *Call {
	requirePos(pos, "Call")
	switch site {
	case OrdinaryCall, TailCall:
	default:
		panic("ir: Call: the call site is not stated")
	}
	// AN OPERAND IS NEVER ABSENT, which is what separates this from `arith`.
	// `NoTemp` means "there is no such operand" — the second operand of a
	// unary, the value of a Unit `Return` — and a call has no absent argument:
	// a Unit argument is still a value with a temporary. A NoTemp here is a
	// producer that skipped a slot, which would emit a call with a hole.
	for i, a := range args {
		if a == NoTemp {
			panic("ir: Call: operand " + strconv.Itoa(i) + " is absent")
		}
	}
	return &Call{
		pos: pos, dst: dst, form: form, callee: callee, fn: fn, keyAt: keyAt,
		args: append([]Temp(nil), args...), tail: site == TailCall,
	}
}

// Form is how this call's callee is determined.
func (c *Call) Form() CalleeForm { return c.form }

// Callee is the declaration a direct call names, or the interface method a
// dispatched or bound call selects an implementation of. It is nil for an
// indirect call.
func (c *Call) Callee() *Symbol { return c.callee }

// Fn is the operand holding an indirect call's callee, or NoTemp.
func (c *Call) Fn() Temp { return c.fn }

// KeyAt is the operand whose type identity selects a dispatched call's
// implementation. It is -1 for every other form.
func (c *Call) KeyAt() int { return c.keyAt }

// Tail reports whether this call is in tail position. See the file comment for
// why the bit is here and not on the callee or on the block.
func (c *Call) Tail() bool { return c.tail }

// Site is Tail as the typed value a constructor takes.
func (c *Call) Site() CallSite {
	if c.tail {
		return TailCall
	}
	return OrdinaryCall
}

// NumArgs is the operand count. It is the callee's arity: a call whose source
// omitted a defaulted parameter has an operand for it.
func (c *Call) NumArgs() int { return len(c.args) }

// Arg is operand n.
func (c *Call) Arg(n int) Temp { return c.args[n] }

func (c *Call) Pos() Pos  { return c.pos }
func (c *Call) Dst() Temp { return c.dst }

func (c *Call) AppendUses(dst []Temp) []Temp {
	if c.form == CalleeIndirect {
		dst = append(dst, c.fn)
	}
	return append(dst, c.args...)
}

func (c *Call) String() string {
	s := c.dst.String() + " = " + c.Site().String() + " "
	switch c.form {
	case CalleeDirect:
		if c.crosses {
			s += "host "
		}
		s += c.callee.Name()
	case CalleeDispatched:
		s += "dispatch " + c.callee.Name() + "@" + strconv.Itoa(c.keyAt)
	case CalleeBound:
		s += "bound " + c.callee.Name()
	case CalleeIndirect:
		s += "value " + c.fn.String()
	}
	s += "("
	for i, a := range c.args {
		if i > 0 {
			s += ", "
		}
		s += a.String()
	}
	return s + ")"
}

func (c *Call) irNode()  {}
func (c *Call) irInstr() {}
