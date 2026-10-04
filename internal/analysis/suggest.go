package analysis

import (
	"strings"
	"unicode"

	"github.com/nomi-language/nomi/internal/ast"
)

// Suggestions for a misspelled name: the "did you mean" an unknown-name
// diagnostic ends with.
//
// The rule is the one rustc and the TypeScript checker use. A candidate is
// close enough when its edit distance to the name, counting an adjacent
// transposition as one edit (optimal string alignment), is at most a third of
// the name's length and at least 1. A candidate that differs only in case is
// the closest of all. Of the close candidates the nearest wins; when two are
// equally near and case does not separate them, nothing is suggested, since a
// guess between two is not a suggestion.

// didYouMean is the hint `did you mean 'x'?` for the one candidate close to
// name, or "" when none is, or when several are equally close. It goes in a
// diagnostic's Hints (TypeError.WithHint), not its message.
func didYouMean(name string, candidates []string) string {
	if best := closestName(name, candidates); best != "" {
		return "did you mean '" + best + "'?"
	}
	return ""
}

// undefinedVariableError is the error for a value name bound nowhere in
// scope. `true` and `false` are ordinary unbound names in Nomi; the Bool
// literals are the variants `True` and `False`, which the hint names.
func undefinedVariableError(n *ast.Ident, candidates []string) TypeError {
	e := errAt(n, "undefined variable '"+n.Name+"'")
	switch n.Name {
	case "true":
		return e.WithHint("the Bool literal is 'True'")
	case "false":
		return e.WithHint("the Bool literal is 'False'")
	}
	return e.WithHint(didYouMean(n.Name, candidates))
}

// closestName is the one candidate close to name, or "".
func closestName(name string, candidates []string) string {
	if name == "" {
		return ""
	}
	limit := max(1, len([]rune(name))/3)
	lower := strings.ToLower(name)
	best, bestDist, bestFold, tied := "", limit+1, 0, false
	seen := map[string]bool{}
	for _, cand := range candidates {
		if cand == name || cand == "" || seen[cand] || !isSuggestable(cand) {
			continue
		}
		seen[cand] = true
		fold := editDistance(lower, strings.ToLower(cand))
		dist := editDistance(name, cand)
		if fold == 0 {
			// Differs from name only in case.
			dist = 0
		}
		if dist > limit {
			continue
		}
		switch {
		case dist < bestDist || (dist == bestDist && fold < bestFold):
			best, bestDist, bestFold, tied = cand, dist, fold, false
		case dist == bestDist && fold == bestFold:
			tied = true
		}
	}
	if tied {
		return ""
	}
	return best
}

// isSuggestable reports whether a name is one a program could have meant: a
// name the toolchain synthesized (`__show`, `$get`) is not.
func isSuggestable(name string) bool {
	if strings.HasPrefix(name, "_") {
		return false
	}
	for _, r := range name {
		if r != '_' && r != '.' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

// editDistance is the optimal-string-alignment distance between a and b:
// insertions, deletions, substitutions and adjacent transpositions each cost
// one.
func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev2 := make([]int, len(rb)+1)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
			if i > 1 && j > 1 && ra[i-1] == rb[j-2] && ra[i-2] == rb[j-1] {
				cur[j] = min(cur[j], prev2[j-2]+1)
			}
		}
		prev2, prev, cur = prev, cur, prev2
	}
	return prev[len(rb)]
}

// valueNamesAt are the value names in scope at (line, col): functions,
// parameters and bindings declared before it, `once`s, file objects and
// variants. extra is a scope to search first (an attached test's).
func (c *checker) valueNamesAt(line, col int, extra *Scope) []string {
	pos := Pos{Line: line, Col: col}
	var names []string
	add := func(scope *Scope) {
		if scope == nil {
			return
		}
		for _, sym := range scope.AllVisible() {
			switch sym.Kind {
			case SymbolParam, SymbolBinding:
				if sym.Pos.Line > 0 && posBefore(pos, sym.Pos) {
					continue
				}
			case SymbolFunction, SymbolOnce, SymbolModule, SymbolEnumVariant:
			default:
				continue
			}
			names = append(names, sym.Name)
		}
	}
	add(extra)
	if c.fa != nil {
		add(c.fa.ScopeAt(pos))
	}
	return names
}

// typeNamesAt are the type and variant names in scope at (line, col), and
// every type the registry knows.
func (c *checker) typeNamesAt(line, col int) []string {
	var names []string
	if c.fa != nil {
		if scope := c.fa.ScopeAt(Pos{Line: line, Col: col}); scope != nil {
			for _, sym := range scope.AllVisible() {
				switch sym.Kind {
				case SymbolStruct, SymbolEnum, SymbolEnumVariant, SymbolType, SymbolTypeAlias, SymbolInterface:
					names = append(names, sym.Name)
				}
			}
		}
	}
	return append(names, c.reg.Names()...)
}

// variantNames are et's variant names.
func variantNames(et *EnumType) []string {
	names := make([]string, len(et.Variants))
	for i, v := range et.Variants {
		names[i] = v.Name
	}
	return names
}

// fieldNames are the names of fields.
func fieldNames(fields []FieldDef) []string {
	names := make([]string, len(fields))
	for i, f := range fields {
		names[i] = f.Name
	}
	return names
}

// typeMemberNames are what `Type.member` can name on typeName: its variants
// when it is an enum, and the functions its impl blocks declare.
func (c *checker) typeMemberNames(typeName string) []string {
	var names []string
	if et, ok := c.reg.Lookup(typeName).(*EnumType); ok && et != nil {
		names = variantNames(et)
	}
	if c.fa != nil {
		for name := range c.fa.TypeMethods[typeName] {
			names = append(names, name)
		}
	}
	return names
}

// importSuggestion is the "did you mean" for an import item the file does
// not export: its public names, when the item is a top-level name.
func importSuggestion(name string, modScope *Scope, ownerSegments []string) string {
	if len(ownerSegments) > 0 {
		return ""
	}
	return didYouMean(name, publicNames(modScope))
}

// publicNames are the names scope declares publicly.
func publicNames(scope *Scope) []string {
	if scope == nil {
		return nil
	}
	var names []string
	for name, sym := range scope.Symbols {
		if sym != nil && sym.Public {
			names = append(names, name)
		}
	}
	return names
}
