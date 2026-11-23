package vmhost

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/analyzedlowering"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/effects"
	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/std"
)

// EffectNotAllowedCode is the Code of the diagnostic for an effect
// nomi.toml's effects tables, or a host's lists, do not allow. Its
// Args are the call's name (`std::fs`); the list it is over: `self` for
// [effects.self], `self.modules` for the entry of the project's module
// under [effects.self.modules], `deps` for [effects.deps],
// `deps.modules` for the module's entry under [effects.deps.modules],
// or `host` for the host's list; the module whose files hold the call, by
// its path, the project's own for its own files, and for `self.modules`
// the entry's key; and then each name that list allows. Over nomi.toml's
// tables (not a host's), it carries the fix that adds what its help says
// to add, an edit to nomi.toml (parser.Fix's Path).
const EffectNotAllowedCode = "effect-not-allowed"

// EffectNotDeclaredCode is the Code of the diagnostic for an effect
// in a dependency's files that the [effects.self] in the
// dependency's own nomi.toml does not allow (OverDeclared): the module
// declares fewer effects than its code has, and the fix is in that module. Its
// Args are EffectNotAllowedCode's, with the list `self`: the call's
// name, `self`, the module, and each std name its own std holds; or, over its entry for the file's module under its own
// [effects.self.modules], the list `self.modules` and each name that
// entry holds.
const EffectNotDeclaredCode = "effect-not-declared"

// EffectsNotDeterminedCode is the Code of the diagnostic for a public
// generic function of the project's own files whose effects the compiler
// cannot tell for every type it may be called at (irbuild's abstract.go:
// its abstract body did not lower), while [effects.self] holds the
// project's code to a list. Its Args are the function's name and `self`.
const EffectsNotDeterminedCode = "effects-not-determined"

// effectProblems is the program held to its nomi.toml effect
// tables, and to the host's list when the host gave one
// (WithEffects), from the roots in rs: a diagnostic for each list
// entry that names nothing (effectListProblems), or, when every entry
// names something, one for each effect call the roots reach that a list
// does not allow, each call once per list, in position order, with the
// calls. Nil when there are no lists, and then nothing is walked.
func (p *Program) effectProblems(rs ...Roots) (Diagnostics, []NotAllowed) {
	if p.effects == nil && p.hostEffects() == nil {
		return nil, nil
	}
	return p.walkedProblems(p.walks(rs))
}

// walks is the effect calls each of rs reaches, one walk each
// (EffectCalls).
func (p *Program) walks(rs []Roots) []Walk {
	out := make([]Walk, len(rs))
	for i, r := range rs {
		out[i] = p.walk(r)
	}
	return out
}

// walkedProblems is effectProblems over walks already walked.
func (p *Program) walkedProblems(walks []Walk) (Diagnostics, []NotAllowed) {
	host := p.hostEffects()
	if p.effects == nil && host == nil {
		return nil, nil
	}
	if ds := p.effectListProblems(); len(ds) > 0 {
		return ds, nil
	}
	var ds Diagnostics
	var calls []NotAllowed
	seen := map[string]bool{}
	fixes := map[string]*parser.Fix{}
	own := map[string]bool{}
	add := func(n NotAllowed, w Walk) {
		key := fmt.Sprint(n.Site, "\x00", n.Name, "\x00", n.Host)
		if seen[key] {
			return
		}
		seen[key] = true
		calls = append(calls, n)
		d := p.notAllowedDiagnostic(n, w.Tests, fixes)
		d.Related = p.leadsTo(n, w, own)
		ds = append(ds, d)
	}
	public := false
	for _, w := range walks {
		var declared map[string]*analysis.SelfEffects
		if p.effects != nil {
			declared = p.declaredLists(w.Calls)
		}
		for _, n := range notAllowedBy(w.Calls, w.Tests, p.effects, declared) {
			add(n, w)
		}
		for _, n := range notAllowedBy(w.Calls, w.Tests, host, nil) {
			n.Host = true
			add(n, w)
		}
		public = public || w.public
	}
	if public && p.effects != nil && p.effects.Self != nil {
		ds = append(ds, p.undeterminedDiagnostics()...)
	}
	return ds, calls
}

// undeterminedDiagnostics is an error at each public function of the
// project's own files (publicFuncs) whose abstract body did not lower, so
// the walk from it cannot say which effects it has for every type it may
// be called at, and [effects.self] cannot hold it.
func (p *Program) undeterminedDiagnostics() Diagnostics {
	_, declines := p.res.Abstract()
	if len(declines) == 0 {
		return nil
	}
	files := make([]string, len(declines))
	for i, d := range declines {
		files[i] = d.At.File()
	}
	public := p.publicAt(files)
	var ds Diagnostics
	for _, d := range declines {
		path, line, col := d.At.File(), d.At.Line(), d.At.Col()
		if !public(path, line, col) {
			continue
		}
		diag := frontend.NewDiagnostic(path, p.sources[path], line, col,
			fmt.Sprintf("the effects of `%s` for every type it may be called at are not known, so [effects.self] cannot hold it", d.Fn))
		diag.Code = EffectsNotDeterminedCode
		diag.Args = []string{d.Fn, string(OverSelf)}
		diag.Hints = append(diag.Hints, d.Reason,
			"a call that gives it types is checked at those types")
		ds = append(ds, diag)
	}
	return ds
}

