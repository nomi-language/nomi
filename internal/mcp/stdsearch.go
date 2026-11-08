package mcp

import (
	"maps"
	"sort"
	"strings"
	"sync"
	"unicode"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/hoverdoc"
	"github.com/nomi-language/nomi/std"
)

// stdEntry is one public function of the stdlib, as a program calls it.
type stdEntry struct {
	Name      string `json:"name"` // as called: `Iter.map`, `io.print`
	Module    string `json:"module"`
	Signature string `json:"signature"`
	Doc       string `json:"doc,omitempty"` // the first paragraph
	params    []*typeNode
	defaulted []bool // whether each parameter has a default, so a query may leave it out
	ret       *typeNode
	bounds    map[string][]string // a type variable's interface bounds, by name
}

// stdIndex lists the stdlib's public functions once per process, from the
// analyzed declarations the compiler uses, never a second parse.
var stdIndex = sync.OnceValue(func() []stdEntry {
	lib := std.All()
	modules := make([]string, 0, len(lib.Nodes))
	for name := range lib.Nodes {
		if _, internal := analysis.InternalStdModule(name); internal || strings.HasPrefix(name, "_") {
			continue
		}
		modules = append(modules, name)
	}
	sort.Strings(modules)
	var out []stdEntry
	for _, module := range modules {
		for _, n := range lib.Nodes[module] {
			out = append(out, entriesOf(module, n)...)
		}
	}
	return out
})

func entriesOf(module string, n ast.Node) []stdEntry {
	switch d := n.(type) {
	case *ast.FuncDef:
		if d.Public && !d.ImplFunction {
			return []stdEntry{entry(module, module+"."+d.Name, d.Name, d.TypeParams, d.Params, d.ReturnTypeExpr, d.Doc, "", d.WhereClauses)}
		}
	case *ast.ExternFunc:
		if d.Public && !d.ImplFunction {
			return []stdEntry{entry(module, module+"."+d.Name, d.Name, d.TypeParams, d.Params, d.ReturnTypeExpr, d.Doc, "", d.WhereClauses)}
		}
	case *ast.ImplBlock:
		if d.Interface != nil || d.Receiver == nil {
			return nil
		}
		receiver := d.Receiver.TypeString()
		owner := receiver
		if i := strings.IndexByte(owner, '<'); i >= 0 {
			owner = owner[:i]
		}
		var out []stdEntry
		for _, item := range d.Items {
			switch f := item.(type) {
			case *ast.FuncDef:
				if f.Public {
					out = append(out, entry(module, owner+"."+f.Name, f.Name, f.TypeParams, f.Params, f.ReturnTypeExpr, f.Doc, receiver, d.WhereClauses, f.WhereClauses))
				}
			case *ast.ExternFunc:
				if f.Public {
					out = append(out, entry(module, owner+"."+f.Name, f.Name, f.TypeParams, f.Params, f.ReturnTypeExpr, f.Doc, receiver, d.WhereClauses, f.WhereClauses))
				}
			}
		}
		return out
	case *ast.InterfaceDef:
		if !d.Public {
			return nil
		}
		var out []stdEntry
		for i := range d.Methods {
			m := &d.Methods[i]
			e := entry(module, d.Name+"."+m.Name, m.Name, m.TypeParams, m.Params, m.ReturnTypeExpr, m.Doc, d.Name, d.WhereClauses, m.WhereClauses)
			e.Signature = hoverdoc.RenderInterfaceMethodSig(m)
			out = append(out, e)
		}
		return out
	}
	return nil
}

