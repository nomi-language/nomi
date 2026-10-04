package ffirun

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestFFIRun_RoundTrip is the end-to-end integration test for source-declared
// Go FFI: stages a temp project with an `echo_upper` binding, lets Prepare
// generate the wrapper, then `go run`s the wrapper directly (NOT via
// ffirun.ExecVM, which calls os.Exit and would kill the test process).
// Verifies that the captured stdout is the upper-cased input — proving that
// the wrapper successfully:
//
//  1. loaded the entry main.nomi on the VM with its host table,
//  2. crossed into the Go-side function through its generated adapter,
//  3. round-tripped the String through the adapter.
func TestFFIRun_RoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test invokes go run; skipped in -short mode")
	}
	t.Setenv("NOMI_FFIRUN_CACHE_ROOT", t.TempDir())

	projectRoot := t.TempDir()
	nomiRoot := findNomiLangRoot(t)

	// binding lives at <projectRoot>/echobinding/. echobinding.go
	// declares a normal exported `EchoUpper(s) -> upper(s)` impl.
	bindingDir := filepath.Join(projectRoot, "echobinding")
	if err := os.MkdirAll(bindingDir, 0o755); err != nil {
		t.Fatalf("mkdir echobinding: %v", err)
	}
	bindingSrc := `package echobinding

import "strings"

func EchoUpper(s string) string {
	return strings.ToUpper(s)
}
`
	if err := os.WriteFile(filepath.Join(bindingDir, "echo.go"), []byte(bindingSrc), 0o644); err != nil {
		t.Fatalf("write echobinding/echo.go: %v", err)
	}
	bindingGoMod := `module echobinding

go 1.26.3
`
	if err := os.WriteFile(filepath.Join(bindingDir, "go.mod"), []byte(bindingGoMod), 0o644); err != nil {
		t.Fatalf("write echobinding/go.mod: %v", err)
	}

	// Project go.mod requires both the binding (relative replace —
	// exercises writeAbsolutizedGoMod's rewrite step) and nomi (so
	// the wrapper's `import "github.com/nomi-language/nomi/vmhost"` resolves).
	projectGoMod := fmt.Sprintf(`module testproject

go 1.26.3

require echobinding v0.0.0

require github.com/nomi-language/nomi v0.0.0

replace echobinding => ./echobinding

replace github.com/nomi-language/nomi => %s
`, nomiRoot)
	if err := os.WriteFile(filepath.Join(projectRoot, "go.mod"), []byte(projectGoMod), 0o644); err != nil {
		t.Fatalf("write project go.mod: %v", err)
	}
	mustWriteHelper(t, filepath.Join(projectRoot, "nomi.toml"), `[module]
name = "testproject"
entry_points = ["main"]

`)

	entrySrc := `import {
  std/io
}

gopkg "echobinding" as ffi

fn echo_upper(s: String): String go ffi.EchoUpper

fn main() {
  io.print(echo_upper("hello"))
}
	`
	entryPath := filepath.Join(projectRoot, "main.nomi")
	if err := os.WriteFile(entryPath, []byte(entrySrc), 0o644); err != nil {
		t.Fatalf("write main.nomi: %v", err)
	}

	res, err := Prepare(entryPath)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if res.FastPath {
		t.Fatal("expected FastPath=false (echobinding has source-declared binding)")
	}
	if len(res.DiscoveredPackages) != 1 || res.DiscoveredPackages[0] != "echobinding" {
		t.Fatalf("DiscoveredPackages: got %v, want [echobinding]", res.DiscoveredPackages)
	}
	if res.WrapperPath == "" || res.WrapperDir == "" {
		t.Fatalf("Result missing wrapper paths: %+v", res)
	}
	if _, err := os.Stat(res.WrapperPath); err != nil {
		t.Fatalf("stat wrapper: %v", err)
	}

	// Run the wrapper directly. We bypass ffirun.Exec because it
	// calls os.Exit; using exec.Command lets the test capture stdout
	// and outlive the child.
	//
	// Mirror what Exec does in production: cmd.Dir = WrapperDir so
	// Go's module resolution finds the wrapper's go.mod, AND
	// NOMI_FFIRUN_USER_CWD is set so the wrapper restores the user's
	// cwd before user code runs. For the roundtrip test the cwd
	// doesn't matter content-wise; we just want to exercise the
	// same launch path Exec uses.
	cmd := exec.Command("go", "run", "-mod=mod", res.WrapperPath, wrapperModeFlag, "vmrun", entryPath)
	cmd.Dir = res.WrapperDir
	userCwd, _ := os.Getwd()
	cmd.Env = append(os.Environ(), "NOMI_FFIRUN_USER_CWD="+userCwd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go run wrapper: %v\nstdout:\n%s\nstderr:\n%s",
			err, stdout.String(), stderr.String())
	}
	got := strings.TrimSpace(stdout.String())
	if got != "HELLO" {
		t.Errorf("expected HELLO, got %q\nstdout:\n%s\nstderr:\n%s",
			got, stdout.String(), stderr.String())
	}
}

