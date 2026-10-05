package ir

// Lint: the well-formedness check every retained ir.Func passes before a
// consumer reads it.
//
// WHY THIS EXISTS, AND IT IS NOT "MORE TESTS". A comparison of outputs is a
// comparison between two things that share a producer, so a producer bug
// reproduces identically on both sides and says nothing about the
// representation itself. Lint checks the representation directly.
//
// GHC has exactly this exposure — its native code generator and its bytecode
// interpreter both consume STG from Core — and its answer is `-dcore-lint` /
// `-dstg-lint`. The users guide states whose sanity it checks: "It checks
// GHC's sanity, not yours." This is that, at this representation's level.
//
// TEN RULES, EACH NAMED BY A PROPERTY THE GRAPH COULD GET WRONG or by a node a
// read-back asked for. None of them is a rule about a property the IR cannot
// yet express, and none is a rule whose population is empty, which is the
// standing refusal: a rule for an absent property cannot fire, and a lint that
// cannot fire is worse than no lint. Rule 6 is the one place that refusal is
// bent, and the bend is argued at the rule rather than left to a reader to
// notice.
//
//   - RuleBlockTerminated. This graph's terminator set is Jump, Branch and
//     Return, and the fault edge lives on the block rather than as a
//     terminator, so a block with no terminator is the one way control can
//     fall off the end of the representation. `Block.SetTerm` rejects a
//     SECOND terminator and nothing requires a first.
//   - RuleTempDefinedBeforeUse. The def-use chain is not built by the
//     producer; a VM allocates its own slots and needs every read to have a
//     definition that reaches it. Func.defs is what makes it answerable and
//     this is what asks it.
//   - RulePositionValid. Position is mandatory and per node (point 2 of
//     ir.go's package header).
//     `requirePos` gates `Pos.IsValid()`, which is `line >= 1`, and `At` and
//     `AtSynthesized` reject an empty file, so the defect is unconstructable
//     from outside this package. The rule's remaining population is a Pos
//     built by a composite literal INSIDE it.
//   - RulePositionSpan. Named by a CONSUMER rather than by a graph property.
//     A `Pos` carries an END so the graph can say how far a construct's text
//     reaches, and the rule asks the two questions a producer can be wrong
//     about: the end is
//     present, and it is not before the start. Its population is TOTAL rather
//     than the handful of spanning positions — `At` sets a point's end to its
//     own start, so every position in all 235 retained functions is checked —
//     which is what keeps it from being a rule with a three-member population.
//     Its third question, "the end is in a different file", is refused as a
//     rule and closed at the constructor instead: the end carries no file, so
//     the defect is unconstructable. See ir.go's Pos and checkPos.
//   - RuleModuleDeclaredOnce. Every declaration a `Module` names is named
//     once. `Module` is the container that makes the question askable.
//   - RuleSlotDeclaredBeforeUse. `ir.Slot` declares storage with a
//     type and no value, so a consumer that allocates frames has something
//     to size — and a declaration is not a definition, so this rule is the
//     thing that keeps the two apart. It asks two questions the graph can be
//     wrong about: one Temp declared by two Slots, and a write or a read of
//     slot storage on a path the declaration does not reach.
//   - RuleFaultEdgeResolved. `Block.SetFault` is the
//     exceptional edge the terminator set does not have. The rule asks that
//     an edge names a block of this function and that a block claiming one
//     has an instruction that can fault.
//   - RuleNoMatchIsLast. Named by a READ-BACK rather than by a graph
//     property. `ir.NoMatch` diverges, so an
//     instruction after one is code the graph claims is reachable and no
//     execution reaches. Its population is the retained `case`, which is not
//     empty. See nomatch.go.
//   - RuleLocalDeclared. Named by a consumer. A `RefLocal` names a local,
//     and the rule asks whether the function declares it. A destructuring
//     parameter is where a producer can get this wrong: its names must be
//     declared in the graph, not only in some scope outside it, because a VM
//     frame has to resolve every read. Its population is every retained
//     function with a parameter, so the passing side is production and the
//     failing side is a plant.
//
//     THE RULE IS ABOUT EXISTENCE AND NOT ABOUT ORDER, which is a narrowing
//     worth stating because `RuleSlotDeclaredBeforeUse` does ask the path
//     question one class over. A `RefLocal`'s own temporary is its
//     destination, so `RuleTempDefinedBeforeUse` already covers every value
//     the read produces; what it cannot see is whether the NAME the read
//     resolves against is declared at all. A path-sensitive version — the
//     `available` analysis over "a Bind of this Symbol" — would have an
//     empty failing population in production today, which is this file's
//     standing reason not to add a rule.
//
//     Its declaration set is wider than any graph the producer builds. The
//     set is "a parameter or one of its own Bindings", and in the retained
//     population every `RefLocal` names a parameter: `Bind` is built and
//     executed and nothing reads one by NAME, because the producer resolves a
//     bound name to the `Bind`'s destination temporary at lowering time. The
//     second half of the set is correct and has no production member. It is
//     not dead: a hand-built `RefLocal` naming a `Bind`'s symbol is admitted
//     here and must be run correctly by the consumer.
//
//   - RuleOperandShape. Named by a consumer. `internal/vm/vm.go` checks
//     operand types when a value arrives in a register (`branches on %T,
//     which is not a Bool` and its kin). This rule asks the derivable part of
//     that question at the gate.
//
//     The absence it works around is on the DEFINITION side: an
//     operand-bearing instruction does not need a type, because a shape is a
//     function of fields the nodes already have, plus two stored facts
//     (`Param.Shape` and `Proj.Shape`) for what no derivation reaches. See
//     shape.go.
//
//     Its failing side is a plant, which is RuleLocalDeclared's footing
//     above. It is the only fence for a producer that answers `FloatArith()`
//     where the kind is Int, which wraps where Nomi traps.
//
// THE DIVISION BETWEEN A CONSTRUCTOR PANIC AND A LINT VIOLATION is the same
// everywhere here and is worth stating once: a constructor rejects what ONE
// CALL can be wrong about, and Lint reports what only the whole graph or the
// whole container can be wrong about. `Module.AddFunc` carries the argument
// for the one case where that looks like laxness.
//
// A VIOLATION IS A PRODUCER BUG, so Lint returns every violation rather than
// the first: a producer that got one node wrong usually got a family wrong,
// and reporting one at a time turns one diagnosis into N runs.
//
// THE CALLER FAILS THE BUILD. Lint itself returns an error rather than
// panicking, because a linter that cannot be called from a test that asserts
// it fires is a linter nobody can plant a violation against —
// lint_test.go plants one per rule. `internal/irbuild` panics on the error, for
// requirePos's reason: a malformed IR is a producer bug with no user input
// that reaches it.

