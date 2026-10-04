package parser

import (
	"github.com/nomi-language/nomi/internal/ast"
	"testing"
)

// Negative numeric literals must be accepted as KEYS in map patterns, not just
// as values. Regression coverage for the parser/grammar gap where `{-2 => v}`
// was not recognized as a map pattern (it fell into struct-pattern parsing and
// errored with "expected field name in struct pattern").
//
// Map-pattern keys are arbitrary expressions, so a negative literal `-2`
// parses as a unary-negation expression (`Unary{-, IntLit{2}}`) rather than a
// pre-folded literal node. It evaluates to -2.

func mapPatternBranch0(t *testing.T, src string) *ast.MapPattern {
	t.Helper()
	expr := parseExpr(t, src)
	c, ok := expr.(*ast.Case)
	if !ok {
		t.Fatalf("expected *ast.Case, got %T", expr)
	}
	mp, ok := c.Branches[0].Pattern.(*ast.MapPattern)
	if !ok {
		t.Fatalf("branch[0]: expected *ast.MapPattern, got %T", c.Branches[0].Pattern)
	}
	return mp
}

// assertNegIntKey checks that key is `-(intLit)` with the given magnitude.
func assertNegIntKey(t *testing.T, key ast.Node, mag int64) {
	t.Helper()
	u, ok := key.(*ast.Unary)
	if !ok || u.Op != "-" {
		t.Fatalf("key: expected Unary{-, ...}, got %T %v", key, key)
	}
	il, ok := u.Right.(*ast.IntLit)
	if !ok || il.Value != mag {
		t.Fatalf("key: expected Unary{-, IntLit{%d}}, got %T %v", mag, u.Right, u.Right)
	}
}

func TestCaseMapPatternNegativeIntKey(t *testing.T) {
	mp := mapPatternBranch0(t, "case m {\n {-2 => v} -> v\n _ -> \"no\"\n}")
	assertNegIntKey(t, mp.Entries[0].Key, 2)
}

func TestCaseMapPatternNegativeFloatKey(t *testing.T) {
	mp := mapPatternBranch0(t, "case m {\n {-2.5 => v} -> v\n _ -> \"no\"\n}")
	u, ok := mp.Entries[0].Key.(*ast.Unary)
	if !ok || u.Op != "-" {
		t.Fatalf("entry[0] key: expected Unary{-, ...}, got %T %v", mp.Entries[0].Key, mp.Entries[0].Key)
	}
	fl, ok := u.Right.(*ast.FloatLit)
	if !ok || fl.Value != 2.5 {
		t.Fatalf("entry[0] key: expected Unary{-, FloatLit{2.5}}, got %T %v", u.Right, u.Right)
	}
}

func TestCaseMapPatternNegativeDecimalKey(t *testing.T) {
	mp := mapPatternBranch0(t, "case m {\n {-2.5d => v} -> v\n _ -> \"no\"\n}")
	u, ok := mp.Entries[0].Key.(*ast.Unary)
	if !ok || u.Op != "-" {
		t.Fatalf("entry[0] key: expected Unary{-, ...}, got %T %v", mp.Entries[0].Key, mp.Entries[0].Key)
	}
	dl, ok := u.Right.(*ast.DecimalLit)
	if !ok || dl.Lexeme != "2.5d" {
		t.Fatalf("entry[0] key: expected Unary{-, DecimalLit{2.5d}}, got %T %v", u.Right, u.Right)
	}
}

// Mixed: a positive and a negative key in the same map pattern.
func TestCaseMapPatternMixedSignKeys(t *testing.T) {
	mp := mapPatternBranch0(t, "case m {\n {-1 => a, 2 => b} -> a\n _ -> \"no\"\n}")
	if len(mp.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(mp.Entries))
	}
	assertNegIntKey(t, mp.Entries[0].Key, 1)
	k1, ok := mp.Entries[1].Key.(*ast.IntLit)
	if !ok || k1.Value != 2 {
		t.Fatalf("entry[1] key: expected IntLit{2}, got %T %v", mp.Entries[1].Key, mp.Entries[1].Key)
	}
}

// Exercises the type-prefixed path — parseTypePrefixedBracePattern +
// parseMapPatternEntries — via a distinct map type prefix.
func TestCaseTypePrefixedMapPatternNegativeKey(t *testing.T) {
	mp := mapPatternBranch0(t, "case m {\n Kvs{-2 => v} -> v\n _ -> \"no\"\n}")
	assertNegIntKey(t, mp.Entries[0].Key, 2)
}

