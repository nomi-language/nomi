package vm

// FUNCTION BREAKPOINTS: stop at a function's first statement each time an
// activation of it starts, named as the stack names its frames.
//
// A name matches a function by any of these spellings:
//
//   - its name as the stack shows it, without type arguments: `transact`,
//     `Account.deposit`, `Card.to_string` (an impl's function is named by
//     the type it is for), `Maybe.map`;
//   - the same with type arguments, which picks one instance of a generic
//     function: `ident<Int>`, `Maybe.map<Int, Int>`;
//   - either of those qualified by its file's name: `tiny_bank.transact`,
//     `maybe.Maybe.map`;
//   - for an impl's function, the interface method it implements:
//     `Display.to_string` stops in every impl the program has.
//
// A name may match several functions: each instance of a generic function,
// each impl of an interface method, a function of the same name in two
// files. It stops in all of them. A lambda has no name, and a function with
// no statement (a synthesized one) cannot be named.
//
// A thread stops at the first statement an activation reaches: the
// activation's `entered` flag is set when it starts, and when a tail call
// replaces its function, and the statement hook clears it. A recursive call
// is an activation like any other, and stops again.

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/nomi-language/nomi/internal/ir"
)

// FunctionBreakpointSpec is one function breakpoint a client asks for.
type FunctionBreakpointSpec struct {
	Name string
	// Hit decides at each entry whether to stop, as BreakpointSpec's does.
	Hit func(*Scope) Hit
}

// FunctionBreakpoint is one requested function breakpoint and what it
// names.
type FunctionBreakpoint struct {
	Name     string
	Verified bool
	// Pos is the first statement of the first function the name matched.
	Pos ir.Pos
	// Functions are the functions it stops in, as the stack names them.
	Functions []string
	// Similar are the nameable functions whose names are close to an
	// unverified breakpoint's, nearest first.
	Similar []string
}

// fnHook is a function breakpoint resolved to one function: its index in
// the requested list, and its decision.
type fnHook struct {
	index int
	hit   func(*Scope) Hit
}

// debugFunc is a function a breakpoint can name.
type debugFunc struct {
	fn *ir.Func
	// names are the spellings that name it; display is how the stack does.
	names   []string
	display string
	// entry is its first statement.
	entry ir.Pos
}

// addDebugFuncLocked records f as nameable under its spellings and alias,
// an interface method it implements (or "").
func (d *Debugger) addDebugFuncLocked(f *ir.Func, alias string) {
	if f == nil {
		return
	}
	switch f.Name() {
	case "lambda", "concurrent", "":
		return
	}
	if p := f.Pos(); !p.IsValid() || p.Synthesized() {
		return
	}
	entry, ok := entryStmt(f)
	if !ok {
		return
	}
	if i, seen := d.funcIndex[f]; seen {
		if alias != "" {
			d.funcs[i].names = append(d.funcs[i].names, alias)
		}
		return
	}
	unq, stem, args := spellings(f)
	names := []string{unq, stem + "." + unq}
	if args != "" {
		names = append(names, unq+args, stem+"."+unq+args)
	}
	if alias != "" {
		names = append(names, alias)
	}
	if d.funcIndex == nil {
		d.funcIndex = map[*ir.Func]int{}
	}
	d.funcIndex[f] = len(d.funcs)
	d.funcs = append(d.funcs, debugFunc{fn: f, names: names, display: frameName(f), entry: entry})
}

// spellings are the parts of f's name: as source code calls it, without
// its file's name or type arguments (`Maybe.map`); its file's name
// (`maybe`); and its type arguments (`<Int, Int>`), or "".
func spellings(f *ir.Func) (name, stem, args string) {
	base, args, _ := strings.Cut(f.Name(), "<")
	if args != "" {
		args = "<" + args
	}
	stem = strings.TrimSuffix(filepath.Base(f.Pos().File()), ".nomi")
	return strings.TrimPrefix(base, stem+"."), stem, args
}

// entryStmt is f's first statement: the first mark of its entry block, or
// of any block when the entry block has none.
func entryStmt(f *ir.Func) (ir.Pos, bool) {
	blocks := f.Blocks()
	for _, b := range blocks {
		if marks := b.Stmts(); len(marks) > 0 {
			return marks[0].Pos, true
		}
	}
	return ir.Pos{}, false
}

