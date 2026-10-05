// Package ir is the representation between the checked Nomi AST, as
// `internal/irbuild` builds it, and its consumer, the VM (`internal/vm`), which
// compiles each Func to bytecode.
//
// Three decisions are load-bearing in this file and the three beside it:
//
//  1. A checked `Int` operation records that overflow is a fault at this
//     point. It does not record how the fault is delivered: delivery is a
//     consumer property, and a `trap bool` on the node would make every
//     producer know which consumer it was serving. See arith.go.
//
//  2. Position is mandatory and per node. Most of the builder's lowering
//     sites inherit the ambient `gen.nomiLine` cursor, and a cursor has to be
//     re-established by hand after every child lowering, and one missed
//     restore misplaces every node after it (position_witness_test.go).
//     A per-node position needs no remembering. Nothing in
//     this package inherits a position: every constructor takes one as its
//     first parameter, every field holding one is unexported, and
//     Block.Append and Block.SetTerm reject a node whose position is invalid.
//
//  3. An assertion's rendered source text is a field on the node:
//     `Assert.Text`, `Record.Text` and `Try.Text`. `try` carries one because
//     whoever reports a non-local exit prints where it left from. The
//     renderer is one function (`format.RenderNode`); which node to render is
//     the producer's choice, and it is recorded here. See assert.go and
//     try.go.
//
// Shape: linear with explicit temporaries, basic blocks and terminators, not a
// tree. The builder has already linearized the AST by hand in three mechanisms
// whose only job is turning a tree into named temporaries (`gen.slot` /
// `gen.fixSlot`, `expr.pure`, `gen.operand` / `gen.hold`), so this is the shape
// it already has rather than a new one. Blocks and terminators are here for one
// reason, and it is not speculative generality: Go's `&&` and `||`
// short-circuit only over expressions, and a Nomi right operand can need
// statements, so short-circuit `and` / `or` cannot be an expression tree. See
// logic.go.
//
// Not SSA: a temporary may be assigned by more than one instruction, because
// that is what the two arms of a branch writing one destination requires and
// what `gen.slot` / `gen.fixSlot` already is. Copy exists for exactly that.
//
// Scope: sixteen of the builder's 22 operation classes are modelled here:
// const, ref, arith, logic, make, proj, match, destructure, assert, try,
// closure, iter, call, branch, jump and render, plus the type and
// declaration table (table.go), which is not an operation class but the thing
// an operation's operands and callee are named in.
//
// Five classes are partly routed, and each says which part. `iter` covers
// every operation over a sequence; `Iter.loop`, the three widened callback
// frames and the sort family are refused by name. `call` covers the call in
// all three of its callee forms and leaves the partial application, the
// operand holds, four non-call lowerings inside routed owners, and the
// dictionary dispatch; see call.go and internal/irbuild/ircall.go. `jump`
// covers the jump sites whose target block exists and leaves those whose
// target belongs to a construct nothing lowers; see
// internal/irbuild/irjump.go. `tail` and `interp`: below.
//
// Five classes contribute no instruction, for two reasons.
// `logic` and `branch` are pure control flow whose content is the
// graph's shape (`logic` is a Branch, a block and a Copy; `branch` is a chain
// of Branches and Jumps over one join), so `logic` has a builder instead,
// BeginShortCircuit, and internal/irbuild builds a branch's blocks directly.
// `bind`, `tail` and `interp` contribute none because their operations
// exist under another class's name: a Copy and a Bind for `bind`, a
// Copy and a Jump over a Region for `tail`, and a Const plus a Render for
// `interp`. See logic.go, and internal/irbuild/irbind.go, irtail.go
// and irinterp.go.
//
// `tail` is also taken as a fact: the builder's tail driver is `closure`,
// `branch`, `bind` and `jump` with no call in it, while tail position is a
// front-end fact the consumer reads, so `ir.Call.Tail()` records the fact.
// What the class carries beyond that fact is a cycle, a whole-module
// strongly connected component, which the VM's tail transfer never asks for,
// so it stays in the builder.
//
// Three classes have no node, deliberately rather than by omission: `box`,
// `effect` and `conc`. `box`'s three owners are three unrelated operations:
// a Go pointer for a self-reaching type, a `gen.coerce` arm with no position,
// and a type retag that emits nothing; a consumer that needs to skip a
// representation change reads `Table`'s own `TypeForm`. `effect` is a scope
// stack with two blockers: nothing in this package writes a declaration, and
// the general scope-region form (a region whose exit block holds the
// restores) is owed by the retained population rather than by a missing
// type. `conc`'s blocker is narrower: fault.go puts the exceptional edge on
// the block, but no terminator resumes an unwind, so a handler can run and
// cannot hand the fault back.
// See internal/irbuild/irbox.go, ireffect.go and irconc.go.
//
// `Symbol` below is what the table replaces where a signature is needed; it
// remains the right shape wherever a name plus an identity is all an operand
// has, which includes a pattern's own bindings — see destructure.go.
package ir

