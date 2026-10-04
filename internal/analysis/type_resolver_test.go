package analysis

import (
	"github.com/nomi-language/nomi/internal/ast"
	"strings"
	"testing"
)

func TestResolveTypeExpr_SimpleInt(t *testing.T) {
	reg := NewTypeRegistry()
	te := &ast.SimpleType{Name: "Int", Line: 1, Col: 1}
	got, err := ResolveTypeExpr(te, reg, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != TypeInt {
		t.Fatalf("expected TypeInt, got %v", got)
	}
}

func TestResolveTypeExpr_SimpleUnknown(t *testing.T) {
	reg := NewTypeRegistry()
	te := &ast.SimpleType{Name: "Foo", Line: 3, Col: 5}
	_, err := ResolveTypeExpr(te, reg, nil, nil)
	if err == nil {
		t.Fatal("expected error for unknown type")
	}
	tyErr, ok := err.(TypeError)
	if !ok {
		t.Fatalf("expected TypeError, got %T", err)
	}
	if tyErr.Line != 3 || tyErr.Col != 5 {
		t.Fatalf("wrong position: line %d col %d", tyErr.Line, tyErr.Col)
	}
}

func TestResolveTypeExpr_SimpleUserStruct(t *testing.T) {
	reg := NewTypeRegistry()
	st := &StructType{Name: "Point", Fields: []FieldDef{
		{Name: "x", Type: TypeInt},
		{Name: "y", Type: TypeInt},
	}}
	reg.Register("Point", st)

	te := &ast.SimpleType{Name: "Point", Line: 1, Col: 1}
	got, err := ResolveTypeExpr(te, reg, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != st {
		t.Fatalf("expected registered StructType, got %v", got)
	}
}

func TestResolveTypeExpr_FuncType(t *testing.T) {
	reg := NewTypeRegistry()
	// (Int, String) -> Bool
	te := &ast.FuncType{
		Params: []ast.TypeExpr{
			&ast.SimpleType{Name: "Int", Line: 1, Col: 1},
			&ast.SimpleType{Name: "String", Line: 1, Col: 6},
		},
		Return: &ast.SimpleType{Name: "Bool", Line: 1, Col: 18},
		Line:   1,
		Col:    1,
	}
	got, err := ResolveTypeExpr(te, reg, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ft, ok := got.(*FuncType)
	if !ok {
		t.Fatalf("expected *FuncType, got %T", got)
	}
	if len(ft.Params) != 2 {
		t.Fatalf("expected 2 params, got %d", len(ft.Params))
	}
	if ft.Params[0] != TypeInt {
		t.Fatalf("expected first param TypeInt, got %v", ft.Params[0])
	}
	if ft.Params[1] != TypeString {
		t.Fatalf("expected second param TypeString, got %v", ft.Params[1])
	}
	if ft.Return != TypeBool {
		t.Fatalf("expected return TypeBool, got %v", ft.Return)
	}
}

func TestResolveTypeExpr_ListInt(t *testing.T) {
	reg := NewTypeRegistry()
	te := &ast.GenericType{
		Name:   "List",
		Params: []ast.TypeExpr{&ast.SimpleType{Name: "Int", Line: 1, Col: 6}},
		Line:   1,
		Col:    1,
	}
	got, err := ResolveTypeExpr(te, reg, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	lt, ok := got.(*ListType)
	if !ok {
		t.Fatalf("expected *ListType, got %T", got)
	}
	if lt.Elem != TypeInt {
		t.Fatalf("expected Elem TypeInt, got %v", lt.Elem)
	}
}

func TestResolveTypeExpr_MapStringInt(t *testing.T) {
	reg := NewTypeRegistry()
	te := &ast.GenericType{
		Name: "Map",
		Params: []ast.TypeExpr{
			&ast.SimpleType{Name: "String", Line: 1, Col: 5},
			&ast.SimpleType{Name: "Int", Line: 1, Col: 13},
		},
		Line: 1,
		Col:  1,
	}
	got, err := ResolveTypeExpr(te, reg, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	mt, ok := got.(*MapType)
	if !ok {
		t.Fatalf("expected *MapType, got %T", got)
	}
	if mt.Key != TypeString {
		t.Fatalf("expected Key TypeString, got %v", mt.Key)
	}
	if mt.Val != TypeInt {
		t.Fatalf("expected Val TypeInt, got %v", mt.Val)
	}
}

// registerStdlibEnumsForTest pre-populates a registry with Maybe and Result
// as they would appear after stdlib is loaded, plus Wrapper (a representative
// generic multi-variant enum) — generic enums keyed by TypeParam_ pointers so
// use-site instantiations can substitute concrete TypeArgs into the variants.
// Used by resolver / type-builder unit tests that don't go through std.Load()
// but still need these names to resolve.
func registerStdlibEnumsForTest(reg *TypeRegistry) {
	tT := &TypeParam_{Name_: "T"}
	maybe := &EnumType{
		Name:          "Maybe",
		TypeParams:    []string{"T"},
		TypeParamDefs: []*TypeParam_{tT},
		Variants: []VariantDef{
			{Name: "Some", DataType: tT},
			{Name: "None"},
		},
	}
	reg.Register("Maybe", maybe)

	rT := &TypeParam_{Name_: "T"}
	rE := &TypeParam_{Name_: "E"}
	result := &EnumType{
		Name:          "Result",
		TypeParams:    []string{"T", "E"},
		TypeParamDefs: []*TypeParam_{rT, rE},
		Variants: []VariantDef{
			{Name: "Ok", DataType: rT},
			{Name: "Err", DataType: rE},
		},
	}
	reg.Register("Result", result)

	wR := &TypeParam_{Name_: "R"}
	wrapper := &EnumType{
		Name:          "Wrapper",
		TypeParams:    []string{"R"},
		TypeParamDefs: []*TypeParam_{wR},
		Variants: []VariantDef{
			{Name: "Empty"},
			{Name: "Full", DataType: wR},
			{Name: "Partial"},
			{Name: "PartialWith", DataType: wR},
			{Name: "Sealed"},
		},
	}
	reg.Register("Wrapper", wrapper)
}

func TestResolveTypeExpr_MaybeInt(t *testing.T) {
	reg := NewTypeRegistry()
	registerStdlibEnumsForTest(reg)
	te := &ast.GenericType{
		Name:   "Maybe",
		Params: []ast.TypeExpr{&ast.SimpleType{Name: "Int", Line: 1, Col: 7}},
		Line:   1,
		Col:    1,
	}
	got, err := ResolveTypeExpr(te, reg, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	et, ok := got.(*EnumType)
	if !ok {
		t.Fatalf("expected *EnumType, got %T", got)
	}
	if et.Name != "Maybe" {
		t.Fatalf("expected name Maybe, got %s", et.Name)
	}
	if len(et.Variants) != 2 {
		t.Fatalf("expected 2 variants, got %d", len(et.Variants))
	}
	if et.Variants[0].Name != "Some" {
		t.Fatalf("expected Some, got %s", et.Variants[0].Name)
	}
	if et.Variants[1].Name != "None" || et.Variants[1].DataType != nil {
		t.Fatalf("expected bare None, got %s(%v)", et.Variants[1].Name, et.Variants[1].DataType)
	}
	// The resolved type carries TypeArgs but variants reference the registered
	// TypeParam_s — substitution happens at consumption sites.
	if len(et.TypeArgs) != 1 || et.TypeArgs[0] != TypeInt {
		t.Fatalf("expected TypeArgs [Int], got %v", et.TypeArgs)
	}
}

func TestResolveTypeExpr_FuncTypeNilReturn_TupleType(t *testing.T) {
	reg := NewTypeRegistry()
	// (Int, String) with no arrow → TupleType
	te := &ast.FuncType{
		Params: []ast.TypeExpr{
			&ast.SimpleType{Name: "Int", Line: 1, Col: 2},
			&ast.SimpleType{Name: "String", Line: 1, Col: 7},
		},
		Return: nil,
		Line:   1,
		Col:    1,
	}
	got, err := ResolveTypeExpr(te, reg, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tt, ok := got.(*TupleType)
	if !ok {
		t.Fatalf("expected *TupleType, got %T", got)
	}
	if len(tt.Elems) != 2 {
		t.Fatalf("expected 2 elems, got %d", len(tt.Elems))
	}
	if tt.Elems[0] != TypeInt {
		t.Fatalf("expected first elem TypeInt, got %v", tt.Elems[0])
	}
	if tt.Elems[1] != TypeString {
		t.Fatalf("expected second elem TypeString, got %v", tt.Elems[1])
	}
}

func TestResolveTypeExpr_TypeParam(t *testing.T) {
	reg := NewTypeRegistry()
	tp := &TypeParam_{Name_: "T"}
	typeParams := map[string]*TypeParam_{"T": tp}

	te := &ast.SimpleType{Name: "T", Line: 1, Col: 1}
	got, err := ResolveTypeExpr(te, reg, typeParams, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != tp {
		t.Fatalf("expected TypeParam_ T, got %v", got)
	}
}

func TestResolveTypeExpr_ListWrongArity(t *testing.T) {
	reg := NewTypeRegistry()
	te := &ast.GenericType{
		Name: "List",
		Params: []ast.TypeExpr{
			&ast.SimpleType{Name: "Int", Line: 1, Col: 6},
			&ast.SimpleType{Name: "String", Line: 1, Col: 11},
		},
		Line: 1,
		Col:  1,
	}
	_, err := ResolveTypeExpr(te, reg, nil, nil)
	if err == nil {
		t.Fatal("expected error for wrong arity")
	}
	tyErr, ok := err.(TypeError)
	if !ok {
		t.Fatalf("expected TypeError, got %T", err)
	}
	if tyErr.Line != 1 || tyErr.Col != 1 {
		t.Fatalf("wrong position: line %d col %d", tyErr.Line, tyErr.Col)
	}
}

func TestResolveTypeExpr_SelfInScope(t *testing.T) {
	reg := NewTypeRegistry()
	selfTp := &TypeParam_{Name_: "self"}
	typeParams := map[string]*TypeParam_{"self": selfTp}

	te := &ast.SelfType{Line: 5, Col: 3}
	got, err := ResolveTypeExpr(te, reg, typeParams, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != selfTp {
		t.Fatalf("expected self TypeParam_, got %v", got)
	}
}

func TestResolveTypeExpr_SelfOutOfScope(t *testing.T) {
	reg := NewTypeRegistry()
	te := &ast.SelfType{Line: 5, Col: 3}
	_, err := ResolveTypeExpr(te, reg, nil, nil)
	if err == nil {
		t.Fatal("expected error for self outside scope")
	}
}

func TestResolveTypeExpr_ResultType(t *testing.T) {
	reg := NewTypeRegistry()
	registerStdlibEnumsForTest(reg)
	te := &ast.GenericType{
		Name: "Result",
		Params: []ast.TypeExpr{
			&ast.SimpleType{Name: "Int", Line: 1, Col: 8},
			&ast.SimpleType{Name: "String", Line: 1, Col: 13},
		},
		Line: 1,
		Col:  1,
	}
	got, err := ResolveTypeExpr(te, reg, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	et, ok := got.(*EnumType)
	if !ok {
		t.Fatalf("expected *EnumType, got %T", got)
	}
	if et.Name != "Result" {
		t.Fatalf("expected name Result, got %s", et.Name)
	}
	if len(et.Variants) != 2 {
		t.Fatalf("expected 2 variants, got %d", len(et.Variants))
	}
	if et.Variants[0].Name != "Ok" {
		t.Fatalf("expected Ok variant, got %s", et.Variants[0].Name)
	}
	if et.Variants[1].Name != "Err" {
		t.Fatalf("expected Err variant, got %s", et.Variants[1].Name)
	}
	if len(et.TypeArgs) != 2 || et.TypeArgs[0] != TypeInt || et.TypeArgs[1] != TypeString {
		t.Fatalf("expected TypeArgs [Int, String], got %v", et.TypeArgs)
	}
}

func TestResolveTypeExpr_MaybeIntTypeArgs(t *testing.T) {
	reg := NewTypeRegistry()
	registerStdlibEnumsForTest(reg)
	te := &ast.GenericType{
		Name:   "Maybe",
		Params: []ast.TypeExpr{&ast.SimpleType{Name: "Int", Line: 1, Col: 1}},
		Line:   1, Col: 1,
	}
	resolved, err := ResolveTypeExpr(te, reg, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	et, ok := resolved.(*EnumType)
	if !ok {
		t.Fatalf("expected EnumType, got %T", resolved)
	}
	if et.Name != "Maybe" {
		t.Fatalf("expected base name Maybe, got %s", et.Name)
	}
	if len(et.TypeArgs) != 1 || et.TypeArgs[0] != TypeInt {
		t.Fatalf("expected TypeArgs [Int], got %v", et.TypeArgs)
	}
	if et.String() != "Maybe<Int>" {
		t.Fatalf("expected Maybe<Int>, got %s", et.String())
	}
}

func TestResolveTypeExpr_ResultIntStringTypeArgs(t *testing.T) {
	reg := NewTypeRegistry()
	registerStdlibEnumsForTest(reg)
	te := &ast.GenericType{
		Name: "Result",
		Params: []ast.TypeExpr{
			&ast.SimpleType{Name: "Int", Line: 1, Col: 1},
			&ast.SimpleType{Name: "String", Line: 1, Col: 5},
		},
		Line: 1, Col: 1,
	}
	resolved, err := ResolveTypeExpr(te, reg, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	et, ok := resolved.(*EnumType)
	if !ok {
		t.Fatalf("expected EnumType, got %T", resolved)
	}
	if et.Name != "Result" {
		t.Fatalf("expected base name Result, got %s", et.Name)
	}
	if len(et.TypeArgs) != 2 {
		t.Fatalf("expected 2 TypeArgs, got %d", len(et.TypeArgs))
	}
	if et.String() != "Result<Int, String>" {
		t.Fatalf("expected Result<Int, String>, got %s", et.String())
	}
}

// A dotted type name carrying type arguments — `Holder.Box<Int>` — parses as
// a QualifiedType whose Member is a GenericType, so TypeString() produces
// "Holder.Box<Int>" while the registry holds it under "Holder.Box". Looking
// up the key with its arguments baked in never matched, and the reference
// failed as an unknown type even though the declaration had been accepted.
//
// The non-generic spelling always worked, which is what made this look like
// a rule about dotted names rather than a gap in one branch.
func TestQualifiedGenericTypeResolves(t *testing.T) {
	_, errs := checkSource(`
pub type Holder

pub enum Holder.Flag {
  On
  Off
}

pub enum Holder.Box<T> {
  Full T
  Empty
}

fn flag(f: Holder.Flag): Int {
  case f {
    .On -> 1
    .Off -> 0
  }
}

fn boxed(x: Holder.Box<Int>): Int {
  case x {
    .Full(n) -> n
    .Empty -> 0
  }
}
`)
	for _, err := range errs {
		if strings.Contains(err.Message, "unknown type") {
			t.Fatalf("qualified generic type failed to resolve: %v", errs)
		}
	}
}
