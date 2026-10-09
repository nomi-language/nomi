package format

import "testing"

// A single dotted name imported in braces keeps them: `import A.{B.C}`
// without braces, `import A.B.C`, reads `B` as part of the path. An owner's
// selector still drops them. Found by FuzzFormatKeepsMeaning on
// `import A.{A.A}`.
func TestFormat_ImportSingleDottedNameKeepsBraces(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{"import A.{A.A}\n", "import A.{A.A}\n"},
		{"import shapes.{Probe.Reading}\n", "import shapes.{Probe.Reading}\n"},
		{"import std/maybe.Maybe.{Some, None}\n", "import std/maybe.Maybe.{None, Some}\n"},
		{"import shapes.{Circle}\n", "import shapes.Circle\n"},
	} {
		formatsKeepingMeaning(t, c.src, c.want)
	}
}