import (
	"fmt"
	"strconv"
)

// --- positions -------------------------------------------------------------

// Pos is the Nomi source EXTENT of one IR node: where its text starts and
// where it ends.
//
// It is a value with unexported fields, so the only way to build a usable one
// is At, AtSynthesized or Spanning, and the only way to name it in a composite
// literal from outside this package is the empty `ir.Pos{}` — which IsValid
// reports false for and which every constructor here panics on. That is the
// whole enforcement story and its two halves are unequal, so they are stated
// separately in position_test.go: OMITTING a position is a compile error,
// FABRICATING an invalid one is a panic at construction.
//
// The end exists for attribution. A multi-line construct has a start and an
// extent, and a consumer needs both: it blames the START, and it resolves a
// breakpoint on an interior line through the EXTENT. For example,
// `escape_program` in `15-app-and-defer/deadline_floor/deadline_floor_test.nomi` is a
// triple-quoted literal whose statement starts on line 25 while its text
// reaches line 43, `^context = ${attempt}`, which is inside the literal and
// not a statement at all. A point position cannot distinguish "the construct
// starts here" from "the construct's text reaches here".
//
// The end is the extent of the construct's NODES, which is what the AST can
// supply. `ast` records no end position for any node: a triple-quoted literal
// carries its opening line and its decoded value, so its closing `"""` line
// is not derivable without re-reading source. In the example above the node
// extent is 25..43 and the text extent is 25..61, because the literal's last
// interpolation hole sits eighteen lines above its closing delimiter. So an
// end here is a LOWER BOUND on the text, exact wherever the construct's last
// line holds a node; the text extent would need an end position from the
// parser. A consumer reading `Covers` must know which of the two it gets.
//
// A SEPARATE `Span` TYPE WAS CONSIDERED AND REFUSED, and so was an end
// carrying its own FILE. A construct's text is in one file — that is what a
// construct is — so an end with its own path is a representation able to
// express nonsense, and `At`'s empty-file gate is the precedent for closing
// such a hole at the constructor instead of reporting it: the end here has no
// file field, so "an end in a different file" is UNCONSTRUCTABLE rather than
// lintable. See TestPos_TheEndCannotNameADifferentFile, which is the positive
// for that, and `RulePositionSpan` for the two questions that remain askable.
// A distinct `Span` type would have meant a second parameter at all 40-odd
// constructors in this package for a field nine tenths of them set to their
// own start.
type Pos struct {
	file string
	line int32
	col  int32
	// endLine and endCol are the last position the construct's own nodes
	// reach. For a POINT — every position `At` builds, which is every
	// position in this representation except an interpolation's — they are
	// the start, so "the end is present" is a total property and the lint
	// rule that asks it has every position as its population rather than
	// the handful that span.
	endLine int32
	endCol  int32
	// synth marks a position the front end derived rather than read: `derive`
	// synthesis allocates from a band around 2^30, which is not a real source
	// line. The builder's `gen.at` IGNORES such a line and leaves the cursor
	// where it was, which is a second inheritance
	// path into the same defect class as the `Iter.loop` exit. A synthesized Pos
	// carries the ORIGIN it was derived from, so a consumer can blame the
	// `derive` the programmer wrote instead of inheriting.
	synth bool
}

// At is a position the front end read from source, for a construct whose text
// is on ONE line: its end is its start. line and col are 1-based.
//
// It panics on a line below 1 and on an EMPTY FILE. A node with no position is
// the defect this package exists to remove, so producing one is a producer bug
// and not an input to validate.
//
// The file gate matters because `IsValid()` is only `line >= 1`: without it,
// `At("", 7, 1)` would produce a position no consumer can open a file at, a
// diagnostic that names a line in nothing. `Lint`'s RulePositionValid would
// report it, but Lint runs only over what a producer retains. Rejecting it
// here makes the defect unconstructable rather than reportable, and the rule
// keeps the narrower population every gate in this package leaves it: a Pos
// built by a composite literal inside this package.
//
// Every builder `gen` that lowers anything takes its path from its module
// (`native.go`'s constructor and `newStdGen`). The one `&gen{}` literal in
// the builder with no path, `stdinstance.go`'s per-package text assembler,
// lowers nothing and reaches no position.
func At(file string, line, col int) Pos {
	requireAt(file, line, col)
	return Pos{file: file, line: int32(line), col: int32(col),
		endLine: int32(line), endCol: int32(col)}
}

