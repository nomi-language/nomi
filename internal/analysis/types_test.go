package analysis

import "testing"

// ---------------------------------------------------------------------------
// String() tests
// ---------------------------------------------------------------------------

func TestPrimitiveString(t *testing.T) {
	cases := []struct {
		typ  Type
		want string
	}{
		{TypeInt, "Int"},
		{TypeFloat, "Float"},
		{TypeString, "String"},
		{TypeBool, "Bool"},
		{TypeUnit, "Unit"},
		{TypeInfallible, "Infallible"},
	}
	for _, c := range cases {
		if got := c.typ.String(); got != c.want {
			t.Errorf("got %q, want %q", got, c.want)
		}
	}
}

func TestStructString(t *testing.T) {
	s := &StructType{Name: "Point", Fields: []FieldDef{
		{Name: "x", Type: TypeInt},
		{Name: "y", Type: TypeInt},
	}}
	if got := s.String(); got != "Point" {
		t.Errorf("got %q, want %q", got, "Point")
	}
}

func TestEnumString(t *testing.T) {
	e := &EnumType{Name: "Option", Variants: []VariantDef{
		{Name: "Some", DataType: TypeInt},
		{Name: "None"},
	}}
	if got := e.String(); got != "Option" {
		t.Errorf("got %q, want %q", got, "Option")
	}
}