import (
	"sort"
	"strconv"
	"strings"
)

// LintRule names one well-formedness property.
type LintRule string

const (
	// RuleBlockTerminated: every block has a terminator.
	RuleBlockTerminated LintRule = "block terminated"
	// RuleTempDefinedBeforeUse: every path from the entry to a use of a
	// temporary passes through a definition of it.
	RuleTempDefinedBeforeUse LintRule = "temp defined before use"
	// RulePositionValid: every node's position names a line and a file.
	RulePositionValid LintRule = "position valid"
	// RuleModuleDeclaredOnce: no declaration identity is recorded twice in
	// one module container.
	RuleModuleDeclaredOnce LintRule = "module declared once"
	// RuleSlotDeclaredBeforeUse: one Temp is declared by at most one Slot,
	// and every path to a read or a write of slot storage passes through
	// its declaration.
	RuleSlotDeclaredBeforeUse LintRule = "slot declared before use"
	// RuleFaultEdgeResolved: a block's exceptional edge names a block of
	// this function, and a block claiming one has something that can fault.
	RuleFaultEdgeResolved LintRule = "fault edge resolved"
	// RuleNoMatchIsLast: nothing follows a NoMatch in its block.
	RuleNoMatchIsLast LintRule = "nomatch is last"
	// RuleLocalDeclared: every RefLocal names a local this function
	// declares — a parameter or one of its own Bindings.
	RuleLocalDeclared LintRule = "local declared"
	// RuleOperandShape: where the graph says what shape an operand holds,
	// the position it is used in accepts that shape. Silent where it does
	// not say; see shape.go.
	RuleOperandShape LintRule = "operand shape"
	// RulePositionSpan: every position's END is present and is not before its
	// start. See lintPositions.
	RulePositionSpan LintRule = "position span"
	// RuleDeferRegistered: every RunDefer names a Defer of this function,
	// and no id is registered twice.
	RuleDeferRegistered LintRule = "defer registered"
	// RuleTempTyped: every temporary the function names — a parameter, a
	// slot, a destination or an operand — has a stored value type.
	RuleTempTyped LintRule = "temp typed"
	// RuleTempType: each stored type agrees with what writes the temporary
	// and with what the positions that read it require. See typelint.go.
	RuleTempType LintRule = "temp type"
)

// Violation is one node failing one rule.
type Violation struct {
	Rule LintRule
	// Pos is the position of the offending node, which may itself be the
	// thing that is wrong.
	Pos Pos
	// What names the node in the function: "b0", "b2 instr 3", "func total".
	What string
	// Why is the specific failure.
	Why string
}

func (v Violation) String() string {
	return string(v.Rule) + ": " + v.What + ": " + v.Why + " at " + v.Pos.String()
}

// LintError is every violation one function or one module has.
type LintError struct {
	// Subject names what was linted: a function's name, or `module <name>`.
	Subject    string
	Violations []Violation
}

func (e *LintError) Error() string {
	var b strings.Builder
	b.WriteString("ir: Lint: ")
	b.WriteString(strconv.Itoa(len(e.Violations)))
	b.WriteString(" violation")
	if len(e.Violations) != 1 {
		b.WriteString("s")
	}
	b.WriteString(" in ")
	b.WriteString(e.Subject)
	for _, v := range e.Violations {
		b.WriteString("\n  ")
		b.WriteString(v.String())
	}
	return b.String()
}