func entry(module, qualified, name string, tps []ast.TypeParam, params []ast.Param, ret ast.TypeExpr, doc, self string, where ...[]ast.WhereConstraint) stdEntry {
	e := stdEntry{
		Name:      qualified,
		Module:    module,
		Signature: hoverdoc.RenderFuncSig(name, tps, params, ret),
		Doc:       firstParagraph(doc),
		bounds:    map[string][]string{},
	}
	for _, tp := range tps {
		for _, b := range tp.Bounds {
			e.bounds[tp.Name] = append(e.bounds[tp.Name], parseType(b.TypeString()).name)
		}
	}
	for _, ws := range where {
		for _, w := range ws {
			for _, b := range w.Bounds {
				e.bounds[w.Name] = append(e.bounds[w.Name], parseType(b.TypeString()).name)
			}
		}
	}
	for _, p := range params {
		e.defaulted = append(e.defaulted, p.Default != nil)
		if p.TypeAnnotation == nil {
			e.params = append(e.params, &typeNode{name: "?"})
			continue
		}
		e.params = append(e.params, parseType(substituteSelf(p.TypeAnnotation.TypeString(), self)))
	}
	if ret != nil {
		e.ret = parseType(substituteSelf(ret.TypeString(), self))
	} else {
		e.ret = &typeNode{name: "Unit"}
	}
	return e
}

func substituteSelf(t, self string) string {
	if self == "" || t != "self" {
		return t
	}
	return self
}

func firstParagraph(doc string) string {
	doc = strings.TrimSpace(doc)
	if i := strings.Index(doc, "\n\n"); i >= 0 {
		doc = doc[:i]
	}
	return strings.Join(strings.Fields(doc), " ")
}

// searchStd answers the entries whose name holds query, or, when query has
// `->`, whose signature matches it: the same parameter types in any order
// and the same result, a single capital letter in either standing for any
// type, consistently. A query type also matches an interface it implements
// (`List<T>` an `Iter<T>` parameter), and a candidate's result an interface
// the query asks for. Exact matches come first, then ones that instantiate
// a generic, then ones that go through an impl.
func searchStd(query string, limit int) []stdEntry {
	query = strings.TrimSpace(query)
	if limit <= 0 {
		limit = 20
	}
	type hit struct {
		e     stdEntry
		score int
	}
	var hits []hit
	if arrow := strings.LastIndex(query, "->"); arrow >= 0 && depthAt(query, arrow) == 0 {
		qParams := splitTop(strings.TrimSpace(trimParens(strings.TrimSpace(query[:arrow]))))
		qp := make([]*typeNode, 0, len(qParams))
		for _, s := range qParams {
			qp = append(qp, parseType(s))
		}
		qr := parseType(strings.TrimSpace(query[arrow+2:]))
		for _, e := range stdIndex() {
			if score, ok := matchSignature(qp, qr, e); ok {
				hits = append(hits, hit{e, score})
			}
		}
	} else {
		q := strings.ToLower(query)
		for _, e := range stdIndex() {
			name := strings.ToLower(e.Name)
			switch {
			case name == q || strings.HasSuffix(name, "."+q):
				hits = append(hits, hit{e, 0})
			case strings.Contains(name, q):
				hits = append(hits, hit{e, 1})
			case q != "" && strings.Contains(strings.ToLower(e.Doc), q):
				hits = append(hits, hit{e, 2})
			}
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].score < hits[j].score })
	out := []stdEntry{}
	for i := 0; i < len(hits) && i < limit; i++ {
		out = append(out, hits[i].e)
	}
	return out
}

// typeNode is a type as written: a name with arguments (`Map<K, V>`), a
// tuple (`#`, args), or a function (`->`, params then the result).
type typeNode struct {
	name string
	args []*typeNode
}

func (t *typeNode) isVar() bool {
	return len(t.args) == 0 && len(t.name) == 1 && unicode.IsUpper(rune(t.name[0]))
}

func parseType(s string) *typeNode {
	s = strings.TrimSpace(s)
	switch {
	case strings.HasPrefix(s, "("):
		if arrow := topArrow(s); arrow >= 0 {
			params := parseList(trimParens(strings.TrimSpace(s[:arrow])))
			return &typeNode{name: "->", args: append(params, parseType(s[arrow+2:]))}
		}
		if items := parseList(trimParens(s)); len(items) >= 2 {
			return &typeNode{name: "#", args: items}
		}
		return parseType(trimParens(s))
	}
	if i := strings.IndexByte(s, '<'); i > 0 && strings.HasSuffix(s, ">") {
		return &typeNode{name: s[:i], args: parseList(s[i+1 : len(s)-1])}
	}
	return &typeNode{name: s}
}