// Exercises the nested path — parseSinglePattern's LBRACE branch +
// parseMapPatternEntries (distinct from parseCaseBranch's inline map parser
// above) — via an enum payload.
func TestEnumPayloadMapPatternNegativeKey(t *testing.T) {
	expr := parseExpr(t, "case x {\n Some({-2 => v}) -> v\n _ -> \"no\"\n}")
	c := expr.(*ast.Case)
	ep, ok := c.Branches[0].Pattern.(*ast.EnumPattern)
	if !ok {
		t.Fatalf("branch[0]: expected EnumPattern, got %T", c.Branches[0].Pattern)
	}
	mp, ok := ep.Payload.(*ast.MapPattern)
	if !ok {
		t.Fatalf("payload: expected MapPattern, got %T", ep.Payload)
	}
	assertNegIntKey(t, mp.Entries[0].Key, 2)
}

// --- Expression keys (map-pattern keys are arbitrary expressions) ---

// Bare enum variant key (`True`) parses as an EnumPattern node in key
// position (the expression parser produces an EnumPattern for a bare
// TYPE_IDENT in a pattern-free expression context it yields an Ident/Type;
// confirm whatever node it produces is *not* the restricted-key error).
func TestCaseMapPatternVariantKey(t *testing.T) {
	mp := mapPatternBranch0(t, "case m {\n {True => v} -> v\n _ -> \"no\"\n}")
	if len(mp.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(mp.Entries))
	}
	// The key is whatever the expression parser yields for `True`.
	if mp.Entries[0].Key == nil {
		t.Fatalf("entry[0] key is nil")
	}
}

// Dotted-variant key (`Color.Red`).
func TestCaseMapPatternDottedVariantKey(t *testing.T) {
	mp := mapPatternBranch0(t, "case m {\n {Color.Red => v} -> v\n _ -> \"no\"\n}")
	if mp.Entries[0].Key == nil {
		t.Fatalf("entry[0] key is nil")
	}
}

// Empty braces must not be misclassified as a map pattern by the speculative
// key-expression disambiguation (no `=>`, so it stays a struct pattern).
func TestEmptyBraceNotMapPattern(t *testing.T) {
	expr := parseExpr(t, "case m {\n {} -> \"empty\"\n _ -> \"no\"\n}")
	c := expr.(*ast.Case)
	if _, ok := c.Branches[0].Pattern.(*ast.MapPattern); ok {
		t.Fatalf("empty {} must not parse as a MapPattern, got %T", c.Branches[0].Pattern)
	}
}

// Payload-variant key (`Some(2)`).
func TestCaseMapPatternPayloadVariantKey(t *testing.T) {
	mp := mapPatternBranch0(t, "case m {\n {Some(2) => v} -> v\n _ -> \"no\"\n}")
	if mp.Entries[0].Key == nil {
		t.Fatalf("entry[0] key is nil")
	}
	if _, ok := mp.Entries[0].Key.(*ast.Call); !ok {
		t.Fatalf("entry[0] key: expected CallExpr for Some(2), got %T", mp.Entries[0].Key)
	}
}

// Tuple key (`(1, 2)`).
func TestCaseMapPatternTupleKey(t *testing.T) {
	mp := mapPatternBranch0(t, "case m {\n {(1, 2) => v} -> v\n _ -> \"no\"\n}")
	if _, ok := mp.Entries[0].Key.(*ast.TupleLit); !ok {
		t.Fatalf("entry[0] key: expected TupleLit, got %T", mp.Entries[0].Key)
	}
}

// List key (`[1, 2]`).
func TestCaseMapPatternListKey(t *testing.T) {
	mp := mapPatternBranch0(t, "case m {\n {[1, 2] => v} -> v\n _ -> \"no\"\n}")
	if _, ok := mp.Entries[0].Key.(*ast.ListLit); !ok {
		t.Fatalf("entry[0] key: expected ListLit, got %T", mp.Entries[0].Key)
	}
}

// Named-struct key (`Point{x: 1}`).
func TestCaseMapPatternNamedStructKey(t *testing.T) {
	mp := mapPatternBranch0(t, "case m {\n {Point{x: 1} => v} -> v\n _ -> \"no\"\n}")
	if mp.Entries[0].Key == nil {
		t.Fatalf("entry[0] key is nil")
	}
	if _, ok := mp.Entries[0].Key.(*ast.StructLit); !ok {
		t.Fatalf("entry[0] key: expected StructLit for Point{x: 1}, got %T", mp.Entries[0].Key)
	}
}

// Distinct constructor key (`Id(5)`) parses as a call expression.
func TestCaseMapPatternDistinctKey(t *testing.T) {
	mp := mapPatternBranch0(t, "case m {\n {Id(5) => v} -> v\n _ -> \"no\"\n}")
	if _, ok := mp.Entries[0].Key.(*ast.Call); !ok {
		t.Fatalf("entry[0] key: expected CallExpr for Id(5), got %T", mp.Entries[0].Key)
	}
}

