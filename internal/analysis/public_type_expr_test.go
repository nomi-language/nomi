package analysis_test

import "testing"

// A public function's signature may name no private type of its file,
// wherever in a type expression the name stands: alone, as a type
// argument, a tuple's element, a member of a conjunction, a field of an
// anonymous struct, or a parameter or result of a function type. The
// check reads a type expression's parts through ast.Children, so it has
// no list of the kinds of type expression to fall behind.
func TestVisibility_APrivateTypeAnywhereInAPublicSignature(t *testing.T) {
	const decls = `interface Hidden
    fn hid(value: self) -> Int
end

interface Named
    fn name(value: self) -> String
end

struct Secret
    n: Int
end

`
	for _, tc := range []struct{ name, param, at string }{
		{"alone", "s: Secret", "Secret"},
		{"a type argument", "s: List<Secret>", "Secret"},
		{"a tuple's element", "s: (Int, Secret)", "Secret"},
		{"a conjunction's first member", "s: Hidden and Display", "Hidden"},
		{"a conjunction's second member", "s: Display and Named", "Named"},
		{"an anonymous struct's field", "s: {inner: Secret}", "Secret"},
		{"a nested anonymous struct's field", "s: {outer: {inner: Maybe<Secret>}}", "Secret"},
		{"a function type's parameter", "s: (Secret) -> Int", "Secret"},
		{"a function type's result", "s: (Int) -> Secret", "Secret"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := decls + "pub fn exposed(" + tc.param + ")\n    _ = s\nend\n"
			_, errs := checkSourceWithStdlib(src)
			want := "function parameter exposes private type " + tc.at
			found := false
			for _, e := range errs {
				found = found || e.Message == want
			}
			if !found {
				t.Fatalf("errors = %v, want %q", errs, want)
			}
		})
	}
	t.Run("public types are exposed freely", func(t *testing.T) {
		src := "pub interface Shown\n    fn show(value: self) -> String\nend\n\npub struct Open\n    n: Int\nend\n\n" +
			"pub fn exposed(s: Shown and Display, r: {inner: Open}, f: (Open) -> Open)\n    _ = (s, r, f)\nend\n"
		_, errs := checkSourceWithStdlib(src)
		expectNoStdlibErrors(t, errs)
	})
}
