package analysis

import (
	"maps"
	"sort"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// Annotation help: where nothing says which way a value flows, two types
// must be one (spec §5 *Assignability*), so two typed collections joined
// with `+`, two typed spreads, the branches of an `if` or `case` and the
// elements of an unannotated list are refused when they differ, even
// where both convert to one type. An annotation says the way. The help
// names it as source to paste: `annotate the binding: `mine: List<(Route,
// Handler)> = ...``.
//
// The suggested type is found in this order:
//
//  1. Walking the two types together (annotationJoin): where they differ, a
//     type alias the file can name whose type both parts convert to; one
//     the enclosing function's signature names wins a tie, and a tie it
//     does not break suggests nothing. Where no alias fits, the walk goes
//     into a List, Map, tuple or function result, and failing that takes
//     the part the other converts to.
//  2. Failing that, one side's whole type, when exactly one of the two
//     passes the check below.
//
// A candidate is spelled as source and resolved as an annotation is, so a
// name the file cannot write is never suggested, and then checked as the
// annotation would check: the value checked against it, as a binding's
// value is (annotationTrialPasses). One that fails suggests nothing; the error
// stands as it was.

// aliasCandidate is a type alias the file names as name, whose type is t.
type aliasCandidate struct {
	name string
	t    Type
}

// visibleAliases are the type aliases the file can name: its own and the
// ones it imports by name, bare, and each public alias of a module it
// imports, qualified (`http.Handler`). A bound alias (`A and B`) is not a
// type a value has and is left out. They are in name order.
func (c *checker) visibleAliases() []aliasCandidate {
	if c.aliasesDone {
		return c.aliases
	}
	c.aliasesDone = true
	if c.fa == nil || c.fa.ModuleScope == nil || c.reg == nil {
		return nil
	}
	var names []string
	for name, sym := range c.fa.ModuleScope.Symbols {
		if plainAlias(sym) {
			names = append(names, name)
			continue
		}
		if sym.Kind != SymbolModule || sym.ModuleScope == nil {
			continue
		}
		c.modules = append(c.modules, name)
		for member, m := range sym.ModuleScope.Symbols {
			if m.Public && m.Resolved == nil && plainAlias(m) {
				names = append(names, name+"."+member)
			}
		}
	}
	sort.Strings(names)
	sort.Strings(c.modules)
	for _, name := range names {
		t := c.reg.Lookup(name)
		if t == nil || isNilType(t) || containsTypeVar(t) || ContainsTypeParam(t) {
			continue
		}
		c.aliases = append(c.aliases, aliasCandidate{name: name, t: t})
	}
	return c.aliases
}

// plainAlias reports whether sym names a typealias of one type.
func plainAlias(sym *Symbol) bool {
	a := resolvedTypeAlias(sym)
	if a == nil {
		return false
	}
	ta, ok := a.Node.(*ast.TypeAlias)
	return ok && len(ta.Bounds) == 0 && ta.TargetTypeExpr != nil
}

// signatureTypeNames are the type names the enclosing function's
// parameters and result spell, bare or qualified.
func (c *checker) signatureTypeNames() map[string]bool {
	out := map[string]bool{}
	fn := c.currentFnDecl
	if fn == nil {
		return out
	}
	var tes []ast.TypeExpr
	for _, p := range fn.Params {
		if p.TypeAnnotation != nil {
			tes = append(tes, p.TypeAnnotation)
		}
	}
	if fn.ReturnTypeExpr != nil {
		tes = append(tes, fn.ReturnTypeExpr)
	}
	for _, te := range tes {
		ast.Inspect(te, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.SimpleType:
				out[v.Name] = true
			case *ast.QualifiedType:
				if m, ok := v.Member.(*ast.SimpleType); ok {
					out[v.Module+"."+m.Name] = true
				}
			}
			return true
		})
	}
	return out
}

