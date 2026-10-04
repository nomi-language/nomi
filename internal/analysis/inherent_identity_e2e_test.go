package analysis_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/std"
)

// duplicateTypeOwnedFn is the substring of the diagnostic these tests are about.
const duplicateTypeOwnedFn = "duplicate type-owned function"

// buildInherentFixture runs the full BuildProjectWithCache pipeline over an
// on-disk temp project, which is the only path that reaches
// detectInherentImplCollisions — the unit tests in inherent_identity_test.go
// cover the key, this covers the SEQUENCING. PopulateInherentReceiverOrigins
// has to run after Sweep C-types and before the coherence checks; a unit test
// cannot see that, and getting it wrong yields a silently unpopulated field
// that falls back to exactly the buggy behaviour.
func buildInherentFixture(t *testing.T, files map[string]string) *analysis.FileAnalysis {
	t.Helper()
	tmp := t.TempDir()
	for rel, content := range files {
		if err := os.WriteFile(filepath.Join(tmp, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(filepath.Join(tmp, "main.nomi"))
	if err != nil {
		t.Fatal(err)
	}
	entryNodes, _ := parser.ParseWithRecovery(lexer.Lex(string(data)))
	loader := func(projectRoot string, modulePath []string) ([]ast.Node, error) {
		filePath := filepath.Join(projectRoot, filepath.Join(modulePath...)) + ".nomi"
		fileData, err := os.ReadFile(filePath)
		if err != nil {
			return nil, err
		}
		nodes, _ := parser.ParseWithRecovery(lexer.Lex(string(fileData)))
		return nodes, nil
	}
	lib := std.Load()
	fa, _, _ := analysis.BuildProjectWithCache(
		entryNodes, lib.Primitives, lib.Modules, lib.Files, tmp, loader,
	)
	if fa == nil {
		t.Fatal("expected non-nil FileAnalysis")
	}
	return fa
}

func duplicateTypeOwnedDiagnostics(fa *analysis.FileAnalysis) []string {
	var out []string
	for _, e := range fa.TypeErrors {
		if strings.Contains(e.Message, duplicateTypeOwnedFn) {
			out = append(out, e.Message)
		}
	}
	return out
}

// THE DEFECT, end to end. `std/calendar` declares `impl Date { pub fn new(year,
// month, day) }`. A file that imports std/calendar and declares a type-owned
// function on its OWN `Date` was rejected before it ran, with the diagnostic
// anchored on the user's line for a collision they could not see.
func TestInherentIdentity_LocalTypeOwnedFnBesideAStdlibOne(t *testing.T) {
	fa := buildInherentFixture(t, map[string]string{
		"main.nomi": `import std/calendar

struct Date {
  flavor: String
}

impl Date {
  pub fn new(a: String, b: String): Date {
    Date{flavor: a + b}
  }
}

fn main() {
  c = Date.new("a", "b")
}
`,
	})
	if got := duplicateTypeOwnedDiagnostics(fa); len(got) != 0 {
		t.Fatalf("local Date.new collided with calendar.Date.new: %v", got)
	}
}

// The negative control at the same level: identity must not have disabled the
// check. Two type-owned functions of one name on ONE type are one ambiguous
// dispatch slot and are still rejected.
func TestInherentIdentity_TwoFunctionsOnOneTypeStillRejected(t *testing.T) {
	fa := buildInherentFixture(t, map[string]string{
		"main.nomi": `import std/calendar

struct Date {
  flavor: String
}

impl Date {
  pub fn new(a: String, b: String): Date {
    Date{flavor: a + b}
  }

  pub fn new(a: String): Date {
    Date{flavor: a}
  }
}

fn main() {
  Unit
}
`,
	})
	got := duplicateTypeOwnedDiagnostics(fa)
	if len(got) != 1 {
		t.Fatalf("got %d duplicate-type-owned diagnostics, want 1: %v", len(got), got)
	}
	if !strings.Contains(got[0], "`Date.new` is defined 2 times") {
		t.Errorf("diagnostic = %q, want it to name Date.new twice", got[0])
	}
}

// A sibling file declaring the same type-owned function on the same type is
// ONE type and still a duplicate: identity is the declaring file of the TYPE,
// not of the impl block, so moving a block to a sibling must not launder the
// collision. (This shape also draws the inherent-orphan diagnostic — an
// inherent impl must live with its receiver — which is why the assertion
// counts only the duplicate-type-owned messages.)
func TestInherentIdentity_CrossFileDuplicateOnOneTypeStillRejected(t *testing.T) {
	fa := buildInherentFixture(t, map[string]string{
		"shapes.nomi": `pub struct Date {
  flavor: String
}

impl Date {
  pub fn new(a: String): Date {
    Date{flavor: a}
  }
}
`,
		"main.nomi": `import shapes: Date

impl Date {
  pub fn new(a: String): Date {
    Date{flavor: a}
  }
}

fn main() {
  Unit
}
`,
	})
	if got := duplicateTypeOwnedDiagnostics(fa); len(got) != 1 {
		t.Fatalf("got %d duplicate-type-owned diagnostics, want 1: %v", len(got), got)
	}
}
