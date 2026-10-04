// Package ffirun_test holds the tests that need nomi/vmhost.
//
// `internal/ffirun` itself cannot import it — `nomi/analysis` imports ffirun and
// `nomi/vmhost` imports analysis — so the differential between the wrapper's
// report and the command's own report has to live in an EXTERNAL test package.
// The template's generated code CAN reach nomi/vmhost because it is compiled
// into a different program, and the package it is generated from cannot.
package ffirun_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ffirun"
	"github.com/nomi-language/nomi/vmhost"
)

// TestWrapperTestReportIsTheCommandsOwnReport is the guard for the defect that
// took `internal/irbuild`'s forty-five pinned fixtures down.
//
// # What went wrong, and why nothing here could see it
//
// The generated wrapper printed each failing case with
//
//	stdfmt.Printf("FAIL %s\n  %v\n", name, result.Err)
//
// and an *rt.AssertionFailure's Error() is only its `line N: <reason>` header.
// Everything a failing assertion explains itself with — the assertion as
// written, the `defined as:` binding, the `values:` operand rows, the pipeline
// stages, an Assertable's `details:` — is produced by rt.WriteAssertionFailure,
// which the reporter dispatches to and a Printf of the error never reaches. So
// every failing assertion in a project on the wrapper path reported a bare
// `assertion failed` where the same file on the fast path explains itself.
//
// IT WAS INVISIBLE FOR TWO INDEPENDENT REASONS, both worth stating because
// either alone would have hidden it. THE CORPUS NEVER FAILS AN ASSERTION —
// `nomi test tests` is 614 passed / 0 failed — so the differential
// sweep compares the stdout of runs in which everything passed and reads
// `0 DIFFED` over any amount of wrong failure text. And the fixtures that DO
// fail assertions all sat on the FAST path, because no file in
// `internal/irbuild/testdata` imported a first-party adapter. Adding
// `std/calendar/`, the adapter directory, made `std/calendar` first-party, nine fixtures in
// that directory import it, and one wrapper serves the whole module scope — so
// forty-five fixtures moved onto the wrapper path in one commit and the
// pre-existing defect became forty-five DIFFs. The regression was the exposure,
// not the defect.
//
// The comparison runs the wrapper's test mode (vmtest) against `nomi test`'s
// in-process path, which report through the same rt reporter.
//
// # Why this is a differential and not an absolute pin
//
// The two paths must produce the same BYTES, and neither is entitled to define
// them. An absolute pin would have to be re-typed every time the report's
// wording changes and would pass while both sides were equally wrong. So the
// same file is run both ways and the outputs are compared, with a positive
// control below so a report that lost its detail on BOTH sides cannot pass.
func TestWrapperTestReportIsTheCommandsOwnReport(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; builds a wrapper binary")
	}
	t.Setenv("NOMI_FFIRUN_CACHE_ROOT", t.TempDir())
	restoreEnv, err := vmhost.DefaultTestEnv()
	if err != nil {
		t.Fatalf("DefaultTestEnv: %v", err)
	}
	defer restoreEnv()

	root := t.TempDir()
	write(t, filepath.Join(root, "go.mod"),
		"module testproject\n\ngo 1.26.3\n\nrequire github.com/nomi-language/nomi v0.0.0\n\nreplace github.com/nomi-language/nomi => "+nomiLangRoot(t)+"\n")
	write(t, filepath.Join(root, "nomi.toml"), "[module]\nname = \"testproject\"\n")
	// A user's own `gopkg` binding is what puts this project on the wrapper
	// path, and it is declared by a SIBLING file rather than by the test file
	// — which is the shape that moved `internal/irbuild/testdata` and is
	// therefore the shape worth pinning. Nothing about the failing assertions
	// depends on it.
	//
	// It used to be `import std/regex.Regex`. That stopped working, in the
	// good direction: std/regex declares `host fn` now and its Go lives in
	// `nomi/stdregex`, so importing it selects no Go package and needs no
	// toolchain. See hostkeyword_toolchain_test.go.
	write(t, filepath.Join(root, "ffi.go"), "package testproject\n\nfunc Echo(s string) string { return s }\n")
	write(t, filepath.Join(root, "adapter.nomi"),
		"gopkg \"testproject\" as ffi\n\npub fn echo(s: String): String go ffi.Echo\n")
	// Three failing shapes, each contributing a different part of the report:
	// a bound operand, a call argument, and a `defined as:` binding.
	entry := filepath.Join(root, "report_test.nomi")
	write(t, entry, `fn no(_v: Int): Bool {
  False
}

test "a bound operand" {
  x = 7
  assert no(x)
}

test "a binary's two sides" {
  left = 2
  right = 3
  assert left == right
}

test "a subject that was defined as something" {
  total = 40 + 2
  assert no(total)
}

test "a bare binding as the subject" {
  holds = 3 > 4
  assert holds
}
`)

	res, err := ffirun.Prepare(entry)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if res == nil || res.FastPath {
		t.Fatal("the staged project did not take the wrapper path, so this test compares " +
			"the fast path against itself and cannot fail")
	}

	var wrapped bytes.Buffer
	passed, failed, blocked, err := ffirun.RunTestCapturedVM(res, entry, 0, false, &wrapped, bytes.NewReader(nil))
	if err != nil {
		t.Fatalf("RunTestCapturedVM: %v\n%s", err, wrapped.String())
	}
	if passed != 0 || failed != 4 || blocked != 0 {
		t.Fatalf("the wrapper reported %d passed / %d failed / %d blocked, want 0/4/0; the fixture "+
			"is meant to fail every case", passed, failed, blocked)
	}

	// The same file through the command's own VM path, assembled as the
	// wrapper's vmtest mode and `nomi test` do: one reporter, one name per
	// case, no summary. No case reaches the sibling's `go` binding, so the
	// program loads without its host table.
	var direct bytes.Buffer
	p, err := vmhost.Load(entry)
	if err != nil {
		t.Fatalf("vmhost.Load: %v", err)
	}
	rep := vmhost.NewTestReport(&direct)
	p.Test(&direct, rep, entry, vmhost.TestOptions{}, func(name string) string { return vmhost.TestName(entry, name) })

	// POSITIVE CONTROL. Without it, a report that dropped the operand block on
	// both paths would satisfy the comparison below and this test would say the
	// defect was fixed while it was universal.
	for _, want := range []string{"values:", "assert no(x)", "= 7", "defined as:"} {
		if !strings.Contains(direct.String(), want) {
			t.Fatalf("the command's own report does not contain %q, so the comparison below "+
				"would hold over a report with no detail in it:\n%s", want, direct.String())
		}
	}

	if wrapped.String() != direct.String() {
		t.Fatalf("the wrapper's report differs from the command's own\n"+
			"--- wrapper ---\n%s\n--- command ---\n%s", wrapped.String(), direct.String())
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// nomiLangRoot is the compiler module's directory. The staged project's go.mod
// replaces the compiler module with it, which the wrapper does not need (it adds that
// replace itself) but must tolerate.
func nomiLangRoot(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolving the compiler module root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
		t.Fatalf("%s has no go.mod, so it is not the compiler module root: %v", dir, err)
	}
	return dir
}
