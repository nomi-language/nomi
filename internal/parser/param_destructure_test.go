package parser

import (
	"github.com/nomi-language/nomi/internal/ast"
	"testing"
)

// funcDefParam parses a single function definition and returns the i-th param.
func funcDefParam(t *testing.T, src string, i int) ast.Param {
	t.Helper()
	nodes := parse(t, src)
	var fd *ast.FuncDef
	for _, n := range nodes {
		if v, ok := n.(*ast.FuncDef); ok {
			fd = v
			break
		}
	}
	if fd == nil {
		t.Fatalf("parse(%q): no FuncDef", src)
	}
	if i >= len(fd.Params) {
		t.Fatalf("parse(%q): want param %d, only %d params", src, i, len(fd.Params))
	}
	return fd.Params[i]
}

// lambdaParam parses a single lambda expression and returns the i-th param.
func lambdaParam(t *testing.T, src string, i int) ast.Param {
	t.Helper()
	expr := parseExpr(t, src)
	lam, ok := expr.(*ast.Lambda)
	if !ok {
		t.Fatalf("parse(%q): expected *ast.Lambda, got %T", src, expr)
	}
	if i >= len(lam.Params) {
		t.Fatalf("parse(%q): want param %d, only %d params", src, i, len(lam.Params))
	}
	return lam.Params[i]
}

// --- function-definition param patterns ---

func TestFuncParam_Plain_NoDestructure(t *testing.T) {
	p := funcDefParam(t, "fn f(x) { x }", 0)
	if p.Name != "x" {
		t.Errorf("expected Name x, got %q", p.Name)
	}
	if p.Destructure != nil {
		t.Errorf("expected nil Destructure for plain param, got %T", p.Destructure)
	}
}

func TestFuncParam_Wildcard(t *testing.T) {
	p := funcDefParam(t, "fn f(_) { 0 }", 0)
	if p.Name != "_" {
		t.Errorf("expected Name _, got %q", p.Name)
	}
	if p.Destructure != nil {
		t.Errorf("expected nil Destructure for wildcard param, got %T", p.Destructure)
	}
}

func TestFuncParam_Tuple(t *testing.T) {
	p := funcDefParam(t, "fn f((a, b): (Int, Int)): Int { a + b }", 0)
	tp, ok := p.Destructure.(*ast.TuplePattern)
	if !ok {
		t.Fatalf("expected *ast.TuplePattern, got %T", p.Destructure)
	}
	if len(tp.Patterns) != 2 {
		t.Fatalf("expected 2 sub-patterns, got %d", len(tp.Patterns))
	}
	if p.TypeAnnotation == nil {
		t.Errorf("expected a type annotation on the tuple param")
	}
}

func TestFuncParam_TypedStruct(t *testing.T) {
	p := funcDefParam(t, "fn f(Point{x, y}): Int { x + y }", 0)
	sp, ok := p.Destructure.(*ast.StructPattern)
	if !ok {
		t.Fatalf("expected *ast.StructPattern, got %T", p.Destructure)
	}
	if sp.TypeName == nil || sp.TypeName.TypeString() != "Point" {
		t.Errorf("expected TypeName Point, got %v", sp.TypeName)
	}
	if len(sp.Fields) != 2 {
		t.Errorf("expected 2 fields, got %d", len(sp.Fields))
	}
}

func TestFuncParam_AnonStruct(t *testing.T) {
	p := funcDefParam(t, "fn f({x, y}: Point): Int { x + y }", 0)
	sp, ok := p.Destructure.(*ast.StructPattern)
	if !ok {
		t.Fatalf("expected *ast.StructPattern, got %T", p.Destructure)
	}
	if sp.TypeName != nil {
		t.Errorf("expected nil TypeName for anon struct, got %v", sp.TypeName)
	}
}

func TestFuncParam_Distinct(t *testing.T) {
	p := funcDefParam(t, "fn f(Dur(x)): Int { x }", 0)
	ep, ok := p.Destructure.(*ast.EnumPattern)
	if !ok {
		t.Fatalf("expected *ast.EnumPattern, got %T", p.Destructure)
	}
	if ep.Variant.TypeString() != "Dur" {
		t.Errorf("expected variant Dur, got %q", ep.Variant.TypeString())
	}
	if ep.Binding != "x" {
		t.Errorf("expected binding x, got %q", ep.Binding)
	}
}

func TestFuncParam_QualifiedEnumVariant(t *testing.T) {
	p := funcDefParam(t, "fn f(e.V(x)): Int { x }", 0)
	ep, ok := p.Destructure.(*ast.EnumPattern)
	if !ok {
		t.Fatalf("expected *ast.EnumPattern, got %T", p.Destructure)
	}
	qt, ok := ep.Variant.(*ast.QualifiedType)
	if !ok {
		t.Fatalf("expected *ast.QualifiedType variant, got %T", ep.Variant)
	}
	if qt.Module != "e" {
		t.Errorf("expected module e, got %q", qt.Module)
	}
}

