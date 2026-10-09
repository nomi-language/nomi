package format

import "testing"

// The formatter writes an import's lowercase dotted segments as file path
// segments (`api.http.header.{self}` as `api/http/header.{self}`). An owner
// is a type, whose name is PascalCase, so both spellings import the same
// thing, and SameMeaning compares where the formatter splits the path, not
// where the source did. Found by FuzzFormatKeepsMeaning on `import A.a0.A`.
func TestSameMeaning_ImportPathSplit(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{"import A.a0.A\n", "import A/a0.A\n"},
		{"import api.http.header.{self, canonical_name}\n", "import api/http/header.{self, canonical_name}\n"},
		{"import shape.Shape.{Circle}\n", "import shape.Shape.Circle\n"},
		// A quoted segment parses as an Ident and an unquoted capitalized
		// one as a TypeIdent; both are the name.
		{"import\"AAAA0\"\n", "import AAAA0\n"},
	} {
		formatsKeepingMeaning(t, c.src, c.want)
	}
}