// editorWalks is checked, the walks `nomi check` holds to the lists
// (checkedRoots), then the walk from every function of the program's own
// lowering that they do not reach (Roots.Besides): what an editor's hover
// and marks say, so a function nothing calls yet shows its effects too.
// The lists, `nomi effects`, a build and a test's reach read the checked
// walks alone.
func (p *Program) editorWalks(checked []Walk) []Walk {
	return append(slices.Clip(checked), p.walk(Roots{Besides: checked}))
}

// reachedCalls is what an editor's hover says of the effects code has
// (analyzedlowering.Reached), from walks. First each effect call
// walks reach, once per call and name, in the walks' order: where a
// diagnostic about it points and its callee's span (shownSpan), and the
// line saying the effect it has and whether nomi.toml's lists allow it
// (effects.ReachedLine), as standingsOf decides. Then the lines of the
// functions the calls are in, and of those that call them, and of the
// calls and functions that can act with no effect a list names
// (reachedFuncs). A call a walk's lists refuse reads as refused, whatever
// the other walks decide, except that a line that fills changes no line
// an earlier walk made, and is marked Fills, which a hover shows only
// where another program's lowering has no line (hoverdoc.AppendReach): a
// walk that fills (Walk.fills), and a call only a public function reaches
// (vm.EffectWalk.FromLate), which a program that calls the function says
// more of.
//
// A line is Marked, drawn bold, at a call that can act
// (vm.EffectWalk.ActCalls, actingCalls): what the compiler cannot show
// is pure. So the read of a `once` (`io.stdout`), a std function named
// as a value and a `dbg`, whose keyword already shows its effect, have
// their line and no mark.
func (p *Program) reachedCalls(walks []Walk) []analyzedlowering.Reached {
	type key struct {
		at   ir.Pos
		name string
	}
	acting, actOrder := p.actingCalls(walks)
	index := map[key]int{}
	var out []analyzedlowering.Reached
	called := map[span]bool{}
	listed := p.effects != nil
	standings := make([][]standing, len(walks))
	for wi, w := range walks {
		if listed {
			standings[wi] = standingsOf(w.Calls, w.Tests, p.effects, p.declaredLists(w.Calls))
		}
		for i, c := range w.Calls {
			s := p.spanOf(c.at)
			a := acting[s]
			if a != nil && a.some != "" {
				// The line that it may act stands for the call (reachedFuncs).
				continue
			}
			r := reachedOf(c, standings[wi], i)
			text := effects.ReachedLine(r, listed)
			fills := w.fills || w.reach.CallFromLate(i)
			k := key{c.at, c.Name}
			if j, seen := index[k]; seen {
				if !r.Allowed && !fills {
					out[j].Text = text
				}
				out[j].Fills = out[j].Fills && fills
				continue
			}
			index[k] = len(out)
			called[s] = true
			out = append(out, analyzedlowering.Reached{Path: s.path, Line: s.line, Col: s.col, EndLine: s.endLine, EndCol: s.endCol,
				Text: text, Marked: a != nil, Fills: fills})
		}
	}
	return append(out, p.reachedFuncs(walks, standings, acting, actOrder, called)...)
}

// reachedOf is how standings, the standings of a walk's calls, nil when
// the program has no lists, stand on c, its call at i, as a hover says it.
func reachedOf(c EffectCall, standings []standing, i int) effects.Reached {
	r := effects.Reached{Name: c.Name, Allowed: true}
	if standings != nil {
		s := standings[i]
		r.Allowed, r.In = !s.refused, s.in
		if s.refused && s.List == OverDeclared {
			r.Own = c.Module
		}
	}
	return r
}

// span is where a hover shows a line: a file, as the program names it,
// and the callee's or the name's span (shownSpan).
type span struct {
	path                       string
	line, col, endLine, endCol int
}

// spanOf is the span a report shows the call or read at at (shownSpan).
func (p *Program) spanOf(at ir.Pos) span {
	path, line, col, endLine, endCol := p.shownSpan(at)
	return span{path, line, col, endLine, endCol}
}

// actingCall is what the walks say of one call that can act
// (vm.ActCall): the interface function, for a call only some of whose
// implementations can, and whether only a walk that fills, or a public
// function alone, reaches it.
type actingCall struct {
	some  string
	fills bool
}

