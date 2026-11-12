package hoverdoc

import (
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/format"
)

// The `..` of a literal's spread: a struct update's `{..alice, age: 31}`,
// and each spread of a list, vector, set or map literal, `[..xs, 1]`. Both
// characters answer with what the spread puts into the literal and the
// type it spreads; the head after the `..` answers as its symbol, as it
// always did. A bare `..` names the value already at the field or key the
// literal updates, and a `..` operand (`visits: .. + 1`) answers as the
// current value of the field or key it belongs to.

// structSpread claims the `..` of a struct update.
func (s *shapeSearch) structSpread(n *ast.StructLit) {
	if n.Spread == nil || n.SpreadLine == 0 {
		return
	}
	s.claim(n.SpreadLine, n.SpreadCol, 2, spanSize(spanOf(n))-1, func() string {
		base := s.spreadSubject(n.Spread)
		if base.dots == "" {
			return ""
		}
		text := "struct update: " + base.dots + " copies every field of " + base.what
		if ty := s.fa.ExprTypes[n]; ty != nil {
			text += " into the new `" + displayValueType(ty) + "`"
		}
		names := make([]string, 0, len(n.Fields))
		for _, f := range n.Fields {
			if f.Name != "" {
				names = append(names, "`"+f.Name+"`")
			}
		}
		switch len(names) {
		case 0:
		case 1:
			text += "; the field written after it (" + names[0] + ") replaces that one"
		default:
			text += "; the fields written after it (" + strings.Join(names, ", ") + ") replace those"
		}
		return text
	})
}

// collectionSpreads claims the `..` of each spread of a list, vector, set
// or map literal n; kind is the literal's shape.
func (s *shapeSearch) collectionSpreads(n ast.Node, kind string, spreads []ast.Spread) {
	size := spanSize(spanOf(n)) - 1
	for _, sp := range spreads {
		head := sp.Head
		s.claim(sp.Line, sp.Col, 2, size, func() string {
			subj := s.spreadSubject(head)
			if subj.dots == "" {
				return ""
			}
			text := kind + " spread: " + subj.dots + " puts every "
			switch kind {
			case "map":
				return text + "entry of " + subj.what + " here; a later key replaces an earlier one"
			case "set":
				return text + "member of " + subj.what + " here"
			}
			return text + "element of " + subj.what + " here, in order"
		})
	}
}

// currentValue claims a `..` written as an operand, `visits: .. + 1`: the
// current value of the field or key it belongs to (ast.CurrentValuesOf).
func (s *shapeSearch) currentValue(n *ast.CurrentValue) {
	if !n.Operand {
		return
	}
	s.claim(n.Line, n.Col, 2, 2, func() string {
		ty := s.fa.ExprTypes[n]
		if ty == nil {
			return ""
		}
		text := "the current value"
		if at := s.currentPositions()[n]; at != "" {
			text += " of " + at
		}
		return text + ", a `" + displayValueType(ty) + "`"
	})
}

// currentPositions names the field or key each `..` of the file belongs
// to: "`street`" for a field, "the key `\"home\"`" for a map key.
func (s *shapeSearch) currentPositions() map[*ast.CurrentValue]string {
	if s.positions != nil {
		return s.positions
	}
	s.positions = map[*ast.CurrentValue]string{}
	field := func(lit *ast.StructLit) {
		for _, f := range lit.Fields {
			for _, cv := range ast.CurrentValuesOf(f.Value) {
				s.positions[cv] = "`" + f.Name + "`"
			}
		}
	}
	for _, top := range s.nodes {
		ast.Inspect(top, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.StructLit:
				if v.Spread != nil {
					field(v)
				}
			case *ast.Call:
				if patch := ast.StructUpdatePatch(v); patch != nil {
					field(patch)
				}
			case *ast.MapLit:
				if len(v.Spreads) == 0 {
					break
				}
				for _, e := range v.Entries[min(v.Spreads[0].At, len(v.Entries)):] {
					for _, cv := range ast.CurrentValuesOf(e.Value) {
						s.positions[cv] = "the key `" + format.RenderNode(e.Key) + "`"
					}
				}
			}
			return true
		})
	}
	return s.positions
}

// spreadText is how a hover names a spread: the spread as code, `..xs`,
// and what it spreads, "`xs`, a `List<Int>`,".
type spreadText struct {
	dots, what string
}

// spreadSubject names the spread whose head is head, or is empty when the
// checker gave the head no type.
func (s *shapeSearch) spreadSubject(head ast.Node) spreadText {
	ty := s.fa.ExprTypes[head]
	if ty == nil {
		return spreadText{}
	}
	typ := "a `" + displayValueType(ty) + "`,"
	switch h := head.(type) {
	case *ast.CurrentValue:
		return spreadText{dots: "`..`", what: "the value this literal updates, " + typ}
	case *ast.Ident:
		return spreadText{dots: "`.." + h.Name + "`", what: "`" + h.Name + "`, " + typ}
	}
	return spreadText{dots: "`..`", what: "the value after it, " + typ}
}