func TestFFIRun_SourceBindingRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test invokes go run; skipped in -short mode")
	}
	t.Setenv("NOMI_FFIRUN_CACHE_ROOT", t.TempDir())

	projectRoot := t.TempDir()
	nomiRoot := findNomiLangRoot(t)

	bindingDir := filepath.Join(projectRoot, "taggedbinding")
	if err := os.MkdirAll(bindingDir, 0o755); err != nil {
		t.Fatalf("mkdir taggedbinding: %v", err)
	}
	bindingSrc := `package taggedbinding

type Box struct {
	Label string
}

func MakeBox(label string) *Box {
	return &Box{Label: label}
}

func BoxLabel(box *Box) string {
	return box.Label
}
`
	mustWriteHelper(t, filepath.Join(bindingDir, "echo.go"), bindingSrc)
	mustWriteHelper(t, filepath.Join(bindingDir, "go.mod"), `module taggedbinding

go 1.26.3
`)

	mustWriteHelper(t, filepath.Join(projectRoot, "go.mod"), fmt.Sprintf(`module taggedtest

go 1.26.3

require taggedbinding v0.0.0

require github.com/nomi-language/nomi v0.0.0

replace taggedbinding => ./taggedbinding

replace github.com/nomi-language/nomi => %s
`, nomiRoot))
	mustWriteHelper(t, filepath.Join(projectRoot, "nomi.toml"), `[module]
name = "taggedtest"
entry_points = ["main"]

`)

	entryPath := filepath.Join(projectRoot, "main.nomi")
	mustWriteHelper(t, entryPath, `import {
  std/io
}

gopkg "taggedbinding" as box

opaque type RawBox go box.Box

fn make_box(label: String): RawBox go box.MakeBox

fn box_label(raw: RawBox): String go box.BoxLabel

fn main() {
  io.print(box_label(make_box("hello")))
}
`)

	res, err := Prepare(entryPath)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if res.FastPath {
		t.Fatal("expected FastPath=false (source binding discovered)")
	}
	if len(res.DiscoveredPackages) != 1 || res.DiscoveredPackages[0] != "taggedbinding" {
		t.Fatalf("DiscoveredPackages: got %v, want [taggedbinding]", res.DiscoveredPackages)
	}

	cmd := exec.Command("go", "run", "-mod=mod", res.WrapperPath, wrapperModeFlag, "vmrun", entryPath)
	cmd.Dir = res.WrapperDir
	userCwd, _ := os.Getwd()
	cmd.Env = append(os.Environ(), "NOMI_FFIRUN_USER_CWD="+userCwd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go run wrapper: %v\nstdout:\n%s\nstderr:\n%s",
			err, stdout.String(), stderr.String())
	}
	got := strings.TrimSpace(stdout.String())
	if got != "hello" {
		t.Errorf("expected hello, got %q\nstdout:\n%s\nstderr:\n%s",
			got, stdout.String(), stderr.String())
	}
}

