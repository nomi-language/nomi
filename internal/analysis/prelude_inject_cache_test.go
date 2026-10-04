package analysis

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPreludeImports_StickyParseErrorCache pins the polish-commit
// behavior of preludeImportsCacheErr (see prelude_inject.go,
// commit 78fddfa): when prelude.nomi parses with errors, the wrapped
// error is sticky-cached keyed by stdlibPath. Subsequent calls with
// the same stdlibPath short-circuit on the cache and do NOT re-stat /
// re-lex / re-parse the file from disk.
//
// Why this matters: a long-lived LSP analyzes on every keystroke. A
// temporarily-broken std/prelude.nomi (mid-edit) would otherwise cost
// ReadFile + Lex + ParseWithRecovery on every analyze pass until the
// file is fixed — wasted work on a known-failed input.
//
// Test shape: write a malformed prelude.nomi, call preludeImports,
// assert it errors. Then OVERWRITE prelude.nomi with a syntactically
// valid file and call preludeImports again — assert the same error is
// still returned. If the sticky cache were broken (re-reading on every
// call), the second call would succeed on the now-valid file. The
// stuck error proves the cache short-circuited and the disk was not
// re-read.
//
// cache isolation: t.TempDir() yields a unique path per test, so the
// package-level preludeImportsCacheErr singleton does not interfere
// with sibling tests (their stdlibPath keys differ).
func TestPreludeImports_StickyParseErrorCache(t *testing.T) {
	stdRoot := t.TempDir()
	preludePath := filepath.Join(stdRoot, "prelude.nomi")

	// Malformed prelude: a bare `@@@` is guaranteed to lex/parse as an
	// error (no token starts with three at-signs). The exact content
	// doesn't matter as long as ParseWithRecovery returns at least one
	// parse error.
	malformed := "@@@ not valid nomi syntax\n"
	if err := os.WriteFile(preludePath, []byte(malformed), 0644); err != nil {
		t.Fatalf("write malformed prelude: %v", err)
	}

	stmts, err := preludeImports(stdRoot, nil)
	if err == nil {
		t.Fatalf("expected parse error on first call, got nil (stmts=%v)", stmts)
	}
	if stmts != nil {
		t.Errorf("expected nil stmts on parse error, got %v", stmts)
	}
	if !strings.Contains(err.Error(), "std/prelude.nomi: parse error") {
		t.Errorf("expected wrapped 'std/prelude.nomi: parse error' message, got %q", err.Error())
	}
	firstErrMsg := err.Error()

	// Overwrite with a well-formed file. An empty prelude.nomi is a
	// trivially-valid Nomi source — the parser accepts zero top-level
	// nodes. If the sticky cache is honored, preludeImports should NOT
	// observe this new content; it should keep returning the first
	// call's wrapped parse error.
	if err := os.WriteFile(preludePath, []byte(""), 0644); err != nil {
		t.Fatalf("overwrite prelude with valid content: %v", err)
	}

	stmts2, err2 := preludeImports(stdRoot, nil)
	if err2 == nil {
		t.Fatalf("sticky cache broken: second call succeeded after overwrite (re-read disk instead of using cached error); stmts=%v", stmts2)
	}
	if stmts2 != nil {
		t.Errorf("expected nil stmts on second call (cached error), got %v", stmts2)
	}
	if err2.Error() != firstErrMsg {
		t.Errorf("second call returned a different error message — cache not sticky.\n  first:  %q\n  second: %q",
			firstErrMsg, err2.Error())
	}
	// Same wrapped-error identity is the strongest signal of cache use:
	// preludeImports stores the constructed error in the map and returns
	// the cached value verbatim on subsequent calls. fmt.Errorf does NOT
	// wrap (no %w here), so the returned error pointer should compare
	// equal across calls.
	if err != err2 {
		t.Errorf("expected identical cached error pointer across calls, got distinct errors (first=%p, second=%p)", err, err2)
	}
}