func parseList(s string) []*typeNode {
	var out []*typeNode
	for _, part := range splitTop(s) {
		out = append(out, parseType(part))
	}
	return out
}

// splitTop splits s at the commas outside brackets.
func splitTop(s string) []string {
	var out []string
	depth, start := 0, 0
	for i, r := range s {
		switch r {
		case '<', '(', '[', '{':
			depth++
		case '>', ')', ']', '}':
			if r == '>' && i > 0 && s[i-1] == '-' {
				continue
			}
			depth--
		case ',':
			if depth == 0 {
				out = append(out, strings.TrimSpace(s[start:i]))
				start = i + 1
			}
		}
	}
	if rest := strings.TrimSpace(s[start:]); rest != "" {
		out = append(out, rest)
	}
	return out
}

func depthAt(s string, at int) int {
	depth := 0
	for i, r := range s[:at] {
		switch r {
		case '<', '(', '[', '{':
			depth++
		case '>', ')', ']', '}':
			if r == '>' && i > 0 && s[i-1] == '-' {
				continue
			}
			depth--
		}
	}
	return depth
}

func topArrow(s string) int {
	for i := 0; i+1 < len(s); i++ {
		if s[i] == '-' && s[i+1] == '>' && depthAt(s, i) == 0 {
			return i
		}
	}
	return -1
}

func trimParens(s string) string {
	if strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") && depthAt(s, len(s)-1) == 1 {
		return s[1 : len(s)-1]
	}
	return s
}

// matchSignature matches the query's parameters, in any order, and result
// against e's; a parameter with a default may be left out. It answers 0 for
// a match up to renaming, 1 for one that instantiates one of e's type
// variables, and 2 for one that goes through an impl.
func matchSignature(qp []*typeNode, qr *typeNode, e stdEntry) (int, bool) {
	var required, optional []int
	for i, d := range e.defaulted {
		if d {
			optional = append(optional, i)
		} else {
			required = append(required, i)
		}
	}
	extra := len(qp) - len(required)
	if extra < 0 || extra > len(optional) || len(qp) > 5 {
		return 0, false
	}
	best, found := 0, false
	subsets(optional, extra, func(chosen []int) {
		params := append(append([]int{}, required...), chosen...)
		if score, ok := matchOrdered(qp, qr, e, params); ok && (!found || score < best) {
			best, found = score, true
		}
	})
	return best, found
}

// subsets calls f with each k-element subset of xs.
func subsets(xs []int, k int, f func([]int)) {
	var rec func(start int, picked []int)
	rec = func(start int, picked []int) {
		if len(picked) == k {
			f(picked)
			return
		}
		for i := start; i < len(xs); i++ {
			rec(i+1, append(picked, xs[i]))
		}
	}
	rec(0, nil)
}

// matchOrdered matches qp against e's parameters at indexes params, in any
// order, and qr against e's result.
func matchOrdered(qp []*typeNode, qr *typeNode, e stdEntry, params []int) (int, bool) {
	best, found := 0, false
	permute(len(qp), func(order []int) bool {
		u := unifier{q: map[string]string{}, c: map[string]*typeNode{}, bounds: e.bounds}
		ok := true
		for i, j := range order {
			if !u.unify(qp[i], e.params[params[j]]) {
				ok = false
				break
			}
		}
		if ok && u.unifyResult(qr, e.ret) {
			score := 0
			switch {
			case u.viaImpl:
				score = 2
			case u.instantiated:
				score = 1
			}
			if !found || score < best {
				best, found = score, true
			}
			return score == 0
		}
		return false
	})
	return best, found
}

// unifier binds the candidate's type variables to query types, and each
// query variable to one candidate variable.
type unifier struct {
	q            map[string]string    // query variable -> candidate variable
	c            map[string]*typeNode // candidate variable -> query type
	bounds       map[string][]string  // candidate variable -> interfaces it must implement
	instantiated bool
	viaImpl      bool
}

func (u *unifier) snapshot() unifier {
	s := *u
	s.q, s.c = maps.Clone(u.q), maps.Clone(u.c)
	return s
}

