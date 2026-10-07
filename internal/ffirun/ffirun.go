// Package ffirun handles discovery + wrapper codegen + cache for the
// nomi CLI's Go-backed execution path. Internal to the nomi CLI; not
// part of the public embedding API.
//
// The package decomposes the path into three stages:
//
//  1. Discovery (discovery.go): walk up to the controlling go.mod, then
//     scan Nomi source for top-level `gopkg "import/path" as alias`
//     handles and `go alias.Symbol` type/function bindings.
//  2. Codegen (codegen.go): emit a single-file main package holding a
//     VM host table of adapters generated for each discovered binding,
//     which dispatches to nomi/vmhost's Check, Load/Run or Test on the
//     entry path passed through the private wrapper-mode flag.
//  3. Cache (cache.go): persist the generated wrapper under
//     ~/.cache/nomi/builds/<project-hash>/ along with a hash record;
//     regenerate when go.mod, go.sum, the discovered set, the
//     wrapper template, or the compiler (identity.go) changes. The
//     last of those is in the key because the cached artifact is a
//     wrapper binary that statically links the front end, the IR
//     builder and the VM, not just the generated main.go.
//
// Prepare orchestrates the three stages and returns a Result the CLI
// acts on. ExecVM runs the cached wrapper binary.
//
// This file is the public surface of the package; the other files
// are implementation details kept private by Go's lowercase
// convention.
package ffirun

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Result describes what Prepare discovered and produced. The caller
// is expected to take the fast (in-process) path when FastPath is
// true, and invoke ExecVM/Check/RunTestVM when it is false.
type Result struct {
	// FastPath is true when nomi run should bypass the build path
	// entirely: either no go.mod was found above the entry, or
	// discovery found no source-level Go FFI bindings.
	FastPath bool

	// WrapperPath is the absolute path of the generated wrapper's
	// main.go. Empty when FastPath is true.
	WrapperPath string

	// WrapperDir is the directory containing the wrapper (its
	// generated go.mod / go.sum live alongside main.go). Empty when
	// FastPath is true. The `go run` command must be invoked from
	// this directory so Go's module resolution sees the wrapper's
	// own go.mod.
	WrapperDir string

	// DiscoveredPackages is the sorted list of import paths whose
	// source-level Go FFI bindings the wrapper registers. Empty when
	// FastPath is true.
	DiscoveredPackages []string

	// projectRoot, discovered and allRequires are what the wrapper was
	// generated from, kept so `nomi build` can generate the project's
	// runner from the same inputs (runner.go).
	projectRoot string
	discovered  []DiscoveredPackage
	allRequires []string
}

// GoModRoot returns the go.mod root that would control FFI discovery for
// entryPath. It is exposed so callers that process many files can cache Prepare
// results by project root instead of repeatedly loading the same Go package.
func GoModRoot(entryPath string) (string, bool) {
	absEntry, err := filepath.Abs(entryPath)
	if err != nil {
		return "", false
	}
	return findGoModRoot(filepath.Dir(absEntry))
}