// actingCalls is each call walks reach that can act (vm.EffectWalk.ActCalls),
// by its callee's span, with the spans in the order the walks found them.
// A call with no call syntax where it is, whose span is a point
// (shownSpan), is one the builder made where the source names a function
// as a value (`io.print` passed on), and is no call. A `dbg` is left out
// too: its keyword is the effect, in plain sight on its line, so it is
// not marked, though its hover names the effect (reachedCalls). A call
// of a function that holds one is in acting, as any call that can act is.
func (p *Program) actingCalls(walks []Walk) (map[span]*actingCall, []span) {
	out := map[span]*actingCall{}
	var order []span
	for _, w := range walks {
		if w.reach == nil {
			continue
		}
		for _, c := range w.reach.ActCalls() {
			s := p.spanOf(c.At)
			if s.endLine == s.line && s.endCol == s.col || p.dbgAt(c.At) {
				continue
			}
			fills := w.fills || w.reach.FromLate(c.In)
			if a := out[s]; a != nil {
				a.fills = a.fills && fills
				continue
			}
			out[s] = &actingCall{some: c.Some, fills: fills}
			order = append(order, s)
		}
	}
	return out, order
}

// reachedFuncs is the hover lines of the functions of the program and its
// dependencies that can have effects: for each function a
// walk reached whose edges lead to an effect call (vm.EffectWalk.Reaches),
// the line saying the effects it can have, through which functions, and whether
// nomi.toml's lists allow it (effects.FunctionReach), on its
// declaration's name and on each call of it, or use of it as a value, in
// code outside std; for a `once`'s initializer, on the once's name and on
// each read of it. Each name counts once a function: the call of it the
// fewest edges away, unless a list refuses one, in which case the line
// says refused and goes through the refused one, as a call's line does;
// the tables allowing it in one module's files and in another's are named
// together. A generic function's instances share a declaration, whose line
// holds what any of them can reach, and a call of several functions (a
// call through an interface) holds what any of them can. A generic
// function no call instantiates has no instance, and so no line. A
// function with no declaration in source (a lambda, a synthesized body)
// has none. A walk that fills, and a function only a public function
// reaches, add lines only at the places no earlier walk gave one, so
// their lists' standing never changes a line theirs made, and the line is
// marked Fills while only they give it.
//
// A function that can act (vm.EffectWalk.Acts) and reaches no effect call
// has the line that it acts on what it is passed (effects.HeldLine), at
// the same places, and so does each call in acting, the calls that can
// act, that has no line of its own and no line in called, the places of
// the effect calls' lines. A call through an interface only some of
// whose implementations can act has the line that it may
// (effects.MayLine) and no other. A line is Marked at a call in acting,
// and at the name of a function, not a `once`, that can act. After
// them, at the same places, the line of the app fields each function reads
// or replaces, itself or through a function it can reach
// (vm.EffectWalk.Uses, effects.UsesLine), marked Uses: information a
// hover shows, which no list holds and no token marks. standings are each
// walk's standings (reachedCalls).
func (p *Program) reachedFuncs(walks []Walk, standings [][]standing, acting map[span]*actingCall, actOrder []span, called map[span]bool) []analyzedlowering.Reached {
	// reach is what one declaration, or one call, can reach: each name's
	// line, in the order the names were first found.
	type reach struct {
		names []string
		by    map[string]effects.Reached
		// held reports that it can act with no effect call, and may names
		// the interface function of a call only some of whose
		// implementations can; marked that a token is drawn bold there.
		held   bool
		may    string
		marked bool
		// fills reports that a walk that fills made the place's line.
		fills bool
	}
	add := func(to *reach, r effects.Reached) {
		old, seen := to.by[r.Name]
		switch {
		case !seen:
			to.names = append(to.names, r.Name)
		case old.Allowed && r.Allowed:
			for _, in := range r.In {
				if !slices.Contains(old.In, in) {
					old.In = append(slices.Clone(old.In), in)
				}
			}
			r = old
		case !old.Allowed:
			r = old
		}
		to.by[r.Name] = r
	}
	var order []span
	at := map[span]*reach{}
	// use is the line at s that a walk adds to, nil when its line fills
	// and an earlier walk's line, which does not, made it.
	use := func(s span, fills bool) *reach {
		if r := at[s]; r != nil {
			if fills && !r.fills {
				return nil
			}
			r.fills = r.fills && fills
			return r
		}
		r := &reach{by: map[string]effects.Reached{}, fills: fills}
		at[s] = r
		order = append(order, s)
		return r
	}
	// spansOf is where a hover shows f's lines: its declaration's name, a
	// function's or the `once` it initializes, and each of sites; nil for
	// a function with no declaration in source. marks says which of them
	// are drawn bold: the name of a function that acts, never a once's,
	// and each site that is a call in acting. A site that is a call only
	// some of whose implementations can act is not f's: the line that it
	// may stands for it, whichever walk reached f first.
	spansOf := func(f *ir.Func, sites []ir.Pos, acts bool) (spans []span, marks []bool) {
		path := f.Pos().File()
		line, col, name, once := p.declAt(path, f.Pos().Line(), f.Pos().Col())
		if name == "" {
			return nil, nil
		}
		spans = []span{{path, line, col, line, col + len(name)}}
		marks = []bool{acts && !once}
		for _, site := range sites {
			s := p.spanOf(site)
			if a := acting[s]; a != nil && a.some != "" {
				continue
			}
			spans = append(spans, s)
			marks = append(marks, acting[s] != nil)
		}
		return spans, marks
	}
	// fields is the app fields each span's function uses, in the order
	// the spans were first found (effects.UsesLine).
	var fieldOrder []span
	fields := map[span][]string{}
	for wi, w := range walks {
		if w.reach == nil {
			continue
		}
		key := func(i int) string { return w.Calls[i].Name + "\x00" + w.Calls[i].Module }
		named := map[*ir.Func]bool{}
		for _, f := range w.reach.Reaches(key) {
			named[f.Func] = true
			var lines []effects.Reached
			for _, rc := range f.Reached {
				r := reachedOf(w.Calls[rc.Call], standings[wi], rc.Call)
				r.Through = rc.Through
				lines = append(lines, r)
			}
			fills := w.fills || w.reach.FromLate(f.Func)
			spans, marks := spansOf(f.Func, f.Sites, w.reach.Acts(f.Func))
			for i, s := range spans {
				if to := use(s, fills); to != nil {
					for _, r := range lines {
						add(to, r)
					}
					to.marked = to.marked || marks[i]
				}
			}
		}
		for _, f := range w.reach.Acting() {
			if named[f.Func] {
				continue
			}
			fills := w.fills || w.reach.FromLate(f.Func)
			spans, marks := spansOf(f.Func, f.Sites, true)
			for i, s := range spans {
				if to := use(s, fills); to != nil {
					to.held = true
					to.marked = to.marked || marks[i]
				}
			}
		}
		for _, f := range w.reach.Uses() {
			spans, _ := spansOf(f.Func, f.Sites, false)
			for _, s := range spans {
				if _, seen := fields[s]; !seen {
					fieldOrder = append(fieldOrder, s)
				}
				for _, field := range f.Fields {
					if !slices.Contains(fields[s], field) {
						fields[s] = append(fields[s], field)
					}
				}
			}
		}
	}
	for _, s := range actOrder {
		a := acting[s]
		if to := use(s, a.fills); to != nil {
			if a.some != "" {
				to.may = cmp.Or(p.spanText(s), a.some)
			}
			to.held = true
			to.marked = true
		}
	}
	listed := p.effects != nil
	out := make([]analyzedlowering.Reached, 0, len(order)+len(fieldOrder))
	for _, s := range order {
		r := at[s]
		var text string
		switch {
		case r.may != "":
			text = effects.MayLine(r.may)
		case len(r.names) > 0:
			lines := make([]effects.Reached, len(r.names))
			for i, n := range r.names {
				lines[i] = r.by[n]
			}
			text = effects.FunctionReach(lines, listed)
		case r.held && !called[s]:
			text = effects.HeldLine
		default:
			continue
		}
		out = append(out, analyzedlowering.Reached{Path: s.path, Line: s.line, Col: s.col, EndLine: s.endLine, EndCol: s.endCol,
			Text: text, Marked: r.marked, Fills: r.fills})
	}
	for _, s := range fieldOrder {
		out = append(out, analyzedlowering.Reached{Path: s.path, Line: s.line, Col: s.col, EndLine: s.endLine, EndCol: s.endCol,
			Text: effects.UsesLine(p.appFieldNames(fields[s])), Uses: true})
	}
	return out
}