// TestFFIRun_TupleReturnRoundTrip proves the FFI-routed path honours the
// multi-return projection: N >= 2 non-error Go returns arrive as a Nomi
// tuple, and a trailing error wraps that tuple in Result<(A, B), String>.
// The reshaping lives in the generated adapter, so this test is the check
// that nothing in discovery/validate/codegen rejects the shape before it
// gets there.
func TestFFIRun_TupleReturnRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test invokes go run; skipped in -short mode")
	}
	t.Setenv("NOMI_FFIRUN_CACHE_ROOT", t.TempDir())

	projectRoot := t.TempDir()
	nomiRoot := findNomiLangRoot(t)

	bindingDir := filepath.Join(projectRoot, "taggedbinding")
	if err := os.MkdirAll(bindingDir, 0o755); err != nil {
		t.Fatalf("mkdir taggedbinding: %v", err)
	}
	mustWriteHelper(t, filepath.Join(bindingDir, "tuple.go"), `package taggedbinding

import (
	"fmt"
	"strings"
)

func DivMod(a, b int64) (int64, int64) {
	return a / b, a % b
}

func SplitOnce(s, sep string) (string, string, error) {
	before, after, found := strings.Cut(s, sep)
	if !found {
		return "", "", fmt.Errorf("separator %q not found", sep)
	}
	return before, after, nil
}
`)
	mustWriteHelper(t, filepath.Join(bindingDir, "go.mod"), `module taggedbinding

go 1.26.3
`)

	mustWriteHelper(t, filepath.Join(projectRoot, "go.mod"), fmt.Sprintf(`module taggedtest

go 1.26.3

require taggedbinding v0.0.0

require github.com/nomi-language/nomi v0.0.0

replace taggedbinding => ./taggedbinding

replace github.com/nomi-language/nomi => %s
`, nomiRoot))
	mustWriteHelper(t, filepath.Join(projectRoot, "nomi.toml"), `[module]
name = "taggedtest"
entry_points = ["main"]

`)

	entryPath := filepath.Join(projectRoot, "main.nomi")
	mustWriteHelper(t, entryPath, `import {
  std/io
}

gopkg "taggedbinding" as tup

fn divmod(a: Int, b: Int): (Int, Int) go tup.DivMod

fn split_once(s: String, sep: String): Result<(String, String), String> go tup.SplitOnce

fn main() {
  (q, r) = divmod(17, 5)
  io.print("${q},${r}")
  case split_once("a=b", "=") {
    Ok(pair) -> io.print("${pair.0}|${pair.1}")
    Err(msg) -> io.print("err: ${msg}")
  }
  case split_once("ab", "=") {
    Ok(pair) -> io.print("${pair.0}|${pair.1}")
    Err(msg) -> io.print("err: ${msg}")
  }
}
`)

	res, err := Prepare(entryPath)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	cmd := exec.Command("go", "run", "-mod=mod", res.WrapperPath, wrapperModeFlag, "vmrun", entryPath)
	cmd.Dir = res.WrapperDir
	userCwd, _ := os.Getwd()
	cmd.Env = append(os.Environ(), "NOMI_FFIRUN_USER_CWD="+userCwd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go run wrapper: %v\nstdout:\n%s\nstderr:\n%s",
			err, stdout.String(), stderr.String())
	}
	got := strings.TrimSpace(stdout.String())
	want := "3,2\na|b\nerr: separator \"=\" not found"
	if got != want {
		t.Errorf("tuple round-trip output mismatch:\ngot:\n%s\nwant:\n%s\nstderr:\n%s",
			got, want, stderr.String())
	}
}

