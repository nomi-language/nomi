package irbuild

import (
	"github.com/nomi-language/nomi/internal/ast"
)

// Refusing a statement without losing the names it introduced.
//
// # The rule
//
// A statement that refuses its initializer and returns without binding the
// name makes every later mention of that name report `unbound name` too: one
// real refusal produces a report per subsequent use. The same holds for every
// statement kind that introduces a name: a binding, `once`, the destructuring
// statements, and `assert pattern = expr`.
//
// So the rule is general rather than per-site: **a refused statement still
// binds every name it declares.** kindInvalid is the binding, so nothing
// downstream emits against it and no later mention is reported (see the Ident
// arm of expr()).
//
// # Why this is not "hiding a subtree"
//
// The refusal that matters is recorded at the statement — `once binding`,
// `map destructuring`, `pattern destructuring` — with its own position and its
// own name. What is suppressed is only the ECHO of that refusal at each use of
// the name it defined, which names no construct anybody has to lower. The
// complete-blocker-set machinery exists so a file's set names every construct
// blocking it; a construct that appears in the set only because another one was
// refused is the opposite of that.

// patternBinders returns every name a pattern binds, at any depth.
//
// Syntactic, and deliberately so: it must answer for patterns this builder
// cannot MATCH, which is the whole case it exists for. That is also why it does
// not reuse case.go's walkers — those resolve types as they go and refuse what
// they cannot represent, and a refusal is exactly what has already happened by
// the time this is called.
func patternBinders(p ast.Node) []string {
	if isNilNode(p) {
		return nil
	}
	switch t := p.(type) {
	case *ast.IdentPattern:
		return []string{t.Name}

	case *ast.EnumPattern:
		// A variant binds either one name directly (`Some(x)`) or a nested
		// pattern (`Some((a, b))`), never both — see ast.EnumPattern.Binding.
		if t.Binding != "" {
			return []string{t.Binding}
		}
		return patternBinders(t.Payload)

	case *ast.StructPattern:
		var out []string
		for _, f := range t.Fields {
			out = appendPatternField(out, f)
		}
		return out

	case *ast.TuplePattern:
		var out []string
		for _, sub := range t.Patterns {
			out = append(out, patternBinders(sub)...)
		}
		return out

	case *ast.ListPattern:
		var out []string
		for _, sub := range t.Heads {
			out = append(out, patternBinders(sub)...)
		}
		return append(out, patternBinders(t.TailSpread)...)

	case *ast.MapPattern:
		var out []string
		for _, e := range t.Entries {
			out = append(out, patternBinders(e.Pattern)...)
		}
		return out
	}
	// A wildcard and every literal pattern bind nothing.
	return nil
}

// appendPatternField adds the name a struct-pattern field binds. `Point{x}` puns
// (Binding == Name); `Point{x: a}` renames; `Point{x: 1}` matches and binds
// nothing, which is what a non-nil Pattern means.
func appendPatternField(out []string, f ast.StructPatternField) []string {
	if f.Pattern != nil {
		return append(out, patternBinders(f.Pattern)...)
	}
	name := f.Binding
	if name == "" {
		name = f.Name
	}
	if name == "" {
		return out
	}
	return append(out, name)
}

// bindPatternNamesInvalid is bindDeclaredNamesInvalid for a PARAMETER's
// pattern, which is not a statement and so has no declaredNames entry.
//
// Same rule, same reason: a destructuring parameter this builder refused still
// puts its names in the body's scope in the source, so leaving them unbound
// reports the refusal again at every use: `fn scale_point({x, y}: Point)`
// would report one refused pattern three times.
func (g *gen) bindPatternNamesInvalid(pat ast.Node) {
	for _, name := range patternBinders(pat) {
		if ast.IsDiscardName(name) {
			continue
		}
		// kindInvalid: cascade — same rule for a parameter pattern; see bindDeclaredNamesInvalid.
		if l, bound := g.top()[name]; bound && l.k != kindInvalid {
			continue
		}
		g.bind(name, local{k: kindInvalid})
	}
}
