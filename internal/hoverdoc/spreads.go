package hoverdoc

import (
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// The `..` of a literal's spread: a struct update's `.{..alice, age: 31}`,
// and each spread of a list, vector, set or map literal, `[..xs, 1]`. Both
// characters answer with what the spread puts into the literal and the
// type it spreads; the head after the `..` answers as its symbol, as it
// always did. A bare `..` names the value already at the field or key the
// literal updates.

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
