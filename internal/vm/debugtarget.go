package vm

// STEP INTO A TARGET: the calls a stopped frame's statement makes that a
// step in could enter, and a step in that runs until one of them is
// entered.
//
// # The statement's calls
//
// A statement is the instructions from the mark the frame stopped at up to
// the next mark that is not a pipe stage, following jumps and branches
// (StatementInstrs): an `if`'s condition is its statement, and each arm is
// its own. Within them a target is:
//
//   - a direct call to a function with Nomi statements, named as the stack
//     names it (`double`, `Account.deposit`, `maybe.Maybe.map<Int, Int>`);
//   - a dispatched call, named by its interface method (`Display.to_string`),
//     which any impl the program has enters;
//   - a call through a function value whose function is known where the
//     frame stopped, named by the variable that holds it;
//   - a callback: a lambda or a named function passed to a call or an
//     `Iter` operation of the statement, named by both as source code
//     spells them, `Iter.map(|n| ...) callback`, `Maybe.map(double)
//     callback`.
//
// A call into Go (a host function) is not a target itself; a callback it
// is passed is. While steps skip a file (SetSkipFiles: the standard
// library, unless justMyCode is off), a function there is not a target. The
// targets are in the order they are entered: a callback at the call or
// operation it is passed to. Two targets with one label are told apart by
// the call's ordinal (`double (call 2)`).
//
// # Stepping into one
//
// StepInto resumes the program, and stops the thread at the first
// statement of the first activation of the target's functions that the
// stopped activation starts, directly or through Go (a callback's parent is
// the activation that forced its pipeline). For the Nth call to one
// function, the Nth such activation, counting none started inside another.
// A breakpoint or an exception stops the program first, as it would a step
// in. When the statement finishes without entering the target (an empty
// list's callback is never called), the thread stops where a step over
// would have, and the stop says the target was not entered.

import (
	"errors"
	"sort"
	"strconv"
	"strings"

	"github.com/nomi-language/nomi/internal/ir"
)

// StepTarget is one call a stopped frame's statement makes that a step in
// could enter.
type StepTarget struct {
	ID    int
	Label string
	// Pos is where the call, the lambda or the named function is written.
	Pos ir.Pos
}

// stepTarget is a StepTarget with what entering it means.
type stepTarget struct {
	StepTarget
	// origin is the stopped activation, and code its function's code then:
	// a tail call that replaces origin's function enters a callee too.
	origin *frame
	code   *code
	funcs  map[*ir.Func]bool
	// nth is which activation of funcs, counted among those origin starts
	// and none started inside another, enters the target.
	nth int
	// gen is the resume count the target was found at.
	gen uint64
}

// StepInTargets are the targets of the statement a frame Stack handed out
// is at. Only the innermost frame of a stopped thread has any: an outer
// frame is inside a call already.
func (d *Debugger) StepInTargets(frameID int) ([]StepTarget, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if frameID < 1 || frameID > len(d.frames) {
		return nil, errors.New("vm: no frame " + strconv.Itoa(frameID) + "; the program resumed")
	}
	ref := d.frames[frameID-1]
	if ref.via || ref.fr == nil || ref.th == nil || ref.th.at != ref.fr || ref.fr.c == nil || ref.fr.c.dbg == nil ||
		ref.m == nil {
		return nil, nil
	}
	found := d.findTargets(ref)
	d.targets = found
	out := make([]StepTarget, len(found))
	for i, t := range found {
		out[i] = t.StepTarget
	}
	return out, nil
}

// targetSite is one target before it is numbered.
type targetSite struct {
	at    int
	label string
	pos   ir.Pos
	funcs map[*ir.Func]bool
}