// Prepare runs discovery for the project containing entryPath (walks
// up for go.mod), regenerates the wrapper if its hash inputs changed,
// and returns a Result describing what to do next.
//
// FastPath=true is returned when discovery finds no source-level Go
// bindings. A project with no go.mod still takes the wrapper path when it
// binds a Go standard library package or writes inline Go; the wrapper stages
// a synthetic Go module of its own (writeSyntheticGoMod).
//
// Errors are returned for:
//   - Cache I/O failures (mkdir, hash read/write, etc.).
//   - Codegen failures (template execution).
//
// On any error after the cache directory has been touched, Prepare
// removes the cache dir so the next attempt starts fresh — preventing
// a corrupted cached wrapper from being served on retry.
func Prepare(entryPath string) (*Result, error) {
	absEntry, err := filepath.Abs(entryPath)
	if err != nil {
		return nil, fmt.Errorf("ffirun: resolving entry path: %w", err)
	}
	// An extensionless `#!` script is rooted at its own directory, as the
	// front end roots it (frontend.Checker.PrepareFile): a nomi.toml above
	// that directory does not widen its program. The go.mod walk is not
	// bounded, since the IR builder resolves a `gopkg` through the nearest
	// go.mod above the declaring file, whatever that file is named.
	script := !isNomiSourceName(absEntry)
	projectRoot, ok := findGoModRoot(filepath.Dir(absEntry))
	if !ok {
		sourceRoot := filepath.Dir(absEntry)
		if manifestRoot, found := findNomiManifestRoot(sourceRoot); found && !script {
			sourceRoot = manifestRoot
		}
		discovered, err := discoverForEntry(sourceRoot, sourceRoot, absEntry)
		if err != nil {
			return nil, err
		}
		if len(discovered) == 0 {
			return &Result{FastPath: true}, nil
		}
		// With no go.mod, a `gopkg` can name only a Go standard library
		// package, and the wrapper is its own module (writeSyntheticGoMod).
		if err := validateDiscoveredGoBindings(sourceRoot, discovered); err != nil {
			return nil, err
		}
		cacheDir, err := cacheDirForProject(sourceRoot)
		if err != nil {
			return nil, err
		}
		if err := ensureWrapper(cacheDir, sourceRoot, discovered, nil); err != nil {
			_ = os.RemoveAll(cacheDir)
			return nil, err
		}
		return &Result{
			FastPath:           false,
			WrapperPath:        filepath.Join(cacheDir, "main.go"),
			WrapperDir:         cacheDir,
			DiscoveredPackages: discoveredImportPaths(discovered),
			projectRoot:        sourceRoot,
			discovered:         discovered,
		}, nil
	}

	sourceRoot := filepath.Dir(absEntry)
	if !script {
		sourceRoot = findNomiPackageRoot(sourceRoot, projectRoot)
	}
	discovered, err := discoverForEntry(projectRoot, sourceRoot, absEntry)
	if err != nil {
		return nil, err
	}
	if len(discovered) == 0 {
		// go.mod exists but no source-level Go FFI bindings were found
		// → still fast path. Pure-Nomi
		// multi-package projects ride this branch.
		return &Result{FastPath: true}, nil
	}
	if err := validateDiscoveredGoBindings(projectRoot, discovered); err != nil {
		return nil, err
	}

	// Snapshot the project's full direct-require list, which is a cache
	// key input. listDirectRequires errors are non-fatal: go.mod and
	// go.sum are keyed on their own.
	allRequires, _ := listDirectRequires(projectRoot)

	cacheDir, err := cacheDirForProject(projectRoot)
	if err != nil {
		return nil, err
	}
	if err := ensureWrapper(cacheDir, projectRoot, discovered, allRequires); err != nil {
		// Wipe the cache so the next attempt starts fresh rather
		// than serving a half-written wrapper.
		_ = os.RemoveAll(cacheDir)
		return nil, err
	}

	return &Result{
		FastPath:           false,
		WrapperPath:        filepath.Join(cacheDir, "main.go"),
		WrapperDir:         cacheDir,
		DiscoveredPackages: discoveredImportPaths(discovered),
		projectRoot:        projectRoot,
		discovered:         discovered,
		allRequires:        allRequires,
	}, nil
}

func discoveredImportPaths(discovered []DiscoveredPackage) []string {
	out := make([]string, len(discovered))
	for i, d := range discovered {
		out[i] = d.ImportPath
	}
	return out
}

const wrapperModeFlag = "--nomi-wrapper-mode"

// RunCapturedVM is ExecVM with the child's streams supplied by the caller and
// its exit code returned rather than adopted, so a golden recorder or a test
// observes what `nomi run` would print without becoming it. stdin is a
// parameter because an unattended run must not inherit a terminal.
func RunCapturedVM(r *Result, entryAbsPath string, stdout, stderr io.Writer, stdin io.Reader, args ...string) (int, error) {
	return runCapturedMode(r, "vmrun", entryAbsPath, stdout, stderr, stdin, args...)
}

