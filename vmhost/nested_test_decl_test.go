package vmhost

import (
	"strings"
	"testing"
)

// FuzzFrontEndAcceptsSoItLowers found the front end accepting a `test`
// declared inside a test body, which the builder declined. A `test` is a
// declaration of the file or of a group, so the parser refuses one in any
// body, and a `group` and a `gopkg` likewise.
func TestTopLevelOnlyDeclarationsInABodyAreRejected(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"test in a test", "test \"outer\" {\n    test \"inner\" { assert 1 == 1 }\n    assert 1 == 1\n}\n",
			"main.nomi:2:5: a `test` is declared at the top level of a file or in a `group`; it cannot be nested inside a body"},
		{"test in a function", "import std/io\n\nfn f() -> Int {\n    test \"inner\" { assert 1 == 1 }\n    1\n}\n\nfn main() {\n    io.print(f())\n}\n",
			"main.nomi:4:5: a `test` is declared at the top level of a file or in a `group`; it cannot be nested inside a body"},
		{"test in an attached prompt", "//! test \"x\" { assert 1 == 1 }\n//! assert answer() == 42\n//\nfn answer() -> Int {\n    42\n}\n",
			"main.nomi:1:5: a `test` is declared at the top level of a file or in a `group`; it cannot be nested inside a body"},
		{"group in a test", "test \"outer\" {\n    group \"g\" {\n        test \"a\" { assert 1 == 1 }\n    }\n    assert 1 == 1\n}\n",
			"main.nomi:2:5: a `group` is declared at the top level of a file; it cannot be nested inside a body"},
		{"gopkg in a function", "import std/io\n\nfn main() {\n    gopkg \"strings\"\n    io.print(1)\n}\n",
			"main.nomi:4:5: a `gopkg` declaration belongs at the top level of a file, beside the `go` bindings that use it; it cannot appear inside a body"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o := checkLowers(t.TempDir(), c.src)
			if o.accepted {
				t.Fatalf("the front end accepts this:\n%s", c.src)
			}
			if !strings.Contains(o.rejection, c.want) {
				t.Fatalf("rejection does not contain %q:\n%s", c.want, o.rejection)
			}
		})
	}
}
