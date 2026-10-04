package parser

import (
	"github.com/nomi-language/nomi/internal/ast"
	"testing"
)

// Composite/expression keys in map *literals* — the construction-side mirror of
// the expression map-*pattern* key support. The Go parser used to detect a map
// literal with a single-token heuristic (string/int/float/decimal/ident key
// followed by `=>`), which couldn't see tuple/variant/list/struct/computed
// keys, so `{(1, 2) => "v"}` was misparsed as a block and choked on `=>`.
// Detection now speculatively parses the key expression and peeks for `=>`,
// so any keyable expression works as a literal key — exactly like the value
// layer (which already keys on composites) and the tree-sitter grammar (which
// already parses them).

func TestMapLiteral_TupleKey(t *testing.T) {
	expr := parseExpr(t, `{(1, 2) => "v"}`)
	ml, ok := expr.(*ast.MapLit)
	if !ok {
		t.Fatalf("expected *ast.MapLit, got %T", expr)
	}
	if len(ml.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(ml.Entries))
	}
	if _, ok := ml.Entries[0].Key.(*ast.TupleLit); !ok {
		t.Fatalf("expected TupleLit key, got %T", ml.Entries[0].Key)
	}
}

func TestMapLiteral_VariantKey_FieldAccess(t *testing.T) {
	expr := parseExpr(t, `{Color.Red => "r"}`)
	ml, ok := expr.(*ast.MapLit)
	if !ok {
		t.Fatalf("expected *ast.MapLit, got %T", expr)
	}
	if len(ml.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(ml.Entries))
	}
	if _, ok := ml.Entries[0].Key.(*ast.FieldAccess); !ok {
		t.Fatalf("expected FieldAccess key, got %T", ml.Entries[0].Key)
	}
}

func TestMapLiteral_BoolKey(t *testing.T) {
	expr := parseExpr(t, `{True => "yes", False => "no"}`)
	ml, ok := expr.(*ast.MapLit)
	if !ok {
		t.Fatalf("expected *ast.MapLit, got %T", expr)
	}
	if len(ml.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(ml.Entries))
	}
}

func TestMapLiteral_ListKey(t *testing.T) {
	expr := parseExpr(t, `{[1, 2] => "v"}`)
	ml, ok := expr.(*ast.MapLit)
	if !ok {
		t.Fatalf("expected *ast.MapLit, got %T", expr)
	}
	if len(ml.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(ml.Entries))
	}
	if _, ok := ml.Entries[0].Key.(*ast.ListLit); !ok {
		t.Fatalf("expected ListLit key, got %T", ml.Entries[0].Key)
	}
}

func TestMapLiteral_StructKey(t *testing.T) {
	expr := parseExpr(t, `{Point{x: 1, y: 2} => "v"}`)
	ml, ok := expr.(*ast.MapLit)
	if !ok {
		t.Fatalf("expected *ast.MapLit, got %T", expr)
	}
	if len(ml.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(ml.Entries))
	}
	if _, ok := ml.Entries[0].Key.(*ast.StructLit); !ok {
		t.Fatalf("expected StructLit key, got %T", ml.Entries[0].Key)
	}
}

func TestMapLiteral_ComputedKey(t *testing.T) {
	expr := parseExpr(t, `{f(a) => "v"}`)
	ml, ok := expr.(*ast.MapLit)
	if !ok {
		t.Fatalf("expected *ast.MapLit, got %T", expr)
	}
	if len(ml.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(ml.Entries))
	}
	if _, ok := ml.Entries[0].Key.(*ast.Call); !ok {
		t.Fatalf("expected Call key, got %T", ml.Entries[0].Key)
	}
}

func TestMapLiteral_TypePrefixed_TupleKey(t *testing.T) {
	expr := parseExpr(t, `Kvs{(1, 2) => "v"}`)
	ml, ok := expr.(*ast.MapLit)
	if !ok {
		t.Fatalf("expected *ast.MapLit, got %T", expr)
	}
	if ml.TypeName == nil || ml.TypeName.TypeString() != "Kvs" {
		t.Fatalf("expected TypeName 'Kvs', got %v", ml.TypeName)
	}
	if len(ml.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(ml.Entries))
	}
	if _, ok := ml.Entries[0].Key.(*ast.TupleLit); !ok {
		t.Fatalf("expected TupleLit key, got %T", ml.Entries[0].Key)
	}
}