// Spanning is a position whose construct's text reaches past its start:
// `endLine`/`endCol` are the last position the construct's own nodes occupy.
//
// It panics on everything `At` panics on, plus an END BEFORE THE START. That
// is a producer bug of the same species as a missing position — there is no
// input a user can supply that produces a construct ending before it begins —
// so it is rejected at the call rather than reported by Lint. `Lint` keeps the
// same narrower population every gate here leaves it: a Pos built by a
// composite literal INSIDE this package.
//
// ITS ONE PRODUCER TODAY IS AN INTERPOLATED STRING, and that is stated so
// nobody prices work off the constructor's generality. `internal/irbuild`'s
// `irNodeSpan` answers a span for `*ast.StringInterp` and `*ast.TaggedString`
// — whose `${...}` holes are nodes with their own lines — and a POINT for
// every other node in the language. The second multi-line construct Nomi has,
// a pipe chain written one stage per line, has no construction site for a span
// because the desugaring hands each STAGE's own call node to the call path and
// never positions the chain itself.
func Spanning(file string, line, col, endLine, endCol int) Pos {
	requireAt(file, line, col)
	if endLine < line || (endLine == line && endCol < col) {
		panic(fmt.Sprintf("ir: Spanning(%q, %d, %d, %d, %d): a construct cannot end "+
			"before it starts", file, line, col, endLine, endCol))
	}
	return Pos{file: file, line: int32(line), col: int32(col),
		endLine: int32(endLine), endCol: int32(endCol)}
}

func requireAt(file string, line, col int) {
	if line < 1 {
		panic(fmt.Sprintf("ir: At(%q, %d, %d): a position needs a 1-based line", file, line, col))
	}
	if file == "" {
		panic(fmt.Sprintf("ir: At(%q, %d, %d): a position needs the file it names a line in",
			file, line, col))
	}
}

// AtSynthesized is the position of derived code, blamed on the source it was
// derived FROM. originLine and originCol are the position of the construct
// that caused the synthesis — the `derive` clause, or the type declaration a
// synthesized impl belongs to.
//
// A POINT, deliberately. What a synthesized position names is the origin a
// consumer must blame, and an origin has no extent: the derived code's own
// text does not exist in any file.
func AtSynthesized(file string, originLine, originCol int) Pos {
	p := At(file, originLine, originCol)
	p.synth = true
	return p
}

// File is the .nomi path this position names.
func (p Pos) File() string { return p.file }

// Line is the 1-based Nomi line the construct STARTS on. This is what a
// consumer blames: a `//line` directive, a diagnostic, a breakpoint's
// reported location.
func (p Pos) Line() int { return int(p.line) }

// Col is the 1-based Nomi column the construct starts at.
func (p Pos) Col() int { return int(p.col) }

// EndLine is the last line the construct's own nodes reach. Equal to Line for
// a point, which is every position except an interpolation's.
func (p Pos) EndLine() int { return int(p.endLine) }

// EndCol is the last column the construct's own nodes reach.
func (p Pos) EndCol() int { return int(p.endCol) }

// IsValid reports whether this position names a line. The zero Pos does not.
func (p Pos) IsValid() bool { return p.line >= 1 }

// Spans reports whether this construct's nodes reach past its start line.
func (p Pos) Spans() bool { return p.endLine > p.line }

// Covers reports whether this construct's extent includes the given 1-based
// line.
//
// This is the query a breakpoint resolves through, and it is the reason the
// end is on the representation rather than in the producer. A user asking to
// break on line 30 of `deadline_floor_test.nomi` is asking about a line inside
// a triple-quoted literal: no instruction carries 30, and only the extent
// relates 30 to the instruction that computes the literal. The extent is the
// node extent (see this type's header), so a line past the construct's last
// node is not covered even though its text is.
func (p Pos) Covers(line int) bool {
	return p.IsValid() && line >= int(p.line) && line <= int(p.endLine)
}

// Synthesized reports whether this position is the ORIGIN of derived code
// rather than a position read from source. A consumer emitting debug
// information blames the origin; it must not fall back to an ambient cursor.
func (p Pos) Synthesized() bool { return p.synth }

func (p Pos) String() string {
	if !p.IsValid() {
		return "<no position>"
	}
	s := p.file + ":" + strconv.Itoa(int(p.line)) + ":" + strconv.Itoa(int(p.col))
	// THE END IS PRINTED ONLY WHEN IT SAYS SOMETHING. Every point would
	// otherwise render `f.nomi:7:3-7:3`, which is 1500 positions of noise in
	// a dump for the three that span.
	if p.Spans() {
		s += "-" + strconv.Itoa(int(p.endLine)) + ":" + strconv.Itoa(int(p.endCol))
	}
	if p.synth {
		s += " (synthesized from)"
	}
	return s
}

// --- names ------------------------------------------------------------------

// Symbol is a resolved declaration: a binding, a `once` cell, a function, an
// impl method, a type.
//
// ITS IDENTITY IS THE POINTER, NEVER THE TEXT. Two declarations with the same
// printed name are two symbols. That is not a stylistic preference: comparing
// declarations by printed name is a defect this repository has already had, in
// which `calendar.Error` and `json.Error` compared equal and produced the
// uninterpretable diagnostic `expected Error, got Error`.
//
// A Symbol is a name plus an identity and NOTHING ELSE, which is exactly
// enough for an operand that only has to be named — a local read, a `once`
// cell, an empty container's element type. It is NOT enough wherever a rule
// has to choose between two declarations: that needs a signature, which is
// `Decl` in table.go. The `Add.add` shape is where the boundary shows.
type Symbol struct {
	name string
}

