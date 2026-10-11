package ir

import "strconv"

// Debug information: what a source-level debugger needs from a function that
// running it does not. Two tables, both filled by the producer and both
// carried by the image encoder:
//
//   - The VARIABLE TABLE (Func.Vars). One entry per parameter and per named
//     binding the producer lowered: its Nomi name, the temporary holding its
//     value, its type as Nomi spells it, and its scope. A rebinding is its
//     own entry, so a debugger shows the binding in force: of two entries
//     with one name that are both visible, the one whose scope starts later.
//     Synthetic temporaries get no entry.
//   - STATEMENT MARKS (Block.Stmts). The instructions a step stops before:
//     the first instruction of each statement, of each `case` arm and `if`
//     branch, of each loop iteration and of each `|>` stage. This is DWARF's
//     `is_stmt`. A synthesized position is never a mark.
//
// Neither table changes what a function computes, and a consumer that is not
// debugging reads neither.

// Var is one named value of a function, as a debugger shows it.
type Var struct {
	// Name is the Nomi name.
	Name string
	// Temp holds the value. It is a parameter's temporary or one some
	// instruction of the function writes (RuleVarTemp).
	Temp Temp
	// Type is the value's type as Nomi spells it (`List<Int>`), which a
	// watch expression is type-checked against.
	Type string
	// Scope is where the name is visible: from the binding to the end of the
	// block, arm or function that encloses it. A binding the producer could
	// not place in a block reaches the end of its function, which Scope says
	// with an end line of MaxScopeLine.
	Scope Pos
	// Decl is where the name is bound, spanning the name with its end
	// exclusive: the `name` of `name = ...`, the name inside a pattern, a
	// parameter's name. A captured value's is the binding it captured, in
	// the function that made the closure. It is the zero Pos when the
	// producer does not know it.
	Decl Pos
}

// MaxScopeLine is the end line of a scope that reaches the end of its
// function.
const MaxScopeLine = 1<<31 - 1

// AddVar records one named value. The producer calls it where the name
// becomes visible; scope must be a valid, unsynthesized position, and decl
// one too or the zero Pos.
func (f *Func) AddVar(name string, t Temp, typ string, scope, decl Pos) {
	if t == NoTemp || name == "" {
		panic("ir: Func.AddVar: a variable needs a name and a temporary")
	}
	requirePos(scope, "Func.AddVar")
	if decl.IsValid() {
		requirePos(decl, "Func.AddVar")
	}
	f.vars = append(f.vars, Var{Name: name, Temp: t, Type: typ, Scope: scope, Decl: decl})
}

// Vars is the function's variable table in the order the producer recorded
// it.
func (f *Func) Vars() []Var { return f.vars }

// Visible reports whether the scope includes the source position (line, col).
func (v Var) Visible(line, col int) bool {
	s := v.Scope
	if line < s.Line() || (line == s.Line() && col < s.Col()) {
		return false
	}
	return line < s.EndLine() || (line == s.EndLine() && col <= s.EndCol())
}

// StmtKind is what a statement mark begins.
type StmtKind uint8

const (
	// StmtStatement is a statement, or a body's final expression.
	StmtStatement StmtKind = iota
	// StmtArm is a `case` arm or an `if` branch.
	StmtArm
	// StmtIteration is one iteration of a loop.
	StmtIteration
	// StmtStage is one stage of a `|>` pipeline. Stepping over treats a
	// pipeline as one statement, so a step over passes its stages; a step
	// into and a breakpoint stop at them.
	StmtStage
)

func (k StmtKind) String() string {
	switch k {
	case StmtArm:
		return "arm"
	case StmtIteration:
		return "iteration"
	case StmtStage:
		return "stage"
	}
	return "statement"
}

// StmtMark says the instruction at Index of its block begins a statement at
// Pos. An Index equal to the block's instruction count marks the terminator.
type StmtMark struct {
	Index int
	Pos   Pos
	Kind  StmtKind
}

// MarkStmt marks the next instruction this block appends (or its terminator,
// when none follows) as the start of a statement at pos. A synthesized or
// invalid position marks nothing: derived code is never a stop. A second mark
// on the same line before the same instruction is dropped, so the outermost
// construct, which the producer marks first, names the stop. One on another
// line is kept: a pipeline whose head computes nothing (`xs` then
// `|> Iter.map(f)` on the next line) starts its first stage where its
// statement starts, and a breakpoint on either line stops there.
func (b *Block) MarkStmt(pos Pos, kind StmtKind) {
	if !pos.IsValid() || pos.Synthesized() || b.term != nil {
		return
	}
	at := len(b.instrs)
	for i := len(b.stmts) - 1; i >= 0 && b.stmts[i].Index == at; i-- {
		if b.stmts[i].Pos.Line() == pos.Line() {
			return
		}
	}
	b.stmts = append(b.stmts, StmtMark{Index: at, Pos: pos, Kind: kind})
}

// Stmts are the block's statement marks in instruction order.
func (b *Block) Stmts() []StmtMark { return b.stmts }

// shiftStmts moves the marks after an instruction inserted at index.
func (b *Block) shiftStmts(index int) {
	for i := range b.stmts {
		if b.stmts[i].Index > index {
			b.stmts[i].Index++
		}
	}
}

