package analysis

import "testing"

func TestTypeRegistry_PrimitivesPrePopulated(t *testing.T) {
	reg := NewTypeRegistry()

	cases := []struct {
		name string
		want Type
	}{
		{"Int", TypeInt},
		{"Float", TypeFloat},
		{"String", TypeString},
		{"Bool", TypeBool},
		{"Unit", TypeUnit},
		{"Infallible", TypeInfallible},
	}

	for _, tc := range cases {
		got := reg.Lookup(tc.name)
		if got != tc.want {
			t.Errorf("Lookup(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestTypeRegistry_UnknownReturnsNil(t *testing.T) {
	reg := NewTypeRegistry()
	if got := reg.Lookup("NoSuchType"); got != nil {
		t.Errorf("Lookup(unknown) = %v, want nil", got)
	}
}

func TestTypeRegistry_RegisterAndLookup(t *testing.T) {
	reg := NewTypeRegistry()
	custom := &StructType{Name: "MyStruct", Fields: []FieldDef{{Name: "x", Type: TypeInt}}}
	reg.Register("MyStruct", custom)

	got := reg.Lookup("MyStruct")
	if got != custom {
		t.Errorf("Lookup(MyStruct) = %v, want %v", got, custom)
	}
}
