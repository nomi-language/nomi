package analysis

import (
	"strings"
	"testing"
)

// The user-file half of TestModuleNameIsNotAFileMember: an imported file's
// own name and the files it imports whole are not members reached through
// it, and a type qualifier with a repeated segment names nothing either.
func TestModuleNameIsNotAUserFileMember(t *testing.T) {
	modules := map[string]string{
		"other":  "pub fn thing() -> Int { 0 }\n",
		"shapes": "import other\n\npub enum Shape {\n  Dot(Int)\n  Empty\n}\n\npub fn f() -> Int { other.thing() }\n",
	}
	cases := []struct {
		name string
		body string
		want string
		col  int
	}{
		{"own name, call", "  x = shapes.shapes.f()", "file 'shapes' has no member 'shapes'", 14},
		{"own name, value", "  x = shapes.shapes.f", "file 'shapes' has no member 'shapes'", 14},
		{"its import, call", "  x = shapes.other.thing()", "file 'shapes' has no member 'other'", 14},
		{"its import, value", "  x = shapes.other", "file 'shapes' has no member 'other'", 14},
		{"repeated type, call", "  x = shapes.Shape.Shape.Dot(1)", "type 'shapes.Shape' has no member 'Shape'", 20},
		{"repeated type, value", "  x = shapes.Shape.Shape.Empty", "type 'shapes.Shape' has no member 'Shape'", 20},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := "import shapes\n\nfn main() {\n" + tc.body + "\n  x\n}\n"
			_, errs := checkSourceWithModules(t, src, modules)
			var got []string
			for _, e := range errs {
				got = append(got, e.Message)
				if e.Message == tc.want && e.Line == 4 && e.Col == tc.col {
					return
				}
			}
			t.Fatalf("want %q at 4:%d, got:\n  %s", tc.want, tc.col, strings.Join(got, "\n  "))
		})
	}
	t.Run("members still resolve", func(t *testing.T) {
		src := "import shapes\n\nfn make() -> shapes.Shape {\n  x = shapes.f()\n  shapes.Shape.Dot(x)\n}\n"
		if _, errs := checkSourceWithModules(t, src, modules); len(errs) != 0 {
			t.Fatalf("errors: %v", errs)
		}
	})
}
