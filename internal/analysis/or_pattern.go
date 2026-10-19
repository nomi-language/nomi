package analysis

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// orAlternative is one name an alternative of an `or` pattern binds: the
// type it binds the name at and its site.
type orAlternative struct {
	ty  Type
	pos Pos
}

// checkOrPattern checks `A or B or ...`: each alternative against the value,
// then requires every alternative to bind the same names, each at one type.
//
// The builder makes each name one symbol whose definition sites are the
// alternatives' sites (builder.bindPatternName), so checking an alternative
// sets that symbol's type; it is read after each alternative and set back to
// the first alternative's at the end. A name an `_` starts binds nothing and
// is not compared.
func (c *checker) checkOrPattern(n *ast.OrPattern, expectedTy Type) {
	bound := make([]map[string]orAlternative, len(n.Alternatives))
	order := make([][]string, len(n.Alternatives))
	for i, alt := range n.Alternatives {
		c.checkPattern(alt, expectedTy)
		m := map[string]orAlternative{}
		for _, s := range patternSites(alt) {
			if ast.IsDiscardName(s.name) {
				continue
			}
			if _, dup := m[s.name]; dup {
				// The first alternative's repeat is the builder's to report
				// (definePatternRoot), as any pattern's is.
				if i > 0 {
					c.report(TypeError{Line: s.pos.Line, Col: s.pos.Col, EndLine: s.pos.Line, EndCol: s.pos.Col + len(s.name),
						Message: fmt.Sprintf("`%s` is bound twice in one pattern; give each binding its own name", s.name)})
				}
				continue
			}
			var ty Type
			if sym := c.fa.Definitions[s.pos]; sym != nil {
				ty = sym.Type
			}
			m[s.name] = orAlternative{ty: ty, pos: s.pos}
			order[i] = append(order[i], s.name)
		}
		bound[i] = m
	}
	first := bound[0]
	firstText := patternText(n.Alternatives[0])
	for i := 1; i < len(n.Alternatives); i++ {
		altText := patternText(n.Alternatives[i])
		for _, name := range order[0] {
			if _, ok := bound[i][name]; !ok {
				c.report(c.atPattern(n.Alternatives[i], orMissingBindingMessage(name, firstText, altText)))
			}
		}
		for _, name := range order[i] {
			b := bound[i][name]
			f, ok := first[name]
			if !ok {
				c.report(TypeError{Line: b.pos.Line, Col: b.pos.Col, EndLine: b.pos.Line, EndCol: b.pos.Col + len(name),
					Message: orMissingBindingMessage(name, altText, firstText)})
				continue
			}
			if f.ty == nil || b.ty == nil {
				continue
			}
			if err := c.unify(f.ty, b.ty, nil); err != nil && !TypesEqual(resolveTypeVar(f.ty), resolveTypeVar(b.ty)) {
				c.report(TypeError{Line: b.pos.Line, Col: b.pos.Col, EndLine: b.pos.Line, EndCol: b.pos.Col + len(name),
					Message: c.typef("`%s` is %s %s in `%s` but %s %s in `%s`; a name an `or` pattern binds has one type in every alternative",
						name, articleFor(f.ty.String()), f.ty, firstText, articleFor(b.ty.String()), b.ty, altText)})
			}
		}
	}
	for _, name := range order[0] {
		f := first[name]
		if sym := c.fa.Definitions[f.pos]; sym != nil && f.ty != nil {
			sym.Type = f.ty
		}
	}
	if expectedTy != nil && c.patternsExhaustiveOver(expectedTy, n.Alternatives) {
		if c.fa.CoveringOrPatterns == nil {
			c.fa.CoveringOrPatterns = map[*ast.OrPattern]bool{}
		}
		c.fa.CoveringOrPatterns[n] = true
	}
}

// OrPatternCovers reports whether the alternatives of or, a pattern of fa's
// file, together match every value of the type at its position
// (CoveringOrPatterns).
func (fa *FileAnalysis) OrPatternCovers(or *ast.OrPattern) bool {
	return fa != nil && fa.CoveringOrPatterns[or]
}

// DefinitionSites returns every position sym is defined at in fa, in source
// order. A name an `or` pattern binds is one symbol with a site in each
// alternative (builder.bindPatternName); every other symbol has at most one.
func (fa *FileAnalysis) DefinitionSites(sym *Symbol) []Pos {
	if fa == nil || sym == nil || sym.Kind != SymbolBinding {
		return nil
	}
	var sites []Pos
	for pos, s := range fa.Definitions {
		if s == sym {
			sites = append(sites, pos)
		}
	}
	sort.Slice(sites, func(i, j int) bool {
		if sites[i].Line != sites[j].Line {
			return sites[i].Line < sites[j].Line
		}
		return sites[i].Col < sites[j].Col
	})
	return sites
}

// orMissingBindingMessage is the error for an alternative of an `or`
// pattern that does not bind a name another alternative binds.
func orMissingBindingMessage(name, binds, lacks string) string {
	return fmt.Sprintf("`%s` is bound by `%s` but not by `%s`; every alternative of an `or` pattern must bind the same names", name, binds, lacks)
}

// atPattern is an error spanning pattern n.
func (c *checker) atPattern(n ast.Node, msg string) TypeError {
	if sp, ok := spanOf(n); ok {
		return TypeError{Line: sp.StartLine, Col: sp.StartCol, EndLine: sp.EndLine, EndCol: sp.EndCol, Message: msg}
	}
	line, col := nodeLineCol(n)
	return TypeError{Line: line, Col: col, Message: msg}
}

