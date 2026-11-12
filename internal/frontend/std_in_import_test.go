package frontend

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/std"
)

// A std file has no prelude, so a membership test `x in c` needs Contains
// imported, and the `in` is that import's use: the unused-import check
// counts it. It reported the import unused, so a std module could write
// `in` neither with the import nor without it.
func TestStdModuleInUsesItsContainsImport(t *testing.T) {
	src, ok := std.ReadFile("hex")
	if !ok {
		t.Fatal("no std/hex")
	}
	fn := "\npub fn has_one(xs: List<Int>) -> Bool\n    1 in xs\nend\n"
	without := strings.Replace(string(src), "import\n", "import\n    bool.Bool\n    int.Int\n    lists.List\n", 1) + fn
	_, _, err := New(Config{}).CheckStdlibSource("hex", without, Mode{Tests: true})
	if err == nil || !strings.Contains(err.Error(), "`in` needs the interface `Contains` in scope") {
		t.Fatalf("without the import, the check says %v; want the `in` to need Contains", err)
	}
	with := strings.Replace(without, "import\n", "import\n    contains.Contains\n", 1)
	if _, _, err := New(Config{}).CheckStdlibSource("hex", with, Mode{Tests: true}); err != nil {
		t.Fatalf("with Contains imported, an `in` does not check: %v", err)
	}
}