func TestFFIRun_ProjectLocalSourceBindingRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test invokes go run; skipped in -short mode")
	}
	t.Setenv("NOMI_FFIRUN_CACHE_ROOT", t.TempDir())

	projectRoot := t.TempDir()
	nomiRoot := findNomiLangRoot(t)

	mustWriteHelper(t, filepath.Join(projectRoot, "go.mod"), fmt.Sprintf(`module localbindingtest

go 1.26.3

require github.com/nomi-language/nomi v0.0.0

replace github.com/nomi-language/nomi => %s
`, nomiRoot))
	mustWriteHelper(t, filepath.Join(projectRoot, "nomi.toml"), `[module]
name = "localbindingtest"
entry_points = ["main"]

`)
	mustWriteHelper(t, filepath.Join(projectRoot, "binding.go"), `package localbindingtest

import "strings"

func EchoUpper(s string) string {
	return strings.ToUpper(s)
}
`)
	entryPath := filepath.Join(projectRoot, "main.nomi")
	mustWriteHelper(t, entryPath, `import {
  std/io
}

gopkg "localbindingtest" as ffi

fn echo_upper(s: String): String go ffi.EchoUpper

fn main() {
  io.print(echo_upper("hello"))
}
`)

	res, err := Prepare(entryPath)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if res.FastPath {
		t.Fatal("expected FastPath=false (project-local source binding discovered)")
	}
	if len(res.DiscoveredPackages) != 1 || res.DiscoveredPackages[0] != "localbindingtest" {
		t.Fatalf("DiscoveredPackages: got %v, want [localbindingtest]", res.DiscoveredPackages)
	}

	cmd := exec.Command("go", "run", "-mod=mod", res.WrapperPath, wrapperModeFlag, "vmrun", entryPath)
	cmd.Dir = res.WrapperDir
	userCwd, _ := os.Getwd()
	cmd.Env = append(os.Environ(), "NOMI_FFIRUN_USER_CWD="+userCwd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go run wrapper: %v\nstdout:\n%s\nstderr:\n%s",
			err, stdout.String(), stderr.String())
	}
	got := strings.TrimSpace(stdout.String())
	if got != "HELLO" {
		t.Errorf("expected HELLO, got %q\nstdout:\n%s\nstderr:\n%s",
			got, stdout.String(), stderr.String())
	}
}

func TestFFIRun_TestProgramTaggedFFIApp(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test invokes go run; skipped in -short mode")
	}
	t.Setenv("NOMI_FFIRUN_CACHE_ROOT", t.TempDir())

	nomiRoot := findNomiLangRoot(t)
	entryPath := filepath.Join(
		nomiRoot,
		"tests",
		"18-ffi-and-dynamic",
		"tagged_ffi_app",
		"main.nomi",
	)

	res, err := Prepare(entryPath)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if res.FastPath {
		t.Fatal("expected FastPath=false (tests FFI fixture has source binding)")
	}
	if len(res.DiscoveredPackages) != 1 || res.DiscoveredPackages[0] != "taggedffiapp" {
		t.Fatalf("DiscoveredPackages: got %v, want [taggedffiapp]", res.DiscoveredPackages)
	}

	cmd := exec.Command("go", "run", "-mod=mod", res.WrapperPath, wrapperModeFlag, "vmrun", entryPath)
	cmd.Dir = res.WrapperDir
	userCwd, _ := os.Getwd()
	cmd.Env = append(os.Environ(), "NOMI_FFIRUN_USER_CWD="+userCwd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go run wrapper: %v\nstdout:\n%s\nstderr:\n%s",
			err, stdout.String(), stderr.String())
	}
	got := strings.TrimSpace(stdout.String())
	if got != "FIXTURE" {
		t.Errorf("expected FIXTURE, got %q\nstdout:\n%s\nstderr:\n%s",
			got, stdout.String(), stderr.String())
	}

	testPath := filepath.Join(
		nomiRoot,
		"tests",
		"18-ffi-and-dynamic",
		"tagged_ffi_app",
		"main_test.nomi",
	)
	passed, failed, blocked, err := RunTestVM(res, testPath, 0, false, "text")
	if err != nil {
		t.Fatalf("RunTestVM: %v", err)
	}
	if passed != 2 || failed != 0 || blocked != 0 {
		t.Fatalf("RunTestVM counts: got %d passed, %d failed, %d blocked; want 2 passed, 0 failed, 0 blocked",
			passed, failed, blocked)
	}
}

