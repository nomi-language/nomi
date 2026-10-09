package format

import "testing"

// An interface requirement's parameter list is written as a function's,
// destructuring patterns included. A pattern parameter used to come out as
// the name the parser gave it with no type, `fn a(__destr_1_1: )`, which does
// not parse. Found by FuzzFormatKeepsMeaning on `interface A{fn A(0)}`.
func TestFormat_InterfaceRequirementPatternParam(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{"interface A {fn a(0)}\n", "interface A {\n    fn a(0)\n}\n"},
		{"interface A {\n    fn a((x, y): (Int, Int)): Int\n}\n", "interface A {\n    fn a((x, y): (Int, Int)): Int\n}\n"},
		{"interface A {\n    fn a(x: Int, y: Int = 1): Int\n}\n", "interface A {\n    fn a(x: Int, y: Int = 1): Int\n}\n"},
	} {
		formatsKeepingMeaning(t, c.src, c.want)
	}
}
