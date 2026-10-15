// Package declinesites lists the places internal/irbuild can decline a body:
// the sites whose execution can leave a body the front end accepted
// unretained, so that a program reaching it fails before its first effect.
//
// # What counts as a decline site
//
// The builder declines by returning failure up the call chain to an attempt
// (`irScalarBuild`, `irOnceCellLower`, `irTestBodyAttempt`), and names the
// reason afterwards (`irDeclineNote`, or `declineReason` in `lowerExact`). A
// site is one of:
//
//   - note: a call that names a decline reason: `irDeclineNote`,
//     `irDeclineNoteOnce`, `irDeclineAtPattern`, or a function or closure that
//     forwards its own parameter to one of them, given a reason that is not
//     itself a forwarded parameter or `irDeclineBodyWhy`; or an assignment
//     to `irDeclineBodyWhy` followed by a return. Notes in irdecline.go are
//     the census itself, not sites.
//   - false: a `return` whose value in a decline position is the literal
//     `false`, unless every disjunct of the condition that guards it is the
//     failure of a lowering function's own decline result (`if !ok` with `ok`
//     from such a call, or `if !lowerX(...)`): that return only passes a
//     decline on, and the site is in the callee. A return in the same block
//     as a note, after it, is that note's site.
//   - computed: a `return` whose value in a decline position is computed
//     (`ok && x`, `!x`, a predicate's result, a map lookup's ok), so it may be
//     false.
//
// A decline position is a (function, result index) pair. The attempts'
// results and `lower`/`lowerExact`/`lowerNode`'s ok (index 3) are decline
// positions. A position is one too when a decline position returns it
// (`return bl.lowerX(n)`, or `return t, k, m, ok` with `ok` from the call),
// or when a guarded return or a note passes its failure on (`if !ok {
// return ..., false }`). The callee must be a lowering function: one that
// reaches `lowerExact`, a note or `irDeclineClose` through the package's
// own calls. A guard on anything else (a lookup, a kind predicate, a map
// read) is itself the site, with the guard as its description.
//
// The analysis is syntactic: calls resolve by name within the package (a
// method name defined on several receivers resolves to all of them), a
// variable's source is its last assignment before the use, and a returned
// value is never followed through a non-call expression or through an
// anonymous closure's own returns. It overcounts rather than misses: a
// lowering function another caller probes (`if _, ok := bl.lowerX(n); ok`)
// still has its false returns listed, though they decline only on the path
// that passes them on.
package declinesites

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Site is one decline site.
type Site struct {
	// Func is the enclosing function: `Recv.name` for a method, `name` for a
	// function, and `outer/name` for a closure bound to a name inside outer.
	Func string
	// Kind is note, false or computed.
	Kind string
	// Desc is what distinguishes the site within its function: a note's
	// reason as written, or the guarding condition of a return.
	Desc string
	// N numbers sites with the same Func and Desc in source order, from 1.
	N         int
	File      string
	Line, Col int
}

// Key identifies a site without its line, so it survives edits elsewhere in
// its file.
func (s Site) Key() string {
	return fmt.Sprintf("%s | %s | %s#%d", s.Func, s.Kind, s.Desc, s.N)
}

// Pos is the site's file:line.
func (s Site) Pos() string { return fmt.Sprintf("%s:%d", s.File, s.Line) }

// baseNoteFuncs are the functions that name a decline, with the index of the
// reason argument.
var baseNoteFuncs = map[string]int{
	"irDeclineNote":      0,
	"irDeclineNoteOnce":  0,
	"irDeclineAtPattern": 1,
}

// markers are the calls that make a function a lowering function, besides
// the notes.
var markers = map[string]bool{
	"irScalarBuilder.lowerExact": true,
	"irDeclineClose":             true,
}

