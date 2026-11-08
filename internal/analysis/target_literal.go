package analysis

import (
	"strconv"

	"github.com/nomi-language/nomi/internal/ast"
)

// A string literal with no tag, written where a type with a CheckedLiteral
// impl is expected, is a checked literal of that type (spec §18 *Literals
// typed by their position*): `u: Url = "https://example.com"`, an argument to
// a `Url` parameter, a `Url` field, a returned `Url`, an element of a
// `List<Url>`. It is the same rule a dot-variant follows: the position names
// the type, and the literal is read as that type's literal. Only a literal
// takes its type from the position; a String value never becomes a Url.
//
// The literal keeps its source form, so the formatter writes what was
// written. The checker gives it a twin, the `<Type>"..."` it means, and checks
// that as any checked literal: FileAnalysis.TargetLiterals maps the literal to
// the twin, and TargetLiteralBuilds maps the twin to the `build` the position
// chose, which a twin cannot find by its tag's name, since the type need not
// be in scope by that name where the literal is written. The IR builder lowers
// the literal as its twin.
//
// `Literal` impls are not included: such a literal is its handler's result,
// usually a `Result<T, E>` built when the program runs, so a position that
// expects a T would still not get one.

// untaggedString is node's parts and form when it is a string literal with
// no tag: a plain one or an interpolated one.
func untaggedString(node ast.Node) (parts []ast.StringPart, raw, triple bool, line, col int, span ast.Span, ok bool) {
	switch n := node.(type) {
	case *ast.StringLit:
		return []ast.StringPart{ast.StringText{Value: n.Value}}, n.Raw, n.Triple, n.Line, n.Col, n.Span, true
	case *ast.StringInterp:
		return n.Parts, false, n.Triple, n.Line, n.Col, n.Span, true
	}
	return nil, false, false, 0, 0, ast.Span{}, false
}

// targetLiteralType is the named type expected names, with the `build` of
// its CheckedLiteral impl, when it has one.
func (c *checker) targetLiteralType(expected Type) (string, *ast.FuncDef) {
	if expected == nil || c.fa.Program() == nil {
		return "", nil
	}
	resolved := resolveTypeVar(expected)
	var name string
	switch t := resolved.(type) {
	case *DistinctType:
		name = t.Name
	case *StructType:
		name = t.Name
	case *EnumType:
		name = t.Name
	default:
		return "", nil
	}
	build := resolveConcreteImpl(c.fa.Program().ImplIndex, "CheckedLiteral", "build", resolved)
	if build == nil {
		return "", nil
	}
	return name, build
}

// checkTargetTypedLiteral checks node, an untagged string literal, as the
// checked literal of the type expected names. handled is false when node is
// not such a literal or the type has no CheckedLiteral impl, and the literal
// is then a String.
func (c *checker) checkTargetTypedLiteral(node ast.Node, expected Type) (Type, bool) {
	parts, raw, triple, line, col, span, ok := untaggedString(node)
	if !ok {
		return nil, false
	}
	name, build := c.targetLiteralType(expected)
	if build == nil {
		return nil, false
	}
	twin := &ast.TaggedString{Tag: name, Raw: raw, Triple: triple, Parts: parts, Line: line, Col: col + len(name)}
	twin.Span = span
	if prev := c.fa.TargetLiterals[node]; prev != nil && prev.Tag == name {
		// The literal checked again (an unannotated `once`'s value, which
		// the inference phase checks before its file's check does) is the
		// literal it was, so a reader keyed by node finds one twin.
		twin = prev
	}
	if c.fa.TargetLiterals == nil {
		c.fa.TargetLiterals = map[ast.Node]*ast.TaggedString{}
		c.fa.TargetLiteralBuilds = map[*ast.TaggedString]*ast.FuncDef{}
	}
	c.fa.TargetLiterals[node] = twin
	c.fa.TargetLiteralBuilds[twin] = build
	ty := c.checkTaggedString(twin)
	c.fa.recordExprType(twin, ty)
	c.registerTargetLiteralHover(node, line, col, span, ty)
	return ty, true
}

// registerTargetLiteralHover makes a hover on the literal name the type its
// position gave it, which the spelling does not, as registerByteHover does
// for a Byte literal.
func (c *checker) registerTargetLiteralHover(node ast.Node, line, col int, span ast.Span, ty Type) {
	pos := Pos{Line: line, Col: col}
	if _, taken := c.fa.References[pos]; taken || line <= 0 || ty == nil {
		return
	}
	text := `"…"`
	if lit, ok := node.(*ast.StringLit); ok && !lit.Triple && !lit.Raw && len(lit.Value) <= 60 {
		text = strconv.Quote(lit.Value)
	}
	width := len(text)
	if !span.IsZero() && span.EndLine == span.StartLine && span.EndCol > span.StartCol {
		width = span.EndCol - span.StartCol
	}
	c.fa.References[pos] = &Symbol{Name: text, Kind: SymbolLiteral, Pos: pos, Type: ty, Span: width}
}