// findTargets lists the targets of the statement ref is at. d.mu is held.
func (d *Debugger) findTargets(ref frameRef) []*stepTarget {
	fr, m := ref.fr, ref.m
	f := fr.c.fn
	instrs := StatementInstrs(f, ref.point.block, ref.point.index)
	// Where each temporary's value goes, following copies.
	users := map[ir.Temp][]int{}
	var uses []ir.Temp
	for i, in := range instrs {
		uses = in.AppendUses(uses[:0])
		for _, t := range uses {
			users[t] = append(users[t], i)
		}
	}
	var consumers func(t ir.Temp, seen map[ir.Temp]bool) []int
	consumers = func(t ir.Temp, seen map[ir.Temp]bool) []int {
		if seen[t] {
			return nil
		}
		seen[t] = true
		var out []int
		for _, i := range users[t] {
			if cp, isCopy := instrs[i].(*ir.Copy); isCopy {
				out = append(out, consumers(cp.Dst(), seen)...)
				continue
			}
			out = append(out, i)
		}
		return out
	}
	defined := fr.c.dbg.defined(f)
	var sites []targetSite
	callback := func(i int, text string, body *ir.Func, pos ir.Pos) {
		if body == nil || !d.mine(body) {
			return
		}
		for _, c := range consumers(instrs[i].Dst(), map[ir.Temp]bool{}) {
			var what string
			switch u := instrs[c].(type) {
			case *ir.Iter:
				what = "Iter." + u.Op().Name()
			case *ir.Call:
				if u.Form() == ir.CalleeIndirect && u.Fn() == instrs[i].Dst() {
					// Called right here, not passed on.
					sites = append(sites, targetSite{at: c, label: text, pos: pos, funcs: set(body)})
					continue
				}
				what = calleeName(m, u)
			default:
				continue
			}
			sites = append(sites, targetSite{at: c, label: what + "(" + text + ") callback", pos: pos, funcs: set(body)})
		}
	}
	for i, in := range instrs {
		if !in.Pos().IsValid() || in.Pos().Synthesized() {
			continue
		}
		switch n := in.(type) {
		case *ir.Call:
			switch n.Form() {
			case ir.CalleeDirect:
				if n.Crosses() {
					continue
				}
				callee, err := m.resolveFunc(n.Callee(), "")
				if err != nil || !d.mine(callee) || !hasStmts(callee) {
					continue
				}
				sites = append(sites, targetSite{at: i, label: frameName(callee), pos: n.Pos(), funcs: set(callee)})
			case ir.CalleeDispatched:
				funcs := map[*ir.Func]bool{}
				for _, impl := range m.dispatch[n.Callee()] {
					if callee, err := m.resolveFunc(impl, ""); err == nil && d.mine(callee) && hasStmts(callee) {
						funcs[callee] = true
					}
				}
				if len(funcs) > 0 {
					sites = append(sites, targetSite{at: i, label: n.Callee().Name(), pos: n.Pos(), funcs: funcs})
				}
			case ir.CalleeIndirect:
				if fv, isLocal := f.Def(n.Fn()).(*ir.FuncValue); isLocal && instrsHold(instrs, fv) {
					continue // a lambda called where it is made: its FuncValue lists it
				}
				if !defined.Before(ref.point.block, ref.point.index, n.Fn()) {
					continue
				}
				v, err := fr.read(n.Fn())
				body := funcOf(v)
				if err != nil || body == nil || !d.mine(body) || !hasStmts(body) {
					continue
				}
				label := frameName(body)
				for _, vr := range f.Vars() {
					if vr.Temp == n.Fn() {
						label = vr.Name
					}
				}
				sites = append(sites, targetSite{at: i, label: label, pos: n.Pos(), funcs: set(body)})
			}
		case *ir.FuncValue:
			callback(i, lambdaText(n), n.Body(), n.Pos())
		case *ir.Ref:
			if n.Kind() != ir.RefFunc {
				continue
			}
			if body, err := m.resolveFunc(n.Sym(), ""); err == nil && hasStmts(body) {
				callback(i, frameName(body), body, n.Pos())
			}
		}
	}
	sort.SliceStable(sites, func(i, j int) bool { return sites[i].at < sites[j].at })
	var out []*stepTarget
	labels := map[string]int{}
	for _, s := range sites {
		labels[s.label]++
		label := s.label
		if n := labels[s.label]; n > 1 {
			label += " (call " + strconv.Itoa(n) + ")"
		}
		nth := 1
		for _, prev := range out {
			if overlaps(prev.funcs, s.funcs) {
				nth++
			}
		}
		out = append(out, &stepTarget{StepTarget: StepTarget{ID: len(out) + 1, Label: label, Pos: s.pos},
			origin: fr, code: fr.c, funcs: s.funcs, nth: nth, gen: d.gen})
	}
	return out
}

