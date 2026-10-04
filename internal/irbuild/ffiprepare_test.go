package irbuild

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ffirun"
)

// TestIRNoCorpusFileFailsFFIPreparation checks the premise ffiPrepare's
// refusal rests on: no corpus file fails ffirun.Prepare.
//
// ffiPrepare reports a preparation error rather than treating the file as one
// that needs no wrapper. Discovery walks up to a go.mod and reads every
// `.nomi` file in scope, so it could fail for reasons unrelated to the file
// being compared; if that happened for a corpus file, the refusal would break
// the sweep. When a corpus file starts failing preparation legitimately, this
// names the file and the reason, so the refusal can be narrowed deliberately.
//
// It shares the package's one corpus enumeration (corpusAnalysis), so it adds
// no walk. Prepare runs discovery, codegen and cache I/O and does not build
// the wrapper binary (ensureCachedWrapperBinary, reached from RunCaptured), so
// this costs milliseconds per file.
func TestIRNoCorpusFileFailsFFIPreparation(t *testing.T) {
	_, files := corpusAnalysis(t)

	var fastPath, wrapper int
	var failures []string
	for _, f := range files {
		res, err := ffirun.Prepare(f.Path)
		switch {
		case err != nil:
			failures = append(failures, f.Rel+": "+err.Error())
		case res == nil:
			failures = append(failures, f.Rel+": Prepare returned (nil, nil)")
		case res.FastPath:
			fastPath++
		default:
			wrapper++
		}
	}
	sort.Strings(failures)

	t.Logf("%d corpus files: %d FastPath, %d wrapper, %d preparation failures",
		len(files), fastPath, wrapper, len(failures))

	// The wrapper count is asserted as a floor rather than left implicit,
	// because zero failures is also what a run that prepared nothing would
	// report. The fifteen files of the five `gopkg` projects under 18-ffi-and-dynamic/ are
	// the corpus's only coverage of the wrapper path; a reading of 0 here means
	// the measurement is vacuous, not that preparation is healthy.
	if wrapper == 0 {
		t.Errorf("0 corpus files took the wrapper path, so this test cannot see a " +
			"preparation failure at all. Expect one per file in the five `gopkg` projects " +
			"under tests/18-ffi-and-dynamic/*_app/; check NOMI_FFIRUN_CACHE_ROOT " +
			"is set (TestMain does it) and that discovery still runs")
	}
	if len(failures) != 0 {
		t.Errorf("%d corpus file(s) fail ffirun.Prepare:\n  %s\n"+
			"ffiPrepare in differential_test.go refuses to fall back on a preparation error, "+
			"which assumes this set is empty. If one of these is legitimate (a project that "+
			"cannot prepare and whose file should still be compared through the "+
			"in-process path), ffiPrepare's refusal has to narrow to exclude it, and "+
			"this test has to record which and why.",
			len(failures), strings.Join(failures, "\n  "))
	}
}