// Lint checks one function for well-formedness. It returns *LintError, or nil.
func Lint(f *Func) error {
	if f == nil {
		panic("ir: Lint(nil)")
	}
	var vs []Violation
	lintFunc(f, &vs)
	if len(vs) == 0 {
		return nil
	}
	return &LintError{Subject: "func " + f.Name(), Violations: vs}
}

// LintModule checks one module container and every function in it.
//
// ONE ERROR FOR THE WHOLE MODULE rather than one per function, for the reason
// Lint reports every violation instead of the first: a producer that got one
// declaration wrong usually got a family wrong, and reporting one at a time
// turns one diagnosis into N runs.
func LintModule(m *Module) error {
	if m == nil {
		panic("ir: LintModule(nil)")
	}
	var vs []Violation
	var st moduleLintState
	lintModuleDecls(m, &st, &vs)
	for _, f := range m.Funcs() {
		lintFunc(f, &vs)
	}
	for _, cell := range m.Cells() {
		if f := cell.Initializer(); f != nil {
			lintFunc(f, &vs)
		}
	}
	if len(vs) == 0 {
		return nil
	}
	return &LintError{Subject: m.String(), Violations: vs}
}

// LintModuleAdded is LintModule over what the module gained since the previous
// LintModuleAdded call: every function and cell recorded since then, each
// linted exactly as LintModule lints it, and RuleModuleDeclaredOnce asked of
// each new entry against every entry already checked.
//
// Why it exists. A producer that lints after every AddFunc (the right place,
// since a violation is then reported at the call that caused it) would pay
// for LintModule re-linting every earlier function each time, which is
// quadratic in the module and dominated by the stdlib's modules.
//
// THE SAME RULES. Every per-function rule is a property of one function tree,
// and a function already linted is not re-linted. The one module-level rule is
// a property of the set; it is kept incremental by the seen-sets on the
// module, which RemoveFunc and RemoveCell maintain. So the violations a
// sequence of LintModuleAdded calls reports are the violations the matching
// sequence of LintModule calls would have reported for the first time.
//
// WHAT IT ASSUMES: a function is not changed after the module records it. A
// producer that cannot promise that runs LintModule once when the module is
// finished; internal/irbuild does, for every module it hands a consumer.
func LintModuleAdded(m *Module) error {
	if m == nil {
		panic("ir: LintModuleAdded(nil)")
	}
	var vs []Violation
	fromFunc, fromCell := m.lint.funcs, m.lint.cells
	lintModuleDecls(m, &m.lint, &vs)
	for _, f := range m.funcs[fromFunc:] {
		lintFunc(f, &vs)
	}
	for _, cell := range m.cells[fromCell:] {
		if f := cell.Initializer(); f != nil {
			lintFunc(f, &vs)
		}
	}
	if len(vs) == 0 {
		return nil
	}
	return &LintError{Subject: m.String(), Violations: vs}
}

func lintFunc(f *Func, vs *[]Violation) {
	lintFuncTree(f, vs, map[*Func]bool{})
}

func lintFuncTree(f *Func, vs *[]Violation, seen map[*Func]bool) {
	if seen[f] {
		return
	}
	seen[f] = true
	for _, b := range f.Blocks() {
		for _, in := range b.Instrs() {
			if n, ok := in.(*FuncValue); ok {
				lintFuncTree(n.Body(), vs, seen)
			}
		}
	}
	lintPositions(f, vs)
	lintTerminators(f, vs)
	lintFaultEdges(f, vs)
	lintTempDefs(f, vs)
	lintSlots(f, vs)
	lintNoMatch(f, vs)
	lintLocals(f, vs)
	lintOperandShapes(f, vs)
	lintDefers(f, vs)
	lintTempTypes(f, vs)
}

