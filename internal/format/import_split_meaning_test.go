package format

import "testing"

// The formatter writes an import's lowercase dotted segments as file path
// segments (`api.http.header.{self}` as `api/http/header.{self}`). An owner
// is a type, whose name is PascalCase, so the parser reads both spellings
// with the same file path, and the formatter splits where the parser did.
// Found by FuzzFormatKeepsMeaning on `import A.a0.A`, and on
// `{{import std.a000000.A…}}`, an import in a block, which SameMeaning used
// to compare with the source's split.
func TestSameMeaning_ImportPathSplit(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{"import A.a0.A\n", "import A/a0.A\n"},
		{"import api.http.header.{self, canonical_name}\n", "import api/http/header.{self, canonical_name}\n"},
		{"import shape.Shape.{Circle}\n", "import shape.Shape.Circle\n"},
		{"import api.http.Header.{self, Name}\n", "import api/http.Header.{self, Name}\n"},
		// A PascalCase segment after a `/` is a file path segment.
		{"import a/B.c\n", "import a/B.c\n"},
		{"{{import std.a000000.A0000000000000000000000}}\n", "{ { import std/a000000.A0000000000000000000000 } }\n"},
		// A quoted segment parses as an Ident and an unquoted capitalized
		// one as a TypeIdent; both are the name.
		{"import\"AAAA0\"\n", "import AAAA0\n"},
		{"{import\"AAAA0\"}\n", "{ import AAAA0 }\n"},
	} {
		formatsKeepingMeaning(t, c.src, c.want)
	}
}