// Computed key — a bare identifier (`x`).
func TestCaseMapPatternComputedIdentKey(t *testing.T) {
	mp := mapPatternBranch0(t, "case m {\n {x => v} -> v\n _ -> \"no\"\n}")
	id, ok := mp.Entries[0].Key.(*ast.Ident)
	if !ok || id.Name != "x" {
		t.Fatalf("entry[0] key: expected Identifier{x}, got %T %v", mp.Entries[0].Key, mp.Entries[0].Key)
	}
}

// Computed key — a function call (`f(a)`).
func TestCaseMapPatternComputedCallKey(t *testing.T) {
	mp := mapPatternBranch0(t, "case m {\n {f(a) => v} -> v\n _ -> \"no\"\n}")
	if _, ok := mp.Entries[0].Key.(*ast.Call); !ok {
		t.Fatalf("entry[0] key: expected CallExpr for f(a), got %T", mp.Entries[0].Key)
	}
}

// Computed key — an arithmetic expression (`base + 1`).
func TestCaseMapPatternComputedArithKey(t *testing.T) {
	mp := mapPatternBranch0(t, "case m {\n {base + 1 => v} -> v\n _ -> \"no\"\n}")
	if _, ok := mp.Entries[0].Key.(*ast.Binary); !ok {
		t.Fatalf("entry[0] key: expected BinaryExpr for base + 1, got %T", mp.Entries[0].Key)
	}
}

// Bool key as a computed key (`True`) in the type-prefixed map path.
func TestTypePrefixedMapPatternVariantKey(t *testing.T) {
	mp := mapPatternBranch0(t, "case m {\n Kvs{Some(2) => v} -> v\n _ -> \"no\"\n}")
	if _, ok := mp.Entries[0].Key.(*ast.Call); !ok {
		t.Fatalf("entry[0] key: expected CallExpr for Some(2), got %T", mp.Entries[0].Key)
	}
}

// Nested path (enum payload): a tuple key inside `Some({...})`.
func TestEnumPayloadMapPatternTupleKey(t *testing.T) {
	expr := parseExpr(t, "case x {\n Some({(1, 2) => v}) -> v\n _ -> \"no\"\n}")
	c := expr.(*ast.Case)
	ep := c.Branches[0].Pattern.(*ast.EnumPattern)
	mp, ok := ep.Payload.(*ast.MapPattern)
	if !ok {
		t.Fatalf("payload: expected MapPattern, got %T", ep.Payload)
	}
	if _, ok := mp.Entries[0].Key.(*ast.TupleLit); !ok {
		t.Fatalf("entry[0] key: expected TupleLit, got %T", mp.Entries[0].Key)
	}
}

// Disambiguation: `{x}` (single field pun) stays a struct pattern.
func TestCaseBraceStructPunStaysStruct(t *testing.T) {
	expr := parseExpr(t, "case m {\n {x} -> x\n _ -> \"no\"\n}")
	c := expr.(*ast.Case)
	sp, ok := c.Branches[0].Pattern.(*ast.StructPattern)
	if !ok {
		t.Fatalf("branch[0]: expected StructPattern, got %T", c.Branches[0].Pattern)
	}
	if len(sp.Fields) != 1 || sp.Fields[0].Name != "x" {
		t.Fatalf("expected single field 'x', got %+v", sp.Fields)
	}
}

// Disambiguation: `{x: 1}` (field with literal value pattern) stays a struct
// pattern (the `:` separator, not `=>`).
func TestCaseBraceStructFieldStaysStruct(t *testing.T) {
	expr := parseExpr(t, "case m {\n {x: 1} -> x\n _ -> \"no\"\n}")
	c := expr.(*ast.Case)
	sp, ok := c.Branches[0].Pattern.(*ast.StructPattern)
	if !ok {
		t.Fatalf("branch[0]: expected StructPattern, got %T", c.Branches[0].Pattern)
	}
	if len(sp.Fields) != 1 || sp.Fields[0].Name != "x" {
		t.Fatalf("expected single field 'x', got %+v", sp.Fields)
	}
}

// Disambiguation: multi-field struct pattern `{x, y}` stays a struct pattern.
func TestCaseBraceMultiFieldStaysStruct(t *testing.T) {
	expr := parseExpr(t, "case m {\n {x, y} -> x\n _ -> \"no\"\n}")
	c := expr.(*ast.Case)
	sp, ok := c.Branches[0].Pattern.(*ast.StructPattern)
	if !ok {
		t.Fatalf("branch[0]: expected StructPattern, got %T", c.Branches[0].Pattern)
	}
	if len(sp.Fields) != 2 {
		t.Fatalf("expected 2 fields, got %d", len(sp.Fields))
	}
}