// spanText is the text of the program's source at s when s is on one
// line, as the author wrote it (`UserStore.find`); "" otherwise.
func (p *Program) spanText(s span) string {
	if s.line != s.endLine || s.endCol <= s.col {
		return ""
	}
	text, err := p.sourceOf(s.path)
	if err != nil {
		return ""
	}
	lines := strings.Split(text, "\n")
	if s.line > len(lines) || s.endCol-1 > len(lines[s.line-1]) {
		return ""
	}
	return lines[s.line-1][s.col-1 : s.endCol-1]
}

// EffectError is a program refused because it has effects the lists
// it is held to do not allow: nomi.toml's [effects] lists,
// or the host's (WithEffects). Calls and Diagnostics hold every call;
// its text gives one per name and list (Shown).
type EffectError struct {
	// Calls is each call a list does not allow, with whose list it is
	// over and the chain from a root that reaches it.
	Calls []NotAllowed
	// Diagnostics is the error for each of Calls, in the same order.
	Diagnostics Diagnostics
}

// AllCallsEnv names the environment variable that makes a
// *EffectError's text give every call; `--all-calls` on `nomi check`,
// `run`, `test` and `build` sets it. An environment variable rather than a
// flag alone, as frontend.DiagnosticsFormatEnv is: the error renders its
// own text (Error), wherever it is printed, and reads no flag.
const AllCallsEnv = "NOMI_ALL_CALLS"

