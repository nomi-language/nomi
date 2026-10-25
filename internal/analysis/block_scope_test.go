package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// blockForms are the places a block opens a scope. Each holds BODY where
// the block's statements go.
var blockForms = []struct {
	name string
	form string
}{
	{"if arm", "if flag {\nBODY\n}"},
	{"else arm", "if flag {\nUnit\n} else {\nBODY\n}"},
	{"else if arm", "if flag {\nUnit\n} else if total > 0 {\nBODY\n} else {\nUnit\n}"},
	{"case arm", "case flag {\nTrue -> {\nBODY\n}\nFalse -> Unit\n}"},
	{"bare block", "{\nBODY\n}"},
	{"nested block", "{\n{\nBODY\n}\n}"},
	{"concurrent", "concurrent {\nBODY\n}"},
	{"lambda body", "f = fn -> {\nBODY\n}\nf()"},
	{"block value", "v = {\nBODY\n}\n_ = v"},
}

// blockScopeProgram puts form, with body in it, between an outer `total`
// binding and a read of it after the form. The returned line is body's
// first line.
func blockScopeProgram(form, body string) (string, int) {
	head := "fn main() {\nflag = True\ntotal = 1\n"
	before, after, _ := strings.Cut(form, "BODY")
	src := head + before + body + after + "\n_ = total\n_ = flag\n}\n"
	return src, strings.Count(head+before, "\n") + 1
}

func expectNeverReadAt(t *testing.T, errs []analysis.TypeError, name string, line int) {
	t.Helper()
	want := "binding '" + name + "' is never read"
	var found bool
	for _, e := range unusedBindingErrors(errs) {
		if e.Line != line {
			t.Errorf("unexpected unused-binding error at line %d: %s", e.Line, diagText(e))
			continue
		}
		if !strings.Contains(e.Message, want) {
			t.Errorf("line %d: got %q, want %q", line, e.Message, want)
		}
		if !strings.Contains(diagText(e), "prefix it with '_' if the value is intentionally ignored") {
			t.Errorf("line %d: missing the '_' hint: %s", line, diagText(e))
		}
		found = true
	}
	if !found {
		msgs := make([]string, len(errs))
		for i, e := range errs {
			msgs[i] = e.Error()
		}
		t.Fatalf("the front end admits an unread %q at line %d; got %d errors:\n  %s",
			name, line, len(errs), strings.Join(msgs, "\n  "))
	}
}

// A binding inside a block is scoped to that block, so one that shadows an
// outer name and that nothing in the block reads is never read, whatever
// the block. The read after the block reads the outer binding.
func TestBlockScope_ShadowNothingReadsIsNeverRead(t *testing.T) {
	for _, f := range blockForms {
		t.Run(f.name, func(t *testing.T) {
			src, line := blockScopeProgram(f.form, "total = total + 1")
			expectNeverReadAt(t, buildProjectForDefaultConfig(src), "total", line)
		})
	}
}

// The same binding read inside its block is fine.
func TestBlockScope_ShadowReadInItsBlockIsAccepted(t *testing.T) {
	for _, f := range blockForms {
		t.Run(f.name, func(t *testing.T) {
			src, _ := blockScopeProgram(f.form, "total = total + 1\n_ = total")
			expectNoErrorsT(t, buildProjectForDefaultConfig(src))
		})
	}
}

// A `_` binding as a block's last statement is accepted, and the block's
// value is Unit.
func TestBlockScope_DiscardBindingLastIsAccepted(t *testing.T) {
	for _, f := range blockForms {
		t.Run(f.name, func(t *testing.T) {
			src, _ := blockScopeProgram(f.form, "_total = total + 1")
			expectNoErrorsT(t, buildProjectForDefaultConfig(src))
		})
	}
}

// A name bound inside a block is not in scope after it.
func TestBlockScope_BindingDoesNotLeak(t *testing.T) {
	for _, f := range blockForms {
		if f.name == "lambda body" || f.name == "block value" {
			continue
		}
		t.Run(f.name, func(t *testing.T) {
			src, _ := blockScopeProgram(f.form, "inner = total + 1\n_ = inner")
			src = strings.Replace(src, "_ = total\n", "_ = inner\n", 1)
			errs := buildProjectForDefaultConfig(src)
			expectErrorT(t, errs, "undefined variable 'inner'")
		})
	}
}

// A block whose value is read must not end in a binding: its value is Unit.
func TestBlockScope_BlockEndingInABindingIsUnit(t *testing.T) {
	errs := buildProjectForDefaultConfig(`
fn main() {
  v = if True {
    _x = 1
  } else {
    2
  }
  _ = v
}
`)
	expectErrorT(t, errs, "if/else branch type mismatch: then is Unit, else is Int")
}

// The other bodies that hold statements: a test body, the `else` of a
// refutable binding, and a block after a `with` line.
func TestBlockScope_OtherBodies(t *testing.T) {
	t.Run("test body", func(t *testing.T) {
		expectNeverReadAt(t, buildProjectForDefaultConfig(`
test "t" {
  total = 1
  assert total == 1
  total = total + 1
}
`), "total", 5)
	})
	t.Run("refutable binding else", func(t *testing.T) {
		expectNeverReadAt(t, buildProjectForDefaultConfig(`
fn pick(m: Maybe<Int>) -> Int {
  total = 1
  Some(n) = m else {
    total = total + 1
    return 0
  }
  n + total
}
fn main() {
  _ = pick(None)
}
`), "total", 5)
	})
	t.Run("with", func(t *testing.T) {
		expectNeverReadAt(t, buildProjectForDefaultConfig(`
struct App { port: Int }
fn boot() -> Result<App, String> { Ok(#App{port: 3}) }
fn main() {
  total = App.port
  if True {
    with App.port = 4
    total = total + App.port
  }
  _ = total
}
`), "total", 8)
	})
}