// orAlternatives returns the alternatives p matches by, looking through its
// `as` names and splicing nested `or` patterns: `.A or (.B or .C) as x` is
// `.A`, `.B` and `.C`. A pattern that is not an `or` is its one alternative.
// What a pattern matches is the union of what its alternatives match.
func orAlternatives(p ast.Node) []ast.Node {
	inner := ast.WithoutAs(p)
	or, ok := inner.(*ast.OrPattern)
	if !ok {
		return []ast.Node{p}
	}
	var out []ast.Node
	for _, alt := range or.Alternatives {
		out = append(out, orAlternatives(alt)...)
	}
	return out
}

// expandOrPatterns replaces each `or` pattern among patterns by its
// alternatives (orAlternatives).
func expandOrPatterns(patterns []ast.Node) []ast.Node {
	var out []ast.Node
	for _, p := range patterns {
		out = append(out, orAlternatives(p)...)
	}
	return out
}

// patternText renders a pattern as source, for a diagnostic that quotes it.
// A map pattern's keys are expressions and render as `..`.
func patternText(n ast.Node) string {
	switch p := n.(type) {
	case nil:
		return "_"
	case *ast.WildcardPattern:
		return "_"
	case *ast.IdentPattern:
		return p.Name
	case *ast.AsPattern:
		return patternText(p.Pattern) + " as " + p.Name
	case *ast.OrPattern:
		parts := make([]string, len(p.Alternatives))
		for i, alt := range p.Alternatives {
			parts[i] = patternText(alt)
			if _, isAs := alt.(*ast.AsPattern); isAs {
				parts[i] = "(" + parts[i] + ")"
			}
		}
		return strings.Join(parts, " or ")
	case *ast.IntLit:
		if p.Lexeme != "" {
			return p.Lexeme
		}
		return strconv.FormatInt(p.Value, 10)
	case *ast.FloatLit:
		if p.Lexeme != "" {
			return p.Lexeme
		}
		return strconv.FormatFloat(p.Value, 'g', -1, 64)
	case *ast.DecimalLit:
		return p.Lexeme
	case *ast.StringLit:
		return stringPatternText(p)
	case *ast.CodepointLit:
		return "'" + p.Lexeme + "'"
	case *ast.Binary:
		return patternText(p.Left) + " " + p.Op + " " + patternText(p.Right)
	case *ast.EnumPattern:
		s := p.Variant.TypeString()
		switch {
		case p.Payload != nil:
			if tp, ok := p.Payload.(*ast.TuplePattern); ok && tp.Flat {
				return s + "(" + patternListText(tp.Patterns) + ")"
			}
			return s + "(" + patternText(p.Payload) + ")"
		case p.Binding != "":
			return s + "(" + p.Binding + ")"
		}
		return s
	case *ast.StructPattern:
		var b strings.Builder
		b.WriteString(shapeHeadText("#", p.TypeName))
		b.WriteString("{")
		for i, f := range p.Fields {
			if i > 0 {
				b.WriteString(", ")
			}
			switch {
			case f.Pattern != nil:
				b.WriteString(f.Name + ": " + patternText(f.Pattern))
			case f.Binding != "" && f.Binding != f.Name:
				b.WriteString(f.Name + ": " + f.Binding)
			default:
				b.WriteString(f.Name)
			}
		}
		b.WriteString("}")
		return b.String()
	case *ast.TuplePattern:
		return shapeHeadText("#", p.TypeName) + "(" + patternListText(p.Patterns) + ")"
	case *ast.VectorPattern:
		parts := make([]string, 0, len(p.Heads)+len(p.Tails)+1)
		for _, h := range p.Heads {
			parts = append(parts, patternText(h))
		}
		if p.Spread {
			rest := ".."
			if p.Rest != nil {
				rest += patternText(p.Rest)
			}
			parts = append(parts, rest)
		}
		for _, t := range p.Tails {
			parts = append(parts, patternText(t))
		}
		return shapeHeadText("#", p.TypeName) + "[" + strings.Join(parts, ", ") + "]"
	case *ast.SetPattern:
		return shapeHeadText("%", p.TypeName) + "[..]"
	case *ast.ListPattern:
		parts := make([]string, 0, len(p.Heads)+1)
		for _, h := range p.Heads {
			parts = append(parts, patternText(h))
		}
		if p.TailSpread != nil {
			parts = append(parts, ".."+patternText(p.TailSpread))
		}
		prefix := ""
		if p.TypeName != nil {
			prefix = p.TypeName.TypeString()
		}
		return prefix + "[" + strings.Join(parts, ", ") + "]"
	case *ast.MapPattern:
		return shapeHeadText("%", p.TypeName) + "{..}"
	}
	return "_"
}

func patternListText(ps []ast.Node) string {
	parts := make([]string, len(ps))
	for i, p := range ps {
		parts[i] = patternText(p)
	}
	return strings.Join(parts, ", ")
}

// shapeHeadText is how a literal or pattern of a built-in shape starts: its
// symbol and, when it names one, the type built on the shape (`#`, `#Point`,
// `%Kvs`, `#.Circle`).
func shapeHeadText(sigil string, name ast.TypeExpr) string {
	if name == nil {
		return sigil
	}
	return sigil + name.TypeString()
}