func TestFFIRun_CallbackSourceBindingRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test invokes go run; skipped in -short mode")
	}
	t.Setenv("NOMI_FFIRUN_CACHE_ROOT", t.TempDir())

	projectRoot := t.TempDir()
	nomiRoot := findNomiLangRoot(t)

	bindingDir := filepath.Join(projectRoot, "callbackbinding")
	if err := os.MkdirAll(bindingDir, 0o755); err != nil {
		t.Fatalf("mkdir callbackbinding: %v", err)
	}
	mustWriteHelper(t, filepath.Join(bindingDir, "callback.go"), `package callbackbinding

func ApplyTwice(s string, f func(string) (string, error)) (string, error) {
	first, err := f(s)
	if err != nil {
		return "", err
	}
	return f(first)
}
`)
	mustWriteHelper(t, filepath.Join(bindingDir, "go.mod"), `module callbackbinding

go 1.26.3
`)
	mustWriteHelper(t, filepath.Join(projectRoot, "go.mod"), fmt.Sprintf(`module callbacktest

go 1.26.3

require callbackbinding v0.0.0

require github.com/nomi-language/nomi v0.0.0

replace callbackbinding => ./callbackbinding

replace github.com/nomi-language/nomi => %s
`, nomiRoot))
	mustWriteHelper(t, filepath.Join(projectRoot, "nomi.toml"), `[module]
name = "callbacktest"
entry_points = ["main"]

`)

	entryPath := filepath.Join(projectRoot, "main.nomi")
	mustWriteHelper(t, entryPath, `import {
  std/io
}

gopkg "callbackbinding" as callback

fn apply_twice(s: String, f: (String) -> Result<String, String>): Result<String, String> go callback.ApplyTwice

fn main() {
  case apply_twice("ha", |s| Ok("${s}!")) {
    Err(e) -> io.print("error: ${e}")
    Ok(v) -> io.print(v)
  }
}
`)

	res, err := Prepare(entryPath)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if res.FastPath {
		t.Fatal("expected FastPath=false (callback binding has source binding)")
	}
	if len(res.DiscoveredPackages) != 1 || res.DiscoveredPackages[0] != "callbackbinding" {
		t.Fatalf("DiscoveredPackages: got %v, want [callbackbinding]", res.DiscoveredPackages)
	}

	cmd := exec.Command("go", "run", "-mod=mod", res.WrapperPath, wrapperModeFlag, "vmrun", entryPath)
	cmd.Dir = res.WrapperDir
	userCwd, _ := os.Getwd()
	cmd.Env = append(os.Environ(), "NOMI_FFIRUN_USER_CWD="+userCwd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go run wrapper: %v\nstdout:\n%s\nstderr:\n%s",
			err, stdout.String(), stderr.String())
	}
	got := strings.TrimSpace(stdout.String())
	if got != "ha!!" {
		t.Errorf("expected ha!!, got %q\nstdout:\n%s\nstderr:\n%s",
			got, stdout.String(), stderr.String())
	}
}

func TestFFIRun_TimeDurationProjectsToDuration(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test invokes go run; skipped in -short mode")
	}
	t.Setenv("NOMI_FFIRUN_CACHE_ROOT", t.TempDir())

	projectRoot := t.TempDir()
	nomiRoot := findNomiLangRoot(t)

	bindingDir := filepath.Join(projectRoot, "durationbinding")
	if err := os.MkdirAll(bindingDir, 0o755); err != nil {
		t.Fatalf("mkdir durationbinding: %v", err)
	}
	mustWriteHelper(t, filepath.Join(bindingDir, "duration.go"), `package durationbinding

import stdtime "time"

func Timeout() stdtime.Duration {
	return 1500 * stdtime.Millisecond
}

func Double(d stdtime.Duration) stdtime.Duration {
	return d * 2
}
`)
	mustWriteHelper(t, filepath.Join(bindingDir, "go.mod"), `module durationbinding

go 1.26.3
`)
	mustWriteHelper(t, filepath.Join(projectRoot, "go.mod"), fmt.Sprintf(`module durationtest

go 1.26.3

require durationbinding v0.0.0

require github.com/nomi-language/nomi v0.0.0

replace durationbinding => ./durationbinding

replace github.com/nomi-language/nomi => %s
`, nomiRoot))
	mustWriteHelper(t, filepath.Join(projectRoot, "nomi.toml"), `[module]
name = "durationtest"
entry_points = ["main"]
`)
	entryPath := filepath.Join(projectRoot, "main.nomi")
	mustWriteHelper(t, entryPath, `import {
  std/duration.Duration
  std/io
}

gopkg "durationbinding" as duration

fn timeout(): Duration go duration.Timeout

fn double(d: Duration): Duration go duration.Double

fn main() {
  timeout()
    |> double()
    |> Duration.as_millis()
    |> io.print()
}
`)

	res, err := Prepare(entryPath)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if res.FastPath {
		t.Fatal("expected FastPath=false (duration binding has source binding)")
	}

	cmd := exec.Command("go", "run", "-mod=mod", res.WrapperPath, wrapperModeFlag, "vmrun", entryPath)
	cmd.Dir = res.WrapperDir
	userCwd, _ := os.Getwd()
	cmd.Env = append(os.Environ(), "NOMI_FFIRUN_USER_CWD="+userCwd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go run wrapper: %v\nstdout:\n%s\nstderr:\n%s",
			err, stdout.String(), stderr.String())
	}
	got := strings.TrimSpace(stdout.String())
	if got != "3000" {
		t.Errorf("expected 3000, got %q\nstdout:\n%s\nstderr:\n%s",
			got, stdout.String(), stderr.String())
	}
}