// CommandVM is the unstarted command `nomi run` launches for entryAbsPath:
// the wrapper binary, built if it is not cached, in run mode. The caller
// supplies its streams and may add to its environment (a later entry wins)
// before starting it. A recorder that has to stop a program that serves until
// killed starts it this way, so it holds the process.
func CommandVM(r *Result, entryAbsPath string, args ...string) (*exec.Cmd, error) {
	return commandMode(r, "vmrun", entryAbsPath, args...)
}

func commandMode(r *Result, mode, entryAbsPath string, args ...string) (*exec.Cmd, error) {
	if r == nil || r.FastPath {
		return nil, fmt.Errorf("ffirun: a wrapper command for a FastPath result")
	}
	wrapperBin, err := ensureCachedWrapperBinary(r)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(wrapperBin, append([]string{wrapperModeFlag, mode, entryAbsPath}, args...)...)
	// Start in the wrapper dir, matching the old go-run launch shape. The
	// wrapper restores the user's cwd at startup via NOMI_FFIRUN_USER_CWD before
	// any user code runs — see the chdir block in the wrapper template.
	cmd.Dir = r.WrapperDir
	userCwd, _ := os.Getwd()
	cmd.Env = append(os.Environ(), "NOMI_FFIRUN_USER_CWD="+userCwd)
	return cmd, nil
}

func runCapturedMode(r *Result, mode, entryAbsPath string, stdout, stderr io.Writer, stdin io.Reader, args ...string) (int, error) {
	if r == nil || r.FastPath {
		return 0, fmt.Errorf("ffirun: RunCaptured called on a FastPath result")
	}
	cmd, err := commandMode(r, mode, entryAbsPath, args...)
	if err != nil {
		return 0, err
	}
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err = runForwardingSignals(cmd)
	if err == nil {
		return 0, nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode(), nil
	}
	return 0, fmt.Errorf("ffirun: launching wrapper: %w", err)
}

// Check runs the generated wrapper in check mode: the front end over the entry
// (vmhost.Check) with the project's generated host table, and nothing run.
// Building the wrapper is part of the check: it compiles the generated
// adapters against the project's Go.
func Check(r *Result, entryAbsPath string) error {
	if r == nil || r.FastPath {
		return fmt.Errorf("ffirun: Check called on a FastPath result")
	}
	wrapperBin, err := ensureCachedWrapperBinary(r)
	if err != nil {
		return err
	}
	cmd := exec.Command(wrapperBin, wrapperModeFlag, "check", entryAbsPath)
	cmd.Dir = r.WrapperDir
	userCwd, _ := os.Getwd()
	cmd.Env = append(os.Environ(), "NOMI_FFIRUN_USER_CWD="+userCwd)
	cmd.Stdin = os.Stdin
	out, err := cmd.CombinedOutput()
	if err != nil {
		if len(out) > 0 {
			if cleaned := strings.TrimRight(cleanWrapperCommandOutput(r, string(out)), "\r\n"); cleaned != "" {
				return wrapperReport(cleaned)
			}
		}
		return fmt.Errorf("ffirun: checking wrapper: %w", err)
	}
	return nil
}

const ffiTestResultMarker = "__NOMI_FFI_TEST_RESULT"

// RunTestVM runs one test file's cases through the generated FFI wrapper,
// reporting in `nomi test --format`'s format ("text" or "json"), and answers
// the passed, failed and blocked counts. The wrapper prints per-case output;
// this function strips the private count marker before forwarding it.
//
// In json mode the wrapper's stdout carries only records, so its stderr —
// where a test's own output goes in that mode — is forwarded to this
// process's stderr instead of being merged into the stream.
func RunTestVM(r *Result, testAbsPath string, line int, lineSet bool, format string) (passed, failed, blocked int, err error) {
	return runTestMode(r, "vmtest", testAbsPath, line, lineSet, format, os.Stdout, os.Stderr, os.Stdin)
}

// RunTestCapturedVM is RunTestCaptured on the VM.
func RunTestCapturedVM(r *Result, testAbsPath string, line int, lineSet bool, stdout io.Writer, stdin io.Reader) (passed, failed, blocked int, err error) {
	return runTestMode(r, "vmtest", testAbsPath, line, lineSet, "text", stdout, os.Stderr, stdin)
}