// spellType is t as the file writes it in an annotation: each named type
// by the name the file resolves to it, bare or qualified by a module the
// file imports. ok is false for a type with a part no name reaches, or one
// no annotation spells (an open type, a function with defaults).
func (c *checker) spellType(t Type, depth int) (string, bool) {
	t = resolveTypeVar(t)
	if t == nil || isNilType(t) || depth > maxTypeRenderDepth {
		return "", false
	}
	depth++
	each := func(ts []Type) ([]string, bool) {
		out := make([]string, len(ts))
		for i, x := range ts {
			s, ok := c.spellType(x, depth)
			if !ok {
				return nil, false
			}
			out[i] = s
		}
		return out, true
	}
	switch v := t.(type) {
	case *ListType:
		e, ok := c.spellType(v.Elem, depth)
		return "List<" + e + ">", ok
	case *MapType:
		kv, ok := each([]Type{v.Key, v.Val})
		if !ok {
			return "", false
		}
		return "Map<" + kv[0] + ", " + kv[1] + ">", true
	case *TupleType:
		if len(v.Elems) == 0 {
			return "Unit", true
		}
		elems, ok := each(v.Elems)
		return "(" + strings.Join(elems, ", ") + ")", ok
	case *FuncType:
		if v.DefaultCount != 0 || len(v.WhereBounds) != 0 {
			return "", false
		}
		params, ok := each(v.Params)
		if !ok {
			return "", false
		}
		ret := "Unit"
		if v.Return != nil {
			if ret, ok = c.spellType(v.Return, depth); !ok {
				return "", false
			}
			if _, fn := resolveTypeVar(v.Return).(*FuncType); fn {
				ret = "(" + ret + ")"
			}
		}
		return "(" + strings.Join(params, ", ") + ") -> " + ret, true
	case *PrimitiveType:
		return c.spellNominal(t, v.Name_, v.Origin, nil, depth)
	case *StructType:
		return c.spellNominal(t, v.Name, v.Origin, v.TypeArgs, depth)
	case *EnumType:
		return c.spellNominal(t, v.Name, v.Origin, v.TypeArgs, depth)
	case *DistinctType:
		return c.spellNominal(t, v.Name, v.Origin, v.TypeArgs, depth)
	case *InterfaceType:
		return c.spellNominal(t, v.Name, v.Origin, v.TypeArgs, depth)
	}
	return "", false
}

// spellNominal is the named type t, of the given name, origin and type
// arguments, as the file writes it.
func (c *checker) spellNominal(t Type, name, origin string, args []Type, depth int) (string, bool) {
	c.visibleAliases()
	base := ""
	for _, cand := range append([]string{name}, c.qualifiedNames(name)...) {
		if spelledAs(c.reg.Lookup(cand), t, name, origin) {
			base = cand
			break
		}
	}
	if base == "" {
		return "", false
	}
	if len(args) == 0 {
		return base, true
	}
	parts := make([]string, len(args))
	for i, a := range args {
		s, ok := c.spellType(a, depth)
		if !ok {
			return "", false
		}
		parts[i] = s
	}
	return base + "<" + strings.Join(parts, ", ") + ">", true
}

// qualifiedNames are name qualified by each module the file imports.
func (c *checker) qualifiedNames(name string) []string {
	out := make([]string, len(c.modules))
	for i, m := range c.modules {
		out[i] = m + "." + name
	}
	return out
}

// spelledAs reports whether got, a type a name resolves to, is the named
// type t of the given name and origin, or its generic declaration.
func spelledAs(got, t Type, name, origin string) bool {
	if got == nil || isNilType(got) {
		return false
	}
	if p, ok := t.(*PrimitiveType); ok {
		return TypesIdentical(got, p)
	}
	switch g := got.(type) {
	case *StructType:
		return g.Name == name && g.Origin == origin
	case *EnumType:
		return g.Name == name && g.Origin == origin
	case *DistinctType:
		return g.Name == name && g.Origin == origin
	case *InterfaceType:
		return g.Name == name && g.Origin == origin
	}
	return false
}