func TestFFIRun_TimeTimeProjectsToInstant(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test invokes go run; skipped in -short mode")
	}
	t.Setenv("NOMI_FFIRUN_CACHE_ROOT", t.TempDir())

	projectRoot := t.TempDir()
	nomiRoot := findNomiLangRoot(t)

	bindingDir := filepath.Join(projectRoot, "timebinding")
	if err := os.MkdirAll(bindingDir, 0o755); err != nil {
		t.Fatalf("mkdir timebinding: %v", err)
	}
	mustWriteHelper(t, filepath.Join(bindingDir, "time.go"), `package timebinding

import stdtime "time"

func CreatedAt() stdtime.Time {
	return stdtime.Unix(1700000000, 250000000).UTC()
}

func PlusSecond(t stdtime.Time) stdtime.Time {
	return t.Add(stdtime.Second)
}
`)
	mustWriteHelper(t, filepath.Join(bindingDir, "go.mod"), `module timebinding

go 1.26.3
`)
	mustWriteHelper(t, filepath.Join(projectRoot, "go.mod"), fmt.Sprintf(`module timetest

go 1.26.3

require timebinding v0.0.0

require github.com/nomi-language/nomi v0.0.0

replace timebinding => ./timebinding

replace github.com/nomi-language/nomi => %s
`, nomiRoot))
	mustWriteHelper(t, filepath.Join(projectRoot, "nomi.toml"), `[module]
name = "timetest"
entry_points = ["main"]
`)
	entryPath := filepath.Join(projectRoot, "main.nomi")
	mustWriteHelper(t, entryPath, `import {
  std/instant.Instant
  std/io
}

gopkg "timebinding" as go_time

fn created_at(): Instant go go_time.CreatedAt

fn plus_second(t: Instant): Instant go go_time.PlusSecond

fn main() {
  created_at()
    |> plus_second()
    |> Instant.to_seconds()
    |> io.print()
}
`)

	res, err := Prepare(entryPath)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if res.FastPath {
		t.Fatal("expected FastPath=false (time binding has source binding)")
	}

	cmd := exec.Command("go", "run", "-mod=mod", res.WrapperPath, wrapperModeFlag, "vmrun", entryPath)
	cmd.Dir = res.WrapperDir
	userCwd, _ := os.Getwd()
	cmd.Env = append(os.Environ(), "NOMI_FFIRUN_USER_CWD="+userCwd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go run wrapper: %v\nstdout:\n%s\nstderr:\n%s",
			err, stdout.String(), stderr.String())
	}
	got := strings.TrimSpace(stdout.String())
	if got != "1700000001" {
		t.Errorf("expected 1700000001, got %q\nstdout:\n%s\nstderr:\n%s",
			got, stdout.String(), stderr.String())
	}
}

