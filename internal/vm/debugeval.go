package vm

// EVALUATING AN EXPRESSION IN A STOPPED FRAME: a watch, the debug console, a
// hover, a breakpoint's condition or a logpoint's message.
//
// The expression is lowered elsewhere (vmhost's debugeval.go) as a function
// whose parameters are the frame's variables. Scope is what it is evaluated
// against: the frame's variables and the machine the frame runs on. Run
// links the expression's lowering into a view of that machine and calls the
// function there, refusing first when it can reach an effect (Effects) and
// running under Limits, so an evaluation can neither change what the program
// does next nor hang.
//
// THE VIEW LEAVES THE MACHINE AS IT IS. Machine.Extend makes another
// lowering's module the machine's entry, which the REPL can do between
// inputs because nothing runs on its machine then. A stopped program is
// still running: its goroutines are parked mid-activation, and a test run
// boots each case from the machine's entry. So the view copies the machine's
// tables and adds the expression's modules to the copies: the expression
// calls the program's own functions, reads its forced `once` cells and binds
// its host tables, and the program never sees the expression's module. The
// view compiles what it runs into a code table of its own, without
// statement hooks, so an evaluation never stops at a breakpoint and never
// parks behind the stopped thread.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/nomi-language/nomi/internal/ir"
)

// ScopeVar is one variable visible in a frame, with its value.
type ScopeVar struct {
	Name string
	// Type is the variable's type as an annotation spells it.
	Type  string
	Value any
	// Decl is where it is bound (ir.Var.Decl).
	Decl ir.Pos
}

// Scope is a frame as an expression evaluated in it sees it.
type Scope struct {
	// Pos is where the frame is.
	Pos ir.Pos
	// Func is the frame's function, as the stack names it.
	Func string
	Vars []ScopeVar
	// Pass names the run of the program the frame is in (debugtravel.go):
	// a step back starts another, which runs from the start again.
	// Replaying says the frame is in recorded history, which already ran
	// once: a logpoint there logged it then.
	Pass      uint64
	Replaying bool
	m         *Machine
	d         *Debugger
}

// Var is the visible variable named name.
func (s *Scope) Var(name string) (ScopeVar, bool) {
	for _, v := range s.Vars {
		if v.Name == name {
			return v, true
		}
	}
	return ScopeVar{}, false
}

// Scope is the scope of a frame Stack handed out, for evaluating an
// expression in it. A frame that is a Go-driven operation (`Iter.map`) has
// none.
func (d *Debugger) Scope(frameID int) (*Scope, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if frameID < 1 || frameID > len(d.frames) {
		return nil, errors.New("vm: no frame " + fmt.Sprint(frameID) + "; the program resumed")
	}
	ref := d.frames[frameID-1]
	if ref.via || ref.fr == nil || ref.fr.c == nil || ref.fr.c.dbg == nil {
		return nil, errors.New("this frame is a call into Go, which has no variables to evaluate in")
	}
	s := &Scope{Pos: ref.pos, Func: frameName(ref.fr.fn), Vars: visibleVars(ref.fr, ref.pos, ref.point), m: ref.m, d: d}
	if ref.th != nil && ref.th.pass != nil {
		s.Pass, s.Replaying = ref.th.pass.id, ref.th.pass.replaying.Load()
	}
	return s, nil
}

// EffectsError is Run's refusal of an expression that can reach an effect.
type EffectsError struct {
	// Effects names each effect reached, sorted.
	Effects []string
}

func (e *EffectsError) Error() string {
	return "the expression has effects:\n  " + strings.Join(e.Effects, "\n  ")
}

// UnretainedError is Run's refusal of an expression that reaches code the
// VM cannot run.
type UnretainedError struct {
	Found []Unretained
}

func (e *UnretainedError) Error() string {
	var names []string
	for _, u := range e.Found {
		names = append(names, u.Name)
	}
	return "the expression reaches code the VM cannot run: " + strings.Join(names, ", ")
}

// Run calls f, the expression's function in the lowering entry and rest,
// with args, on a view of the scope's machine (the file comment) and under
// lim. Unless allowEffects, an f that can reach an effect is refused with an
// *EffectsError before anything runs. It answers f's result, ErrLimit when
// lim ran out, or the expression's own failure.
func (s *Scope) Run(entry *ir.Module, rest []*ir.Module, f *ir.Func, args []any, lim Limits, allowEffects bool) (any, error) {
	if s.m == nil {
		return nil, errors.New("vm: the frame's machine is gone")
	}
	view := s.m.evalView(&s.d.views, entry, rest)
	if found := view.Unretained([]*ir.Func{f}, nil); len(found) > 0 {
		return nil, &UnretainedError{Found: found}
	}
	if !allowEffects {
		if effects := view.Effects(f); len(effects) > 0 {
			return nil, &EffectsError{Effects: effects}
		}
	}
	return view.Evaluate(context.Background(), f, args, lim)
}

