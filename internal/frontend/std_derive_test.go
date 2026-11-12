package frontend

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/std"
)

// A std module's derives are held to their fields' impls as a program
// file's are: a derived Equatable over a function field is refused, where
// the edited module's check skipped the derive field check and the derived
// body then declined in lowering.
func TestStdModuleDeriveChecksItsFields(t *testing.T) {
	src, ok := std.ReadFile("presence")
	if !ok {
		t.Fatal("no std/presence")
	}
	edited := string(src) + "\nstruct Drv\n    v: (Int) -> Int\nend\n\nderive Equatable for Drv\n"
	_, _, err := New(Config{}).CheckStdlibSource("presence", edited, Mode{Tests: true})
	const want = "no impl of `Equatable` for `(Int) -> Int` (required via `derive Equatable for Drv`, field `v`)"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("the edited module checks as %v; want %q", err, want)
	}
	if _, _, err := New(Config{}).CheckStdlibSource("presence", string(src), Mode{Tests: true}); err != nil {
		t.Fatalf("std/presence as it is does not check: %v", err)
	}
}