func TestFuncTypeString(t *testing.T) {
	f := &FuncType{Params: []Type{TypeInt, TypeString}, Return: TypeBool}
	want := "(Int, String) -> Bool"
	if got := f.String(); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFuncTypeStringNilReturn(t *testing.T) {
	f := &FuncType{Params: []Type{TypeInt}, Return: nil}
	want := "(Int) -> Unit"
	if got := f.String(); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestTupleString(t *testing.T) {
	tu := &TupleType{Elems: []Type{TypeInt, TypeString}}
	want := "(Int, String)"
	if got := tu.String(); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestListString(t *testing.T) {
	l := &ListType{Elem: TypeInt}
	want := "List<Int>"
	if got := l.String(); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestMapString(t *testing.T) {
	m := &MapType{Key: TypeString, Val: TypeInt}
	want := "Map<String, Int>"
	if got := m.String(); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestAnonStructString(t *testing.T) {
	a := &AnonStructType{Fields: []FieldDef{
		{Name: "name", Type: TypeString},
		{Name: "age", Type: TypeInt},
	}}
	want := "{name: String, age: Int}"
	if got := a.String(); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestTypeParamString(t *testing.T) {
	tp := &TypeParam_{Name_: "T"}
	if got := tp.String(); got != "T" {
		t.Errorf("got %q, want %q", got, "T")
	}
}

func TestTypeVarString(t *testing.T) {
	tv := &TypeVar{ID: 1}
	if got := tv.String(); got != "?1" {
		t.Errorf("unresolved: got %q, want %q", got, "?1")
	}
	tv.Resolved = TypeInt
	if got := tv.String(); got != "Int" {
		t.Errorf("resolved: got %q, want %q", got, "Int")
	}
}

func TestInterfaceString(t *testing.T) {
	i := &InterfaceType{Name: "Display"}
	if got := i.String(); got != "Display" {
		t.Errorf("got %q, want %q", got, "Display")
	}
}

func TestDistinctString(t *testing.T) {
	d := &DistinctType{Name: "UserID", Inner: TypeInt}
	if got := d.String(); got != "UserID" {
		t.Errorf("got %q, want %q", got, "UserID")
	}
}

// ---------------------------------------------------------------------------
// TypesEqual tests
// ---------------------------------------------------------------------------

func TestPrimitiveEquality(t *testing.T) {
	if !TypesEqual(TypeInt, TypeInt) {
		t.Error("Int should equal Int")
	}
	if TypesEqual(TypeInt, TypeString) {
		t.Error("Int should not equal String")
	}
	if TypesEqual(TypeInt, TypeFloat) {
		t.Error("Int should not equal Float")
	}
}

func TestStructEquality(t *testing.T) {
	a := &StructType{Name: "Point", Fields: []FieldDef{{Name: "x", Type: TypeInt}}}
	b := &StructType{Name: "Point", Fields: []FieldDef{{Name: "y", Type: TypeFloat}}}
	c := &StructType{Name: "Vec"}
	if !TypesEqual(a, b) {
		t.Error("same-name structs should be equal")
	}
	if TypesEqual(a, c) {
		t.Error("different-name structs should not be equal")
	}
}

func TestEnumEquality(t *testing.T) {
	a := &EnumType{Name: "Color"}
	b := &EnumType{Name: "Color"}
	c := &EnumType{Name: "Shape"}
	if !TypesEqual(a, b) {
		t.Error("same-name enums should be equal")
	}
	if TypesEqual(a, c) {
		t.Error("different-name enums should not be equal")
	}
}

func TestListEquality(t *testing.T) {
	a := &ListType{Elem: TypeInt}
	b := &ListType{Elem: TypeInt}
	c := &ListType{Elem: TypeString}
	if !TypesEqual(a, b) {
		t.Error("List<Int> should equal List<Int>")
	}
	if TypesEqual(a, c) {
		t.Error("List<Int> should not equal List<String>")
	}
}

func TestFuncTypeEquality(t *testing.T) {
	a := &FuncType{Params: []Type{TypeInt, TypeString}, Return: TypeBool}
	b := &FuncType{Params: []Type{TypeInt, TypeString}, Return: TypeBool}
	c := &FuncType{Params: []Type{TypeInt}, Return: TypeBool}
	d := &FuncType{Params: []Type{TypeInt, TypeString}, Return: TypeInt}
	if !TypesEqual(a, b) {
		t.Error("identical func types should be equal")
	}
	if TypesEqual(a, c) {
		t.Error("different param count should not be equal")
	}
	if TypesEqual(a, d) {
		t.Error("different return type should not be equal")
	}
}

func TestFuncTypeNilReturnEquality(t *testing.T) {
	a := &FuncType{Params: []Type{TypeInt}, Return: nil}
	b := &FuncType{Params: []Type{TypeInt}, Return: TypeUnit}
	if !TypesEqual(a, b) {
		t.Error("nil return should equal Unit return")
	}
}

func TestTupleEquality(t *testing.T) {
	a := &TupleType{Elems: []Type{TypeInt, TypeString}}
	b := &TupleType{Elems: []Type{TypeInt, TypeString}}
	c := &TupleType{Elems: []Type{TypeString, TypeInt}}
	d := &TupleType{Elems: []Type{TypeInt}}
	if !TypesEqual(a, b) {
		t.Error("identical tuples should be equal")
	}
	if TypesEqual(a, c) {
		t.Error("different-order tuples should not be equal")
	}
	if TypesEqual(a, d) {
		t.Error("different-length tuples should not be equal")
	}
}

func TestMapEquality(t *testing.T) {
	a := &MapType{Key: TypeString, Val: TypeInt}
	b := &MapType{Key: TypeString, Val: TypeInt}
	c := &MapType{Key: TypeInt, Val: TypeInt}
	if !TypesEqual(a, b) {
		t.Error("identical maps should be equal")
	}
	if TypesEqual(a, c) {
		t.Error("different key types should not be equal")
	}
}

func TestTypeVarResolvedEquality(t *testing.T) {
	a := &TypeVar{ID: 1, Resolved: TypeInt}
	b := &TypeVar{ID: 2, Resolved: TypeInt}
	if !TypesEqual(a, b) {
		t.Error("TypeVars resolved to same type should be equal")
	}
	c := &TypeVar{ID: 1, Resolved: TypeString}
	if TypesEqual(a, c) {
		t.Error("TypeVars resolved to different types should not be equal")
	}
}

func TestTypeVarUnresolvedEquality(t *testing.T) {
	a := &TypeVar{ID: 1}
	b := &TypeVar{ID: 1}
	c := &TypeVar{ID: 2}
	if !TypesEqual(a, b) {
		t.Error("same-ID unresolved TypeVars should be equal")
	}
	if TypesEqual(a, c) {
		t.Error("different-ID unresolved TypeVars should not be equal")
	}
}

func TestTypeVarChainEquality(t *testing.T) {
	inner := &TypeVar{ID: 2, Resolved: TypeBool}
	outer := &TypeVar{ID: 1, Resolved: inner}
	if !TypesEqual(outer, TypeBool) {
		t.Error("chained TypeVar should resolve through to final type")
	}
}

func TestCrossTypeInequality(t *testing.T) {
	if TypesEqual(TypeInt, &StructType{Name: "Int"}) {
		t.Error("PrimitiveType should not equal StructType even with same name")
	}
	if TypesEqual(&ListType{Elem: TypeInt}, &TupleType{Elems: []Type{TypeInt}}) {
		t.Error("ListType should not equal TupleType")
	}
}

func TestTypeParamEquality(t *testing.T) {
	a := &TypeParam_{Name_: "T"}
	b := &TypeParam_{Name_: "T"}
	c := &TypeParam_{Name_: "U"}
	if !TypesEqual(a, b) {
		t.Error("same-name type params should be equal")
	}
	if TypesEqual(a, c) {
		t.Error("different-name type params should not be equal")
	}
}

func TestAnonStructEquality(t *testing.T) {
	a := &AnonStructType{Fields: []FieldDef{
		{Name: "x", Type: TypeInt},
		{Name: "y", Type: TypeFloat},
	}}
	b := &AnonStructType{Fields: []FieldDef{
		{Name: "x", Type: TypeInt},
		{Name: "y", Type: TypeFloat},
	}}
	c := &AnonStructType{Fields: []FieldDef{
		{Name: "x", Type: TypeInt},
		{Name: "z", Type: TypeFloat},
	}}
	if !TypesEqual(a, b) {
		t.Error("identical anon structs should be equal")
	}
	if TypesEqual(a, c) {
		t.Error("different field names should not be equal")
	}
}

func TestAnonStructEquality_FieldOrderIgnored(t *testing.T) {
	a := &AnonStructType{Fields: []FieldDef{
		{Name: "x", Type: TypeInt},
		{Name: "y", Type: TypeFloat},
	}}
	b := &AnonStructType{Fields: []FieldDef{
		{Name: "y", Type: TypeFloat},
		{Name: "x", Type: TypeInt},
	}}
	if !TypesEqual(a, b) {
		t.Error("anon structs with same fields in different order should be equal")
	}
}

func TestAnonStructEquality_DifferentArity(t *testing.T) {
	a := &AnonStructType{Fields: []FieldDef{{Name: "x", Type: TypeInt}}}
	b := &AnonStructType{Fields: []FieldDef{
		{Name: "x", Type: TypeInt},
		{Name: "y", Type: TypeFloat},
	}}
	if TypesEqual(a, b) {
		t.Error("anon structs with different field counts should not be equal")
	}
}

func TestAnonStructEquality_DifferentFieldType(t *testing.T) {
	a := &AnonStructType{Fields: []FieldDef{{Name: "x", Type: TypeInt}}}
	b := &AnonStructType{Fields: []FieldDef{{Name: "x", Type: TypeString}}}
	if TypesEqual(a, b) {
		t.Error("same field name with different types should not be equal")
	}
}

func TestAnonStructEquality_NotEqualToNominalSameShape(t *testing.T) {
	anon := &AnonStructType{Fields: []FieldDef{{Name: "x", Type: TypeInt}}}
	nominal := &StructType{Name: "Foo", Fields: []FieldDef{{Name: "x", Type: TypeInt}}}
	if TypesEqual(anon, nominal) {
		t.Error("anon struct must not equal a nominal struct with the same shape")
	}
}

func TestAnonStructEquality_NestedReorder(t *testing.T) {
	inner1 := &AnonStructType{Fields: []FieldDef{
		{Name: "x", Type: TypeInt},
		{Name: "y", Type: TypeInt},
	}}
	inner2 := &AnonStructType{Fields: []FieldDef{
		{Name: "y", Type: TypeInt},
		{Name: "x", Type: TypeInt},
	}}
	a := &AnonStructType{Fields: []FieldDef{
		{Name: "p", Type: inner1},
		{Name: "q", Type: TypeBool},
	}}
	b := &AnonStructType{Fields: []FieldDef{
		{Name: "q", Type: TypeBool},
		{Name: "p", Type: inner2},
	}}
	if !TypesEqual(a, b) {
		t.Error("nested anon structs should compare order-insensitively at every level")
	}
}

func TestAnonStructString_PreservesDeclaredOrder(t *testing.T) {
	a := &AnonStructType{Fields: []FieldDef{
		{Name: "x", Type: TypeInt},
		{Name: "y", Type: TypeFloat},
	}}
	b := &AnonStructType{Fields: []FieldDef{
		{Name: "y", Type: TypeFloat},
		{Name: "x", Type: TypeInt},
	}}
	if a.String() == b.String() {
		t.Errorf("equal anon structs with different declared order should still render differently; both gave %q", a.String())
	}
	if a.String() != "{x: Int, y: Float}" {
		t.Errorf("a.String() = %q, want %q", a.String(), "{x: Int, y: Float}")
	}
	if b.String() != "{y: Float, x: Int}" {
		t.Errorf("b.String() = %q, want %q", b.String(), "{y: Float, x: Int}")
	}
}

func TestEnumTypeStringWithTypeArgs(t *testing.T) {
	et := &EnumType{
		Name:     "Maybe",
		TypeArgs: []Type{TypeInt},
		Variants: []VariantDef{
			{Name: "Some", DataType: TypeInt},
			{Name: "None"},
		},
	}
	if et.String() != "Maybe<Int>" {
		t.Fatalf("expected Maybe<Int>, got %s", et.String())
	}
}

func TestEnumTypeStringNoTypeArgs(t *testing.T) {
	et := &EnumType{Name: "Color"}
	if et.String() != "Color" {
		t.Fatalf("expected Color, got %s", et.String())
	}
}

func TestStructTypeStringWithTypeArgs(t *testing.T) {
	st := &StructType{
		Name:     "Pair",
		TypeArgs: []Type{TypeInt, TypeString},
	}
	if st.String() != "Pair<Int, String>" {
		t.Fatalf("expected Pair<Int, String>, got %s", st.String())
	}
}

func TestTypesEqual_MaybeIntVsMaybeString(t *testing.T) {
	a := &EnumType{Name: "Maybe", TypeArgs: []Type{TypeInt}}
	b := &EnumType{Name: "Maybe", TypeArgs: []Type{TypeString}}
	if TypesEqual(a, b) {
		t.Fatal("Maybe<Int> should not equal Maybe<String>")
	}
}

func TestTypesEqual_MaybeIntVsMaybeInt(t *testing.T) {
	a := &EnumType{Name: "Maybe", TypeArgs: []Type{TypeInt}}
	b := &EnumType{Name: "Maybe", TypeArgs: []Type{TypeInt}}
	if !TypesEqual(a, b) {
		t.Fatal("Maybe<Int> should equal Maybe<Int>")
	}
}

func TestContainsTypeParam_AnonStruct(t *testing.T) {
	tParam := &TypeParam_{Name_: "T"}

	withParam := &AnonStructType{Fields: []FieldDef{
		{Name: "val", Type: tParam},
	}}
	if !ContainsTypeParam(withParam) {
		t.Error("anon struct with TypeParam_ field type should contain a type param")
	}

	nested := &AnonStructType{Fields: []FieldDef{
		{Name: "wrap", Type: &ListType{Elem: tParam}},
	}}
	if !ContainsTypeParam(nested) {
		t.Error("anon struct with TypeParam_ inside a List<T> field should contain a type param")
	}

	concrete := &AnonStructType{Fields: []FieldDef{
		{Name: "val", Type: TypeInt},
	}}
	if ContainsTypeParam(concrete) {
		t.Error("anon struct with no TypeParam_ should not contain a type param")
	}
}