// TestFFIRun_SQLiteSourceBindings binds testdata/sqlite, a separate Go module
// whose package imports a third-party driver (modernc.org/sqlite), through
// `opaque type` and `fn ... go` source bindings, and runs the wrapper.
func TestFFIRun_SQLiteSourceBindings(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test invokes go run; skipped in -short mode")
	}
	t.Setenv("NOMI_FFIRUN_CACHE_ROOT", t.TempDir())

	projectRoot := t.TempDir()
	nomiRoot := findNomiLangRoot(t)
	sqliteRoot, err := filepath.Abs(filepath.Join("testdata", "sqlite"))
	if err != nil {
		t.Fatal(err)
	}

	mustWriteHelper(t, filepath.Join(projectRoot, "go.mod"), fmt.Sprintf(`module sqlitetaggedtest

go 1.26.3

require sqlite v0.0.0

require github.com/nomi-language/nomi v0.0.0

replace sqlite => %s

replace github.com/nomi-language/nomi => %s
`, sqliteRoot, nomiRoot))
	mustWriteHelper(t, filepath.Join(projectRoot, "nomi.toml"), `[module]
name = "sqlitetaggedtest"
entry_points = ["main"]

`)

	entryPath := filepath.Join(projectRoot, "main.nomi")
	mustWriteHelper(t, entryPath, `import {
  std/io
  sqlite/sqlite
}

fn main() {
  case sqlite.open(":memory:") {
    Err(e) -> io.print("open error: ${e}")
    Ok(conn) -> {
      defer sqlite.close(conn)
      _ = sqlite.exec(conn, "CREATE TABLE people (name TEXT)", [])
      _ = sqlite.exec(conn, "INSERT INTO people VALUES (?)", ["Ada"])
      case sqlite.query_all(conn, "SELECT name FROM people", []) {
        Err(e) -> io.print("query error: ${e}")
        Ok(rows) -> io.inspect(rows)
      }
    }
  }
}
`)

	res, err := Prepare(entryPath)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if res.FastPath {
		t.Fatal("expected FastPath=false (sqlite source bindings discovered)")
	}
	if len(res.DiscoveredPackages) != 1 || res.DiscoveredPackages[0] != "sqlite" {
		t.Fatalf("DiscoveredPackages: got %v, want [sqlite]", res.DiscoveredPackages)
	}

	cmd := exec.Command("go", "run", "-mod=mod", res.WrapperPath, wrapperModeFlag, "vmrun", entryPath)
	cmd.Dir = res.WrapperDir
	userCwd, _ := os.Getwd()
	cmd.Env = append(os.Environ(), "NOMI_FFIRUN_USER_CWD="+userCwd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go run wrapper: %v\nstdout:\n%s\nstderr:\n%s",
			err, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "Ada") {
		t.Errorf("expected sqlite output to contain Ada\nstdout:\n%s\nstderr:\n%s",
			stdout.String(), stderr.String())
	}
}