// lintModuleDecls applies RuleModuleDeclaredOnce.
//
// THE IDENTITY AND NOT THE NAME. Two same-named declarations are two
// declarations — that is `Symbol`'s founding rule, and comparing them by
// printed name is the defect that produced `expected Error, got Error` in this
// repository — so this reports one identity recorded twice and says nothing
// about two identities sharing a name.
//
// A FUNCTION'S IDENTITY HERE IS THE `*Func` POINTER, because a Func is not
// interned: `NewFunc` mints, so two lowerings of one Nomi function are two
// Funcs and recording one Func twice is the producer bug this catches. A
// cell's identity is its Symbol's, which IS interned through `Table.Symbol`,
// so two `DeclareCell` calls for one declaration are two Cells carrying one
// Symbol and that is the shape reported.
//
// WHAT IT DOES NOT CHECK, AND WHY EACH WOULD BE A RULE THAT CANNOT FIRE:
//
//   - "every Store names a cell this module declares". The def-use rule one
//     level up, and the right rule to want — but no `Store` is reachable
//     from a Module. `internal/irbuild` builds one and consumes it at the
//     same call, as it does for free-standing nodes elsewhere (`irjump.go`,
//     `ircall.go`). So the rule's population is empty and it would pass vacuously over every module in
//     the corpus. It becomes real when a function body containing a `with` is
//     retained, which is not this shape.
//   - "every cell is read or written by something". A cell nothing reads is
//     LEGAL and intended: `internal/irbuild`'s `flushAppCells` emits every cell
//     a module declares rather than only the ones something read, so that one
//     unchanged program does not get two content addresses depending on the
//     order its units are rendered in. A rule against dead storage would
//     report that deliberate choice.
//
// INCREMENTAL BY CONSTRUCTION. It checks the entries past st's prefix against
// st's seen-sets and then extends the prefix, so LintModule (a fresh st) asks
// it of the whole module and LintModuleAdded (the module's own st) asks it of
// what was added since. The sets COUNT entries rather than mark them so that
// RemoveFunc and RemoveCell can take an entry back out.
func lintModuleDecls(m *Module, st *moduleLintState, vs *[]Violation) {
	if st.seenFunc == nil {
		st.seenFunc = map[*Func]int{}
		st.seenSymbol = map[*Symbol]int{}
		st.seenCell = map[*Symbol]int{}
	}
	for _, f := range m.funcs[st.funcs:] {
		st.seenFunc[f]++
		if st.seenFunc[f] > 1 {
			*vs = append(*vs, Violation{Rule: RuleModuleDeclaredOnce, Pos: f.Pos(),
				What: "func " + f.Name(),
				Why:  "the module records this function twice"})
			continue
		}
		if sym := f.Sym(); sym != nil {
			st.seenSymbol[sym]++
			if st.seenSymbol[sym] > 1 {
				*vs = append(*vs, Violation{Rule: RuleModuleDeclaredOnce, Pos: f.Pos(),
					What: "func " + f.Name(),
					Why:  "the module defines this function identity twice"})
			}
		}
	}
	st.funcs = len(m.funcs)
	for _, c := range m.cells[st.cells:] {
		st.seenCell[c.Sym()]++
		if st.seenCell[c.Sym()] > 1 {
			*vs = append(*vs, Violation{Rule: RuleModuleDeclaredOnce, Pos: Pos{},
				What: c.String(),
				Why:  "the module declares storage for this identity twice"})
		}
		if c.Type().Val() == nil {
			*vs = append(*vs, Violation{Rule: RuleTempTyped, Pos: Pos{},
				What: c.String(),
				Why:  "the cell's type states no value type"})
		}
	}
	st.cells = len(m.cells)
}

// lintPositions applies RulePositionValid and RulePositionSpan to the
// function, every block, every instruction and every terminator.
func lintPositions(f *Func, vs *[]Violation) {
	checkPos(vs, f.Pos(), "func "+f.Name())
	for _, b := range f.Blocks() {
		checkPos(vs, b.Pos(), b.ID().String())
		for i, in := range b.Instrs() {
			checkPos(vs, in.Pos(), b.ID().String()+" instr "+strconv.Itoa(i)+" ("+in.String()+")")
		}
		if t := b.Term(); t != nil {
			checkPos(vs, t.Pos(), b.ID().String()+" term ("+t.String()+")")
		}
	}
}

// checkPos asks RulePositionValid's two questions and RulePositionSpan's two.
//
// THE SPAN QUESTIONS ARE ASKED OF EVERY POSITION AND NOT ONLY OF THE ONES
// THAT SPAN, which is what keeps the rule's population total. `At` sets a
// point's end to its own start, so "the end is present" is a property of all
// 337 instruction positions plus every block's, terminator's and function's —
// not of the three that reach past their start line. A rule whose population
// were those three would be this file's standing refusal in a new form.
//
// THE THIRD QUESTION — "the end is in a different file" — IS NOT HERE, AND
// THAT IS A REPRESENTATION DECISION RATHER THAN AN OMISSION. A Pos's end
// carries no file: a construct's text is in one file, so an end with its own
// path is a shape able to express nonsense, and this package closes such a
// hole at the constructor where it can. That is `At`'s empty-file gate's own
// argument, applied one field over. The positive is
// TestPos_TheEndCannotNameADifferentFile.
func checkPos(vs *[]Violation, p Pos, what string) {
	switch {
	case !p.IsValid():
		*vs = append(*vs, Violation{Rule: RulePositionValid, Pos: p, What: what,
			Why: "the position names no line"})
		return
	case p.File() == "":
		*vs = append(*vs, Violation{Rule: RulePositionValid, Pos: p, What: what,
			Why: "the position names no file"})
		return
	}
	switch {
	case p.EndLine() < 1:
		*vs = append(*vs, Violation{Rule: RulePositionSpan, Pos: p, What: what,
			Why: "the position has no end, so nothing can say how far the construct reaches"})
	case p.EndLine() < p.Line() || (p.EndLine() == p.Line() && p.EndCol() < p.Col()):
		*vs = append(*vs, Violation{Rule: RulePositionSpan, Pos: p, What: what,
			Why: "the position ends before it starts"})
	}
}

// lintTerminators applies RuleBlockTerminated.
func lintTerminators(f *Func, vs *[]Violation) {
	for _, b := range f.Blocks() {
		if b.Term() == nil {
			*vs = append(*vs, Violation{Rule: RuleBlockTerminated, Pos: b.Pos(),
				What: b.ID().String(), Why: "the block has no terminator, so control falls off the end"})
		}
	}
}