// seeds are the decline positions every other one is reached from.
var seeds = []pos{
	{"irScalarBuild", 1},
	{"irOnceCellLower", 1},
	{"irTestBodyAttempt", 1},
	{"irScalarBuilder.lower", 3},
	{"irScalarBuilder.lowerExact", 3},
	{"irScalarBuilder.lowerNode", 3},
}

type pos struct {
	fn  string
	idx int
}

type fnInfo struct {
	key     string
	file    string
	params  []string
	results []string // result names, "" for unnamed
	nres    int
	body    *ast.BlockStmt
	calls   map[string]bool // keys of callees, closures included
	assigns []assign
	returns []*retInfo
	notes   []noteCall
	whys    []token.Pos // irDeclineBodyWhy assignments followed by a return
}

type assign struct {
	name string
	at   token.Pos
	// src lists the call results the value comes from; computed is set when
	// it may also come from something else.
	src      []pos
	computed bool
}

type retInfo struct {
	stmt *ast.ReturnStmt
	// guard is the condition that guards the return, or nil.
	guard ast.Expr
	// noted is set when a note precedes the return in its block.
	noted bool
}

type noteCall struct {
	call   *ast.CallExpr
	fn     string
	reason ast.Expr
}

type analyzer struct {
	fset    *token.FileSet
	funcs   map[string]*fnInfo
	methods map[string][]string // method name -> keys
	imports map[string]bool     // import names in any file
	notes   map[string]int      // note function key -> reason index
}

// Find parses the non-test Go files of dir, the irbuild package, and answers
// its decline sites in file and line order.
func Find(dir string) ([]Site, error) {
	a := &analyzer{
		fset:    token.NewFileSet(),
		funcs:   map[string]*fnInfo{},
		methods: map[string][]string{},
		imports: map[string]bool{},
		notes:   map[string]int{},
	}
	for k, v := range baseNoteFuncs {
		a.notes[k] = v
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var files []*ast.File
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(a.fset, filepath.Join(dir, name), nil, parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		files = append(files, f)
		for _, im := range f.Imports {
			path, _ := strconv.Unquote(im.Path.Value)
			n := path[strings.LastIndex(path, "/")+1:]
			if im.Name != nil {
				n = im.Name.Name
			}
			a.imports[n] = true
		}
	}
	for _, f := range files {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			key := fd.Name.Name
			if fd.Recv != nil && len(fd.Recv.List) == 1 {
				key = recvName(fd.Recv.List[0].Type) + "." + fd.Name.Name
				a.methods[fd.Name.Name] = append(a.methods[fd.Name.Name], key)
			}
			a.addFunc(key, fd.Type, fd.Body)
		}
	}
	var top []*fnInfo
	for _, fi := range a.funcs {
		top = append(top, fi)
	}
	for _, fi := range top {
		a.registerClosures(fi)
	}
	a.findNoteForwarders()
	for _, fi := range a.funcs {
		a.walkBody(&scanState{fi: fi, closures: a.closuresOf(fi)}, fi.body)
	}
	for _, fi := range a.funcs {
		a.collectNotes(fi)
	}
	lowering := a.loweringFuncs()
	return a.sites(lowering), nil
}

func recvName(t ast.Expr) string {
	switch t := t.(type) {
	case *ast.StarExpr:
		return recvName(t.X)
	case *ast.IndexExpr:
		return recvName(t.X)
	case *ast.IndexListExpr:
		return recvName(t.X)
	case *ast.Ident:
		return t.Name
	}
	return "?"
}

func (a *analyzer) addFunc(key string, typ *ast.FuncType, body *ast.BlockStmt) *fnInfo {
	fi := &fnInfo{key: key, body: body, calls: map[string]bool{}}
	fi.file = filepath.Base(a.fset.Position(body.Pos()).Filename)
	if typ.Params != nil {
		for _, p := range typ.Params.List {
			if len(p.Names) == 0 {
				fi.params = append(fi.params, "")
			}
			for _, n := range p.Names {
				fi.params = append(fi.params, n.Name)
			}
		}
	}
	if typ.Results != nil {
		for _, r := range typ.Results.List {
			if len(r.Names) == 0 {
				fi.results = append(fi.results, "")
			}
			for _, n := range r.Names {
				fi.results = append(fi.results, n.Name)
			}
		}
	}
	fi.nres = len(fi.results)
	a.funcs[key] = fi
	return fi
}