// TestFFIRun_PreservesCWD is the regression test for the cache-dir
// leak: ffirun.Exec used to `os.Chdir(WrapperDir)` before launching
// `go run`, which set the wrapped program's cwd to the cache dir.
// User code calling `sqlite.open("tasks.db")` (or any relative path)
// would then land in ~/.cache/nomi/builds/<hash>/ instead of where
// the user invoked nomi run.
//
// This test stages a binding that exposes os.Getwd() back to Nomi,
// invokes the wrapper from a known tempdir (not the wrapper cache),
// and asserts the wrapper observes that tempdir as cwd. Catches any
// future regression that re-introduces a cwd change inside the
// wrapper launch path.
func TestFFIRun_PreservesCWD(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	t.Setenv("NOMI_FFIRUN_CACHE_ROOT", t.TempDir())

	projectRoot := t.TempDir()
	nomiRoot := findNomiLangRoot(t)

	bindingDir := filepath.Join(projectRoot, "cwdbinding")
	if err := os.MkdirAll(bindingDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	bindingSrc := `package cwdbinding

import "os"

func GetCWD() string {
	d, _ := os.Getwd()
	return d
}
`
	mustWriteHelper(t, filepath.Join(bindingDir, "cwd.go"), bindingSrc)
	mustWriteHelper(t, filepath.Join(bindingDir, "go.mod"), `module cwdbinding

go 1.26.3
`)
	mustWriteHelper(t, filepath.Join(projectRoot, "go.mod"), fmt.Sprintf(`module cwdtest

go 1.26.3

require cwdbinding v0.0.0

require github.com/nomi-language/nomi v0.0.0

replace cwdbinding => ./cwdbinding

replace github.com/nomi-language/nomi => %s
`, nomiRoot))
	mustWriteHelper(t, filepath.Join(projectRoot, "nomi.toml"), `[module]
name = "cwdtest"
entry_points = ["main"]

`)
	entryPath := filepath.Join(projectRoot, "main.nomi")
	mustWriteHelper(t, entryPath, `import {
  std/io
}

gopkg "cwdbinding" as cwd

fn get_cwd(): String go cwd.GetCWD

fn main() {
  io.print(get_cwd())
}
	`)

	res, err := Prepare(entryPath)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if res.FastPath {
		t.Fatal("expected FastPath=false")
	}

	// Mirror Exec's launch contract: cmd.Dir = WrapperDir (so Go's
	// module resolution finds the wrapper's go.mod) AND
	// NOMI_FFIRUN_USER_CWD points at a SEPARATE tempdir representing
	// "what the user's cwd was when they typed nomi run". The
	// wrapper's startup hook chdirs to that env var; the get_cwd
	// extern then sees the user's tempdir, not the wrapper cache.
	runCwd := t.TempDir()
	cmd := exec.Command("go", "run", "-mod=mod", res.WrapperPath, wrapperModeFlag, "vmrun", entryPath)
	cmd.Dir = res.WrapperDir
	cmd.Env = append(os.Environ(), "NOMI_FFIRUN_USER_CWD="+runCwd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go run: %v\nstderr:\n%s", err, stderr.String())
	}
	got := strings.TrimSpace(stdout.String())
	// macOS resolves /tmp via /private/tmp; accept either-direction
	// suffix match so the test stays portable.
	resolvedRunCwd, _ := filepath.EvalSymlinks(runCwd)
	resolvedGot, _ := filepath.EvalSymlinks(got)
	if resolvedGot != resolvedRunCwd && resolvedGot != runCwd {
		t.Errorf("wrapped child saw cwd %q, expected %q (or symlink-resolved equivalent)",
			got, runCwd)
	}
	if strings.Contains(got, "Caches/nomi/builds") || strings.Contains(got, ".cache/nomi/builds") {
		t.Errorf("wrapped child cwd leaked the FFI cache dir: %q", got)
	}
}

// mustWriteHelper is the test-local file writer (mustWrite is in
// main_test.go's package, distinct from this one).
func mustWriteHelper(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func findNomiLangRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for dir := wd; ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			if _, err := os.Stat(filepath.Join(dir, "std")); err == nil {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not find the compiler module root from %s", wd)
		}
	}
}

// TestFFIRun_FastPathNoGoMod confirms that an entry file outside any
// Go module tree returns FastPath=true with empty discovered list.
// This is the single-file `nomi run main.nomi` UX preservation
// check: Phase 5 must NOT route bare Nomi files through the build
// path.
func TestFFIRun_FastPathNoGoMod(t *testing.T) {
	// Use a path that we KNOW has no go.mod above it. /tmp/<random>
	// is created by t.TempDir and inherits no parent module.
	dir := t.TempDir()
	entryPath := filepath.Join(dir, "main.nomi")
	if err := os.WriteFile(entryPath, []byte("fn main() {}\n"), 0o644); err != nil {
		t.Fatalf("write main.nomi: %v", err)
	}
	res, err := Prepare(entryPath)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if !res.FastPath {
		t.Errorf("expected FastPath=true for no-go.mod entry; got %+v", res)
	}
	if len(res.DiscoveredPackages) != 0 {
		t.Errorf("expected no discovered packages; got %v", res.DiscoveredPackages)
	}
}

// TestFFIRun_FastPathNoFFIDeps confirms that a project with a
// go.mod (so the upward walk finds something) but NO FFI deps still
// takes the fast path. This is the pure-Nomi multi-package case
// (a module depending on a sibling Nomi module: go.mod exists for module
// identity, with no Go side at all).
func TestFFIRun_FastPathNoFFIDeps(t *testing.T) {
	projectRoot := t.TempDir()
	// Bare go.mod: a module with no requires.
	if err := os.WriteFile(
		filepath.Join(projectRoot, "go.mod"),
		[]byte("module nomi-only\n\ngo 1.26.3\n"),
		0o644,
	); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	entryPath := filepath.Join(projectRoot, "main.nomi")
	if err := os.WriteFile(entryPath, []byte("fn main() {}\n"), 0o644); err != nil {
		t.Fatalf("write main.nomi: %v", err)
	}
	res, err := Prepare(entryPath)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if !res.FastPath {
		t.Errorf("expected FastPath=true for project with no FFI deps; got %+v", res)
	}
}