// case <composite-keyed map literal> { branches } — the case-scrutinee
// detection site. The map literal is the case *value*, not ad-hoc conditionals.
func TestMapLiteral_CaseScrutinee_TupleKey(t *testing.T) {
	expr := parseExpr(t, `case {(1, 2) => "v"} { _ -> 1 }`)
	c, ok := expr.(*ast.Case)
	if !ok {
		t.Fatalf("expected *ast.Case, got %T", expr)
	}
	if c.Value == nil {
		t.Fatalf("expected non-nil case Value (the map literal), got nil (ad-hoc)")
	}
	ml, ok := c.Value.(*ast.MapLit)
	if !ok {
		t.Fatalf("expected case Value *ast.MapLit, got %T", c.Value)
	}
	if len(ml.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(ml.Entries))
	}
	if _, ok := ml.Entries[0].Key.(*ast.TupleLit); !ok {
		t.Fatalf("expected TupleLit key, got %T", ml.Entries[0].Key)
	}
}

// --- Disambiguation regressions: these {…} forms must NOT become map literals ---

func TestMapLiteral_Disambig_Block_Call(t *testing.T) {
	expr := parseExpr(t, `{ io.print(1) }`)
	if _, ok := expr.(*ast.Block); !ok {
		t.Fatalf("expected *ast.Block, got %T", expr)
	}
}

func TestMapLiteral_Disambig_Block_Binding(t *testing.T) {
	expr := parseExpr(t, "{ x = 5\n x }")
	if _, ok := expr.(*ast.Block); !ok {
		t.Fatalf("expected *ast.Block, got %T", expr)
	}
}

func TestMapLiteral_Disambig_AnonStruct(t *testing.T) {
	expr := parseExpr(t, `{x: 1}`)
	sl, ok := expr.(*ast.StructLit)
	if !ok {
		t.Fatalf("expected *ast.StructLit (anon), got %T", expr)
	}
	if sl.TypeName != nil {
		t.Fatalf("expected nil TypeName for anonymous struct literal, got %v", sl.TypeName)
	}
}

// `{ x }` is a block (single bare-ident expression), not a map / struct —
// the ident-led ambiguity called out in the design's open items.
func TestMapLiteral_Disambig_Block_BareIdent(t *testing.T) {
	expr := parseExpr(t, `{ x }`)
	if _, ok := expr.(*ast.Block); !ok {
		t.Fatalf("expected *ast.Block, got %T", expr)
	}
}

// `{ x, y }` is anonymous-struct field punning, not a map.
func TestMapLiteral_Disambig_AnonStruct_Punning(t *testing.T) {
	expr := parseExpr(t, `{ x, y }`)
	sl, ok := expr.(*ast.StructLit)
	if !ok {
		t.Fatalf("expected *ast.StructLit (anon punning), got %T", expr)
	}
	if sl.TypeName != nil {
		t.Fatalf("expected nil TypeName, got %v", sl.TypeName)
	}
	if len(sl.Fields) != 2 {
		t.Fatalf("expected 2 punned fields, got %d", len(sl.Fields))
	}
}

func TestMapLiteral_Disambig_Empty(t *testing.T) {
	expr := parseExpr(t, `{}`)
	if _, ok := expr.(*ast.Block); !ok {
		t.Fatalf("expected *ast.Block for empty braces, got %T", expr)
	}
}

func TestMapLiteral_Disambig_CaseAdHoc(t *testing.T) {
	expr := parseExpr(t, "case { x > 0 -> \"pos\"\n _ -> \"neg\" }")
	c, ok := expr.(*ast.Case)
	if !ok {
		t.Fatalf("expected *ast.Case, got %T", expr)
	}
	if c.Value != nil {
		t.Fatalf("expected nil Value for ad-hoc case, got %T", c.Value)
	}
}