// TestIRFFIPreparationFailureIsReported is the planted positive for
// ffiPrepare's refusal, and it asserts both halves.
//
// "ffiPrepare returns an error" alone would pass for a function that errored
// on every call, so the control runs first: the same fixture, the cache root
// TestMain set, a real wrapper and no error.
//
// The plant then points NOMI_FFIRUN_CACHE_ROOT at a regular file so MkdirAll
// cannot succeed. That is the same error return as internal/ffirun's refusal
// of the default cache root under `go test`, without depending on that
// refusal's wording, so the plant fires for any unusable cache root.
//
// It requires that the harness reports the failure rather than running the
// file unwrapped. An unwrapped run loads a program whose externs nobody
// registered and prints a well-formed `nomi test` report with a FAIL line and
// a summary, exit 1, naming no cause, and vmReference would hand that to the
// caller as the program's answer. So the stdout assertion is the one that
// matters; stderr naming the failure is necessary and not sufficient.
func TestIRFFIPreparationFailureIsReported(t *testing.T) {
	// Two files of ONE project, named directly rather than enumerated: one
	// project is not a population, so this adds no corpus walk (see
	// TestCorpusOnlyHasOneEnumerationSite). Both are needed because the VM
	// command forks on whether a file declares tests: `main_test.nomi` runs as
	// `nomi test`, `main.nomi` as `nomi run`.
	project := filepath.Join("..", "..", "tests", "18-ffi-and-dynamic", "callback_ffi_app")
	testFile, err := filepath.Abs(filepath.Join(project, "main_test.nomi"))
	if err != nil {
		t.Fatalf("resolving the FFI test fixture: %v", err)
	}
	runFile, err := filepath.Abs(filepath.Join(project, "main.nomi"))
	if err != nil {
		t.Fatalf("resolving the FFI run fixture: %v", err)
	}

	for _, path := range []string{testFile, runFile} {
		res, err := ffiPrepare(path)
		if err != nil {
			t.Fatalf("the control failed: ffiPrepare(%s) = %v with the cache root TestMain "+
				"set, so the plant below cannot distinguish a real failure from a broken "+
				"harness", filepath.Base(path), err)
		}
		if res == nil {
			t.Fatalf("the control reported no wrapper for %s, so this fixture is not a `gopkg` "+
				"project and the plant cannot fire; repoint it at one",
				filepath.Base(path))
		}
	}
	if leftover := takeFFIPrepareFailures(); len(leftover) != 0 {
		t.Fatalf("a preparation failure was already recorded before this test planted one, "+
			"so it belongs to another test in this run and is reported here rather than "+
			"drained silently:\n  %s", strings.Join(leftover, "\n  "))
	}

	// The plant: a cache root that is a regular file.
	notADir := filepath.Join(t.TempDir(), "cache-root-is-a-file")
	if err := os.WriteFile(notADir, []byte("not a directory\n"), 0o644); err != nil {
		t.Fatalf("staging the unusable cache root: %v", err)
	}
	t.Setenv("NOMI_FFIRUN_CACHE_ROOT", notADir)

	if got, err := ffiPrepare(testFile); err == nil {
		t.Fatalf("ffiPrepare reported no error for an unusable cache root and returned %v; "+
			"the preparation failure is folded into the no-wrapper branch", got)
	}

	for _, path := range []string{testFile, runFile} {
		obs := vmReference(path)
		if !strings.Contains(obs.stderr, "FFI wrapper preparation failed") {
			t.Errorf("%s: the reference observation does not name the preparation failure:\n%s",
				filepath.Base(path), obs)
		}
		if obs.exit != 1 {
			t.Errorf("%s: the reference observation exited %d, want 1: a harness that could "+
				"not prepare a wrapper has not produced a successful run",
				filepath.Base(path), obs.exit)
		}
		if strings.Contains(obs.stdout, "not registered") || strings.Contains(obs.stdout, "FAIL ") {
			t.Errorf("%s: the harness ran the file unwrapped and handed the unwrapped run's own "+
				"unregistered-externs report to the sweep as the reference transcript, a "+
				"plausible wrong answer rather than a failure:\n%s",
				filepath.Base(path), obs.stdout)
		}
		if strings.Contains(obs.stderr, "not registered") {
			t.Errorf("%s: the harness ran the file unwrapped; the unwrapped run's "+
				"unregistered-externs error is the reference stderr:\n%s",
				filepath.Base(path), obs.stderr)
		}

		failures := takeFFIPrepareFailures()
		if len(failures) != 1 {
			t.Fatalf("%s: %d preparation failure(s) recorded, want exactly 1. TestMain's "+
				"ffiPrepareViolation is what fails the RUN when a comparison somewhere "+
				"swallowed one, and it reads this record:\n  %s",
				filepath.Base(path), len(failures), strings.Join(failures, "\n  "))
		}
		if !strings.Contains(failures[0], filepath.Base(path)) {
			t.Errorf("the recorded failure does not name the file it happened to: %q", failures[0])
		}
	}
}