func (e *EffectError) Error() string { return e.Shown().Error() }

// Unwrap is the diagnostics the text gives (Shown), which a caller that
// renders a load's errors finds with errors.As.
func (e *EffectError) Unwrap() error { return e.Shown() }

// Shown is the diagnostics the error's text gives: for each name and the
// list it is over, the first of its calls in Diagnostics' order, with a
// help line counting the others, and of the places the project's code
// leads to it (leadsTo) the first, counting the others; every diagnostic,
// with every place, when AllCallsEnv is set. A host's list is one list over
// the whole program.
func (e *EffectError) Shown() Diagnostics {
	if os.Getenv(AllCallsEnv) != "" {
		return e.Diagnostics
	}
	key := func(n NotAllowed) string {
		if n.Host {
			return fmt.Sprint(n.Name, "\x00", n.Host, n.Tests)
		}
		return fmt.Sprint(n.Name, "\x00", n.List, "\x00", n.Module, "\x00", n.Entry, "\x00", n.Tests)
	}
	first := map[string]int{}
	more := map[string]int{}
	var order []string
	for i, n := range e.Calls {
		k := key(n)
		if _, seen := first[k]; seen {
			more[k]++
			continue
		}
		first[k] = i
		order = append(order, k)
	}
	out := make(Diagnostics, len(order))
	for i, k := range order {
		d := e.Diagnostics[first[k]]
		if n := more[k]; n > 0 {
			d.Hints = append(slices.Clone(d.Hints), moreCalls(n, e.Calls[first[k]].Name))
		}
		if n := len(d.Related) - 1; n > 0 {
			r := d.Related[0]
			r.Message += "; " + morePlaces(n)
			d.Related = []frontend.Related{r}
		}
		out[i] = d
	}
	return out
}

// morePlaces is what a refusal's text adds to the first place the
// project's code leads to the call (leadsTo) when n more do.
func morePlaces(n int) string {
	if n == 1 {
		return "and 1 more place in your code; --all-calls lists it"
	}
	return fmt.Sprintf("and %d more places in your code; --all-calls lists them", n)
}

// moreCalls is the help line of a grouped refusal that stands for n more
// calls of name over the same list, and says how to see them.
func moreCalls(n int, name string) string {
	if n == 1 {
		return fmt.Sprintf("and 1 more call has the effect %s; --all-calls lists it", name)
	}
	return fmt.Sprintf("and %d more calls have the effect %s; --all-calls lists them", n, name)
}

// effectError is effectProblems as an error, nil when there are
// none: a *EffectError for calls the lists do not allow, and the
// diagnostics alone for list entries that name nothing.
func (p *Program) effectError(rs ...Roots) error {
	ds, calls := p.effectProblems(rs...)
	switch {
	case len(calls) > 0:
		return &EffectError{Calls: calls, Diagnostics: ds}
	case len(ds) > 0:
		return ds
	}
	return nil
}

// holdToHost refuses the program when the host holds it to a list
// (WithEffects) and the list has a name that is not one, or the program
// reaches a call the lists do not allow from anything a host can run:
// main and the boot, every function the entry declares, which Call may
// call, and the test cases.
func (p *Program) holdToHost() error {
	if !p.cfg.held {
		return nil
	}
	if err := p.hostEffectProblem(); err != nil {
		return err
	}
	rs := []Roots{{Exports: true, Callable: true}}
	if p.prog.HasTests {
		rs = append(rs, Roots{Tests: &TestOptions{}})
	}
	return p.effectError(rs...)
}

// checkedRoots is what `nomi check` holds to the lists, as it lowers: main
// and the boot with each exported function and each public function of
// the project's own files (publicFuncs), and each test case.
func (p *Program) checkedRoots() []Roots {
	rs := []Roots{{Exports: p.HasExports(), Public: true}}
	if p.prog.HasTests {
		rs = append(rs, Roots{Tests: &TestOptions{}})
	}
	return rs
}