// unify matches a query type against a candidate's parameter type: the
// same type, or an interface the query type implements.
func (u *unifier) unify(q, c *typeNode) bool {
	if c.isVar() {
		if bound, ok := u.c[c.name]; ok {
			return equalType(bound, q)
		}
		if q.isVar() {
			if v, ok := u.q[q.name]; ok && v != c.name {
				return false
			}
			u.q[q.name] = c.name
		} else {
			for _, iface := range u.bounds[c.name] {
				if q.name != iface && !implementsName(q.name, iface) {
					return false
				}
				u.viaImpl = true
			}
			u.instantiated = true
		}
		u.c[c.name] = q
		return true
	}
	if q.isVar() {
		return false
	}
	if q.name != c.name || len(q.args) != len(c.args) {
		return u.through(q, c, false)
	}
	for i := range q.args {
		if !u.unify(q.args[i], c.args[i]) {
			return false
		}
	}
	return true
}

// unifyResult matches the query's result against a candidate's: the same
// type, or the candidate's result implements the interface the query asks
// for (`List<T>` answers an `Iter<T>`).
func (u *unifier) unifyResult(q, c *typeNode) bool {
	saved := u.snapshot()
	if u.unify(q, c) {
		return true
	}
	*u = saved
	return !q.isVar() && !c.isVar() && q.name != c.name && u.through(c, q, true)
}

// through matches sub against super by an interface sub's type implements,
// as the analysis records: each impl's interface type, with the impl's
// receiver matched against sub, is tried against super. reversed says sub
// is the candidate's type and super the query's.
func (u *unifier) through(sub, super *typeNode, reversed bool) bool {
	for _, rel := range stdImpls()[sub.name] {
		if rel.iface.name != super.name {
			continue
		}
		binding := map[string]*typeNode{}
		if !matchPattern(rel.recv, sub, binding) {
			continue
		}
		up := substitute(rel.iface, binding)
		saved := u.snapshot()
		ok := false
		if reversed {
			ok = u.unify(super, up)
		} else {
			ok = u.unify(up, super)
		}
		if ok {
			u.viaImpl = true
			return true
		}
		*u = saved
	}
	return false
}

// matchPattern binds pattern's type variables (an impl's receiver, such as
// `List<T>`) to the parts of t.
func matchPattern(pattern, t *typeNode, binding map[string]*typeNode) bool {
	if pattern.isVar() {
		if b, ok := binding[pattern.name]; ok {
			return equalType(b, t)
		}
		binding[pattern.name] = t
		return true
	}
	if pattern.name != t.name || len(pattern.args) != len(t.args) {
		return false
	}
	for i := range pattern.args {
		if !matchPattern(pattern.args[i], t.args[i], binding) {
			return false
		}
	}
	return true
}

func substitute(t *typeNode, binding map[string]*typeNode) *typeNode {
	if t.isVar() {
		if b, ok := binding[t.name]; ok {
			return b
		}
		return t
	}
	out := &typeNode{name: t.name}
	for _, a := range t.args {
		out.args = append(out.args, substitute(a, binding))
	}
	return out
}

// implRel is one impl: its receiver as written and the interface type it
// implements, with the interface's type arguments in the receiver's terms
// (`List<T>` implements `Iter<T>`, `Map<K, V>` implements `Iter<(K, V)>`).
type implRel struct {
	recv, iface *typeNode
}

func implementsName(typeName, iface string) bool {
	for _, rel := range stdImpls()[typeName] {
		if rel.iface.name == iface {
			return true
		}
	}
	return false
}

