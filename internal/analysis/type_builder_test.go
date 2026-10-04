package analysis

import (
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"testing"
)

// Parse source, build analysis, then build types.
func buildTypesFromSource(src string) (*FileAnalysis, []TypeError) {
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	// Lower type-body items (fn/impl items, interface inherent ops) into
	// top-level impl blocks so the BuildTypes pass sees them. BuildFile
	// lowers internally too, but on its own copy of the slice — the
	// BuildTypes call below needs the lowered nodes as well. Idempotent.
	nodes, lowerErrs := LowerDerives(nodes)
	fa := BuildFile(nodes)
	errs := BuildTypes(fa, nodes)
	errs = append(lowerErrs, errs...)
	return fa, errs
}

func TestTypeBuildFuncWithParams(t *testing.T) {
	fa, errs := buildTypesFromSource(`fn add(x: Int, _y: Int): Int { x }`)
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}

	sym := fa.ModuleScope.Lookup("add")
	if sym == nil {
		t.Fatal("expected symbol 'add'")
	}
	ft, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected FuncType, got %T", sym.Type)
	}
	if len(ft.Params) != 2 {
		t.Fatalf("expected 2 params, got %d", len(ft.Params))
	}
	if ft.Params[0] != TypeInt {
		t.Errorf("param 0: expected Int, got %v", ft.Params[0])
	}
	if ft.Params[1] != TypeInt {
		t.Errorf("param 1: expected Int, got %v", ft.Params[1])
	}
	if ft.Return != TypeInt {
		t.Errorf("return: expected Int, got %v", ft.Return)
	}
}

func TestTypeBuildFuncWithoutReturnAnnotationReturnsUnit(t *testing.T) {
	fa, errs := buildTypesFromSource(`fn log(_message: String) {}`)
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}

	sym := fa.ModuleScope.Lookup("log")
	if sym == nil {
		t.Fatal("expected symbol 'log'")
	}
	ft, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected FuncType, got %T", sym.Type)
	}
	if ft.Return != TypeUnit {
		t.Errorf("return: expected Unit, got %v", ft.Return)
	}
}

func TestTypeBuildStructWithFields(t *testing.T) {
	fa, errs := buildTypesFromSource(`struct Point { x: Float; y: Float }`)
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}

	sym := fa.ModuleScope.Lookup("Point")
	if sym == nil {
		t.Fatal("expected symbol 'Point'")
	}
	st, ok := sym.Type.(*StructType)
	if !ok {
		t.Fatalf("expected StructType, got %T", sym.Type)
	}
	if len(st.Fields) != 2 {
		t.Fatalf("expected 2 fields, got %d", len(st.Fields))
	}
	if st.Fields[0].Name != "x" || st.Fields[0].Type != TypeFloat {
		t.Errorf("field 0: expected x: Float, got %s: %v", st.Fields[0].Name, st.Fields[0].Type)
	}
	if st.Fields[1].Name != "y" || st.Fields[1].Type != TypeFloat {
		t.Errorf("field 1: expected y: Float, got %s: %v", st.Fields[1].Name, st.Fields[1].Type)
	}
}

func TestTypeBuildEnumVariants(t *testing.T) {
	src := `enum Shape {
  Circle Float
  Rectangle { width: Float, height: Float }
  Unknown
}`
	fa, errs := buildTypesFromSource(src)
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}

	sym := fa.ModuleScope.Lookup("Shape")
	if sym == nil {
		t.Fatal("expected symbol 'Shape'")
	}
	et, ok := sym.Type.(*EnumType)
	if !ok {
		t.Fatalf("expected EnumType, got %T", sym.Type)
	}
	if len(et.Variants) != 3 {
		t.Fatalf("expected 3 variants, got %d", len(et.Variants))
	}

	// Circle(Float) — positional variant
	if et.Variants[0].Name != "Circle" {
		t.Errorf("variant 0: expected Circle, got %s", et.Variants[0].Name)
	}
	if et.Variants[0].DataType != TypeFloat {
		t.Errorf("variant 0: expected Float data type, got %v", et.Variants[0].DataType)
	}

	// Rectangle { width: Float, height: Float } — struct variant
	if et.Variants[1].Name != "Rectangle" {
		t.Errorf("variant 1: expected Rectangle, got %s", et.Variants[1].Name)
	}
	if len(et.Variants[1].Fields) != 2 {
		t.Fatalf("variant 1: expected 2 fields, got %d", len(et.Variants[1].Fields))
	}
	if et.Variants[1].Fields[0].Name != "width" || et.Variants[1].Fields[0].Type != TypeFloat {
		t.Errorf("variant 1 field 0: expected width: Float, got %s: %v", et.Variants[1].Fields[0].Name, et.Variants[1].Fields[0].Type)
	}

	// Unknown — bare variant
	if et.Variants[2].Name != "Unknown" {
		t.Errorf("variant 2: expected Unknown, got %s", et.Variants[2].Name)
	}
	if et.Variants[2].DataType != nil {
		t.Errorf("variant 2: expected nil data type, got %v", et.Variants[2].DataType)
	}
}

