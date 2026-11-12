package frontend

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/std"
)

// `subtests` rows of a List whose element nothing determines are rejected
// in a std module's test as in a program's: the rows' type is not
// determined. A std module sees no `Iter` it does not import, and the check
// called such rows something `Iter` does not accept.
func TestStdModuleSubtestsOpenRows(t *testing.T) {
	src, ok := std.ReadFile("hex")
	if !ok {
		t.Fatal("no std/hex")
	}
	edited := string(src) + "\ntest \"open rows\"\n    subtests [[]], got\n        assert got == got\n    end\nend\n"
	_, _, err := New(Config{}).CheckStdlibSource("hex", edited, Mode{Tests: true})
	const want = "the type of the rows of `subtests` is not determined: List<List<_>>"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("the edited module checks as %v; want %q", err, want)
	}
}