// notAllowedDiagnostic is the error for n, reached from tests when tests is
// set: at the call, in the program's or a dependency's file, saying who
// has which effect and what the list allows, how the call is reached, and
// which key of which table to add what to (notAllowedHelp). A call over
// nomi.toml's tables carries the fix that adds it (allowFix), kept in
// fixes for the calls after it.
func (p *Program) notAllowedDiagnostic(n NotAllowed, tests bool, fixes map[string]*parser.Fix) frontend.Diagnostic {
	path, line, col := p.shownAt(n.EffectCall)
	module := cmp.Or(n.Module, p.projectModule())
	var message string
	list, allowed := string(n.List), n.Allowed
	switch {
	case n.Host && tests:
		list = "host"
		message = effects.Message("this program's tests have", n.Name, "the host", " them", n.Allowed)
	case n.Host:
		list = "host"
		message = effects.Message("this program has", n.Name, "the host", "", n.Allowed)
	case n.List == OverSelf && tests:
		message = effects.Message("this module's tests have", n.Name, "[effects.self]", " them", n.Allowed)
	case n.List == OverSelf:
		message = effects.Message("this module's own code has", n.Name, "[effects.self]", " it", n.Allowed)
	case n.List == OverSelfEntry:
		module = n.Entry
		message = effects.Message(fmt.Sprintf("`%s` has", n.Entry), n.Name, "its entry under [effects.self.modules]", " it", n.Allowed)
	case n.List == OverDeps:
		message = effects.Message(n.Module+" has", n.Name, "[effects.deps]", " dependencies", n.Allowed)
	case n.List == OverEntry:
		message = effects.Message(n.Module+" has", n.Name, "its entry under [effects.deps.modules]", " it", n.Allowed)
	case n.Entry != "":
		list = string(OverSelfEntry)
		message = effects.Message(fmt.Sprintf("%s's `%s` has", n.Module, n.Entry), n.Name, "its entry under the module's own [effects.self.modules]", " it", n.Allowed)
	default:
		list = string(OverSelf)
		message = effects.Message(n.Module+" has", n.Name, "its own [effects.self]", " it", n.Allowed)
	}
	d := frontend.NewDiagnostic(path, p.sources[path], line, col, message)
	d.Code = EffectNotAllowedCode
	if n.List == OverDeclared && !n.Host {
		d.Code = EffectNotDeclaredCode
	}
	d.Args = append([]string{n.Name, list, module}, allowed...)
	d.Hints = append(append(d.Hints, n.ReachText()), p.notAllowedHelp(n, tests)...)
	if !n.Host && n.List != OverDeclared {
		d.Fix = p.allowFix(n, tests, fixes)
	}
	return d
}

// leadsTo is where the project's own code leads to n, a refused call in a
// dependency's files, by w, the walk that found it: each place a function
// in the project's files last steps out of them toward the call
// (vm.EffectWalk.Leads), at the callee it calls or the value it uses
// (shownSpan), saying the effect the project's code has there and how. None
// for a call in the project's own files. own keeps, by file, whether a file
// is the project's.
func (p *Program) leadsTo(n NotAllowed, w Walk, own map[string]bool) []frontend.Related {
	if n.Module == "" || w.reach == nil {
		return nil
	}
	within := func(f *ir.Func) bool {
		file := f.Pos().File()
		if file == "" || f.Pos().Stdlib() {
			return false
		}
		is, done := own[file]
		if !done {
			is = p.moduleOf(file) == ""
			own[file] = is
		}
		return is
	}
	has := fmt.Sprintf("has the effect %s in %s", n.Name, n.Module)
	var out []frontend.Related
	for _, l := range w.reach.Leads(n.index, within) {
		path, line, col, endLine, endCol := p.shownSpan(l.At)
		out = append(out, frontend.Related{Path: path, Line: line, Col: col, EndLine: endLine, EndCol: endCol,
			Message: fmt.Sprintf("your code %s here: %s", has, l.Text)})
	}
	return out
}

// stdKey is the key of a table's std list that holds a call reached from
// tests when tests is set: std_in_tests for tests, std otherwise.
func stdKey(tests bool) string {
	if tests {
		return "std_in_tests"
	}
	return "std"
}

// addition is how a help line says to allow n in the project's tables:
// what to add (`add "std::fs"`) and the lists to add it to (`to std under
// [effects.deps]`), the list n names first, then the table its entry
// is under when that denies the call too.
func addition(n NotAllowed, tests bool) (item string, lists []string) {
	key := stdKey(tests)
	item = fmt.Sprintf("add %q", n.Name)
	switch {
	case n.List == OverSelf:
		return item, []string{fmt.Sprintf("to %s under [effects.self]", key)}
	case n.List == OverSelfEntry:
		lists = append(lists, fmt.Sprintf("to %q under [effects.self.modules]", n.Entry))
		if n.CeilingDenies {
			lists = append(lists, fmt.Sprintf("to %s under [effects.self]", key))
		}
		return item, lists
	case n.List == OverEntry || n.List == OverDeclared && n.EntryDenies:
		lists = append(lists, fmt.Sprintf("to %q under [effects.deps.modules]", n.Module))
	}
	if n.List == OverDeps || n.CeilingDenies {
		lists = append(lists, fmt.Sprintf("to %s under [effects.deps]", key))
	}
	return item, lists
}

