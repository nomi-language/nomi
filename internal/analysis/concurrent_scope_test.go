package analysis_test

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/std"
	"strings"
	"testing"
)

// analyzeConcurrent builds + type-checks + runs the concurrent analyzer
// passes (CheckConcurrentScope, CheckTaskLifetime) over a source string,
// returning the combined diagnostics. The helper prepends the normal
// concurrent module + Task type import so fixtures can focus on the
// concurrency rule under test.
func analyzeConcurrent(src string) []analysis.TypeError {
	src = "import std/tasks.Task\n\n" + src
	return analyzeConcurrentRaw(src)
}

func analyzeConcurrentRaw(src string) []analysis.TypeError {
	tokens := lexer.Lex(src)
	nodes, _ := parser.ParseWithRecovery(tokens)
	lib := std.Load()
	fa := analysis.BuildFileWithStdlib(nodes, lib.Primitives, lib.Modules, "", nil)
	analysis.AttachStdlibProjectImpls(fa, lib.Files)
	var errs []analysis.TypeError
	errs = append(errs, analysis.BuildTypes(fa, nodes)...)
	errs = append(errs, analysis.CheckTypes(fa, nodes)...)
	errs = append(errs, analysis.CheckConcurrentScope(fa, nodes)...)
	errs = append(errs, analysis.CheckTaskLifetime(fa, nodes)...)
	return errs
}

func expectConcurrentError(t *testing.T, errs []analysis.TypeError, substr string) {
	t.Helper()
	for _, e := range errs {
		if strings.Contains(diagText(e), substr) {
			return
		}
	}
	msgs := make([]string, len(errs))
	for i, e := range errs {
		msgs[i] = e.Error()
	}
	t.Fatalf("expected error containing %q, got %d errors:\n  %s", substr, len(errs), strings.Join(msgs, "\n  "))
}

func expectNoConcurrentError(t *testing.T, errs []analysis.TypeError, substr string) {
	t.Helper()
	for _, e := range errs {
		if strings.Contains(diagText(e), substr) {
			msgs := make([]string, len(errs))
			for i, e := range errs {
				msgs[i] = e.Error()
			}
			t.Fatalf("expected no error containing %q, got:\n  %s", substr, strings.Join(msgs, "\n  "))
		}
	}
}

func TestConcurrentScope_BareSpawnIsNotPrelude_Errors(t *testing.T) {
	src := `fn main(): Int {
  concurrent {
    t = spawn(|| 42)
    t
  }
}`
	errs := analyzeConcurrentRaw(src)
	expectConcurrentError(t, errs, "undefined variable 'spawn'")
}

func TestConcurrentScope_LocalSpawnFunctionIsNotTaskSpawn_OK(t *testing.T) {
	src := `fn spawn(n: Int): Int {
  n
}

fn main(): Int {
  spawn(42)
}`
	errs := analyzeConcurrentRaw(src)
	expectNoConcurrentError(t, errs, "spawn outside concurrent block")
}

// Rule 1 (positive case): `spawn` directly inside a `concurrent` block.
func TestConcurrentScope_SpawnInsideConcurrentBlock_OK(t *testing.T) {
	src := `fn main(): Int {
  concurrent {
    t = Task.spawn(|| 42)
    Task.await(t)
  }
}`
	errs := analyzeConcurrent(src)
	expectNoConcurrentError(t, errs, "spawn outside concurrent block")
}

// Rule 1 (negative case): bare `spawn` at fn-body top level — no
// enclosing concurrent block, no concurrent-reachable call chain.
func TestConcurrentScope_SpawnOutsideConcurrent_Errors(t *testing.T) {
	src := `fn main(): Int {
  t = Task.spawn(|| 42)
  Task.await(t)
}`
	errs := analyzeConcurrent(src)
	expectConcurrentError(t, errs, "spawn outside concurrent block")
}

// Rule 1 (transitive case): `spawn` inside a helper function that is
// only called from inside a `concurrent` block. The chain bottoms out
// inside a `concurrent` ancestor, so the call is legal.
func TestConcurrentScope_SpawnInHelperReachableFromConcurrent_OK(t *testing.T) {
	src := `fn helper(): Task<Int> {
  Task.spawn(|| 99)
}
fn main(): Int {
  concurrent {
    t = helper()
    Task.await(t)
  }
}`
	errs := analyzeConcurrent(src)
	expectNoConcurrentError(t, errs, "spawn outside concurrent block")
}

// Rule 1 (transitive negative): `spawn` inside a helper that is called
// from a non-concurrent site. Concurrency-reachability never bottoms
// out in a `concurrent` block, so the call must be rejected.
func TestConcurrentScope_SpawnInHelperWithBadCallSite_Errors(t *testing.T) {
	src := `fn helper(): Int {
  t = Task.spawn(|| 99)
  Task.await(t)
}
fn main(): Int {
  helper()
}`
	errs := analyzeConcurrent(src)
	expectConcurrentError(t, errs, "spawn outside concurrent block")
}