// stdImpls indexes the stdlib's impls by receiver type name. Which type
// implements which interface is the analysis's impl table
// (FileAnalysis.Impls); the interface's type arguments, which an impl may
// leave out (`impl Iter for List<T>`), come from its declaration, read off
// the impl's functions against the interface's.
var stdImpls = sync.OnceValue(func() map[string][]implRel {
	lib := std.All()
	implemented := map[string]map[string]bool{}
	for _, fa := range lib.Files {
		for ty, ifaces := range fa.Impls {
			if implemented[ty] == nil {
				implemented[ty] = map[string]bool{}
			}
			for iface, ok := range ifaces {
				if ok {
					implemented[ty][iface] = true
				}
			}
		}
	}
	ifaces := map[string]*ast.InterfaceDef{}
	var impls []*ast.ImplBlock
	for _, nodes := range lib.Nodes {
		for _, n := range nodes {
			switch d := n.(type) {
			case *ast.InterfaceDef:
				ifaces[d.Name] = d
			case *ast.ImplBlock:
				if d.Interface != nil && d.Receiver != nil {
					impls = append(impls, d)
				}
			}
		}
	}
	out := map[string][]implRel{}
	for _, d := range impls {
		recv := parseType(d.Receiver.TypeString())
		iface := parseType(d.Interface.TypeString())
		if !implemented[recv.name][iface.name] {
			continue
		}
		if def := ifaces[iface.name]; def != nil && len(iface.args) == 0 && len(def.TypeParams) > 0 {
			args, ok := inferInterfaceArgs(def, d, recv)
			if !ok {
				continue
			}
			iface = &typeNode{name: iface.name, args: args}
		}
		out[recv.name] = append(out[recv.name], implRel{recv: recv, iface: iface})
	}
	return out
})

// inferInterfaceArgs reads the type arguments an impl that does not write
// them gives its interface: an impl function's parameter types are matched
// against the interface function's, whose own type parameters are the
// unknowns and whose `self` is the receiver.
func inferInterfaceArgs(def *ast.InterfaceDef, impl *ast.ImplBlock, recv *typeNode) ([]*typeNode, bool) {
	vars := map[string]bool{}
	for _, tp := range def.TypeParams {
		vars[tp.Name] = true
	}
	for _, item := range impl.Items {
		var name string
		var params []ast.Param
		switch f := item.(type) {
		case *ast.FuncDef:
			name, params = f.Name, f.Params
		case *ast.ExternFunc:
			name, params = f.Name, f.Params
		default:
			continue
		}
		for _, m := range def.Methods {
			if m.Name != name || len(m.Params) != len(params) {
				continue
			}
			binding := map[string]*typeNode{}
			ok := true
			for i, p := range m.Params {
				if p.TypeAnnotation == nil || params[i].TypeAnnotation == nil {
					continue
				}
				want := p.TypeAnnotation.TypeString()
				if want == "self" {
					continue
				}
				if !bindVars(parseType(want), parseType(params[i].TypeAnnotation.TypeString()), vars, binding) {
					ok = false
					break
				}
			}
			if !ok {
				continue
			}
			args := make([]*typeNode, 0, len(def.TypeParams))
			for _, tp := range def.TypeParams {
				b, found := binding[tp.Name]
				if !found {
					ok = false
					break
				}
				args = append(args, b)
			}
			if ok {
				return args, true
			}
		}
	}
	return nil, false
}

// bindVars matches pattern, whose names in vars are unknowns, against t.
func bindVars(pattern, t *typeNode, vars map[string]bool, binding map[string]*typeNode) bool {
	if len(pattern.args) == 0 && vars[pattern.name] {
		if b, ok := binding[pattern.name]; ok {
			return equalType(b, t)
		}
		binding[pattern.name] = t
		return true
	}
	if pattern.name != t.name || len(pattern.args) != len(t.args) {
		return false
	}
	for i := range pattern.args {
		if !bindVars(pattern.args[i], t.args[i], vars, binding) {
			return false
		}
	}
	return true
}

func equalType(a, b *typeNode) bool {
	if a.name != b.name || len(a.args) != len(b.args) {
		return false
	}
	for i := range a.args {
		if !equalType(a.args[i], b.args[i]) {
			return false
		}
	}
	return true
}

// permute calls f with each ordering of 0..n-1 until f answers true.
func permute(n int, f func([]int) bool) {
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	var rec func(k int) bool
	rec = func(k int) bool {
		if k == n {
			return f(order)
		}
		for i := k; i < n; i++ {
			order[k], order[i] = order[i], order[k]
			if rec(k + 1) {
				return true
			}
			order[k], order[i] = order[i], order[k]
		}
		return false
	}
	rec(0)
}