// --- the block graph, including the exceptional edges ----------------------

// cfg is one function's block graph. It is built once and read by the two
// must analyses below, so they cannot disagree about which edges exist.
//
// The fault edge is an edge here. If the successor set were only what the
// terminator names, a handler block would be unreachable by this analysis
// and every use in it would pass vacuously under the empty-in[] rule. A graph whose
// exceptional control flow the linter cannot see is a graph whose exceptional
// control flow nothing checks.
type cfg struct {
	blocks    []*Block
	edges     []cfgEdge
	preds     [][]cfgEdge
	reachable []bool
}

// cfgEdge is one control transfer. `fault` distinguishes the exceptional
// edge, because what is COMPUTED when control takes it differs: a normal exit
// runs the whole block, and a fault leaves at the first instruction that can
// fault, so nothing at or after that instruction has run.
type cfgEdge struct {
	from, to BlockID
	fault    bool
}

// Reachable reports, per block id, whether control can reach the block from
// the entry, by the same edges lint uses: terminator successors, except from a
// block ending in NoMatch, and fault edges. A block not yet terminated
// contributes no edge.
func Reachable(f *Func) []bool { return buildCFG(f).reachable }

func buildCFG(f *Func) *cfg {
	blocks := f.Blocks()
	c := &cfg{blocks: blocks, preds: make([][]cfgEdge, len(blocks)),
		reachable: make([]bool, len(blocks))}
	add := func(from BlockID, to BlockID, fault bool) {
		e := cfgEdge{from: from, to: to, fault: fault}
		c.edges = append(c.edges, e)
		if int(to) < len(blocks) {
			c.preds[to] = append(c.preds[to], e)
		}
	}
	for i, b := range blocks {
		// An unterminated block contributes no normal edge;
		// RuleBlockTerminated has already reported it.
		// NoMatch always faults, so its syntactic terminator cannot deliver
		// a normal predecessor to the join. Its fault edge remains live.
		diverges := false
		if instrs := b.Instrs(); len(instrs) > 0 {
			_, diverges = instrs[len(instrs)-1].(*NoMatch)
		}
		if t := b.Term(); t != nil && !diverges {
			for _, s := range t.AppendSuccessors(nil) {
				add(BlockID(i), s, false)
			}
		}
		if h, faults := b.Fault(); faults {
			add(BlockID(i), h, true)
		}
	}
	if len(blocks) == 0 {
		return c
	}
	c.reachable[0] = true
	for changed := true; changed; {
		changed = false
		for _, e := range c.edges {
			if int(e.to) >= len(blocks) || !c.reachable[e.from] || c.reachable[e.to] {
				continue
			}
			c.reachable[e.to] = true
			changed = true
		}
	}
	return c
}

// available is the standard MUST analysis over a per-block generated set.
//
// in[b] is the intersection of out[p] over b's reachable predecessors, where
// out[p] depends on WHICH EDGE was taken: `gen` for a normal exit and
// `faultGen` for the exceptional one. The entry starts from `entry`.
//
// AN UNREACHABLE BLOCK IS GIVEN AN EMPTY in[], which is the conservative
// choice and is stated because the alternative is silent. The intersection
// over no predecessors is "everything", so an unreachable block would
// otherwise pass every check vacuously — and a use in such a block is exactly
// the shape a producer bug takes.
func (c *cfg) available(n int, entry []bool, gen, faultGen [][]bool) [][]bool {
	blocks := c.blocks
	in := make([][]bool, len(blocks))
	outN := make([][]bool, len(blocks))
	outF := make([][]bool, len(blocks))
	for i := range blocks {
		in[i] = make([]bool, n)
		outN[i] = make([]bool, n)
		outF[i] = make([]bool, n)
		if i == 0 {
			copy(in[i], entry)
		}
		if i == 0 || !c.reachable[i] {
			for t := 0; t < n; t++ {
				outN[i][t] = in[i][t] || gen[i][t]
				outF[i][t] = in[i][t] || faultGen[i][t]
			}
			continue
		}
		// TOP for every other reachable block, which is what makes an
		// intersection converge downward.
		for t := 0; t < n; t++ {
			outN[i][t], outF[i][t] = true, true
		}
	}
	for changed := true; changed; {
		changed = false
		for i := range blocks {
			if i == 0 || !c.reachable[i] {
				continue
			}
			next := make([]bool, n)
			first := true
			for _, e := range c.preds[i] {
				if !c.reachable[e.from] {
					continue
				}
				src := outN[e.from]
				if e.fault {
					src = outF[e.from]
				}
				if first {
					copy(next, src)
					first = false
					continue
				}
				for t := range next {
					next[t] = next[t] && src[t]
				}
			}
			in[i] = next
			for t := 0; t < n; t++ {
				nn, nf := next[t] || gen[i][t], next[t] || faultGen[i][t]
				if nn != outN[i][t] {
					outN[i][t], changed = nn, true
				}
				if nf != outF[i][t] {
					outF[i][t], changed = nf, true
				}
			}
		}
	}
	return in
}