// RunTestVMWith is RunTestVM with the streams supplied by the caller.
func RunTestVMWith(r *Result, testAbsPath string, line int, lineSet bool, format string, stdout, stderr io.Writer, stdin io.Reader) (passed, failed, blocked int, err error) {
	return runTestMode(r, "vmtest", testAbsPath, line, lineSet, format, stdout, stderr, stdin)
}

func runTestMode(r *Result, mode, testAbsPath string, line int, lineSet bool, format string, stdout, stderr io.Writer, stdin io.Reader) (int, int, int, error) {
	if r == nil || r.FastPath {
		return 0, 0, 0, fmt.Errorf("ffirun: RunTest called on a FastPath result")
	}
	wrapperBin, err := ensureCachedWrapperBinary(r)
	if err != nil {
		return 0, 0, 0, err
	}
	args := []string{wrapperModeFlag, mode, testAbsPath}
	json := format == "json"
	switch {
	case json && lineSet:
		args = append(args, strconv.Itoa(line), format)
	case json:
		args = append(args, "", format)
	case lineSet:
		args = append(args, strconv.Itoa(line))
	}
	cmd := exec.Command(wrapperBin, args...)
	cmd.Dir = r.WrapperDir
	userCwd, _ := os.Getwd()
	cmd.Env = append(os.Environ(), "NOMI_FFIRUN_USER_CWD="+userCwd)
	cmd.Stdin = stdin
	var outBuf, errOut bytes.Buffer
	cmd.Stdout = &outBuf
	if json {
		cmd.Stderr = &errOut
	} else {
		cmd.Stderr = &outBuf
	}
	err = runForwardingSignals(cmd)
	if errOut.Len() > 0 {
		_, _ = stderr.Write(errOut.Bytes())
	}
	out := outBuf.Bytes()
	passed, failed, blocked, visible, ok := parseTestOutput(string(out))
	if visible != "" {
		fmt.Fprint(stdout, visible)
	}
	if !ok {
		if err != nil {
			return 0, 0, 0, fmt.Errorf("ffirun: launching test wrapper: %w\n%s", err, cleanWrapperCommandOutput(r, string(out)))
		}
		return 0, 0, 0, fmt.Errorf("ffirun: test wrapper did not report results")
	}
	// A non-zero child exit is expected when tests fail or are blocked. The
	// parsed counts are enough for the caller to report the aggregate result.
	if err != nil && failed == 0 && blocked == 0 {
		return passed, failed, blocked, fmt.Errorf("ffirun: test wrapper exited unexpectedly: %w", err)
	}
	return passed, failed, blocked, nil
}

// parseTestOutput strips the wrapper's count marker,
// `<marker> passed failed blocked`.
func parseTestOutput(out string) (passed, failed, blocked int, visible string, ok bool) {
	var visibleLines []string
	lines := strings.SplitAfter(out, "\n")
	for _, line := range lines {
		trimmed := strings.TrimRight(line, "\r\n")
		if strings.HasPrefix(trimmed, ffiTestResultMarker+" ") {
			fields := strings.Fields(trimmed)
			if len(fields) == 4 {
				p, pErr := strconv.Atoi(fields[1])
				f, fErr := strconv.Atoi(fields[2])
				b, bErr := strconv.Atoi(fields[3])
				if pErr == nil && fErr == nil && bErr == nil {
					passed, failed, blocked, ok = p, f, b, true
					continue
				}
			}
		}
		visibleLines = append(visibleLines, line)
	}
	return passed, failed, blocked, strings.Join(visibleLines, ""), ok
}

// findGoModRoot walks up from startDir until it finds a directory
// containing a go.mod file, or runs out of parents. Returns the
// directory containing go.mod and true on success; ("", false) when
// none is found.
//
// Keys on go.mod (the FFI-build trigger) rather than nomi.toml (the
// Nomi-package marker). The two markers usually coexist for FFI
// projects, but go.mod is the load-bearing
// signal: pure-Nomi modules have nomi.toml without go.mod and
// belong on the fast path.
func findGoModRoot(startDir string) (string, bool) {
	dir := startDir
	for {
		if info, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil && !info.IsDir() {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}