// closures are the named closures of the function being scanned.
type scanState struct {
	fi       *fnInfo
	closures map[string]string // local name -> key
}

// registerClosures makes each closure bound to a name inside fi
// (`decline := func(...) {...}`) a function of its own, `fi/decline`. An
// anonymous closure's calls count as fi's, and its returns are its own.
func (a *analyzer) registerClosures(fi *fnInfo) {
	ast.Inspect(fi.body, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok || len(as.Lhs) != 1 || len(as.Rhs) != 1 {
			return true
		}
		id, isID := as.Lhs[0].(*ast.Ident)
		lit, isLit := as.Rhs[0].(*ast.FuncLit)
		if !isID || !isLit || id.Name == "_" {
			return true
		}
		key := fi.key + "/" + id.Name
		if _, seen := a.funcs[key]; !seen {
			a.addFunc(key, lit.Type, lit.Body)
		}
		return true
	})
}

func (a *analyzer) walkBody(st *scanState, body *ast.BlockStmt) {
	a.walkStmts(st, body.List, nil)
}

// walkStmts walks a statement list; guard is the condition that guards it.
func (a *analyzer) walkStmts(st *scanState, list []ast.Stmt, guard ast.Expr) {
	noted := false
	for i, s := range list {
		a.recordCalls(st, s)
		switch s := s.(type) {
		case *ast.ReturnStmt:
			g := guard
			if g == nil && i > 0 {
				g = negatedPrevIf(list[i-1])
			}
			st.fi.returns = append(st.fi.returns, &retInfo{stmt: s, guard: g, noted: noted})
		case *ast.AssignStmt:
			a.recordAssign(st, s.Lhs, s.Rhs, s.Pos())
			for _, l := range s.Lhs {
				if id, ok := l.(*ast.Ident); ok && id.Name == "irDeclineBodyWhy" && i+1 < len(list) {
					if _, ret := list[i+1].(*ast.ReturnStmt); ret {
						st.fi.whys = append(st.fi.whys, s.Pos())
					}
				}
			}
		case *ast.DeclStmt:
			if gd, ok := s.Decl.(*ast.GenDecl); ok {
				for _, sp := range gd.Specs {
					if vs, ok := sp.(*ast.ValueSpec); ok && len(vs.Values) > 0 {
						lhs := make([]ast.Expr, len(vs.Names))
						for j, n := range vs.Names {
							lhs[j] = n
						}
						a.recordAssign(st, lhs, vs.Values, vs.Pos())
					}
				}
			}
		case *ast.IfStmt:
			a.walkIf(st, s)
		case *ast.BlockStmt:
			a.walkStmts(st, s.List, guard)
		case *ast.ForStmt:
			if s.Init != nil {
				a.walkStmts(st, []ast.Stmt{s.Init}, nil)
			}
			a.walkStmts(st, s.Body.List, nil)
		case *ast.RangeStmt:
			a.walkStmts(st, s.Body.List, nil)
		case *ast.SwitchStmt:
			if s.Init != nil {
				a.walkStmts(st, []ast.Stmt{s.Init}, nil)
			}
			for _, c := range s.Body.List {
				cc := c.(*ast.CaseClause)
				var g ast.Expr
				if s.Tag == nil && len(cc.List) > 0 {
					g = orOf(cc.List)
				}
				a.walkStmts(st, cc.Body, g)
			}
		case *ast.TypeSwitchStmt:
			if s.Init != nil {
				a.walkStmts(st, []ast.Stmt{s.Init}, nil)
			}
			if as, ok := s.Assign.(*ast.AssignStmt); ok {
				a.recordAssign(st, as.Lhs, as.Rhs, as.Pos())
			}
			for _, c := range s.Body.List {
				a.walkStmts(st, c.(*ast.CaseClause).Body, nil)
			}
		case *ast.SelectStmt:
			for _, c := range s.Body.List {
				a.walkStmts(st, c.(*ast.CommClause).Body, nil)
			}
		case *ast.LabeledStmt:
			a.walkStmts(st, []ast.Stmt{s.Stmt}, guard)
		}
		if a.containsNote(st, s) {
			noted = true
		}
	}
}