// lintFaultEdges applies RuleFaultEdgeResolved.
//
// TWO QUESTIONS, AND THE SECOND IS THE ONE THAT NEEDED ARGUING. An edge whose
// target is not a block of this function is a dangling pointer in the graph
// and needs no defence. An edge on a block WHERE NOTHING CAN FAULT is legal
// Go, legal in an evaluator, and a bug in a VM: the handler block is then
// reachable only along an edge no execution can take, so `available` gives it
// an in[] intersected over a predecessor that never runs, and the frame the
// VM pushes for the handler is a frame nothing pops by faulting. It is the
// dead-handler shape, and unlike the "cell nothing reads" rule this file
// refuses, it is not a deliberate producer choice anywhere: `CanFault` is
// derived from the instructions, so a producer that set the edge and then
// appended no faulting instruction wrote a claim it did not keep.
//
// THE POPULATION OF SET EDGES IS EMPTY IN PRODUCTION TODAY and that is said
// out loud rather than left for a reader to discover: no Nomi construct
// catches a fault, so both halves of this rule are exercised only by planted
// graphs. What IS exercised over the corpus is the other side of the fault
// edge — `CanFault` over every retained function — which is what makes the
// edge's ABSENCE a recorded default rather than an omission. See fault.go.
func lintFaultEdges(f *Func, vs *[]Violation) {
	blocks := f.Blocks()
	for _, b := range blocks {
		h, faults := b.Fault()
		if !faults {
			continue
		}
		switch {
		case int(h) >= len(blocks):
			*vs = append(*vs, Violation{Rule: RuleFaultEdgeResolved, Pos: b.FaultPos(),
				What: b.ID().String(), Why: "the fault edge names " + h.String() +
					", which is not a block of this function"})
		case !b.CanFault():
			*vs = append(*vs, Violation{Rule: RuleFaultEdgeResolved, Pos: b.FaultPos(),
				What: b.ID().String(), Why: "the fault edge names " + h.String() +
					", and nothing in this block can fault"})
		}
	}
}

// lintTempDefs applies RuleTempDefinedBeforeUse.
//
// THE PROPERTY IS "EVERY PATH", NOT "SOME DEFINITION DOMINATES". Those differ
// in this IR and the difference is the point: it is deliberately not SSA, so
// two arms of a branch may both write one destination and neither dominates
// the join. Dominance would report that shape — which `gen.slot` / `gen.fixSlot`
// produces today — as a violation. So this is the standard MUST analysis over
// available definitions; see `cfg.available`.
//
// A PARAMETER'S TEMPORARY IS DEFINED AT ENTRY, by `Func.AddParam`. Nothing in a body writes it — the caller does — so without
// seeding the entry set every function with a parameter would report its
// first read as undefined, and the rule would have to be switched off for
// exactly the population a VM runs.
//
// A FAULT LEAVES AT THE FIRST INSTRUCTION THAT CAN FAULT, so what a handler
// can rely on is what the block computed BEFORE it. Taking the whole block's
// definitions would tell a handler it can read a temporary the fault
// prevented being written, which is the def-use half of the fault edge.
func lintTempDefs(f *Func, vs *[]Violation) {
	blocks := f.Blocks()
	if len(blocks) == 0 {
		return
	}
	n := f.NumTemps() + 1
	c := buildCFG(f)

	gen := make([][]bool, len(blocks))
	faultGen := make([][]bool, len(blocks))
	for i, b := range blocks {
		gen[i] = make([]bool, n)
		faultGen[i] = make([]bool, n)
		cut := b.FirstFaultAt()
		for j, in := range b.Instrs() {
			d := in.Dst()
			if d == NoTemp || int(d) >= n {
				continue
			}
			gen[i][d] = true
			if j < cut {
				faultGen[i][d] = true
			}
		}
	}

	entry := make([]bool, n)
	for _, p := range f.Params() {
		if int(p.Temp) < n {
			entry[p.Temp] = true
		}
	}
	in := c.available(n, entry, gen, faultGen)

	var uses []Temp
	for i, b := range blocks {
		cur := make([]bool, n)
		copy(cur, in[i])
		report := func(t Temp, at Pos, what string) {
			if t == NoTemp {
				return
			}
			why := ""
			switch {
			case int(t) >= n:
				why = t.String() + " is outside this function's temporary namespace of " +
					strconv.Itoa(n-1)
			case f.Def(t) == nil && !entry[t]:
				why = "nothing in this function defines " + t.String()
			case !cur[t]:
				why = t.String() + " is defined, but not on every path reaching here"
			default:
				return
			}
			*vs = append(*vs, Violation{Rule: RuleTempDefinedBeforeUse, Pos: at,
				What: what, Why: why})
		}
		for j, instr := range b.Instrs() {
			uses = instr.AppendUses(uses[:0])
			sortTemps(uses)
			what := b.ID().String() + " instr " + strconv.Itoa(j) + " (" + instr.String() + ")"
			for _, u := range uses {
				report(u, instr.Pos(), what)
			}
			if d := instr.Dst(); d != NoTemp && int(d) < n {
				cur[d] = true
			}
		}
		if t := b.Term(); t != nil {
			uses = t.AppendUses(uses[:0])
			sortTemps(uses)
			what := b.ID().String() + " term (" + t.String() + ")"
			for _, u := range uses {
				report(u, t.Pos(), what)
			}
		}
	}
}

