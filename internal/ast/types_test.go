package ast

import "testing"

// TestAnonStructType_TypeStringCanonical pins the canonical TypeString
// rendering. The formatter's round-trip property tests assert
// `Format(src) == src`, which would silently accept a TypeString change
// that produced a different canonical form (the new form would just
// round-trip to itself). This test locks the spacing: single space after
// `:`, single space after `,`, no space inside braces.
func TestAnonStructType_TypeStringCanonical(t *testing.T) {
	ast := &AnonStructType{
		Fields: []StructField{
			{Name: "name", TypeAnnotation: &SimpleType{Name: "String"}},
			{Name: "age", TypeAnnotation: &SimpleType{Name: "Int"}},
		},
	}
	got := ast.TypeString()
	want := "{name: String, age: Int}"
	if got != want {
		t.Errorf("TypeString = %q, want %q", got, want)
	}
}

// TestAnonStructType_TypeStringEmpty pins the empty-shape rendering.
func TestAnonStructType_TypeStringEmpty(t *testing.T) {
	ast := &AnonStructType{}
	got := ast.TypeString()
	want := "{}"
	if got != want {
		t.Errorf("TypeString = %q, want %q", got, want)
	}
}

// TestAnonStructType_TypeStringNested pins recursive rendering through
// nested anon struct types. Nested types share the same field formatting,
// so a regression in either layer would surface here.
func TestAnonStructType_TypeStringNested(t *testing.T) {
	inner := &AnonStructType{
		Fields: []StructField{
			{Name: "x", TypeAnnotation: &SimpleType{Name: "Int"}},
			{Name: "y", TypeAnnotation: &SimpleType{Name: "Int"}},
		},
	}
	outer := &AnonStructType{
		Fields: []StructField{
			{Name: "point", TypeAnnotation: inner},
			{Name: "label", TypeAnnotation: &SimpleType{Name: "String"}},
		},
	}
	got := outer.TypeString()
	want := "{point: {x: Int, y: Int}, label: String}"
	if got != want {
		t.Errorf("TypeString = %q, want %q", got, want)
	}
}
