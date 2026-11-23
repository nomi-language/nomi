package analysis

import "testing"

// A declared type keeps a part whose type failed to resolve, with no type
// (part_of_no_type.go), so every type's String must print one. A program
// with `{email: Nope}` in a function's parameter used to stop the checker's
// dump, and any message that names the type, with a nil dereference.
func TestTypeString_APartOfNoTypePrints(t *testing.T) {
	anon := &AnonStructType{Fields: []FieldDef{{Name: "email", Type: nil}}}
	types := map[string]Type{
		"{email: <unknown>}":           anon,
		"List<<unknown>>":              &ListType{},
		"Map<<unknown>, <unknown>>":    &MapType{},
		"({email: <unknown>}) -> Unit": &FuncType{Params: []Type{anon}, Return: TypeUnit},
		"(?) -> Unit":                  &FuncType{Params: []Type{nil}, Return: TypeUnit},
		"(<unknown>, Int)":             &TupleType{Elems: []Type{nil, TypeInt}},
	}
	for want, typ := range types {
		if got := typ.String(); got != want {
			t.Errorf("String() = %q, want %q", got, want)
		}
	}
}