// lintSlots applies RuleSlotDeclaredBeforeUse.
//
// TWO QUESTIONS, AND NEITHER IS ASKED BY RuleTempDefinedBeforeUse. That rule
// is about the VALUE in a temporary; this one is about the STORAGE, and the
// two come apart precisely because `Slot.Dst()` is NoTemp — see slot.go on
// why a declaration must not count as a definition.
//
//   - ONE TEMP, AT MOST ONE SLOT. Two declarations for one name is two frame
//     registers for one storage in a VM and a Go redeclaration in the
//     builder's text, and the producer that wrote it believes it has one.
//   - EVERY PATH TO A READ OR A WRITE PASSES THROUGH THE DECLARATION. A
//     write into undeclared storage is the bug a frame allocator crashes on,
//     and it is exactly what a producer that put the `Slot` inside one arm of
//     a branch would produce.
//
// A TEMPORARY NO SLOT DECLARES IS NOT SLOT STORAGE and this rule says nothing
// about it. The population is "Temps some Slot in this function declares",
// which is what keeps the rule from being a second, weaker copy of
// RuleTempDefinedBeforeUse over every temporary in the graph.
func lintSlots(f *Func, vs *[]Violation) {
	blocks := f.Blocks()
	if len(blocks) == 0 {
		return
	}
	n := f.NumTemps() + 1
	isSlot := make([]bool, n)
	any := false
	for _, b := range blocks {
		for _, in := range b.Instrs() {
			s, declares := in.(*Slot)
			if !declares || int(s.Slot()) >= n {
				continue
			}
			isSlot[s.Slot()], any = true, true
		}
	}
	if !any {
		return
	}

	c := buildCFG(f)
	gen := make([][]bool, len(blocks))
	faultGen := make([][]bool, len(blocks))
	// ONE SCAN FOR BOTH DUPLICATE SHAPES. A second declaration in the same
	// block and a second one in a sibling block are one bug — two frame
	// registers for one storage — so they get one report. Two Slots on two
	// arms of a branch is the shape a per-block scan cannot see, and
	// reporting it separately reported the same-block case twice.
	declared := map[Temp]*Slot{}
	for i, b := range blocks {
		gen[i] = make([]bool, n)
		faultGen[i] = make([]bool, n)
		cut := b.FirstFaultAt()
		for j, in := range b.Instrs() {
			s, declares := in.(*Slot)
			if !declares || int(s.Slot()) >= n {
				continue
			}
			if first, dup := declared[s.Slot()]; dup {
				*vs = append(*vs, Violation{Rule: RuleSlotDeclaredBeforeUse, Pos: s.Pos(),
					What: b.ID().String() + " instr " + strconv.Itoa(j) + " (" + s.String() + ")",
					Why: s.Slot().String() + " is already declared at " +
						first.Pos().String()})
			} else {
				declared[s.Slot()] = s
			}
			gen[i][s.Slot()] = true
			if j < cut {
				faultGen[i][s.Slot()] = true
			}
		}
	}

	in := c.available(n, make([]bool, n), gen, faultGen)
	var uses []Temp
	for i, b := range blocks {
		cur := make([]bool, n)
		copy(cur, in[i])
		report := func(t Temp, at Pos, what, how string) {
			if t == NoTemp || int(t) >= n || !isSlot[t] || cur[t] {
				return
			}
			*vs = append(*vs, Violation{Rule: RuleSlotDeclaredBeforeUse, Pos: at,
				What: what, Why: how + " " + t.String() +
					", whose storage is not declared on every path reaching here"})
		}
		for j, instr := range b.Instrs() {
			what := b.ID().String() + " instr " + strconv.Itoa(j) + " (" + instr.String() + ")"
			uses = instr.AppendUses(uses[:0])
			sortTemps(uses)
			for _, u := range uses {
				report(u, instr.Pos(), what, "reads")
			}
			report(instr.Dst(), instr.Pos(), what, "writes")
			if s, declares := instr.(*Slot); declares && int(s.Slot()) < n {
				cur[s.Slot()] = true
			}
			if d := instr.Dst(); d != NoTemp && int(d) < n && isSlot[d] {
				// Reported above when undeclared; recorded here so one
				// missing declaration is not reported once per later write.
				cur[d] = true
			}
		}
		if t := b.Term(); t != nil {
			uses = t.AppendUses(uses[:0])
			sortTemps(uses)
			what := b.ID().String() + " term (" + t.String() + ")"
			for _, u := range uses {
				report(u, t.Pos(), what, "reads")
			}
		}
	}
}