// NewSymbol mints a fresh declaration identity. Two calls with the same name
// return two symbols that are not equal.
func NewSymbol(name string) *Symbol { return &Symbol{name: name} }

// Name is the symbol's printed name. It is not its identity.
func (s *Symbol) Name() string {
	if s == nil {
		return ""
	}
	return s.name
}

func (s *Symbol) String() string { return s.Name() }

// --- temporaries ------------------------------------------------------------

// Temp names a value inside one Func. Every operand is a Temp: a literal is
// its own instruction writing one, so there is no operand shape other than
// "read a temporary".
type Temp uint32

// NoTemp is the absent temporary: the second operand of a unary operation, and
// the value of a Return from a Unit function.
const NoTemp Temp = 0

func (t Temp) String() string {
	if t == NoTemp {
		return "_"
	}
	return "t" + strconv.FormatUint(uint64(t), 10)
}

// BlockID names a basic block inside one Func.
type BlockID uint32

func (b BlockID) String() string { return "b" + strconv.FormatUint(uint64(b), 10) }

// --- the node interfaces ----------------------------------------------------

// Node is anything in the IR that has a position. Every node does: position
// is mandatory and per node (point 2 of the package header).
//
// The interface is sealed by an unexported method, so the set of instructions
// and terminators is closed and a consumer's switch over it can be exhaustive.
type Node interface {
	// Pos is the Nomi position of this node. Never inherited, never absent.
	Pos() Pos
	// String renders the node for diagnostics and tests.
	String() string

	irNode()
}

// Instr is one operation. It reads temporaries and writes at most one.
type Instr interface {
	Node
	// Dst is the temporary this instruction writes, or NoTemp.
	Dst() Temp
	// AppendUses appends the temporaries this instruction reads. It appends
	// rather than allocating so a walk over a whole function can reuse one
	// slice.
	AppendUses(dst []Temp) []Temp

	irInstr()
}

// Term is a block's terminator.
type Term interface {
	Node
	// AppendSuccessors appends the blocks control can reach from here.
	AppendSuccessors(dst []BlockID) []BlockID
	// AppendUses appends the temporaries this terminator reads.
	AppendUses(dst []Temp) []Temp

	irTerm()
}

// --- blocks and functions ---------------------------------------------------

// Block is a basic block: a straight run of instructions and one terminator.
//
// A block carries its OWN position. That is what makes the `jump` class's
// requirement — "the jump's own position and its target's" — structural rather
// than a convention: a Jump has a position and so does the block it names, so
// neither has to be inherited from an ambient position cursor.
type Block struct {
	pos    Pos
	id     BlockID
	label  string
	instrs []Instr
	term   Term
	// owner is the Func this block belongs to, or nil for a block in a bare
	// Region. It exists so Append can record WHICH INSTRUCTION DEFINES A
	// TEMPORARY, which is what makes a Temp a value rather than a name: see
	// Func.Def. A Region has no temporary namespace — NewTemp is on the Func
	// and always was — so there is nothing for a region's block to record
	// into, and nil is that fact rather than a missing wire.
	owner *Func
	// fault is the block control transfers to when an instruction here
	// faults, and hasFault says whether there is one. Jump, Branch and Return
	// are the whole terminator set and none of them is an exceptional edge,
	// while Arith.Faults says twelve
	// operator-and-domain combinations can fault at their own position. The
	// VM must know where control goes. See fault.go, which holds the accessors
	// and the argument for putting the edge on the BLOCK.
	fault    BlockID
	faultPos Pos
	hasFault bool
}

// ID is this block's identity within its Func.
func (b *Block) ID() BlockID { return b.id }

// Label is a human name for diagnostics. It is not an identity.
func (b *Block) Label() string { return b.label }

// Pos is the position of the construct this block begins.
func (b *Block) Pos() Pos { return b.pos }

// Instrs are this block's instructions in order.
func (b *Block) Instrs() []Instr { return b.instrs }

// Term is this block's terminator, or nil while it is still being built.
func (b *Block) Term() Term { return b.term }

// Append adds one instruction.
//
// It rejects a node whose position is invalid. That closes the one hole the
// type system leaves open: `ir.Const{}` is a legal composite literal outside
// this package even with every field unexported, so a zero-value node can be
// built. It cannot be put into a function.
func (b *Block) Append(in Instr) {
	if in == nil {
		panic("ir: Block.Append(nil)")
	}
	if !in.Pos().IsValid() {
		panic("ir: Block.Append: " + in.String() + " has no position; every IR node carries one")
	}
	if b.term != nil {
		panic("ir: Block.Append after " + b.term.String() + " terminated " + b.id.String())
	}
	b.instrs = append(b.instrs, in)
	if b.owner != nil {
		b.owner.noteDef(in)
	}
}