// annotationJoin walks two types that do not unify, spelling a type both
// convert to (step 1 above).
type annotationJoin struct {
	c         *checker
	named     map[string]bool
	ambiguous bool
}

// join is the spelling of a type a and b both convert to, or false.
func (j *annotationJoin) join(a, b Type, depth int) (string, bool) {
	a, b = resolveTypeVar(a), resolveTypeVar(b)
	if j.ambiguous || a == nil || b == nil || isNilType(a) || isNilType(b) || depth > maxTypeRenderDepth {
		return "", false
	}
	if TypesIdentical(a, b) {
		return j.c.spellType(a, depth)
	}
	var fit, named []aliasCandidate
	for _, al := range j.c.visibleAliases() {
		if j.c.fitsInto(al.t, a) && j.c.fitsInto(al.t, b) {
			fit = append(fit, al)
			if j.named[al.name] {
				named = append(named, al)
			}
		}
	}
	switch {
	case len(named) == 1:
		return named[0].name, true
	case len(named) > 1, len(named) == 0 && len(fit) > 1:
		j.ambiguous = true
		return "", false
	case len(fit) == 1:
		return fit[0].name, true
	}
	if s, ok := j.joinParts(a, b, depth+1); ok || j.ambiguous {
		return s, ok
	}
	toA, toB := j.c.fitsInto(a, b), j.c.fitsInto(b, a)
	switch {
	case toA && !toB:
		return j.c.spellType(a, depth)
	case toB && !toA:
		return j.c.spellType(b, depth)
	}
	return "", false
}

// joinParts joins a and b part by part when they are one List, Map, tuple
// or function shape. A function's parameters must be the same: a type
// both functions convert to takes parameters each accepts, which no part
// of either names.
func (j *annotationJoin) joinParts(a, b Type, depth int) (string, bool) {
	switch av := a.(type) {
	case *ListType:
		if bv, ok := b.(*ListType); ok {
			e, ok := j.join(av.Elem, bv.Elem, depth)
			return "List<" + e + ">", ok
		}
	case *MapType:
		if bv, ok := b.(*MapType); ok {
			k, ok := j.join(av.Key, bv.Key, depth)
			if !ok {
				return "", false
			}
			v, ok := j.join(av.Val, bv.Val, depth)
			return "Map<" + k + ", " + v + ">", ok
		}
	case *TupleType:
		bv, ok := b.(*TupleType)
		if !ok || len(av.Elems) != len(bv.Elems) {
			return "", false
		}
		parts := make([]string, len(av.Elems))
		for i := range av.Elems {
			if parts[i], ok = j.join(av.Elems[i], bv.Elems[i], depth); !ok {
				return "", false
			}
		}
		return "(" + strings.Join(parts, ", ") + ")", true
	case *FuncType:
		bv, ok := b.(*FuncType)
		if !ok || len(av.Params) != len(bv.Params) || av.DefaultCount != 0 || bv.DefaultCount != 0 ||
			len(av.WhereBounds) != 0 || len(bv.WhereBounds) != 0 || av.Return == nil || bv.Return == nil {
			return "", false
		}
		params := make([]string, len(av.Params))
		for i := range av.Params {
			if !TypesIdentical(av.Params[i], bv.Params[i]) {
				return "", false
			}
			if params[i], ok = j.c.spellType(av.Params[i], depth); !ok {
				return "", false
			}
		}
		ret, ok := j.join(av.Return, bv.Return, depth)
		return "(" + strings.Join(params, ", ") + ") -> " + ret, ok
	}
	return "", false
}