// StatementInstrs are the instructions of the statement f is at before
// instruction index of block: from there to the next statement mark that
// is not a pipe stage, following jumps and branches, in block order.
func StatementInstrs(f *ir.Func, block ir.BlockID, index int) []ir.Instr {
	var out []ir.Instr
	seen := map[ir.BlockID]bool{}
	var walk func(id ir.BlockID, from int, first bool)
	walk = func(id ir.BlockID, from int, first bool) {
		b := f.Block(id)
		if b == nil || (seen[id] && !first) {
			return
		}
		seen[id] = true
		instrs := b.Instrs()
		for i := from; i <= len(instrs); i++ {
			for _, mk := range b.Stmts() {
				if mk.Index == i && mk.Kind != ir.StmtStage && !(first && i == from) {
					return
				}
			}
			if i < len(instrs) {
				out = append(out, instrs[i])
			}
		}
		if b.Term() == nil {
			return
		}
		for _, next := range b.Term().AppendSuccessors(nil) {
			walk(next, 0, false)
		}
	}
	walk(block, index, true)
	return out
}

// mine reports whether a step may end in f's file.
func (d *Debugger) mine(f *ir.Func) bool {
	return f != nil && (d.skip == nil || !d.skip(f.Pos().File()))
}

// hasStmts reports whether f has a statement a step can stop at.
func hasStmts(f *ir.Func) bool {
	_, ok := entryStmt(f)
	return ok
}

func set(f *ir.Func) map[*ir.Func]bool { return map[*ir.Func]bool{f: true} }

func overlaps(a, b map[*ir.Func]bool) bool {
	for f := range a {
		if b[f] {
			return true
		}
	}
	return false
}

func instrsHold(instrs []ir.Instr, in ir.Instr) bool {
	for _, x := range instrs {
		if x == in {
			return true
		}
	}
	return false
}

// funcOf is the Nomi function a function value runs, or nil.
func funcOf(v any) *ir.Func {
	if fv, isFunc := v.(*functionValue); isFunc && fv.host == nil {
		return fv.body
	}
	return nil
}

// calleeName is how a call a callback is passed to is named.
func calleeName(m *Machine, c *ir.Call) string {
	if c.Form() == ir.CalleeIndirect {
		return "call"
	}
	if c.Form() == ir.CalleeDirect && !c.Crosses() {
		if callee, err := m.resolveFunc(c.Callee(), ""); err == nil {
			name, _, _ := spellings(callee)
			return name
		}
	}
	return c.Callee().Name()
}

// lambdaText is a lambda as a target names it: its parameters, `|n| ...`.
func lambdaText(fv *ir.FuncValue) string {
	body := fv.Body()
	params := body.Params()
	names := make([]string, 0, fv.Arity())
	for i := 0; i < fv.Arity() && i < len(params); i++ {
		name := "_"
		for _, v := range body.Vars() {
			if v.Temp == params[i].Temp {
				name = v.Name
				break
			}
		}
		names = append(names, name)
	}
	if body.Name() != "lambda" {
		// A nested `fn` passed by name.
		return frameName(body)
	}
	return "|" + strings.Join(names, ", ") + "| ..."
}

// StepInto resumes every thread, and stops thread when it enters the target
// StepInTargets listed under id (the file comment).
func (d *Debugger) StepInto(thread, id int) error {
	d.mu.Lock()
	var t *stepTarget
	if id >= 1 && id <= len(d.targets) && d.targets[id-1].gen == d.gen {
		t = d.targets[id-1]
	}
	th := d.threadByID(thread)
	if t == nil || th == nil || th.at != t.origin {
		d.mu.Unlock()
		return errors.New("no step-in target " + strconv.Itoa(id) + " for this thread's current statement; ask for the targets again")
	}
	d.mu.Unlock()
	d.resumeTarget(thread, t)
	return nil
}

// targetStop decides a thread stepping into a target at a statement of fr:
// whether it stops, and why when the statement finished without entering
// it. d.mu is held.
func (d *Debugger) targetStop(th *thread, fr *frame, kind ir.StmtKind, entered bool) (bool, string) {
	t := th.step.target
	replaced := fr == t.origin && fr.c != t.code
	if entered && t.funcs[fr.fn] && (replaced || t.below(fr)) {
		th.step.seen++
		if th.step.seen == t.nth {
			return true, ""
		}
		return false, ""
	}
	if (fr.ldepth <= th.step.depth && kind != ir.StmtStage) || replaced {
		if d.skip != nil && d.skip(fr.fn.Pos().File()) {
			return false, ""
		}
		return true, "Step into " + t.Label + ": the statement finished without entering it, so the step stopped " +
			"at the next statement."
	}
	return false, ""
}

// below reports whether fr was started by the target's origin activation,
// directly or through Go, with no activation of the target's functions
// between them.
func (t *stepTarget) below(fr *frame) bool {
	for p := fr.parent; p != nil; p = p.parent {
		if p == t.origin {
			return true
		}
		if t.funcs[p.fn] {
			return false
		}
	}
	return false
}