func TestFuncParam_DotEnumVariant(t *testing.T) {
	p := funcDefParam(t, "fn f(.V(x): E): Int { x }", 0)
	ep, ok := p.Destructure.(*ast.EnumPattern)
	if !ok {
		t.Fatalf("expected *ast.EnumPattern, got %T", p.Destructure)
	}
	if _, ok := ep.Variant.(*ast.DotVariantType); !ok {
		t.Fatalf("expected *ast.DotVariantType variant, got %T", ep.Variant)
	}
	if p.TypeAnnotation == nil {
		t.Errorf("expected annotation on dot-enum param")
	}
}

func TestFuncParam_MixedWithPlain(t *testing.T) {
	// `Dur(ns), factor: Int` — destructure first param, plain second.
	nodes := parse(t, "fn f(Dur(ns), factor: Int): Int { ns + factor }")
	var fd *ast.FuncDef
	for _, n := range nodes {
		if v, ok := n.(*ast.FuncDef); ok {
			fd = v
		}
	}
	if fd == nil || len(fd.Params) != 2 {
		t.Fatalf("expected a FuncDef with 2 params")
	}
	if _, ok := fd.Params[0].Destructure.(*ast.EnumPattern); !ok {
		t.Errorf("param 0: expected EnumPattern, got %T", fd.Params[0].Destructure)
	}
	if fd.Params[1].Destructure != nil || fd.Params[1].Name != "factor" {
		t.Errorf("param 1: expected plain factor, got name=%q destructure=%T", fd.Params[1].Name, fd.Params[1].Destructure)
	}
}

func TestFuncParam_DistinctSelfTyped_NoAnnotation(t *testing.T) {
	p := funcDefParam(t, "fn f(Dur(x)): Int { x }", 0)
	if p.TypeAnnotation != nil {
		t.Errorf("expected no annotation on self-typed distinct param")
	}
}

func TestFuncParam_DistinctRedundantAnnotation(t *testing.T) {
	// Explicit redundant form stays legal at the parser level.
	p := funcDefParam(t, "fn f(Dur(x): Dur): Int { x }", 0)
	if _, ok := p.Destructure.(*ast.EnumPattern); !ok {
		t.Fatalf("expected EnumPattern, got %T", p.Destructure)
	}
	if p.TypeAnnotation == nil || p.TypeAnnotation.TypeString() != "Dur" {
		t.Errorf("expected annotation Dur, got %v", p.TypeAnnotation)
	}
}

// --- lambda param patterns (parser still accepts every shape) ---

func TestLambdaParam_Distinct(t *testing.T) {
	p := lambdaParam(t, "|Dur(x)| x", 0)
	ep, ok := p.Destructure.(*ast.EnumPattern)
	if !ok {
		t.Fatalf("expected *ast.EnumPattern, got %T", p.Destructure)
	}
	if ep.Binding != "x" {
		t.Errorf("expected binding x, got %q", ep.Binding)
	}
}

func TestLambdaParam_Tuple_StillWorks(t *testing.T) {
	p := lambdaParam(t, "|(a, b)| a", 0)
	if _, ok := p.Destructure.(*ast.TuplePattern); !ok {
		t.Fatalf("expected *ast.TuplePattern, got %T", p.Destructure)
	}
}

func TestLambdaParam_Struct_StillWorks(t *testing.T) {
	p := lambdaParam(t, "|{x, y}| x", 0)
	sp, ok := p.Destructure.(*ast.StructPattern)
	if !ok {
		t.Fatalf("expected *ast.StructPattern, got %T", p.Destructure)
	}
	if sp.TypeName != nil {
		t.Errorf("expected nil TypeName, got %v", sp.TypeName)
	}
}

func TestLambdaParam_TypedStruct(t *testing.T) {
	p := lambdaParam(t, "|Point{x, y}| x", 0)
	sp, ok := p.Destructure.(*ast.StructPattern)
	if !ok {
		t.Fatalf("expected *ast.StructPattern, got %T", p.Destructure)
	}
	if sp.TypeName == nil || sp.TypeName.TypeString() != "Point" {
		t.Errorf("expected TypeName Point, got %v", sp.TypeName)
	}
}

func TestLambdaParam_Map_StillParses(t *testing.T) {
	// The parser still accepts map patterns; the checker is the gatekeeper.
	p := lambdaParam(t, `|{"name" => n}| n`, 0)
	if _, ok := p.Destructure.(*ast.MapPattern); !ok {
		t.Fatalf("expected *ast.MapPattern, got %T", p.Destructure)
	}
}
