package vm_test

import (
	"os/exec"
	"strings"
	"testing"
)

// The VM reads retained IR without linking the front end or the IR builder.
func TestVM_ReadsTheIRAndNothingElse(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "github.com/nomi-language/nomi/internal/vm").CombinedOutput()
	if err != nil {
		t.Fatalf("listing the vm package's dependencies: %v\n%s", err, out)
	}
	deps := strings.Split(strings.TrimSpace(string(out)), "\n")
	banned := map[string]string{
		"github.com/nomi-language/nomi/internal/ast":      "an AST is what a linearizer walks",
		"github.com/nomi-language/nomi/internal/parser":   "a parser is where an AST comes from",
		"github.com/nomi-language/nomi/internal/analysis": "a checked AST is where a linearizer starts",
		"github.com/nomi-language/nomi/internal/irbuild":  "the producer; reading its Go-spelled text would be reading a second lowering",
	}
	found := 0
	for _, d := range deps {
		if why, isBanned := banned[strings.TrimSpace(d)]; isBanned {
			t.Errorf("github.com/nomi-language/nomi/internal/vm depends on %s: %s", d, why)
			found++
		}
	}
	// The positive control: the list is non-empty and contains what the
	// package really does import, so a `go list` that silently answered
	// nothing would not pass this for the wrong reason.
	if len(deps) < 2 {
		t.Fatalf("go list -deps answered %d packages, so the check above is vacuous", len(deps))
	}
	if !slicesContain(deps, "github.com/nomi-language/nomi/internal/ir") || !slicesContain(deps, "github.com/nomi-language/nomi/rt") {
		t.Fatalf("the vm package must depend on nomi/internal/ir and rt; got %v", deps)
	}
	if found == 0 {
		t.Logf("%d dependencies, none of them an AST, a parser, an analyzer or the IR builder",
			len(deps))
	}
}

func slicesContain(ss []string, want string) bool {
	for _, s := range ss {
		if strings.TrimSpace(s) == want {
			return true
		}
	}
	return false
}
