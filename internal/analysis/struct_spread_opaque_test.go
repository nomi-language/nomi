package analysis_test

import "testing"

// OPACITY, for the spread spelling.
//
// The opaque-struct update rules guard every patch route to an opaque
// struct's fields, and the spread form `Counter{..c, value: 9}` is one of
// them. Naming a private field is the exposure wherever the name appears, so
// the spread form is guarded with the same wording, and this file sits beside
// opaque_struct_update_test.go, sharing its module fixtures.
//
// `{..c}` alone names no field, but it is rejected for its own reason: a
// spread that changes no field is its base value.

func TestOpaqueUpdate_SpreadNamingAFieldFromOutsideIsRefused(t *testing.T) {
	_, errs := opaqueProject(t, opaquePatchFiles(`import counter.{self, Counter}

fn wreck(c: Counter): Counter {
  {..c, value: -999}
}
`))
	expectOpaquePatchRefused(t, errs, "value")
}

func TestOpaqueUpdate_SpreadNamingNoFieldIsTheNoFieldError(t *testing.T) {
	_, errs := opaqueProject(t, opaquePatchFiles(`import counter.{Counter}

fn copy(c: Counter): Counter {
  {..c}
}
`))
	want := "a struct spread with no fields is its base value; write `c`"
	if len(errs) != 1 || errs[0].Message != want {
		t.Fatalf("errors = %v, want exactly %q (and no opacity error: no field is named)", errs, want)
	}
}

// Inside the declaring file the field is the owner's own, so the spread is
// legal there — the control that keeps the rule keyed on the FILE rather than
// on the type being opaque at all.
func TestOpaqueUpdate_SpreadInsideTheDeclaringFileIsAccepted(t *testing.T) {
	_, errs := opaqueProject(t, map[string]string{
		"counter.nomi": counterModule + `
pub fn set_value_by_spread(c: Counter, v: Int): Counter {
  {..c, value: v}
}
`,
		"open.nomi": openModule,
		"main.nomi": `import counter.{self, Counter}

fn use(c: Counter): Int {
  counter.value(counter.set_value_by_spread(c, 3))
}
`,
	})
	if len(errs) != 0 {
		t.Fatalf("a spread inside the declaring file must be legal, got %v", errs)
	}
}

// The negative control: the same shape on a TRANSPARENT struct is untouched.
// A rule that refused every spread would pass the negative above and fail
// this.
func TestOpaqueUpdate_SpreadOnATransparentStructIsUntouched(t *testing.T) {
	_, errs := opaqueProject(t, opaquePatchFiles(`import open.{Open}

fn shift(o: Open): Open {
  {..o, a: 9}
}
`))
	if len(errs) != 0 {
		t.Fatalf("a spread over a transparent struct must stay legal, got %v", errs)
	}
}
