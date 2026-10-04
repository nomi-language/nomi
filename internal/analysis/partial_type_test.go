package analysis

import "testing"

// Partial<T> is the parameter type of Struct.update — a deep partial of a
// struct T.

func mkUser() *StructType {
	return &StructType{Name: "User", Fields: []FieldDef{
		{Name: "name", Type: TypeString},
		{Name: "age", Type: TypeInt},
	}}
}

func mkPerson() (*StructType, *StructType) {
	addr := &StructType{Name: "Address", Fields: []FieldDef{
		{Name: "city", Type: TypeString},
		{Name: "zip", Type: TypeString},
	}}
	person := &StructType{Name: "Person", Fields: []FieldDef{
		{Name: "name", Type: TypeString},
		{Name: "address", Type: addr},
	}}
	return person, addr
}

func anon(fields ...FieldDef) *AnonStructType { return &AnonStructType{Fields: fields} }

// pc builds a bare checker for the deep-partial unit tests. `fa` is nil, so
// `implsContext` returns no impl tables and `recordConformanceIfConcrete`
// returns early — the walk then behaves exactly as the old free function did.
// Tests that need real interface-impl admission go through `checkSource`
// instead, since that requires a populated impl registry.
func pc() *checker { return &checker{} }

func TestDeepPartialMatches_FullStructReplacement(t *testing.T) {
	if !pc().deepPartialMatches(mkUser(), mkUser(), nil, Pos{}) {
		t.Fatal("a full User should match Partial<User>")
	}
}

func TestDeepPartialMatches_NarrowerAnonStruct(t *testing.T) {
	if !pc().deepPartialMatches(anon(FieldDef{Name: "name", Type: TypeString}), mkUser(), nil, Pos{}) {
		t.Fatal("{name: String} should match Partial<User>")
	}
}

func TestDeepPartialMatches_NestedAnonStruct(t *testing.T) {
	person, _ := mkPerson()
	patch := anon(FieldDef{Name: "address", Type: anon(FieldDef{Name: "city", Type: TypeString})})
	if !pc().deepPartialMatches(patch, person, nil, Pos{}) {
		t.Fatal("{address: {city: String}} should match Partial<Person>")
	}
}

func TestDeepPartialMatches_NestedFullStruct(t *testing.T) {
	person, addr := mkPerson()
	patch := anon(FieldDef{Name: "address", Type: addr})
	if !pc().deepPartialMatches(patch, person, nil, Pos{}) {
		t.Fatal("{address: Address} should match Partial<Person>")
	}
}

func TestDeepPartialMatches_UnknownFieldRejected(t *testing.T) {
	if pc().deepPartialMatches(anon(FieldDef{Name: "bogus", Type: TypeInt}), mkUser(), nil, Pos{}) {
		t.Fatal("{bogus: Int} must NOT match Partial<User>")
	}
}

func TestDeepPartialMatches_WrongFieldTypeRejected(t *testing.T) {
	if pc().deepPartialMatches(anon(FieldDef{Name: "name", Type: TypeInt}), mkUser(), nil, Pos{}) {
		t.Fatal("{name: Int} must NOT match Partial<User> (name is String)")
	}
}

func TestDeepPartialMatches_NonStructTarget(t *testing.T) {
	if !pc().deepPartialMatches(TypeInt, TypeInt, nil, Pos{}) {
		t.Fatal("Int should match Partial<Int>")
	}
	if pc().deepPartialMatches(TypeString, TypeInt, nil, Pos{}) {
		t.Fatal("String must NOT match Partial<Int>")
	}
}

// When the target struct carries no Fields inline (a thin name reference), the
// fields are resolved from the registry.
func TestDeepPartialMatches_ResolvesFieldsFromRegistry(t *testing.T) {
	reg := NewTypeRegistry()
	reg.Register("User", mkUser())
	thin := &StructType{Name: "User"} // no Fields inline
	if !pc().deepPartialMatches(anon(FieldDef{Name: "name", Type: TypeString}), thin, reg, Pos{}) {
		t.Fatal("{name: String} should match Partial<User> via registry lookup")
	}
}

func TestPartialType_ContainsTypeParam(t *testing.T) {
	tp := &TypeParam_{Name_: "T"}
	if !ContainsTypeParam(&PartialType{Inner: tp}) {
		t.Fatal("Partial<T> should contain a type param")
	}
	if ContainsTypeParam(&PartialType{Inner: mkUser()}) {
		t.Fatal("Partial<User> should not contain a type param")
	}
}

func TestPartialType_ContainsTypeVar(t *testing.T) {
	if !containsTypeVar(&PartialType{Inner: &TypeVar{ID: 1}}) {
		t.Fatal("Partial<?> should contain a type var")
	}
}

func TestPartialType_Substitute(t *testing.T) {
	tp := &TypeParam_{Name_: "T"}
	subs := map[*TypeParam_]Type{tp: mkUser()}
	out, ok := Substitute(&PartialType{Inner: tp}, subs).(*PartialType)
	if !ok {
		t.Fatalf("Substitute(Partial<T>) returned %T, want *PartialType", out)
	}
	if st, ok := out.Inner.(*StructType); !ok || st.Name != "User" {
		t.Fatalf("expected Partial<User>, got Partial<%v>", out.Inner)
	}
}

func TestPartialType_String(t *testing.T) {
	if got := (&PartialType{Inner: mkUser()}).String(); got != "Partial<User>" {
		t.Fatalf("String() = %q, want Partial<User>", got)
	}
}