// Disambiguation in the destructure-binding form: `{x} = expr` stays a struct
// destructure, while `{k => v} = expr` is a map destructure.
func TestMapDestructureExpressionKey(t *testing.T) {
	nodes := parse(t, "{(1, 2) => v} = m")
	md, ok := nodes[0].(*ast.MapDestructure)
	if !ok {
		t.Fatalf("expected MapDestructure, got %T", nodes[0])
	}
	if _, ok := md.Entries[0].Key.(*ast.TupleLit); !ok {
		t.Fatalf("entry[0] key: expected TupleLit, got %T", md.Entries[0].Key)
	}
}

func TestStructDestructureStaysStruct(t *testing.T) {
	nodes := parse(t, "{x, y} = m")
	if _, ok := nodes[0].(*ast.StructDestructure); !ok {
		t.Fatalf("expected StructDestructure, got %T", nodes[0])
	}
}

// --- Lambda map-destructure param (the 5th map-pattern key site) ---
//
// A lambda's brace param disambiguates struct-vs-map the same way every other
// map-pattern site does: a `<keyExpr> => ...` first entry is a map pattern,
// anything else is a struct pattern. Before the 5th-site conversion, the param
// path used a curated 4-scalar-literal key switch, so `|{1 => v}| v` parsed but
// an expression key like `|{Color.Red => v}| v` failed with "unexpected token
// FAT_ARROW". (Note: lambda map-destructure params hit a separate, pre-existing
// checker limitation downstream — `cannot infer type for parameter __destr_N` —
// so this construct doesn't run end-to-end regardless of key type. These tests
// assert PARSER uniformity only.)

func lambdaParam0(t *testing.T, src string) ast.Param {
	t.Helper()
	expr := parseExpr(t, src)
	lam, ok := expr.(*ast.Lambda)
	if !ok {
		t.Fatalf("expected *ast.Lambda, got %T", expr)
	}
	if len(lam.Params) != 1 {
		t.Fatalf("expected 1 lambda param, got %d", len(lam.Params))
	}
	return lam.Params[0]
}

// Expression key (dotted variant) in a lambda map-destructure param parses.
func TestLambdaMapParamDottedVariantKey(t *testing.T) {
	p := lambdaParam0(t, "|{Color.Red => v}| v")
	mp, ok := p.Destructure.(*ast.MapPattern)
	if !ok {
		t.Fatalf("param destructure: expected *ast.MapPattern, got %T", p.Destructure)
	}
	if len(mp.Entries) != 1 {
		t.Fatalf("expected 1 map entry, got %d", len(mp.Entries))
	}
	if mp.Entries[0].Key == nil {
		t.Fatalf("entry[0] key is nil")
	}
	// `Color.Red` parses as field access on the `Color` ident (an enum-variant
	// access shape) — confirm it's the variant expression, not the old error.
	if _, ok := mp.Entries[0].Key.(*ast.FieldAccess); !ok {
		t.Fatalf("entry[0] key: expected FieldAccess for Color.Red, got %T", mp.Entries[0].Key)
	}
}

// Scalar literal key still parses as a map param (regression for the converted
// path — the original curated switch handled this case).
func TestLambdaMapParamScalarKey(t *testing.T) {
	p := lambdaParam0(t, "|{1 => v}| v")
	mp, ok := p.Destructure.(*ast.MapPattern)
	if !ok {
		t.Fatalf("param destructure: expected *ast.MapPattern, got %T", p.Destructure)
	}
	k, ok := mp.Entries[0].Key.(*ast.IntLit)
	if !ok || k.Value != 1 {
		t.Fatalf("entry[0] key: expected IntLit{1}, got %T %v", mp.Entries[0].Key, mp.Entries[0].Key)
	}
}

// Disambiguation: a multi-field struct param `|{x, y}|` stays a struct pattern.
func TestLambdaStructParamStaysStruct(t *testing.T) {
	p := lambdaParam0(t, "|{x, y}| x")
	sp, ok := p.Destructure.(*ast.StructPattern)
	if !ok {
		t.Fatalf("param destructure: expected *ast.StructPattern, got %T", p.Destructure)
	}
	if len(sp.Fields) != 2 || sp.Fields[0].Name != "x" || sp.Fields[1].Name != "y" {
		t.Fatalf("expected struct fields x, y, got %+v", sp.Fields)
	}
}

// Disambiguation: a renamed struct field param `|{x: a}|` stays a struct pattern.
func TestLambdaStructParamRenameStaysStruct(t *testing.T) {
	p := lambdaParam0(t, "|{x: a}| a")
	sp, ok := p.Destructure.(*ast.StructPattern)
	if !ok {
		t.Fatalf("param destructure: expected *ast.StructPattern, got %T", p.Destructure)
	}
	if len(sp.Fields) != 1 || sp.Fields[0].Name != "x" || sp.Fields[0].Binding != "a" {
		t.Fatalf("expected struct field x bound to a, got %+v", sp.Fields)
	}
}