// lintNoMatch applies RuleNoMatchIsLast.
//
// WHAT CAN FIRE, AND WHY NO EXISTING RULE ASKS IT. `NoMatch` diverges:
// control does not continue past it. So an instruction after one is code the
// graph claims is REACHABLE and that no execution reaches.
// `RuleBlockTerminated` asks that the block leaves — it does, by the exit the
// producer puts after the trap,
// which is why the trap is an instruction and not a terminator (nomatch.go
// reason 2). Data-flow analysis removes the normal edge of a block ending
// in NoMatch; this rule separately rejects instructions after the trap.
//
// ITS POPULATION IS NOT EMPTY, which is this file's standing requirement: the
// corpus retains a `case` whose fallthrough block holds a `NoMatch`, so the
// rule's passing side is exercised by production and its failing side by a
// plant. Contrast `RuleFaultEdgeResolved`, whose set-edge population is empty
// and whose bend is argued at that rule.
//
// ONE VIOLATION PER MISPLACED TRAP, not one per dead instruction, which is a
// narrowing of this file's every-violation policy rather than an exception to
// it. That policy is about INDEPENDENT bugs — "a producer that got one node
// wrong usually got a family wrong". A block with six instructions after a
// trap has ONE thing wrong with it, so the violation is the trap's placement
// and the message names the first instruction that cannot run, which is the
// one a producer looks at.
func lintNoMatch(f *Func, vs *[]Violation) {
	for _, b := range f.Blocks() {
		instrs := b.Instrs()
		for i, in := range instrs {
			if _, trap := in.(*NoMatch); !trap || i == len(instrs)-1 {
				continue
			}
			*vs = append(*vs, Violation{
				Rule: RuleNoMatchIsLast,
				Pos:  instrs[i+1].Pos(),
				What: b.ID().String() + " instr " + strconv.Itoa(i+1),
				Why: "follows a nomatch, which diverges, so nothing here can run: " +
					instrs[i+1].String(),
			})
		}
	}
}

// lintLocals applies RuleLocalDeclared.
//
// WHAT NO OTHER RULE SEES. A graph for `fn sum_point(Point{x, y}): Int { x + y }`
// that recorded neither the parameter nor its projections would still satisfy
// `RuleTempDefinedBeforeUse`, because a `RefLocal`'s temporary is its own
// DESTINATION and the read writes it. The missing fact is one level up: the
// NAME resolves against nothing.
//
// THE DECLARATION SET IS PARAMETERS PLUS THIS FUNCTION'S OWN BINDINGS.
// `ir.Bind` IS a declaration — destructure.go's decomposition names it as the
// "name" step — so a read of a bound name is resolvable whether the producer
// spells it as a `RefLocal` or, as `internal/irbuild`'s builder does, by reusing
// the Bind's destination temporary directly.
//
// COMPARED BY IDENTITY AND NOT BY NAME, which is `Symbol`'s founding rule and
// is what makes the rule worth having rather than a spell-check: two
// same-named declarations are two symbols, so a read that resolved against
// the wrong one is reported here even though the printed names agree.
func lintLocals(f *Func, vs *[]Violation) {
	declared := make(map[*Symbol]bool, len(f.Params()))
	for _, p := range f.Params() {
		declared[p.Sym] = true
	}
	for _, b := range f.Blocks() {
		for _, in := range b.Instrs() {
			if bind, isBind := in.(*Bind); isBind {
				declared[bind.Sym()] = true
			}
		}
	}
	for _, b := range f.Blocks() {
		for i, in := range b.Instrs() {
			r, isRef := in.(*Ref)
			if !isRef || r.Kind() != RefLocal || declared[r.Sym()] {
				continue
			}
			*vs = append(*vs, Violation{
				Rule: RuleLocalDeclared,
				Pos:  r.Pos(),
				What: b.ID().String() + " instr " + strconv.Itoa(i),
				Why: "reads local " + r.Sym().Name() + ", which this function neither " +
					"takes as a parameter nor binds",
			})
		}
	}
}

// sortTemps orders a use list so two runs of Lint over one function report in
// one order. AppendUses is an operand order, which is right for a consumer and
// arbitrary for a diagnostic.
func sortTemps(ts []Temp) {
	sort.Slice(ts, func(i, j int) bool { return ts[i] < ts[j] })
}

// lintDefers applies RuleDeferRegistered. The consumers pair a RunDefer with
// its registration by id, so an unregistered id or a registration made twice
// would run the wrong call or none.
func lintDefers(f *Func, vs *[]Violation) {
	registered := map[int]bool{}
	for _, b := range f.Blocks() {
		for i, in := range b.Instrs() {
			d, isDefer := in.(*Defer)
			if !isDefer {
				continue
			}
			if registered[d.ID()] {
				*vs = append(*vs, Violation{
					Rule: RuleDeferRegistered, Pos: d.Pos(),
					What: b.ID().String() + " instr " + strconv.Itoa(i),
					Why:  "registers deferred call " + strconv.Itoa(d.ID()) + " a second time",
				})
			}
			registered[d.ID()] = true
		}
	}
	for _, b := range f.Blocks() {
		for i, in := range b.Instrs() {
			r, isRun := in.(*RunDefer)
			if !isRun || registered[r.ID()] {
				continue
			}
			*vs = append(*vs, Violation{
				Rule: RuleDeferRegistered, Pos: r.Pos(),
				What: b.ID().String() + " instr " + strconv.Itoa(i),
				Why:  "runs deferred call " + strconv.Itoa(r.ID()) + ", which nothing in this function registers",
			})
		}
	}
}