func TestTypeBuildParamSymbolsGetTypes(t *testing.T) {
	fa, errs := buildTypesFromSource(`fn greet(name: String, age: Int): String { _ = age; name }`)
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}

	var foundName, foundAge bool
	for _, sym := range fa.Definitions {
		if sym.Kind == SymbolParam && sym.Name == "name" {
			foundName = true
			if sym.Type != TypeString {
				t.Errorf("param 'name': expected String, got %v", sym.Type)
			}
		}
		if sym.Kind == SymbolParam && sym.Name == "age" {
			foundAge = true
			if sym.Type != TypeInt {
				t.Errorf("param 'age': expected Int, got %v", sym.Type)
			}
		}
	}
	if !foundName {
		t.Error("param symbol 'name' not found in Definitions")
	}
	if !foundAge {
		t.Error("param symbol 'age' not found in Definitions")
	}
}

func TestTypeBuildUnknownTypeError(t *testing.T) {
	_, errs := buildTypesFromSource(`fn bad(_x: Bogus): Int { 0 }`)
	if len(errs) == 0 {
		t.Fatal("expected an error for unknown type Bogus")
	}
	found := false
	for _, e := range errs {
		if e.Message == `unknown type "Bogus"` {
			found = true
		}
	}
	if !found {
		t.Errorf("expected error about unknown type Bogus, got: %v", errs)
	}
}

func TestTypeBuildFuncNoReturnIsNil(t *testing.T) {
	fa, errs := buildTypesFromSource(`fn noop() { }`)
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}

	sym := fa.ModuleScope.Lookup("noop")
	if sym == nil {
		t.Fatal("expected symbol 'noop'")
	}
	ft, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected FuncType, got %T", sym.Type)
	}
	if ft.Return != TypeUnit {
		t.Errorf("expected Unit return (unannotated), got %v", ft.Return)
	}
}

func TestBuildTypesExternFunc(t *testing.T) {
	src := `host fn print(value: String): Unit`
	fa, errs := buildTypesFromSource(src)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	sym := fa.ModuleScope.Lookup("print")
	if sym == nil {
		t.Fatal("print symbol not found")
	}
	ft, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected FuncType, got %T", sym.Type)
	}
	if len(ft.Params) != 1 {
		t.Fatalf("expected 1 param, got %d", len(ft.Params))
	}
	if ft.Params[0] != TypeString {
		t.Errorf("expected String param, got %v", ft.Params[0])
	}
	if ft.Return != TypeUnit {
		t.Errorf("expected Unit return, got %v", ft.Return)
	}
}

func TestBuildTypesExternFuncGeneric(t *testing.T) {
	// Use a locally-declared generic enum (Box<T>) instead of stdlib Maybe
	// so the test doesn't depend on stdlib being loaded. The shape exercises
	// the same generic-extern path: a List<T> param and a generic-enum return.
	src := `enum Box<T> { Wrapped T; Empty }
host fn head<T>(list: List<T>): Box<T>`
	fa, errs := buildTypesFromSource(src)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	sym := fa.ModuleScope.Lookup("head")
	if sym == nil {
		t.Fatal("head symbol not found")
	}
	ft, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected FuncType, got %T", sym.Type)
	}
	if len(ft.Params) != 1 {
		t.Fatalf("expected 1 param, got %d", len(ft.Params))
	}
	// Param should be List<T> where T is a TypeParam
	listTy, ok := ft.Params[0].(*ListType)
	if !ok {
		t.Fatalf("expected ListType, got %T", ft.Params[0])
	}
	if _, ok := listTy.Elem.(*TypeParam_); !ok {
		t.Errorf("expected TypeParam_ element, got %T", listTy.Elem)
	}
}

func TestTypeBuildStructEnumVariantFields(t *testing.T) {
	src := `enum Msg {
  Text { content: String, sender: String }
}`
	fa, errs := buildTypesFromSource(src)
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}

	sym := fa.ModuleScope.Lookup("Msg")
	if sym == nil {
		t.Fatal("expected symbol 'Msg'")
	}
	et, ok := sym.Type.(*EnumType)
	if !ok {
		t.Fatalf("expected EnumType, got %T", sym.Type)
	}
	if len(et.Variants) != 1 {
		t.Fatalf("expected 1 variant, got %d", len(et.Variants))
	}
	v := et.Variants[0]
	if v.Name != "Text" {
		t.Errorf("expected variant Text, got %s", v.Name)
	}
	if len(v.Fields) != 2 {
		t.Fatalf("expected 2 fields, got %d", len(v.Fields))
	}
	if v.Fields[0].Name != "content" || v.Fields[0].Type != TypeString {
		t.Errorf("field 0: expected content: String, got %s: %v", v.Fields[0].Name, v.Fields[0].Type)
	}
	if v.Fields[1].Name != "sender" || v.Fields[1].Type != TypeString {
		t.Errorf("field 1: expected sender: String, got %s: %v", v.Fields[1].Name, v.Fields[1].Type)
	}
}
