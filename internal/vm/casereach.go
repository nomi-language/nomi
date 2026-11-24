package vm

import (
	"slices"
	"strings"

	"github.com/nomi-language/nomi/internal/ir"
)

// WHAT ONE TEST CASE RUNS, for a runner that does not run again a case whose
// code has not changed since it passed (vmhost's stored passes, testpass.go).
//
// ReachCase is Unretained's walk from one case: its body, its group's boot
// and the startup the boot is given. With what the case reaches that this
// machine cannot run, it answers the effects that code can have, in
// Effects' sense (a crossing its declaration does not mark pure, `dbg`
// among them), the hashes keyed by the run's seed it can read
// (Effects.Seeded), and the code itself as parts: each function the walk
// reached, keyed by its identity across lowerings (ir.FuncKey), with the
// SHA-256 of its code without source positions (ir.BodySum). A function
// with no linkage name (the case's body, a lambda) is keyed by its name and
// its file, and its code is also in the sum of each function that makes a
// value of it.
//
// Positions are left out so that code that only moved, a line inserted
// above it, sums as it did. A run hands the program a position as a value
// in one place: a failed assertion's line, in the AssertionFailure a
// `check` answers, and an `assert`, `refute` or `subtests` in an ordinary
// function returns as its `Err`. Those lines are a part's Lines. In the
// case's own body, and the lambdas it makes, an `assert`'s failure is the
// case's failure, which no stored pass holds, so only a `check`'s line is
// kept there.
//
// The same walk answers which cases can run a function (`nomi test
// --reaching`): FuncsAt finds the functions a source line is in, and
// CaseReach.Reaches whether the walk reached one.

// CaseReach is what one test case's walk found (ReachCase).
type CaseReach struct {
	// Unretained is what the case reaches that this machine cannot run, as
	// Unretained answers it.
	Unretained []Unretained
	// Effects is each effect the case's code can have, "F calls K", sorted.
	Effects []string
	// Seeded is each call the case's code can make into a pure crossing
	// whose answer is a hash keyed by the run's seed, "F calls K", sorted
	// (Effects.Seeded).
	Seeded []string
	// Parts is the code the case can run, sorted by key, sum and lines, so
	// one program's walk answers them in one order.
	Parts []CodePart
	// funcs is every function the walk reached (Reaches).
	funcs map[*ir.Func]bool
}

// Pure reports whether the case's result is decided by its code alone: it
// has no effect and reads no hash keyed by the run's seed, so code that did
// not change answers as it did, in this process or another.
func (r CaseReach) Pure() bool { return len(r.Effects) == 0 && len(r.Seeded) == 0 }

// Reaches reports whether the case's walk reached any of fns: whether its
// code can run one of them.
func (r CaseReach) Reaches(fns []*ir.Func) bool {
	for _, f := range fns {
		if r.funcs[f] {
			return true
		}
	}
	return false
}

// FuncsAt is the functions of mods a line of file is in: of those declared
// in file whose code spans line, from their declaration's line to the last
// line their instructions and the lambdas they make reach, the ones
// declared last, so a function nested in another answers for its own
// lines. Each instance of a generic function is one of them.
func FuncsAt(mods []*ir.Module, file string, line int) []*ir.Func {
	var out []*ir.Func
	best := 0
	for _, m := range mods {
		for _, f := range m.Funcs() {
			start := f.Pos().Line()
			if start > line || start < best || !sameFile(f.Pos().File(), file) || lastLine(f, f.Pos().File(), map[*ir.Func]bool{}) < line {
				continue
			}
			if start > best {
				best, out = start, nil
			}
			out = append(out, f)
		}
	}
	return out
}

// lastLine is the last line of file that f's instructions and the bodies of
// the lambdas it makes reach.
func lastLine(f *ir.Func, file string, seen map[*ir.Func]bool) int {
	seen[f] = true
	last := f.Pos().Line()
	for _, b := range f.Blocks() {
		for _, in := range b.Instrs() {
			if p := in.Pos(); p.File() == file && p.EndLine() > last {
				last = p.EndLine()
			}
			if v, ok := in.(*ir.FuncValue); ok && !seen[v.Body()] {
				last = max(last, lastLine(v.Body(), file, seen))
			}
		}
	}
	return last
}