// SetTerm terminates the block.
func (b *Block) SetTerm(t Term) {
	if t == nil {
		panic("ir: Block.SetTerm(nil)")
	}
	if !t.Pos().IsValid() {
		panic("ir: Block.SetTerm: " + t.String() + " has no position; every IR node carries one")
	}
	if b.term != nil {
		panic("ir: Block.SetTerm: " + b.id.String() + " is already terminated by " + b.term.String())
	}
	b.term = t
}

// Region is a block namespace: a list of basic blocks whose BlockIDs are
// unique within it and whose first element is the entry.
//
// SEPARATE FROM Func BECAUSE THE TWO COME APART. The `logic` class has a
// control-flow graph and no declaration position to open a Func at.
// `gen.logical` is reached from `binary`
// from `expr`, five frames below any declaration, and the enclosing construct
// may be a lambda, a test body, an attached-test prompt or a synthesized
// impl, so a consumer retargeting one CLASS at a time has a short-circuit's
// three blocks and no function to hang them on.
//
// Welding the arena to the declaration would force a fabricated position,
// `ir.At(file, 1, 1)` standing in for a compilation unit, which the
// per-node position rule forbids. It would supply a fact the short-circuit shape never reads:
// BeginShortCircuit uses its arena for NewBlock and for nothing else. A
// Region's position is the position of the construct it covers, which for a
// short-circuit is the operator, and that is a position the producer has.
//
// A Func is a Region plus the two facts that make it a FUNCTION: a name and a
// temporary namespace. Nothing is lost by the split, because a whole-function
// consumer still opens a Func and gets the same blocks.
type Region struct {
	pos    Pos
	label  string
	blocks []*Block
	// owner is the function this region belongs to, or nil for a bare one.
	//
	// Ownership is the REGION's fact rather than the entry point's. If only
	// `Func.NewBlock` set `Block.owner`, a block a function made through its
	// OWN region (as `BeginShortCircuit` does when handed `f.Region`) would
	// have no owner, and the instructions appended to it would never be
	// recorded in `Func.defs`, so lint would report temporaries the block
	// plainly defines as undefined.
	//
	// A bare region answers nil: "a fragment has no def table to fill" is
	// stated by the region it is a fragment of, not by which method the
	// caller reached.
	owner *Func
}

// NewRegion opens a block namespace at the position of the construct it
// covers. It creates no entry block: the caller's first NewBlock is the
// entry, which is what a Func's caller already did.
func NewRegion(pos Pos, label string) *Region {
	requirePos(pos, "NewRegion")
	return &Region{pos: pos, label: label}
}

// Pos is the position of the construct this region covers. For a Func it is
// the function's declaration.
func (r *Region) Pos() Pos { return r.pos }

// Label is a human name for diagnostics. It is not an identity.
func (r *Region) Label() string { return r.label }

// NewBlock appends a block at the position of the construct it begins, owned
// by this region's function when it has one — see Region.owner.
func (r *Region) NewBlock(pos Pos, label string) *Block {
	requirePos(pos, "NewBlock")
	b := &Block{pos: pos, id: BlockID(len(r.blocks)), label: label, owner: r.owner}
	r.blocks = append(r.blocks, b)
	return b
}

// Blocks are the region's blocks; index 0 is the entry.
func (r *Region) Blocks() []*Block { return r.blocks }

// Block returns the block with this id, or nil.
func (r *Region) Block(id BlockID) *Block {
	if int(id) >= len(r.blocks) {
		return nil
	}
	return r.blocks[id]
}

// Func is one lowered Nomi function: a Region carrying the declaration's
// position, plus the function's name and its temporary namespace.
type Func struct {
	*Region
	name string
	// sym is the declaration identity a Call names, or nil. See NewFuncFor.
	sym  *Symbol
	next Temp
	// defs is the instruction that first writes each Temp, indexed by the
	// Temp. This is what makes a temporary a value: without it a Temp would be
	// a name whose referent lives outside the representation, the IR could
	// not answer "what computes this operand", and a consumer that retained a
	// function past lowering would hold a graph with dangling operands. A
	// Temp defined here refers to an instruction in this function.
	//
	// FIRST WRITE, not the only one: this IR is deliberately not SSA (see the
	// package header), because two arms of a branch writing one destination
	// is what `gen.slot` / `gen.fixSlot` already is. So Def answers the
	// definition a reader starts from, and Lint's RuleTempDefinedBeforeUse
	// checks the property multiple definitions actually have to satisfy —
	// that every path from the entry to a use passes through one of them.
	defs []Instr
	// params are the function's declared parameters, in order, each with
	// the temporary its value arrives in.
	//
	// This joins the declaration to the frame. A consumer that runs the
	// function has to put the arguments somewhere before the first
	// instruction reads one, and that place is a temporary.
	//
	// A parameter's temporary is defined at entry and by nothing in the
	// body, which Lint's must analysis is told rather than left to infer:
	// see lintTempDefs. Without that, every function with a parameter would
	// report "nothing in this function defines t1" on its first read.
	params []Param
	// types is the stored value type of each temporary, indexed by the Temp:
	// what the producer said the value written there is. See valtype.go and
	// RuleTempTyped.
	types []*ValType
}