func (a *analyzer) walkIf(st *scanState, s *ast.IfStmt) {
	if s.Init != nil {
		a.walkStmts(st, []ast.Stmt{s.Init}, nil)
	}
	a.walkStmts(st, s.Body.List, s.Cond)
	switch e := s.Else.(type) {
	case *ast.BlockStmt:
		a.walkStmts(st, e.List, &ast.UnaryExpr{Op: token.NOT, X: &ast.ParenExpr{X: s.Cond}})
	case *ast.IfStmt:
		a.walkIf(st, e)
	}
}

// negatedPrevIf is the guard of a return that follows `if ok { ... return }`
// with no else: the return runs when the condition failed.
func negatedPrevIf(prev ast.Stmt) ast.Expr {
	ifs, ok := prev.(*ast.IfStmt)
	if !ok || ifs.Else != nil || len(ifs.Body.List) == 0 {
		return nil
	}
	if _, ret := ifs.Body.List[len(ifs.Body.List)-1].(*ast.ReturnStmt); !ret {
		return nil
	}
	return &ast.UnaryExpr{Op: token.NOT, X: ifs.Cond}
}

func orOf(list []ast.Expr) ast.Expr {
	e := list[0]
	for _, x := range list[1:] {
		e = &ast.BinaryExpr{X: e, Op: token.LOR, Y: x}
	}
	return e
}

// containsNote reports whether s, a simple statement, calls a note function
// outside any closure.
func (a *analyzer) containsNote(st *scanState, s ast.Stmt) bool {
	switch s.(type) {
	case *ast.ExprStmt, *ast.AssignStmt:
	default:
		return false
	}
	found := false
	ast.Inspect(s, func(n ast.Node) bool {
		if _, lit := n.(*ast.FuncLit); lit {
			return false
		}
		if c, ok := n.(*ast.CallExpr); ok {
			for _, k := range a.resolve(st, c) {
				if _, note := a.notes[k]; note {
					found = true
				}
			}
		}
		return !found
	})
	return found
}

// recordCalls adds every call in s to the function's callees, closures'
// included. A named closure's own body is scanned as its own function, so
// only its binding is skipped here.
func (a *analyzer) recordCalls(st *scanState, s ast.Stmt) {
	ast.Inspect(s, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.BlockStmt:
			// Nested statement lists are walked by walkStmts; record only
			// expressions at this level.
			return n == nil
		case *ast.CaseClause, *ast.CommClause:
			return false
		case *ast.CallExpr:
			for _, k := range a.resolve(st, n) {
				st.fi.calls[k] = true
			}
		case *ast.FuncLit:
			// An anonymous closure: its calls are the function's, and its
			// statements are not.
			ast.Inspect(n.Body, func(m ast.Node) bool {
				if c, ok := m.(*ast.CallExpr); ok {
					for _, k := range a.resolve(st, c) {
						st.fi.calls[k] = true
					}
				}
				return true
			})
			return false
		}
		return true
	})
	// The headers of compound statements hold expressions too.
	switch s := s.(type) {
	case *ast.IfStmt:
		a.recordExpr(st, s.Cond)
	case *ast.SwitchStmt:
		a.recordExpr(st, s.Tag)
		for _, c := range s.Body.List {
			for _, e := range c.(*ast.CaseClause).List {
				a.recordExpr(st, e)
			}
		}
	case *ast.ForStmt:
		a.recordExpr(st, s.Cond)
	case *ast.RangeStmt:
		a.recordExpr(st, s.X)
	}
}