// notAllowedHelp is the help for n after how it is reached: which key of
// which table to add what to, or remove the call. For a call over a
// dependency's own [effects.self], what the project's tables say of it
// when they do not allow it either, and that the fix is in that module's
// own nomi.toml, named by its path.
func (p *Program) notAllowedHelp(n NotAllowed, tests bool) []string {
	remove := ", or " + effects.RemoveHint(n.Edge == vm.EdgeBoot)
	switch {
	case n.Host:
		return []string{effects.HostAddHint(n.Name, "vmhost.WithEffects") + remove}
	case n.List == OverDeclared:
		var hints []string
		if item, lists := addition(n, tests); len(lists) > 0 {
			hints = append(hints, fmt.Sprintf("the project's nomi.toml does not allow %s to it either; to allow it there, %s %s",
				n.Name, item, strings.Join(lists, " and ")))
		}
		toml := "its nomi.toml"
		if m := p.moduleAt(n.at.File()); m != nil {
			toml = p.tomlName(m.root)
		}
		own := fmt.Sprintf("add %q to std under [effects.self]", n.Name)
		switch {
		case n.Entry != "" && n.OwnStdDenies:
			own = fmt.Sprintf("add %q to %q under [effects.self.modules] and to std under [effects.self]", n.Name, n.Entry)
		case n.Entry != "":
			own = fmt.Sprintf("add %q to %q under [effects.self.modules]", n.Name, n.Entry)
		}
		return append(hints, fmt.Sprintf("%s declares fewer effects than its code has, and the fix is in that module: %s in %s%s",
			n.Module, own, toml, remove))
	}
	var hints []string
	if n.CeilingDenies {
		table := "[effects.deps]"
		if n.List == OverSelfEntry {
			table = "[effects.self]"
		}
		hints = append(hints, fmt.Sprintf("%s under %s does not allow %s either", stdKey(tests), table, n.Name))
	}
	item, lists := addition(n, tests)
	if len(lists) > 0 {
		item += " " + strings.Join(lists, " and ")
	}
	return append(hints, item+" in nomi.toml"+remove)
}

// allowFix is the fix of n, a call over nomi.toml's tables reached from
// tests when tests is set: the edit to nomi.toml that adds what the help
// says to add to each list the call is over (analysis.AllowEffects),
// titled by what it allows. It is nil when the program's nomi.toml is
// staged in memory, or the edit cannot be written. fixes keeps each fix by
// what it adds.
func (p *Program) allowFix(n NotAllowed, tests bool, fixes map[string]*parser.Fix) *parser.Fix {
	path, data := p.manifestPath()
	if data == nil || !filepath.IsAbs(path) {
		return nil
	}
	selfStd, depsStd, inTests := analysis.ListSelfStd, analysis.ListDepsStd, ""
	if tests {
		selfStd, depsStd, inTests = analysis.ListSelfStdInTests, analysis.ListDepsStdInTests, " in tests"
	}
	var adds []analysis.EffectAdd
	var title string
	switch {
	case n.List == OverSelf:
		adds = []analysis.EffectAdd{{List: selfStd, Name: n.Name}}
		title = fmt.Sprintf("Allow %s for this module in nomi.toml", n.Name)
		if tests {
			title = fmt.Sprintf("Allow %s for this module's tests in nomi.toml", n.Name)
		}
	case n.List == OverSelfEntry:
		adds = []analysis.EffectAdd{{List: analysis.ListSelfModules, Module: n.Entry, Name: n.Name}}
		if n.CeilingDenies {
			adds = append(adds, analysis.EffectAdd{List: selfStd, Name: n.Name})
		}
		title = fmt.Sprintf("Allow %s for %s in nomi.toml", n.Name, n.Entry)
	case n.List == OverEntry:
		adds = []analysis.EffectAdd{{List: analysis.ListDepsModules, Module: n.Module, Name: n.Name}}
		if n.CeilingDenies {
			adds = append(adds, analysis.EffectAdd{List: depsStd, Name: n.Name})
		}
		title = fmt.Sprintf("Allow %s for %s in nomi.toml", n.Name, n.Module)
	default:
		adds = []analysis.EffectAdd{{List: depsStd, Name: n.Name}}
		title = fmt.Sprintf("Allow %s for dependencies%s in nomi.toml", n.Name, inTests)
	}
	key := fmt.Sprint(adds)
	if fix, done := fixes[key]; done {
		return fix
	}
	fix, _ := analysis.AllowEffects(path, data, p.effects, title, adds)
	fixes[key] = fix
	return fix
}

// shownAt is where a report about c points: its file as the program names
// it, and the line and column of the call's callee, where the author
// reads the call, rather than the call's own position, its parenthesis.
func (p *Program) shownAt(c EffectCall) (path string, line, col int) {
	path, line, col, _, _ = p.shownSpan(c.at)
	return path, line, col
}