// NewFunc starts a function at the position of its declaration.
func NewFunc(pos Pos, name string) *Func {
	requirePos(pos, "NewFunc")
	f := &Func{Region: NewRegion(pos, name), name: name}
	// EVERY BLOCK THIS FUNCTION'S REGION MAKES IS THIS FUNCTION'S, whichever
	// entry point makes it. See Region.owner.
	f.Region.owner = f
	return f
}

// NewFuncFor starts a function at the position of its declaration, naming the
// DECLARATION IDENTITY the callers of this function name.
//
// `NewFunc` takes a name, and a name is not an identity: `Symbol`'s header
// explains why comparing declarations by printed name is a defect. A consumer
// holding an `ir.Call` has the callee's `*Symbol`, and this is what maps it to
// a body without matching `Call.Callee().Name()` against `Func.Name()`.
//
// `NewFunc` remains for the name-only form: a `Func` built by a consumer
// that has no interned declaration for it, which is every test fixture in
// this package and `internal/ir/posplant`. `Sym` answers nil for those, and
// that nil is the fact rather than a missing wire.
func NewFuncFor(pos Pos, sym *Symbol) *Func {
	if sym == nil {
		panic("ir: NewFuncFor: a function with no declaration identity is NewFunc")
	}
	f := NewFunc(pos, sym.Name())
	f.sym = sym
	return f
}

// Sym is the declaration identity callers of this function name, or nil for a
// Func built by NewFunc.
func (f *Func) Sym() *Symbol { return f.sym }

// Name is the Nomi name of the function.
func (f *Func) Name() string { return f.name }

// Param is one declared parameter: its declaration identity, the temporary its
// value arrives in, and the SHAPE of the value that arrives.
//
// A `*Symbol` AND A Temp, and both are needed for different readers. The
// Symbol is what a `Ref` naming this parameter resolves against — the same
// identity `ir.Closure` carries, so a consumer can tie a read to the
// declaration it reads. The Temp is where the value IS, which is what a
// frame needs and what no Symbol can say.
//
// # The shape, one of the stored facts in shape.go
//
// A parameter's temporary is written by nothing (the caller supplies it), so
// `Func.Def` answers nil for it and no derivation over the body can say what
// arrives. Parameters are the largest group of positions `RuleOperandShape`
// could not otherwise answer: a `RefLocal` naming a parameter, or a parameter
// read directly.
//
// It is a `ValShape` and not an `*Type`, which is why this field is here
// rather than beside `Slot`'s and `Cell`'s. An `*Type` is a name plus a
// two-member `TypeForm`, its methods are `Name`/`Form`/`Existential`/
// `String`, and table.go forbids reading the name as an identity, so an
// `*Type` here would answer none of the rule's questions. The rule's
// conditions are shape questions, so a shape is what is recorded.
//
// `ValUnknown` is a legitimate answer. A producer that cannot say (an
// existential parameter, whose concrete shape is erased by construction; a
// bound-free type parameter, which has one body and many instantiations)
// states `ValUnknown`, and the rule is then silent at that position. Stating a shape that
// is WRONG is the failure mode, and it is the one `RuleOperandShape` reports:
// a recorded shape lands at a position whose demand is also stated, so the two
// either agree or contradict.
type Param struct {
	Sym   *Symbol
	Temp  Temp
	Shape ValShape
}

// AddParam declares one parameter with the shape of the value it receives, and
// allocates the temporary that value arrives in, answering that temporary.
//
// The order of calls is the parameter order, which is the operand order of
// every `Call` to this function. `ir.Call`'s header states why that order is
// in the operand SEQUENCE rather than in a field: named arguments, defaults
// and evaluation order have all been discharged by the time there is a call
// instruction.
//
// THE SHAPE IS A REQUIRED ARGUMENT RATHER THAN A SETTER, which is the same
// choice `ir.ArithKind`'s four factories make: a fact that can be forgotten at
// a construction site is a fact some construction site will forget. A producer
// with nothing to say passes `ValUnknown` and says so.
func (f *Func) AddParam(sym *Symbol, shape ValShape) Temp {
	if sym == nil {
		panic("ir: Func.AddParam: a parameter with no declaration identity cannot be read")
	}
	t := f.NewTemp()
	f.params = append(f.params, Param{Sym: sym, Temp: t, Shape: shape})
	return t
}

// Params are the declared parameters in order.
func (f *Func) Params() []Param { return f.params }