func (a *analyzer) recordExpr(st *scanState, e ast.Expr) {
	if e == nil {
		return
	}
	ast.Inspect(e, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok {
			for _, k := range a.resolve(st, c) {
				st.fi.calls[k] = true
			}
		}
		return true
	})
}

// resolve answers the keys a call may reach.
func (a *analyzer) resolve(st *scanState, c *ast.CallExpr) []string {
	fun := c.Fun
	switch f := fun.(type) {
	case *ast.IndexExpr:
		fun = f.X
	case *ast.IndexListExpr:
		fun = f.X
	}
	switch f := fun.(type) {
	case *ast.Ident:
		if k, ok := st.closures[f.Name]; ok {
			return []string{k}
		}
		if _, ok := a.funcs[f.Name]; ok {
			return []string{f.Name}
		}
	case *ast.SelectorExpr:
		if x, ok := f.X.(*ast.Ident); ok && a.imports[x.Name] {
			return nil
		}
		return a.methods[f.Sel.Name]
	}
	return nil
}

func (a *analyzer) recordAssign(st *scanState, lhs, rhs []ast.Expr, at token.Pos) {
	if len(rhs) == 1 && len(lhs) > 1 {
		call, isCall := unparen(rhs[0]).(*ast.CallExpr)
		for i, l := range lhs {
			id, ok := l.(*ast.Ident)
			if !ok || id.Name == "_" {
				continue
			}
			as := assign{name: id.Name, at: at}
			if isCall {
				for _, k := range a.resolve(st, call) {
					as.src = append(as.src, pos{k, i})
				}
			}
			if len(as.src) == 0 {
				as.computed = true
			}
			st.fi.assigns = append(st.fi.assigns, as)
		}
		return
	}
	for i, l := range lhs {
		id, ok := l.(*ast.Ident)
		if !ok || id.Name == "_" || i >= len(rhs) {
			continue
		}
		as := assign{name: id.Name, at: at}
		as.src, as.computed = a.valueSources(st, rhs[i])
		st.fi.assigns = append(st.fi.assigns, as)
	}
}

// valueSources answers the call results a single value comes from, and
// whether it may come from something else.
func (a *analyzer) valueSources(st *scanState, e ast.Expr) ([]pos, bool) {
	switch e := unparen(e).(type) {
	case *ast.CallExpr:
		var src []pos
		for _, k := range a.resolve(st, e) {
			src = append(src, pos{k, 0})
		}
		return src, len(src) == 0
	case *ast.Ident:
		if e.Name == "true" {
			return nil, false
		}
	}
	return nil, true
}

func unparen(e ast.Expr) ast.Expr {
	for {
		p, ok := e.(*ast.ParenExpr)
		if !ok {
			return e
		}
		e = p.X
	}
}

// findNoteForwarders adds to the note functions every function or closure
// that passes one of its parameters as a note's reason.
func (a *analyzer) findNoteForwarders() {
	for changed := true; changed; {
		changed = false
		for key, fi := range a.funcs {
			if _, known := a.notes[key]; known {
				continue
			}
			ast.Inspect(fi.body, func(n ast.Node) bool {
				c, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				id, ok := c.Fun.(*ast.Ident)
				if !ok {
					return true
				}
				idx, isNote := a.notes[id.Name]
				if !isNote || idx >= len(c.Args) {
					return true
				}
				if r, ok := unparen(c.Args[idx]).(*ast.Ident); ok {
					for pi, p := range fi.params {
						if p == r.Name {
							if _, known := a.notes[key]; !known {
								a.notes[key] = pi
								changed = true
							}
						}
					}
				}
				return true
			})
		}
	}
}