// shownSpan is where a report shows the call at, as shownAt says, with the
// end of the call's callee, one past its last byte. A read of a qualified
// name (`io.stdout`, a `once` whose initializer has effects, or a std
// function used as a value) is at its dot and ends with the name, so the
// name is what a hover and a mark find. A `dbg` is the call that prints,
// and its keyword is its callee. The end is the start when there is no
// syntax for a call, a qualified read or a `dbg` there (syntaxOf).
func (p *Program) shownSpan(at ir.Pos) (path string, line, col, endLine, endCol int) {
	path, line, col = at.File(), at.Line(), at.Col()
	endLine, endCol = line, col
	switch n := p.indexOf(path).at[[2]int{line, col}].(type) {
	case *ast.Call:
		if l, c := leftmost(n.Func); l > 0 {
			line, col = l, c
			endLine, endCol = l, c
			if s, ok := n.Func.(ast.HasSpan); ok && !s.GetSpan().IsZero() {
				endLine, endCol = s.GetSpan().EndLine, s.GetSpan().EndCol
			}
		}
	case *ast.FieldAccess:
		if s := n.GetSpan(); !s.IsZero() {
			endLine, endCol = s.EndLine, s.EndCol
		}
	case *ast.Dbg:
		endCol = col + len("dbg")
	}
	return path, line, col, endLine, endCol
}

// dbgAt reports whether the call at is a `dbg`, whose keyword is its callee
// (shownSpan).
func (p *Program) dbgAt(at ir.Pos) bool {
	_, ok := p.indexOf(at.File()).at[[2]int{at.Line(), at.Col()}].(*ast.Dbg)
	return ok
}

// syntaxIndex is where a file's syntax (syntaxOf) has what a report shows:
// at is the node nodeAtIn answers for each position some node starts at,
// and decls each function and `once` the file declares, by the position of
// its name and, for a function, of its `fn`, any of which its lowered body's
// position is.
type syntaxIndex struct {
	at    map[[2]int]ast.Node
	decls map[[2]int]ast.Node
}

// indexOf is the syntaxIndex of the program's file at path, built once.
func (p *Program) indexOf(path string) *syntaxIndex {
	nodes := p.syntaxOf(path)
	p.parsedMu.Lock()
	defer p.parsedMu.Unlock()
	if ix := p.indexed[path]; ix != nil {
		return ix
	}
	ix := &syntaxIndex{at: map[[2]int]ast.Node{}, decls: map[[2]int]ast.Node{}}
	for _, top := range nodes {
		analysis.WalkNodes(top, func(n ast.Node) {
			if l, c := nodeStart(n); l > 0 {
				if _, seen := ix.at[[2]int{l, c}]; !seen {
					ix.at[[2]int{l, c}] = n
				}
			}
			switch d := n.(type) {
			case *ast.FuncDef:
				if d.Line > 0 {
					ix.decls[[2]int{d.Line, d.Col}] = d
					ix.decls[[2]int{d.Line, d.FnCol}] = d
				}
			case *ast.OnceBinding:
				if d.Line > 0 {
					ix.decls[[2]int{d.Line, d.Col}] = d
				}
			}
		})
	}
	if p.indexed == nil {
		p.indexed = map[string]*syntaxIndex{}
	}
	p.indexed[path] = ix
	return ix
}

// funcDefAt is the function the program's file at path declares at line
// and col (syntaxIndex), or nil.
func (p *Program) funcDefAt(path string, line, col int) *ast.FuncDef {
	fd, _ := p.indexOf(path).decls[[2]int{line, col}].(*ast.FuncDef)
	return fd
}

// declAt is where the name of the function or `once` the program's file
// at path declares at line and col starts (syntaxIndex), the name, and
// whether it is a once's; "" when it declares neither there.
func (p *Program) declAt(path string, line, col int) (nameLine, nameCol int, name string, once bool) {
	switch d := p.indexOf(path).decls[[2]int{line, col}].(type) {
	case *ast.FuncDef:
		return d.Line, d.Col, d.Name, false
	case *ast.OnceBinding:
		return d.Line, d.Col, d.Name, true
	}
	return 0, 0, "", false
}

// syntaxOf is the syntax of the program's file at path: its module's, or,
// for a file whose syntax the program does not hold (a program relinked
// from kept objects holds at most its entry's), its text (sourceOf)
// parsed, once. Nil for a std file and for a text that does not parse.
func (p *Program) syntaxOf(path string) []ast.Node {
	if m := p.modulePath(path); m != nil && m.Nodes != nil {
		return m.Nodes
	}
	if path == "" || std.IsSourcePath(path) {
		return nil
	}
	p.parsedMu.Lock()
	defer p.parsedMu.Unlock()
	if nodes, done := p.parsed[path]; done {
		return nodes
	}
	var nodes []ast.Node
	if text, err := p.sourceOf(path); err == nil {
		if parsed, err := parser.Parse(lexer.Lex(text)); err == nil {
			nodes = parsed
		}
	}
	if p.parsed == nil {
		p.parsed = map[string][]ast.Node{}
	}
	p.parsed[path] = nodes
	return nodes
}
