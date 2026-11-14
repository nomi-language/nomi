package hoverdoc

import (
	"strconv"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// A typed literal reads like an interpolated String, `Html"<p>${name}</p>"`,
// and is not one (spec §18 *Typed literals*). A hover on its prefix, its
// quotes or its text adds a line saying what it is, what builds it and what
// each `${}` takes; a hover on a slot's expression adds what that slot
// takes. Both read the checker's record of the literal
// (analysis.FileAnalysis.TypedLiterals): the impl's `I` for a handler, and
// for an `Html` template what each slot's place in the HTML takes.

// TypedLiteralNote is the line a hover over pos adds when pos is on a
// typed literal, or "" when there is none. at is the literal's extent,
// from its prefix through its closing quote. inSlot reports that pos is
// on a slot's expression: the line is then the slot's, which goes under
// the expression's own hover.
func TypedLiteralNote(fa *analysis.FileAnalysis, nodes []ast.Node, pos analysis.Pos) (note string, at ast.Span, inSlot bool) {
	if fa == nil || len(fa.TypedLiterals) == 0 {
		return "", ast.Span{}, false
	}
	lit, ext := typedLiteralAt(nodes, pos)
	if lit == nil {
		return "", ast.Span{}, false
	}
	rec := fa.TypedLiterals[lit]
	if rec == nil {
		return "", ast.Span{}, false
	}
	for _, part := range lit.Parts {
		se, ok := part.(ast.StringExpr)
		if !ok || !spanCovers(extentOf(se.Expr), pos) {
			continue
		}
		takes := rec.Slot
		if rec.Interface == "" {
			takes = rec.HtmlSlots[se.Expr]
		}
		if takes == nil {
			return "", ext, true
		}
		return "a slot of the `" + lit.Tag + "` literal: takes " + aType(displayValueType(takes)), ext, true
	}
	return typedLiteralLine(fa, lit, rec), ext, false
}

// typedLiteralLine is the hover line for the literal lit itself.
func typedLiteralLine(fa *analysis.FileAnalysis, lit *ast.TaggedString, rec *analysis.TypedLiteral) string {
	slots := 0
	for _, part := range lit.Parts {
		if _, ok := part.(ast.StringExpr); ok {
			slots++
		}
	}
	text := "*typed literal*: "
	ty := fa.ExprTypes[lit]
	if ty != nil {
		shown := displayValueType(ty)
		text += aType(shown)
		if shown != "String" {
			text += ", not a `String`,"
		}
		text += " "
	}
	if rec.Interface == "" {
		text += "read by the compiler as an HTML template"
	} else {
		text += "built by `" + lit.Tag + "`'s `" + rec.Interface + "` impl"
	}
	if slots == 0 {
		return text + " from its text"
	}
	// The text around the slots is one part more than the slots, the
	// first and last present even when empty (spec §18 *Desugaring*).
	text += " from " + plural(slots+1, "text part") + " and " + plural(slots, "slot") + "; "
	if rec.Interface != "" {
		if rec.Slot == nil {
			return strings.TrimSuffix(text, "; ")
		}
		return text + "each `${}` takes " + aType(displayValueType(rec.Slot))
	}
	// An `Html` template's slots take what their place in the HTML shows.
	var kinds []string
	seen := map[string]bool{}
	for _, part := range lit.Parts {
		se, ok := part.(ast.StringExpr)
		if !ok {
			continue
		}
		if t := rec.HtmlSlots[se.Expr]; t != nil {
			if k := aType(displayValueType(t)); !seen[k] {
				seen[k] = true
				kinds = append(kinds, k)
			}
		}
	}
	switch len(kinds) {
	case 0:
		return strings.TrimSuffix(text, "; ")
	case 1:
		return text + "each `${}` takes " + kinds[0]
	}
	return text + "each `${}` takes, by where it stands, " + strings.Join(kinds[:len(kinds)-1], ", ") + " or " + kinds[len(kinds)-1]
}

// plural is n and the noun, made plural when n is not 1.
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}

// typedLiteralAt is the innermost typed literal whose extent, prefix
// through closing quote, holds pos, and that extent.
func typedLiteralAt(nodes []ast.Node, pos analysis.Pos) (*ast.TaggedString, ast.Span) {
	var best *ast.TaggedString
	var bestSpan ast.Span
	for _, top := range nodes {
		ast.Inspect(top, func(n ast.Node) bool {
			lit, ok := n.(*ast.TaggedString)
			if !ok {
				return true
			}
			sp := spanOf(lit)
			if sp.IsZero() {
				return true
			}
			// The prefix stands before the quote the literal's position
			// names.
			if start := lit.Col - len(lit.Tag); lit.Line == sp.StartLine && start >= 1 && start < sp.StartCol {
				sp.StartCol = start
			}
			if spanCovers(sp, pos) && (best == nil || spanSize(sp) < spanSize(bestSpan)) {
				best, bestSpan = lit, sp
			}
			return true
		})
	}
	return best, bestSpan
}

// spanCovers reports whether pos is within sp, its end excluded.
func spanCovers(sp ast.Span, pos analysis.Pos) bool {
	if sp.IsZero() {
		return false
	}
	if pos.Line < sp.StartLine || pos.Line > sp.EndLine {
		return false
	}
	if pos.Line == sp.StartLine && pos.Col < sp.StartCol {
		return false
	}
	if pos.Line == sp.EndLine && pos.Col >= sp.EndCol {
		return false
	}
	return true
}