// collectNotes records fi's note calls whose reason is its own: not a
// parameter it forwards.
func (a *analyzer) collectNotes(fi *fnInfo) {
	closureOf := map[string]string{}
	prefix := fi.key + "/"
	for k := range a.funcs {
		if strings.HasPrefix(k, prefix) {
			closureOf[k[len(prefix):]] = k
		}
	}
	if i := strings.Index(fi.key, "/"); i >= 0 {
		// A closure calls its siblings.
		outer := fi.key[:i] + "/"
		for k := range a.funcs {
			if strings.HasPrefix(k, outer) {
				closureOf[k[len(outer):]] = k
			}
		}
	}
	ast.Inspect(fi.body, func(n ast.Node) bool {
		if as, ok := n.(*ast.AssignStmt); ok && len(as.Lhs) == 1 && len(as.Rhs) == 1 {
			if id, ok := as.Lhs[0].(*ast.Ident); ok && closureOf[id.Name] != "" {
				if _, lit := as.Rhs[0].(*ast.FuncLit); lit {
					// A named closure's body is its own function's.
					return false
				}
			}
		}
		c, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		id, ok := c.Fun.(*ast.Ident)
		if !ok {
			return true
		}
		key := id.Name
		if k, closure := closureOf[id.Name]; closure {
			key = k
		}
		idx, isNote := a.notes[key]
		if !isNote || idx >= len(c.Args) {
			return true
		}
		reason := unparen(c.Args[idx])
		if r, ok := reason.(*ast.Ident); ok {
			if r.Name == "irDeclineBodyWhy" {
				// Passes on irScalarBlock's decline, whose assignment
				// of irDeclineBodyWhy is the site.
				return true
			}
			for _, p := range fi.params {
				if p == r.Name {
					return true
				}
			}
		}
		fi.notes = append(fi.notes, noteCall{call: c, fn: key, reason: reason})
		return true
	})
}

// loweringFuncs answers the functions that reach lowerExact, a note or
// irDeclineClose through the package's own calls.
func (a *analyzer) loweringFuncs() map[string]bool {
	low := map[string]bool{}
	for k := range markers {
		low[k] = true
	}
	for k := range a.notes {
		low[k] = true
	}
	for k, fi := range a.funcs {
		if len(fi.whys) > 0 || strings.HasPrefix(k, "irScalarBuilder.") {
			// A builder method emits into the body being lowered.
			low[k] = true
		}
	}
	for changed := true; changed; {
		changed = false
		for k, fi := range a.funcs {
			if low[k] {
				continue
			}
			for c := range fi.calls {
				if low[c] {
					low[k] = true
					changed = true
					break
				}
			}
		}
	}
	return low
}

// lastAssign is the last assignment to name before at.
func (fi *fnInfo) lastAssign(name string, at token.Pos) *assign {
	var best *assign
	for i := range fi.assigns {
		as := &fi.assigns[i]
		if as.name == name && as.at < at && (best == nil || as.at > best.at) {
			best = as
		}
	}
	return best
}

