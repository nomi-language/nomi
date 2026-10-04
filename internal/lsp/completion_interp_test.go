package lsp

import "testing"

const interpUser = "struct User {\n    name: String\n    age: Int\n}\n\n"

// An interpolation's `${...}` is an operand: it offers the scope's names as
// any operand does, in every string form that interpolates, and the literal
// text around it offers nothing.
func TestCompletion_InsideInterpolation(t *testing.T) {
	tests := []struct {
		name      string
		src       string
		want, not []string
	}{
		{
			"a parameter by prefix",
			interpUser + "fn f(user: User): String {\n    \"hi ${us" + cursorMark + "}\"\n}\n",
			[]string{"user", "User"},
			nil,
		},
		{
			"an empty interpolation offers the scope",
			interpUser + "fn f(user: User): String {\n    count = 1\n    \"hi ${" + cursorMark + "}\"\n}\n",
			[]string{"user", "count", "f", "if", "case"},
			nil,
		},
		{
			"an unclosed interpolation",
			interpUser + "fn f(user: User): String {\n    \"hi ${us" + cursorMark + "\"\n}\n",
			[]string{"user"},
			nil,
		},
		{
			"a multi-line string",
			interpUser + "fn f(user: User): String {\n    \"\"\"\n    hi ${us" + cursorMark + "}\n    \"\"\"\n}\n",
			[]string{"user"},
			nil,
		},
		{
			"a typed literal",
			"import std/calendar.{Date}\n\nfn f(m: String): Date {\n    try Date\"2026-${" + cursorMark + "}-04\"\n}\n",
			[]string{"m"},
			nil,
		},
		{
			"an unimported name brings its import",
			interpUser + "fn f(user: User): String {\n    \"hi ${jso" + cursorMark + "}\"\n}\n",
			[]string{"json"},
			nil,
		},
		{
			"a member after the dot",
			interpUser + "fn f(user: User): String {\n    \"hi ${user." + cursorMark + "}\"\n}\n",
			[]string{"name", "age"},
			[]string{"user"},
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

// The literal part of a string, and a raw string, which does not
// interpolate, offer nothing.
func TestCompletion_StringTextOffersNothing(t *testing.T) {
	for _, src := range []string{
		interpUser + "fn f(user: User): String {\n    \"hi us" + cursorMark + " ${user.name}\"\n}\n",
		interpUser + "fn f(user: User): String {\n    \"${user.name} us" + cursorMark + "\"\n}\n",
		interpUser + "fn f(user: User): String {\n    `hi ${us" + cursorMark + "}`\n}\n",
	} {
		if items := complete(t, src); len(items) != 0 {
			t.Errorf("completion in string text offers %v\n%s", itemLabels(items), src)
		}
	}
}
