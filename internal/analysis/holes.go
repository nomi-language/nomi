package analysis

import (
	"fmt"
	"sort"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// A `_` read as a value is an error (checkDiscardRead), and it also works
// as a typed hole, as in Idris, Agda and Haskell: its error says what type
// the place it stands in expects, and which values in scope have that
// type. A program with a hole never runs, so writing `_` where an
// expression goes asks the checker what fits there.

// maxHoleCandidates is the most values a hole's help lists.
const maxHoleCandidates = 8

// holeHints is e with the hole's help: the type expected at n, and the
// values in scope there that have it, nearest binding first, when any
// does. e is unchanged when nothing gives the place a type.
func (c *checker) holeHints(e TypeError, n *ast.Ident) TypeError {
	if c.fa == nil {
		return e
	}
	want := c.fa.ExpectedTypes[n]
	if want == nil || hasUnsolved(want) {
		return e
	}
	shown := c.typef("%s", want)
	e = e.WithHint(fmt.Sprintf("as a hole, it stands for a value of type `%s`", shown))
	names := c.holeCandidates(n, want)
	if len(names) == 0 {
		return e
	}
	return e.WithHint(fmt.Sprintf("in scope with that type: %s", strings.Join(backquoted(names), ", ")))
}

// hasUnsolved reports whether t is or holds an inference variable no
// constraint solved, which a hole's help cannot name.
func hasUnsolved(t Type) bool {
	return strings.Contains(fmt.Sprint(resolveTypeVar(t)), "?")
}

// holeCandidates are the names of the values in scope at n whose type fits
// want: parameters and bindings declared before n, nearest first, then
// `once` values and functions, then nullary enum variants, each group in
// name order, at most maxHoleCandidates.
func (c *checker) holeCandidates(n *ast.Ident, want Type) []string {
	pos := Pos{Line: n.Line, Col: n.Col}
	scope := c.fa.ScopeAt(pos)
	if scope == nil {
		return nil
	}
	type cand struct {
		name  string
		group int
		at    Pos
	}
	var cands []cand
	seen := map[string]bool{}
	for _, sym := range scope.AllVisible() {
		if sym == nil || sym.Type == nil || seen[sym.Name] || strings.HasPrefix(sym.Name, "_") || !isSuggestable(sym.Name) {
			continue
		}
		group := 0
		switch sym.Kind {
		case SymbolParam, SymbolBinding:
			if sym.Pos.Line == 0 || posBefore(pos, sym.Pos) {
				continue
			}
		case SymbolOnce, SymbolFunction:
			group = 1
		case SymbolEnumVariant:
			if _, payload := sym.Type.(*FuncType); payload {
				continue
			}
			group = 2
		default:
			continue
		}
		if !c.fitsInto(want, sym.Type) {
			continue
		}
		seen[sym.Name] = true
		cands = append(cands, cand{sym.Name, group, sym.Pos})
	}
	sort.SliceStable(cands, func(i, j int) bool {
		a, b := cands[i], cands[j]
		if a.group != b.group {
			return a.group < b.group
		}
		if a.group == 0 && a.at != b.at {
			return posBefore(b.at, a.at)
		}
		return a.name < b.name
	})
	var out []string
	for _, c := range cands {
		if len(out) == maxHoleCandidates {
			break
		}
		out = append(out, c.name)
	}
	return out
}
