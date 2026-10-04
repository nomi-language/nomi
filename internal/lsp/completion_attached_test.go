package lsp

import "testing"

const attachedUser = "struct User {\n    name: String\n    age: Int\n}\n\n"

// A `//!` group is a test body: a name bound on an earlier line is in scope,
// with its type, on the later lines, also while the line being typed does
// not parse yet.
func TestCompletion_AttachedTestBindings(t *testing.T) {
	tests := []struct {
		name      string
		src       string
		want, not []string
	}{
		{
			"member access on a binding",
			attachedUser + "//! u = User{name: \"a\", age: 1}\n//! assert u." + cursorMark + "\npub fn double(n: Int): Int {\n    n * 2\n}\n",
			[]string{"name", "age"},
			nil,
		},
		{
			"member access before a comparison",
			attachedUser + "//! u = User{name: \"a\", age: 1}\n//! assert u." + cursorMark + " == \"a\"\npub fn double(n: Int): Int {\n    n * 2\n}\n",
			[]string{"name", "age"},
			nil,
		},
		{
			"a binding in a later assert",
			attachedUser + "//! user = User{name: \"a\", age: 1}\n//! assert us" + cursorMark + "\npub fn double(n: Int): Int {\n    n * 2\n}\n",
			[]string{"user"},
			nil,
		},
		{
			"another group's binding does not leak",
			"//! first = 1\n//! assert double(first) == 2\n\n//! second = 2\n//! assert double(" + cursorMark + ") == 4\npub fn double(n: Int): Int {\n    n * 2\n}\n",
			[]string{"second", "double"},
			[]string{"first"},
		},
		{
			"a group's binding does not leak into the declaration",
			"//! first = 1\n//! assert double(first) == 2\npub fn double(n: Int): Int {\n    n * " + cursorMark + "\n}\n",
			[]string{"n"},
			[]string{"first"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			items := complete(t, tt.src)
			labelsInclude(t, items, tt.want...)
			labelsExclude(t, items, tt.not...)
		})
	}
}

// A binding in a `//!` group is ranked by the type the position expects, as
// in any test body.
func TestCompletion_AttachedTestBindingFitsTheExpectedType(t *testing.T) {
	src := "//! label = \"x\"\n//! count = 3\n//! assert double(" + cursorMark + ") == 6\npub fn double(n: Int): Int {\n    n * 2\n}\n"
	items := complete(t, src)
	labelsBefore(t, items, "count", "label")
}