// Defined answers which temporaries hold a value on every path reaching a
// point of a function: the must analysis RuleTempDefinedBeforeUse runs,
// offered to a consumer. A debugger shows a variable only where its
// temporary is defined, so it never reads a register nothing wrote.
type Defined struct {
	f  *Func
	in [][]bool
}

// DefinedIn runs the analysis over f.
func DefinedIn(f *Func) *Defined {
	in, _ := definedSets(f)
	return &Defined{f: f, in: in}
}

// Before reports whether t is defined on every path reaching instruction i of
// block b (i equal to the instruction count is the terminator).
func (d *Defined) Before(b BlockID, i int, t Temp) bool {
	if int(b) >= len(d.in) || t == NoTemp || int(t) >= len(d.in[b]) {
		return false
	}
	if d.in[b][t] {
		return true
	}
	instrs := d.f.Block(b).Instrs()
	for j := 0; j < i && j < len(instrs); j++ {
		if instrs[j].Dst() == t {
			return true
		}
	}
	return false
}

// definedSets is the in-set of every block and the entry set.
func definedSets(f *Func) (in [][]bool, entry []bool) {
	blocks := f.Blocks()
	n := f.NumTemps() + 1
	entry = make([]bool, n)
	for _, p := range f.Params() {
		if int(p.Temp) < n {
			entry[p.Temp] = true
		}
	}
	if len(blocks) == 0 {
		return nil, entry
	}
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
	return c.available(n, entry, gen, faultGen), entry
}

// Pipe is one `|>` pipeline of a function, for a debugger's pipeline view:
// where it is and the value each of its stages produced, the head first.
type Pipe struct {
	// Pos spans the whole pipeline.
	Pos Pos
	// Stages are the head and then each stage, in order.
	Stages []PipeStage
}

// PipeStage is one value of a pipeline.
type PipeStage struct {
	// Text is the stage's source (`|> Iter.map(f)`), or the head's (`xs`).
	Text string
	// Temp holds the value once the stage has run.
	Temp Temp
	// Pos is where the stage is written.
	Pos Pos
}

// AddPipe records one pipeline. Its positions must be valid and
// unsynthesized, as a variable's scope must.
func (f *Func) AddPipe(p Pipe) {
	requirePos(p.Pos, "Func.AddPipe")
	for _, s := range p.Stages {
		if s.Temp == NoTemp {
			panic("ir: Func.AddPipe: a stage needs a temporary")
		}
		requirePos(s.Pos, "Func.AddPipe")
	}
	f.pipes = append(f.pipes, p)
}

// Pipes are the function's pipelines in the order the producer recorded
// them: an inner pipeline (one inside a stage's argument) before the one
// that holds it.
func (f *Func) Pipes() []Pipe { return f.pipes }

// Holds reports whether p's span includes the source position (line, col).
func (p Pipe) Holds(line, col int) bool {
	s := p.Pos
	if line < s.Line() || (line == s.Line() && col < s.Col()) {
		return false
	}
	return line < s.EndLine() || (line == s.EndLine() && col <= s.EndCol())
}

// lintDebugInfo applies RuleVarTemp and RuleStmtMark.
func lintDebugInfo(f *Func, vs *[]Violation) {
	params := map[Temp]bool{}
	for _, p := range f.Params() {
		params[p.Temp] = true
	}
	for _, v := range f.vars {
		what := "var " + v.Name + " (" + v.Temp.String() + ")"
		switch {
		case v.Temp == NoTemp || int(v.Temp) > f.NumTemps():
			*vs = append(*vs, Violation{Rule: RuleVarTemp, Pos: v.Scope, What: what,
				Why: "the temporary is outside this function's namespace"})
		case !params[v.Temp] && f.Def(v.Temp) == nil:
			*vs = append(*vs, Violation{Rule: RuleVarTemp, Pos: v.Scope, What: what,
				Why: "no parameter is this temporary and no instruction writes it"})
		}
	}
	for _, p := range f.pipes {
		for _, s := range p.Stages {
			what := "pipe stage " + s.Text + " (" + s.Temp.String() + ")"
			if s.Temp == NoTemp || int(s.Temp) > f.NumTemps() || (!params[s.Temp] && f.Def(s.Temp) == nil) {
				*vs = append(*vs, Violation{Rule: RuleVarTemp, Pos: s.Pos, What: what,
					Why: "no parameter is this temporary and no instruction of this function writes it"})
			}
		}
	}
	for _, b := range f.Blocks() {
		last := -1
		for _, m := range b.stmts {
			what := b.ID().String() + " stmt at " + strconv.Itoa(m.Index)
			switch {
			case m.Index < 0 || m.Index > len(b.instrs):
				*vs = append(*vs, Violation{Rule: RuleStmtMark, Pos: m.Pos, What: what,
					Why: "the mark names no instruction of the block"})
			case m.Index < last:
				*vs = append(*vs, Violation{Rule: RuleStmtMark, Pos: m.Pos, What: what,
					Why: "the marks are not in instruction order"})
			case !m.Pos.IsValid() || m.Pos.Synthesized():
				*vs = append(*vs, Violation{Rule: RuleStmtMark, Pos: m.Pos, What: what,
					Why: "a statement mark needs a source position"})
			}
			last = m.Index
		}
	}
}