func (a *analyzer) sites(lowering map[string]bool) []Site {
	outputs := map[pos]bool{}
	var work []pos
	add := func(p pos) {
		fi, ok := a.funcs[p.fn]
		if !ok || !lowering[p.fn] || outputs[p] || p.idx >= fi.nres {
			return
		}
		outputs[p] = true
		work = append(work, p)
	}
	for _, s := range seeds {
		add(s)
	}
	// propagating answers whether cond is a disjunction of failures of
	// lowering functions' results, adding each as a decline position.
	propagating := func(fi *fnInfo, cond ast.Expr, at token.Pos) bool {
		all := true
		for _, d := range disjuncts(cond) {
			srcs := failureSources(a, fi, d, at, lowering)
			if len(srcs) == 0 {
				all = false
				continue
			}
			for _, s := range srcs {
				add(s)
			}
		}
		return all
	}
	type found struct {
		fn   string
		kind string
		desc string
		at   token.Pos
	}
	seen := map[token.Pos]bool{}
	var out []found
	// A guarded noted return passes on the failure it guards, whatever
	// position it returns.
	for _, fi := range a.funcs {
		for _, r := range fi.returns {
			if r.noted && r.guard != nil {
				propagating(fi, r.guard, r.stmt.Pos())
			}
		}
	}
	for len(work) > 0 {
		p := work[len(work)-1]
		work = work[:len(work)-1]
		fi := a.funcs[p.fn]
		for _, r := range fi.returns {
			var e ast.Expr
			switch {
			case len(r.stmt.Results) == 0:
				if fi.results[p.idx] == "" {
					continue
				}
				e = ast.NewIdent(fi.results[p.idx])
			case len(r.stmt.Results) == 1 && fi.nres > 1:
				// return f(...) passing a tuple through.
				call, ok := unparen(r.stmt.Results[0]).(*ast.CallExpr)
				if !ok {
					continue
				}
				ks := a.resolve(&scanState{fi: fi, closures: a.closuresOf(fi)}, call)
				if len(ks) == 1 {
					// `return no()`, with `no := func() (...) { return
					// ir.NoTemp, kindInvalid, false, false }`: the closure
					// is a spelling of its one return's values.
					if c := a.funcs[ks[0]]; c != nil && strings.Contains(c.key, "/") && len(c.returns) == 1 &&
						len(c.returns[0].stmt.Results) == fi.nres && len(c.params) == 0 {
						e = c.returns[0].stmt.Results[p.idx]
						break
					}
				}
				any := false
				for _, k := range ks {
					if lowering[k] {
						add(pos{k, p.idx})
						any = true
					}
				}
				if !any && !r.noted && !seen[r.stmt.Pos()] {
					seen[r.stmt.Pos()] = true
					out = append(out, found{fi.key, "computed", "return " + text(a.fset, call), r.stmt.Pos()})
				}
				continue
			default:
				if p.idx >= len(r.stmt.Results) {
					continue
				}
				e = r.stmt.Results[p.idx]
			}
			if r.noted {
				if r.guard != nil {
					propagating(fi, r.guard, r.stmt.Pos())
				}
				continue
			}
			kind, desc := a.valueSite(fi, e, r, add, lowering, propagating)
			if kind != "" && !seen[r.stmt.Pos()] {
				seen[r.stmt.Pos()] = true
				out = append(out, found{fi.key, kind, desc, r.stmt.Pos()})
			}
		}
	}
	for _, fi := range a.funcs {
		if fi.file == "irdecline.go" {
			// The census itself: lowerExact's note is the funnel the
			// "false" and "computed" sites name their reason through.
			continue
		}
		for _, n := range fi.notes {
			out = append(out, found{fi.key, "note", text(a.fset, n.reason), n.call.Pos()})
		}
		for _, w := range fi.whys {
			out = append(out, found{fi.key, "note", "irDeclineBodyWhy", w})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		pi, pj := a.fset.Position(out[i].at), a.fset.Position(out[j].at)
		if pi.Filename != pj.Filename {
			return pi.Filename < pj.Filename
		}
		return pi.Offset < pj.Offset
	})
	count := map[string]int{}
	sites := make([]Site, 0, len(out))
	for _, f := range out {
		p := a.fset.Position(f.at)
		desc := clip(f.desc)
		id := f.fn + "|" + f.kind + "|" + desc
		count[id]++
		sites = append(sites, Site{Func: f.fn, Kind: f.kind, Desc: desc, N: count[id],
			File: filepath.Base(p.Filename), Line: p.Line, Col: p.Column})
	}
	return sites
}