// annotationFor is the spelling of the type to suggest for two types a and
// b that do not unify, and the type it resolves to, wrapped by wrap (an
// element type into its list), where passes says the annotation would
// check. ok is false when there is none, or more than one with nothing to
// choose between them.
func (c *checker) annotationFor(a, b Type, wrap func(string) string, passes func(Type) bool) (string, Type, bool) {
	if c.annotationTrial || a == nil || b == nil || containsTypeVar(a) || containsTypeVar(b) {
		return "", nil, false
	}
	j := &annotationJoin{c: c, named: c.signatureTypeNames()}
	s, ok := j.join(a, b, 0)
	if j.ambiguous {
		return "", nil, false
	}
	if ok {
		s = wrap(s)
		if t := c.resolveHelpType(s); t != nil && passes(t) {
			return s, t, true
		}
	}
	var found []string
	var foundT Type
	for _, side := range []Type{a, b} {
		ss, ok := c.spellType(side, 0)
		if !ok {
			continue
		}
		ss = wrap(ss)
		if ss == s {
			continue
		}
		if t := c.resolveHelpType(ss); t != nil && passes(t) {
			found, foundT = append(found, ss), t
		}
	}
	if len(found) != 1 {
		return "", nil, false
	}
	return found[0], foundT, true
}

// resolveHelpType resolves s, a type spelled as source, as an annotation in
// the function being checked resolves it, or nil when it does not parse or
// names a type the file cannot.
func (c *checker) resolveHelpType(s string) Type {
	nodes, err := parser.Parse(lexer.Lex("fn annotation_help(x: " + s + ") -> Unit end\n"))
	if err != nil || len(nodes) != 1 {
		return nil
	}
	fn, ok := nodes[0].(*ast.FuncDef)
	if !ok || len(fn.Params) != 1 || fn.Params[0].TypeAnnotation == nil {
		return nil
	}
	t, err := ResolveDeclaredType(fn.Params[0].TypeAnnotation, c.reg, c.fnTypeParams, nil)
	if err != nil || t == nil || isNilType(t) {
		return nil
	}
	return t
}

// annotationTrialPasses reports whether value, checked against want as a
// binding's value is under an annotation, checks: no error, and a type that
// flows into want. Whatever the trial records is put back.
func (c *checker) annotationTrialPasses(value ast.Node, want Type) bool {
	mark, undeterminedMark := len(c.errors), len(c.undetermined)
	var exprTypes, expectedTypes, patternTypes map[ast.Node]Type
	if c.fa != nil {
		exprTypes, expectedTypes, patternTypes = maps.Clone(c.fa.ExprTypes), maps.Clone(c.fa.ExpectedTypes), maps.Clone(c.fa.PatternTypes)
	}
	saved := saveTypeVars(want)
	prev := c.annotationTrial
	c.annotationTrial = true
	ty := c.checkNodeExpecting(value, want)
	ok := len(c.errors) == mark && ty != nil && c.fitsInto(want, ty)
	c.annotationTrial = prev
	saved.restore()
	c.errors, c.undetermined = c.errors[:mark], c.undetermined[:undeterminedMark]
	if c.fa != nil {
		c.fa.ExprTypes, c.fa.ExpectedTypes, c.fa.PatternTypes = exprTypes, expectedTypes, patternTypes
	}
	return ok
}

// bindingHelp is e with the help that annotates the binding b as s, the
// spelling of a type, and the edit that writes it.
func bindingHelp(e TypeError, b *ast.Binding, s string) TypeError {
	e = e.WithHint("annotate the binding: `" + b.Name + ": " + s + " = ...`")
	at := b.Col + len(b.Name)
	return e.withFix(parser.Replace("Annotate `"+b.Name+"` as `"+s+"`", b.Line, at, b.Line, at, ": "+s))
}

// unannotatedBindingOf is the binding with no annotation that n, a name,
// reads, or nil.
func (c *checker) unannotatedBindingOf(n ast.Node) *ast.Binding {
	id, ok := ungroupExpr(n).(*ast.Ident)
	if !ok || c.fa == nil {
		return nil
	}
	sym := c.fa.References[Pos{Line: id.Line, Col: id.Col}]
	if sym == nil || sym.Kind != SymbolBinding {
		return nil
	}
	b, ok := sym.Node.(*ast.Binding)
	if !ok || b.TypeAnnotation != nil || b.Name != id.Name || b.Value == nil {
		return nil
	}
	return b
}