// NewTemp allocates a fresh temporary.
//
// ON THE FUNC AND NOT ON THE REGION, because a temporary's scope is the
// function: two regions inside one function must not hand out the same Temp,
// and a Region that is a fragment of a function being lowered by a
// partially-retargeted consumer allocates from that consumer's own namespace.
func (f *Func) NewTemp() Temp {
	f.next++
	return f.next
}

// SetType states the value type of temporary t. A later call replaces an
// earlier one; Lint checks the final answer against every writer.
func (f *Func) SetType(t Temp, ty *ValType) {
	if t == NoTemp {
		panic("ir: Func.SetType: NoTemp holds no value")
	}
	if ty == nil {
		panic("ir: Func.SetType: a nil type states nothing")
	}
	for int(t) >= len(f.types) {
		f.types = append(f.types, nil)
	}
	f.types[t] = ty
}

// TempType is the stored value type of t, or nil when none was stated.
func (f *Func) TempType(t Temp) *ValType {
	if t == NoTemp || int(t) >= len(f.types) {
		return nil
	}
	return f.types[t]
}

// NumTemps is how many temporaries this function has allocated. Temps run
// from 1 to NumTemps; 0 is NoTemp.
func (f *Func) NumTemps() int { return int(f.next) }

// Def is the instruction that first writes t, or nil when nothing in this
// function does.
//
// A nil answer for a temporary an instruction READS is a producer bug and
// Lint reports it. It is not reported here, because a builder legitimately
// asks about a destination it has allocated and not yet appended.
func (f *Func) Def(t Temp) Instr {
	if t == NoTemp || int(t) >= len(f.defs) {
		return nil
	}
	return f.defs[t]
}

// noteDef records the first instruction writing in.Dst().
func (f *Func) noteDef(in Instr) {
	if s, isSlot := in.(*Slot); isSlot {
		// A slot's storage type is its declaration's; see Type.Val.
		if v := s.Type().Val(); v != nil && f.TempType(s.Slot()) == nil {
			f.SetType(s.Slot(), v)
		}
	}
	d := in.Dst()
	if d == NoTemp {
		return
	}
	for int(d) >= len(f.defs) {
		f.defs = append(f.defs, nil)
	}
	if f.defs[d] == nil {
		f.defs[d] = in
	}
	if f.TempType(d) == nil {
		if ty := f.intrinsicTypeOf(in); ty != nil {
			f.SetType(d, ty)
		}
	}
}

// intrinsicTypeOf is the type an instruction fixes for its destination by
// itself, or nil when the producer has to state it. A Copy or a Bind whose
// destination the producer has not declared holds its source's value, and a
// read of a parameter holds the parameter's, so each takes that stored type.
func (f *Func) intrinsicTypeOf(in Instr) *ValType {
	switch n := in.(type) {
	case *Copy:
		return f.TempType(n.Src())
	case *Bind:
		return f.TempType(n.Src())
	case *Ref:
		if n.Kind() == RefLocal {
			// A read of a parameter holds the parameter's value.
			for _, p := range f.params {
				if p.Sym == n.Sym() {
					return f.TempType(p.Temp)
				}
			}
			for _, b := range f.Blocks() {
				for _, in := range b.Instrs() {
					if bd, isBind := in.(*Bind); isBind && bd.Sym() == n.Sym() {
						return f.TempType(bd.Dst())
					}
				}
			}
		}
		return nil
	}
	return intrinsicType(in)
}

// requirePos is the single gate every constructor in this package passes
// through. It is a panic rather than an error because a node with no position
// is a producer bug: there is no input a user can supply that reaches it.
func requirePos(p Pos, who string) {
	if !p.IsValid() {
		panic("ir: " + who + ": a node needs a position")
	}
}

// --- terminators ------------------------------------------------------------

// Jump transfers control unconditionally.
type Jump struct {
	pos    Pos
	target BlockID
}

// NewJump jumps to target. pos is the jump's OWN position — the position of the
// construct that leaves, not of whatever was lowered last. The target's
// position is the target Block's.
func NewJump(pos Pos, target BlockID) *Jump {
	requirePos(pos, "NewJump")
	return &Jump{pos: pos, target: target}
}

// Target is the block control transfers to.
func (j *Jump) Target() BlockID { return j.target }

func (j *Jump) Pos() Pos       { return j.pos }
func (j *Jump) String() string { return "jump " + j.target.String() }
func (j *Jump) AppendSuccessors(dst []BlockID) []BlockID {
	return append(dst, j.target)
}
func (j *Jump) AppendUses(dst []Temp) []Temp { return dst }
func (j *Jump) irNode()                      {}
func (j *Jump) irTerm()                      {}

// Branch tests one temporary and transfers control to one of two blocks.
//
// This is the terminator short-circuit `and` / `or` needs, and the reason this
// package has basic blocks at all.
type Branch struct {
	pos     Pos
	cond    Temp
	ifTrue  BlockID
	ifFalse BlockID
}