// evalViewKey names a view evalView built: the machine by its code table,
// which every view of one machine shares, and the expression by its entry.

type evalViewKey struct {
	codes *codeTable
	entry *ir.Module
}

// evalView is a view of m that also links entry and rest, leaving m's own
// tables as they are. See the file comment.
func (m *Machine) evalView(views *sync.Map, entry *ir.Module, rest []*ir.Module) *Machine {
	key := evalViewKey{codes: m.codes, entry: entry}
	if v, ok := views.Load(key); ok {
		return v.(*Machine)
	}
	v := *m
	v.dbg, v.dvia, v.depth, v.hostFrame, v.fuel = nil, "", 0, nil, nil
	v.codes = &codeTable{}
	v.tasks = &taskLimit{}
	v.link = cloneMap(m.link)
	v.ambiguous = cloneMap(m.ambiguous)
	v.modules = cloneMap(m.modules)
	v.onces = cloneMap(m.onces)
	v.testBoots = cloneMap(m.testBoots)
	v.displays = cloneMap(m.displays)
	v.rowDebugs = cloneMap(m.rowDebugs)
	v.equates = cloneMap(m.equates)
	v.hashes = cloneMap(m.hashes)
	v.dispatch = make(map[*ir.Symbol]map[string]*ir.Symbol, len(m.dispatch))
	for method, byType := range m.dispatch {
		v.dispatch[method] = cloneMap(byType)
	}
	if v.link == nil {
		v.link = map[*ir.Symbol]*ir.Func{}
	}
	if v.modules == nil {
		v.modules = map[*ir.Module]bool{}
	}
	if m.adapters != nil {
		v.adapters = &hostTables{root: &v, tables: append([]HostTable(nil), m.adapters.tables...), lookup: m.adapters.lookup}
	}
	// The program's entry becomes an ordinary linked module, as Extend makes
	// a REPL's previous entry, so its functions resolve from the view.
	if m.mod != nil && m.mod != entry {
		v.linkFuncs(m.mod)
	}
	v.mod = entry
	for _, mod := range append([]*ir.Module{entry}, rest...) {
		if mod == nil || v.modules[mod] {
			continue
		}
		v.modules[mod] = true
		v.addOnceCells(mod)
		v.addImpls(mod)
		if mod != entry {
			v.linkFuncs(mod)
		}
	}
	got, _ := views.LoadOrStore(key, &v)
	return got.(*Machine)
}

func cloneMap[K comparable, V any](in map[K]V) map[K]V {
	if in == nil {
		return nil
	}
	out := make(map[K]V, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// ScopeOf is the scope of the activation a breakpoint hook stopped in, for
// a condition or a logpoint evaluated before the thread decides to stop.
func (d *Debugger) scopeOf(fr *frame, site *stmtSite, m *Machine) *Scope {
	s := &Scope{Pos: site.pos, Func: frameName(fr.fn), Vars: visibleVars(fr, site.pos, site.point), m: m, d: d}
	if p := fr.stk.thread.pass; p != nil {
		s.Pass, s.Replaying = p.id, p.replaying.Load()
	}
	return s
}

// LineScope is the variables, without values, visible where a breakpoint on
// file:line lands, which its condition is checked against before the
// program reaches it, and whether it lands at all. A line that several
// functions share (a generic function's instances) answers the first's.
func (d *Debugger) LineScope(file string, line int) (Scope, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	mf, at, ok := d.landing(file, line)
	if !ok {
		return Scope{}, false
	}
	var best *markLine
	for i := range d.marks[mf] {
		mk := &d.marks[mf][i]
		if mk.line == at && mk.fn != nil && (best == nil || mk.pos.Col() < best.pos.Col()) {
			best = mk
		}
	}
	if best == nil {
		return Scope{}, false
	}
	return staticScope(best.fn, best.pos), true
}

// staticScope is the variables of f, without values, visible at pos.
func staticScope(f *ir.Func, pos ir.Pos) Scope {
	visible := map[string]ir.Var{}
	var order []string
	for _, v := range f.Vars() {
		if !v.Visible(pos.Line(), pos.Col()) {
			continue
		}
		if prev, has := visible[v.Name]; has && !startsAfter(v.Scope, prev.Scope) {
			continue
		} else if !has {
			order = append(order, v.Name)
		}
		visible[v.Name] = v
	}
	s := Scope{Pos: pos, Func: frameName(f)}
	for _, name := range order {
		s.Vars = append(s.Vars, ScopeVar{Name: name, Type: visible[name].Type, Decl: visible[name].Decl})
	}
	return s
}