// valueAnnotationHelp is e, the error that the value n's parts have types
// a and b that do not unify, with the help that annotates n: the binding
// whose value n is, or n bound on its own. wrap spells n's type from a
// type of its parts. The annotation must check n (annotationTrialPasses).
func (c *checker) valueAnnotationHelp(e TypeError, n ast.Node, a, b Type, wrap func(string) string) TypeError {
	s, _, ok := c.annotationFor(a, b, wrap, func(t Type) bool { return c.annotationTrialPasses(n, t) })
	if !ok {
		return e
	}
	if bind := c.unannotatedBinding; bind != nil && ungroupExpr(bind.Value) == ungroupExpr(n) {
		return bindingHelp(e, bind, s)
	}
	return e.WithHint("bind the value with an annotation: `value: " + s + " = ...`")
}

// operandAnnotationHelp is e, the error that the values ns, of types ts,
// must have one type, with the help that annotates the bindings each reads
// whose type is not the suggested one. a and b are the two types that do
// not unify, and wrap spells a value's type from a type of their shape.
// Each value that would change must read a binding with no annotation,
// whose value checks against the suggested type.
func (c *checker) operandAnnotationHelp(e TypeError, ns []ast.Node, ts []Type, a, b Type, wrap func(string) string) TypeError {
	return c.operandAnnotationHelpWith(e, ns, ts, a, b, wrap, func(Type) bool { return true })
}

// operandAnnotationHelpWith is operandAnnotationHelp where also must hold
// of the suggested type too.
func (c *checker) operandAnnotationHelpWith(e TypeError, ns []ast.Node, ts []Type, a, b Type, wrap func(string) string, also func(Type) bool) TypeError {
	var bindings []*ast.Binding
	passes := func(t Type) bool {
		bindings = bindings[:0]
		if !also(t) {
			return false
		}
		for i, n := range ns {
			if ts[i] == nil || TypesIdentical(t, ts[i]) {
				continue
			}
			bind := c.unannotatedBindingOf(n)
			if bind == nil || !c.annotationTrialPasses(bind.Value, t) {
				return false
			}
			bindings = append(bindings, bind)
		}
		return len(bindings) > 0
	}
	s, t, ok := c.annotationFor(a, b, wrap, passes)
	if !ok || !passes(t) {
		return e
	}
	if len(bindings) == 1 {
		return bindingHelp(e, bindings[0], s)
	}
	seen := map[*ast.Binding]bool{}
	for _, bind := range bindings {
		if !seen[bind] {
			seen[bind] = true
			e = e.WithHint("annotate the binding: `" + bind.Name + ": " + s + " = ...`")
		}
	}
	return e
}

// spreadAnnotationHelp is e, the error that two spreads of the list literal
// n have element types a and b that do not unify, with the help that
// annotates the bindings its spreads read as one list type. Every spread
// must have been checked, and each element must check against the
// suggested element type.
func (c *checker) spreadAnnotationHelp(e TypeError, n *ast.ListLit, a, b Type) TypeError {
	if c.annotationTrial || c.fa == nil {
		return e
	}
	ns := make([]ast.Node, len(n.Spreads))
	ts := make([]Type, len(n.Spreads))
	for i, sp := range n.Spreads {
		t, ok := c.fa.ExprTypes[sp.Head]
		if !ok || t == nil {
			return e
		}
		ns[i], ts[i] = sp.Head, t
	}
	return c.operandAnnotationHelpWith(e, ns, ts, a, b, listOf, func(t Type) bool {
		lt, ok := t.(*ListType)
		if !ok {
			return false
		}
		for _, item := range n.Items {
			if !c.annotationTrialPasses(item, lt.Elem) {
				return false
			}
		}
		return true
	})
}

// sameSpelling is the identity wrap.
func sameSpelling(s string) string { return s }

// listOf wraps an element type's spelling into its list's.
func listOf(s string) string { return "List<" + s + ">" }