// CodePart is one function a case can run.
type CodePart struct {
	// Key names the function across lowerings: its linkage name, or for a
	// function with none, its name and file (partKey).
	Key string
	// Sum is ir.BodySum of the function.
	Sum [32]byte
	// Lines are the source lines of the function's assertions whose failure
	// is a value the program reads, in instruction order.
	Lines []int
}

// CodeSums memoizes each function's sum and assertion lines over the cases
// of one program, which share most of the code they reach.
type CodeSums struct {
	funcs map[*ir.Func]*funcCode
}

// NewCodeSums is an empty CodeSums.
func NewCodeSums() *CodeSums { return &CodeSums{funcs: map[*ir.Func]*funcCode{}} }

// funcCode is one function's part before the case decides which of its
// assertion lines count.
type funcCode struct {
	key string
	sum [32]byte
	// checks are the lines of its `check`s; rest the lines of its other
	// assertions and `subtests` statements.
	checks, rest []int
}

func (s *CodeSums) of(f *ir.Func) *funcCode {
	if c := s.funcs[f]; c != nil {
		return c
	}
	c := &funcCode{key: partKey(f), sum: ir.BodySum(f)}
	for _, b := range f.Blocks() {
		for _, in := range b.Instrs() {
			switch n := in.(type) {
			case *ir.Assert:
				if n.Keyword() == ir.KeywordCheck {
					c.checks = append(c.checks, n.Pos().Line())
				} else {
					c.rest = append(c.rest, n.Pos().Line())
				}
			case *ir.Subtests:
				c.rest = append(c.rest, n.Pos().Line())
				if n.Name() != ir.NoTemp {
					c.rest = append(c.rest, n.NameLine())
				}
			}
		}
	}
	s.funcs[f] = c
	return c
}

// partKey is f's key among a case's parts: ir.FuncKey, with the file of a
// function that has no linkage name, so two lambdas of one name in
// different files stay apart.
func partKey(f *ir.Func) string {
	key := ir.FuncKey(f)
	if f.Sym() == nil || f.Sym().Link() == "" {
		key += " in " + f.Pos().File()
	}
	return key
}

// ReachCase walks from c as Unretained does and answers what it found
// (CaseReach), each function's sum read from sums. With nil sums it
// answers no parts.
func (m *Machine) ReachCase(c ir.TestCase, sums *CodeSums) CaseReach {
	var boots []*ir.Symbol
	if b := c.Group().Boot; b != nil {
		boots = append(boots, b, c.Group().Startup)
	}
	w := reachWalk{m: m, seen: map[*ir.Func]bool{}, found: map[string]Unretained{},
		parent: map[*ir.Func]*ir.Func{}, effects: map[string]bool{}, unbounded: map[string]bool{},
		seeded: map[string]bool{}}
	reached, found := w.run([]*ir.Func{c.Fn()}, boots)
	out := CaseReach{Unretained: found, Effects: sortedKeys(w.effects), Seeded: sortedKeys(w.seeded), funcs: reached}
	if len(found) > 0 || sums == nil {
		return out
	}
	body := caseBody(c.Fn())
	for f := range reached {
		fc := sums.of(f)
		part := CodePart{Key: fc.key, Sum: fc.sum}
		if body[f] {
			part.Lines = fc.checks
		} else {
			part.Lines = append(slices.Clone(fc.checks), fc.rest...)
			slices.Sort(part.Lines)
		}
		out.Parts = append(out.Parts, part)
	}
	slices.SortFunc(out.Parts, func(a, b CodePart) int {
		if k := strings.Compare(a.Key, b.Key); k != 0 {
			return k
		}
		if k := slices.Compare(a.Sum[:], b.Sum[:]); k != 0 {
			return k
		}
		return slices.Compare(a.Lines, b.Lines)
	})
	return out
}

// caseBody is a case's body and every lambda it makes, at any depth: the
// code whose `assert` fails the case rather than answering a value.
func caseBody(fn *ir.Func) map[*ir.Func]bool {
	body := map[*ir.Func]bool{fn: true}
	queue := []*ir.Func{fn}
	for len(queue) > 0 {
		f := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		for _, b := range f.Blocks() {
			for _, in := range b.Instrs() {
				if v, ok := in.(*ir.FuncValue); ok && !body[v.Body()] {
					body[v.Body()] = true
					queue = append(queue, v.Body())
				}
			}
		}
	}
	return body
}