// NewBranch tests cond. pos is the position of the TEST — for `and` / `or`
// that is the operator's, which is the position a report there blames.
func NewBranch(pos Pos, cond Temp, ifTrue, ifFalse BlockID) *Branch {
	requirePos(pos, "NewBranch")
	if cond == NoTemp {
		panic("ir: NewBranch: a branch needs a condition")
	}
	return &Branch{pos: pos, cond: cond, ifTrue: ifTrue, ifFalse: ifFalse}
}

// Cond is the tested temporary.
func (b *Branch) Cond() Temp { return b.cond }

// IfTrue is the block taken when Cond holds.
func (b *Branch) IfTrue() BlockID { return b.ifTrue }

// IfFalse is the block taken when Cond does not hold.
func (b *Branch) IfFalse() BlockID { return b.ifFalse }

func (b *Branch) Pos() Pos { return b.pos }
func (b *Branch) String() string {
	return "branch " + b.cond.String() + " ? " + b.ifTrue.String() + " : " + b.ifFalse.String()
}
func (b *Branch) AppendSuccessors(dst []BlockID) []BlockID {
	return append(dst, b.ifTrue, b.ifFalse)
}
func (b *Branch) AppendUses(dst []Temp) []Temp { return append(dst, b.cond) }
func (b *Branch) irNode()                      {}
func (b *Branch) irTerm()                      {}

// Return leaves the function.
//
// Its position is the tail's own. An inherited position would be wrong here:
// the return for `fn total(limit: Int): Int { Iter.loop(...) }` is lowered
// after the loop's arms, so an ambient cursor would attribute it to whichever
// arm's line was lowered last.
type Return struct {
	pos    Pos
	val    Temp
	hasVal bool
	ctl    Ctl
}

// Ctl is a signalling callback's control outcome: what `break` and `continue`
// tell the iteration that called it. It is set only on a return a control
// statement produced; an ordinary return from a signalling callback is
// CtlEmit's answer, and from any other function it has no outcome at all.
//
// The widened calling convention itself is recorded at the call, by
// Iter.Signalling; this records which statement produced the return, which
// only the callback's own graph can say.
type Ctl uint8

const (
	// CtlNone is an ordinary return.
	CtlNone Ctl = iota
	// CtlSkip is `continue` in an adapter: no value, ask for more.
	CtlSkip
	// CtlEmitStop is `break v`, or a bare `break` in a reduce, which answers
	// the unchanged accumulator: the value stands, then the source stops.
	CtlEmitStop
	// CtlStop is a bare `break` in an adapter: no value, and the source stops.
	CtlStop
)

func (c Ctl) String() string {
	switch c {
	case CtlSkip:
		return "skip"
	case CtlEmitStop:
		return "emit-stop"
	case CtlStop:
		return "stop"
	}
	return ""
}

// NewReturn returns val.
func NewReturn(pos Pos, val Temp) *Return {
	requirePos(pos, "NewReturn")
	if val == NoTemp {
		panic("ir: NewReturn: NoTemp; use NewReturnUnit for a function returning Unit")
	}
	return &Return{pos: pos, val: val, hasVal: true}
}

// NewReturnUnit returns from a function whose result is Unit.
func NewReturnUnit(pos Pos) *Return {
	requirePos(pos, "NewReturnUnit")
	return &Return{pos: pos}
}

// NewReturnCtl returns from a signalling callback with a control outcome.
// val is the answered value; CtlSkip and CtlStop carry none, and CtlEmitStop
// always carries one.
func NewReturnCtl(pos Pos, val Temp, ctl Ctl) *Return {
	requirePos(pos, "NewReturnCtl")
	switch ctl {
	case CtlSkip, CtlStop:
		if val != NoTemp {
			panic("ir: NewReturnCtl: " + ctl.String() + " answers no value")
		}
		return &Return{pos: pos, ctl: ctl}
	case CtlEmitStop:
		if val == NoTemp {
			panic("ir: NewReturnCtl: emit-stop answers a value")
		}
		return &Return{pos: pos, val: val, hasVal: true, ctl: ctl}
	}
	panic("ir: NewReturnCtl: an ordinary return is NewReturn")
}

// Ctl is the control outcome a control statement gave this return.
func (r *Return) Ctl() Ctl { return r.ctl }

// Val is the returned temporary; HasVal reports whether there is one.
func (r *Return) Val() Temp { return r.val }

// HasVal reports whether this return carries a value.
func (r *Return) HasVal() bool { return r.hasVal }

func (r *Return) Pos() Pos { return r.pos }
func (r *Return) String() string {
	out := "return"
	if r.ctl != CtlNone {
		out += " " + r.ctl.String()
	}
	if !r.hasVal {
		return out
	}
	return out + " " + r.val.String()
}
func (r *Return) AppendSuccessors(dst []BlockID) []BlockID { return dst }
func (r *Return) AppendUses(dst []Temp) []Temp {
	if !r.hasVal {
		return dst
	}
	return append(dst, r.val)
}
func (r *Return) irNode() {}
func (r *Return) irTerm() {}