// normalFuncName is a requested name with its spaces removed, as a name
// with type arguments is spelled in the stack (`Maybe.map<Int, Int>`).
func normalFuncName(name string) string {
	return strings.Join(strings.Fields(name), "")
}

// matchFuncsLocked are the functions name matches.
func (d *Debugger) matchFuncsLocked(name string) []debugFunc {
	want := normalFuncName(name)
	var out []debugFunc
	for _, df := range d.funcs {
		for _, n := range df.names {
			if normalFuncName(n) == want {
				out = append(out, df)
				break
			}
		}
	}
	return out
}

// SetFunctionBreakpoints replaces the function breakpoints with specs and
// answers what each names.
func (d *Debugger) SetFunctionBreakpoints(specs []FunctionBreakpointSpec) []FunctionBreakpoint {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.requestedFns = append([]FunctionBreakpointSpec(nil), specs...)
	d.bpEpoch++
	d.resolveLocked()
	out := make([]FunctionBreakpoint, len(specs))
	for i, s := range specs {
		out[i] = FunctionBreakpoint{Name: s.Name}
		matched := d.matchFuncsLocked(s.Name)
		if len(matched) == 0 {
			out[i].Similar = d.similarFuncsLocked(s.Name)
			continue
		}
		out[i].Verified, out[i].Pos = true, matched[0].entry
		for _, df := range matched {
			out[i].Functions = append(out[i].Functions, df.display)
		}
	}
	return out
}

// resolveFnsLocked rebuilds fnBps from requestedFns.
func (d *Debugger) resolveFnsLocked() {
	d.fnBps = map[*ir.Func][]fnHook{}
	for i, s := range d.requestedFns {
		for _, df := range d.matchFuncsLocked(s.Name) {
			d.fnBps[df.fn] = append(d.fnBps[df.fn], fnHook{index: i, hit: s.Hit})
		}
	}
	snap := d.fnBps
	d.fnSnap.Store(&snap)
}

// FunctionScope is the variables, without values, visible at the first
// statement of the first function name matches, which a function
// breakpoint's condition is checked against before the program reaches it,
// and whether name matches one.
func (d *Debugger) FunctionScope(name string) (Scope, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	matched := d.matchFuncsLocked(name)
	if len(matched) == 0 {
		return Scope{}, false
	}
	return staticScope(matched[0].fn, matched[0].entry), true
}

// similarFuncsLocked are the nameable functions whose names are close to
// name, nearest first, at most five.
func (d *Debugger) similarFuncsLocked(name string) []string {
	want := strings.ToLower(normalFuncName(name))
	if want == "" {
		return nil
	}
	wantLast := want[strings.LastIndex(want, ".")+1:]
	type cand struct {
		name string
		dist int
	}
	best := map[string]int{}
	for _, df := range d.funcs {
		n := df.names[0]
		lower := strings.ToLower(n)
		last := lower[strings.LastIndex(lower, ".")+1:]
		dist := editDistance(want, lower)
		if !strings.Contains(want, ".") {
			// `depost` is close to `Account.deposit`.
			dist = min(dist, editDistance(want, last))
		}
		switch {
		case last == wantLast, strings.Contains(lower, want):
			dist = min(dist, 1)
		case dist > max(2, len(want)/3):
			continue
		}
		if prev, seen := best[n]; !seen || dist < prev {
			best[n] = dist
		}
	}
	var cands []cand
	for n, dist := range best {
		cands = append(cands, cand{n, dist})
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].dist != cands[j].dist {
			return cands[i].dist < cands[j].dist
		}
		return cands[i].name < cands[j].name
	})
	var out []string
	for _, c := range cands {
		if len(out) == 5 {
			break
		}
		out = append(out, c.name)
	}
	return out
}

// editDistance is the Levenshtein distance between a and b, by byte.
func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

// entryHooks are the function breakpoints on fr's function when fr is at
// its first statement.
func (d *Debugger) entryHooks(fr *frame) []fnHook {
	if !fr.entered {
		return nil
	}
	snap := d.fnSnap.Load()
	if snap == nil || len(*snap) == 0 {
		return nil
	}
	return (*snap)[fr.fn]
}