func (a *analyzer) closuresOf(fi *fnInfo) map[string]string {
	m := map[string]string{}
	outer := fi.key
	if i := strings.Index(outer, "/"); i >= 0 {
		outer = outer[:i]
	}
	for k := range a.funcs {
		if strings.HasPrefix(k, outer+"/") {
			m[k[len(outer)+1:]] = k
		}
	}
	return m
}

// valueSite classifies the value e a return gives a decline position.
func (a *analyzer) valueSite(fi *fnInfo, e ast.Expr, r *retInfo, add func(pos), lowering map[string]bool,
	propagating func(*fnInfo, ast.Expr, token.Pos) bool) (kind, desc string) {
	st := &scanState{fi: fi, closures: a.closuresOf(fi)}
	switch v := unparen(e).(type) {
	case *ast.Ident:
		switch v.Name {
		case "true":
			return "", ""
		case "false":
			if r.guard != nil && propagating(fi, r.guard, r.stmt.Pos()) {
				return "", ""
			}
			if r.guard == nil {
				return "false", "unguarded"
			}
			return "false", text(a.fset, r.guard)
		}
		as := fi.lastAssign(v.Name, r.stmt.Pos())
		if as == nil {
			return "computed", "return " + v.Name
		}
		low := false
		for _, s := range as.src {
			if lowering[s.fn] {
				add(s)
				low = true
			}
		}
		if as.computed || !low {
			return "computed", "return " + v.Name
		}
		return "", ""
	case *ast.CallExpr:
		any := false
		for _, k := range a.resolve(st, v) {
			if lowering[k] {
				add(pos{k, 0})
				any = true
			}
		}
		if any {
			return "", ""
		}
	case *ast.BinaryExpr:
		if v.Op == token.LAND {
			// `ok1 && ok2`: each operand is a source of its own; the
			// return is a site when either may be false for another
			// reason.
			k1, _ := a.valueSite(fi, v.X, &retInfo{stmt: r.stmt}, add, lowering, propagating)
			k2, _ := a.valueSite(fi, v.Y, &retInfo{stmt: r.stmt}, add, lowering, propagating)
			if k1 == "" && k2 == "" {
				return "", ""
			}
		}
	}
	return "computed", "return " + text(a.fset, e)
}

func disjuncts(e ast.Expr) []ast.Expr {
	e = unparen(e)
	if b, ok := e.(*ast.BinaryExpr); ok && b.Op == token.LOR {
		return append(disjuncts(b.X), disjuncts(b.Y)...)
	}
	return []ast.Expr{e}
}

// failureSources answers the lowering functions' results whose failure d
// tests (`!ok`, `!lowerX(...)`), or nil.
func failureSources(a *analyzer, fi *fnInfo, d ast.Expr, at token.Pos, lowering map[string]bool) []pos {
	u, ok := unparen(d).(*ast.UnaryExpr)
	if !ok || u.Op != token.NOT {
		return nil
	}
	switch x := unparen(u.X).(type) {
	case *ast.Ident:
		as := fi.lastAssign(x.Name, at)
		if as == nil || as.computed {
			return nil
		}
		var out []pos
		for _, s := range as.src {
			if lowering[s.fn] {
				out = append(out, s)
			}
		}
		if len(out) != len(as.src) {
			return nil
		}
		return out
	case *ast.CallExpr:
		var out []pos
		for _, k := range a.resolve(&scanState{fi: fi, closures: a.closuresOf(fi)}, x) {
			if lowering[k] {
				out = append(out, pos{k, 0})
			}
		}
		return out
	}
	return nil
}

func text(fset *token.FileSet, n ast.Node) string {
	var b bytes.Buffer
	if err := printer.Fprint(&b, fset, n); err != nil {
		return "?"
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func clip(s string) string {
	s = strings.ReplaceAll(s, "\t", " ")
	s = strings.ReplaceAll(s, "|", "¦")
	if r := []rune(s); len(r) > 90 {
		return string(r[:90]) + "…"
	}
	return s
}
