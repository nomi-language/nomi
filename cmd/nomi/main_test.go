package main

import (
	"bytes"
	"fmt"
	"github.com/nomi-language/nomi/internal/expectation"
	"github.com/nomi-language/nomi/internal/termcolor"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// nomiBin is the path of the test-built `nomi` binary. Set once in
// TestMain so each test invocation runs against the binary the test
// process compiled — not a stale ~/go/bin/nomi the user might have
// installed from a different worktree.
var nomiBin string

func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "nomi-test-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "TestMain: mkdir: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmp)
	binPath := filepath.Join(tmp, "nomi")
	build := exec.Command("go", "build", "-o", binPath, ".")
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "TestMain: build nomi: %v\n%s", err, out)
		os.Exit(1)
	}
	// The CLI tests name corpus files, fixtures and scripts relative to the
	// repository root, which is also the module root.
	if err := os.Chdir(filepath.Join("..", "..")); err != nil {
		fmt.Fprintf(os.Stderr, "TestMain: chdir to the repository root: %v\n", err)
		os.Exit(1)
	}
	nomiBin = binPath
	os.Exit(m.Run())
}

// repoRoot returns the absolute path of the repository root, two
// directories above this file (cmd/nomi/main_test.go).
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(file)))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("no go.mod at the repository root %s: %v", root, err)
	}
	return root
}

// TestRunFile_PureNomiSingleFile_FastPath confirms the no-go.mod
// case keeps using the in-process tree-walker — the fast path Phase
// 5 must preserve. A single-file Nomi program that outputs "ok"
// runs without any cache directory being created.
func TestRunFile_PureNomiSingleFile_FastPath(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	dir := t.TempDir()
	entry := filepath.Join(dir, "main.nomi")
	if err := os.WriteFile(entry, []byte(`import std/io
fn main() {
  io.print("ok")
}
	`), 0o644); err != nil {
		t.Fatalf("write entry: %v", err)
	}
	out, err := runNomi(t, cacheRoot, "run", entry)
	if err != nil {
		t.Fatalf("nomi run: %v\nstdout/stderr:\n%s", err, out)
	}
	if !strings.Contains(out, "ok") {
		t.Errorf("expected 'ok' in output; got %q", out)
	}
	// The fast path must NOT have created a cache entry — Prepare
	// short-circuits at the no-go.mod check before reaching the cache
	// dir code.
	entries, _ := os.ReadDir(cacheRoot)
	if len(entries) != 0 {
		t.Errorf("expected empty cache root for fast-path run; got %d entries", len(entries))
	}
}

// TestRunFile_StdlibFileIsRefused: a stdlib module is not a program, so
// `nomi run` on one refuses it by name rather than loading it as an ordinary
// project (which would report a module short-name collision instead).
func TestRunFile_StdlibFileIsRefused(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	path := filepath.Join(repoRoot(t), "std", "compiler.nomi")
	out, err := runNomi(t, cacheRoot, "run", path)
	if err == nil {
		t.Fatalf("nomi run on a std file exited 0:\n%s", out)
	}
	if !strings.Contains(out, "is a stdlib module, not a program; to run its tests, use `nomi test ") {
		t.Fatalf("nomi run on a std file was not refused as a stdlib module:\n%s", out)
	}
	if strings.Contains(out, "module short-name collision") ||
		strings.Contains(out, "private and cannot be imported through") {
		t.Fatalf("stdlib run used ordinary project loading:\n%s", out)
	}
}

// TestRunFile_FFIRoundTrip stages a project with a source-declared Go binding,
// hands it to `nomi run` (the real CLI binary), and verifies the wrapper
// executed the program and routed the extern call back into Go.
func TestRunFile_FFIRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	projectRoot, entryPath := stageEchoFixture(t)
	out, err := runNomi(t, cacheRoot, "run", entryPath)
	if err != nil {
		t.Fatalf("nomi run: %v\nstdout/stderr:\n%s", err, out)
	}
	if !strings.Contains(out, "HELLO") {
		t.Errorf("expected HELLO in output; got %q", out)
	}
	// Build path MUST have created a cache entry.
	entries, _ := os.ReadDir(cacheRoot)
	if len(entries) == 0 {
		t.Errorf("expected at least one cache entry after FFI run; got 0")
	}
	_ = projectRoot
}

func TestRunFile_FFIBytesRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	projectRoot, entryPath := stageByteFixture(t)
	out, err := runNomi(t, cacheRoot, "run", entryPath)
	if err != nil {
		t.Fatalf("nomi run: %v\nstdout/stderr:\n%s", err, out)
	}
	for _, want := range []string{"go:hi", "103", "A"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in output, got:\n%s", want, out)
		}
	}
	_ = projectRoot
}

func TestRunFile_FFIMapRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	projectRoot, entryPath := stageMapFixture(t)
	out, err := runNomi(t, cacheRoot, "run", entryPath)
	if err != nil {
		t.Fatalf("nomi run: %v\nstdout/stderr:\n%s", err, out)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	want := []string{"2", "5"}
	if len(lines) != len(want) {
		t.Fatalf("expected %d output lines, got %d:\n%s", len(want), len(lines), out)
	}
	for i, line := range lines {
		if strings.TrimSpace(line) != want[i] {
			t.Fatalf("line %d: got %q, want %q\nfull output:\n%s", i+1, line, want[i], out)
		}
	}
	_ = projectRoot
}

// TestRunFile_GoBindings runs testdata/go_bindings, whose Go functions take
// and return a Nomi struct, a map of lists, Bytes and Results.
func TestRunFile_GoBindings(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	root := repoRoot(t)
	entry := filepath.Join(root, "cmd", "nomi", "testdata", "go_bindings", "main.nomi")
	out, err := runNomi(t, cacheRoot, "run", entry)
	if err != nil {
		t.Fatalf("nomi run testdata/go_bindings: %v\nstdout/stderr:\n%s", err, out)
	}
	if !strings.Contains(out, "example.com/search tags=[go, nomi]") ||
		!strings.Contains(out, "sha256(nomi) = 0f937e60148316557dbe0dce3e862e77d4d8157bfea24ae4b4c1fa367297c2d9") {
		t.Fatalf("expected go-bindings output, got:\n%s", out)
	}
}

// TestRunFile_GoBindingsInTwoFilesWithOneBaseName runs a program importing
// a/util.nomi and b/util.nomi, each binding its own Go function as greet and
// one more under a name of its own. Keyed by base name, both greets crossed
// as util.greet: the wrapper's host table did not compile ("duplicate key"),
// and with the names apart b's only_b was looked up in a/util.nomi and
// refused. `nomi build`'s executable must agree with `nomi run`.
func TestRunFile_GoBindingsInTwoFilesWithOneBaseName(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "go.mod"), "module twoutil\n\ngo 1.27.0\n")
	mustWrite(t, filepath.Join(root, "nomi.toml"), "[module]\nname = \"twoutil\"\nentry_points = [\"main\"]\n")
	for _, side := range []string{"a", "b"} {
		mustWrite(t, filepath.Join(root, "go"+side, "go"+side+".go"), fmt.Sprintf(
			"package go%[1]s\n\nfunc Greet() string { return \"greet from %[1]s\" }\n\nfunc Only() string { return \"only %[1]s\" }\n", side))
		mustWrite(t, filepath.Join(root, side, "util.nomi"), fmt.Sprintf(
			"gopkg \"twoutil/go%[1]s\"\n\npub fn greet(): String go go%[1]s.Greet\n\npub fn only_%[1]s(): String go go%[1]s.Only\n", side))
	}
	entry := filepath.Join(root, "main.nomi")
	mustWrite(t, entry, `import {
    std/io
    a/util
    b/util as butil
}

fn main() {
    io.print(util.greet())
    io.print(butil.greet())
    io.print(util.only_a())
    io.print(butil.only_b())
}
`)
	want := "greet from a\ngreet from b\nonly a\nonly b\n"
	out, err := runNomi(t, cacheRoot, "run", entry)
	if err != nil || out != want {
		t.Fatalf("nomi run: %v\ngot:\n%s\nwant:\n%s", err, out, want)
	}
	bin := filepath.Join(t.TempDir(), "twoutil")
	if out, err := runNomi(t, cacheRoot, "build", "-o", bin, entry); err != nil {
		t.Fatalf("nomi build: %v\n%s", err, out)
	}
	got, err := exec.Command(bin).CombinedOutput()
	if err != nil || string(got) != want {
		t.Fatalf("built executable: %v\ngot:\n%s\nwant:\n%s", err, got, want)
	}
}

func TestRunFile_InlineGoCompileErrorPointsAtNomiSource(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	root := t.TempDir()
	nomiRoot := repoRoot(t)
	mustWrite(t, filepath.Join(root, "go.mod"), fmt.Sprintf(`module badinline

go 1.26.3

require github.com/nomi-language/nomi v0.0.0

replace github.com/nomi-language/nomi => %s
`, nomiRoot))
	entry := filepath.Join(root, "main.nomi")
	mustWrite(t, entry, `gopkg "strconv"

fn bad(n: Int): Int go {
  return strconv.Itoa(int(n))
}

fn main() {
  bad(1)
}
`)
	out, err := runNomi(t, cacheRoot, "run", entry)
	if err == nil {
		t.Fatalf("expected inline Go compile failure, got success:\n%s", out)
	}
	wantLoc := "main.nomi:4:"
	for _, want := range []string{wantLoc, "cannot use strconv.Itoa"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected inline Go diagnostic to contain %q, got:\n%s", want, out)
		}
	}
	if strings.Contains(out, "main.go:") && !strings.Contains(out, wantLoc) {
		t.Fatalf("expected diagnostic to prefer Nomi source location, got:\n%s", out)
	}
}

func TestRunFile_FFIWrapperCompileErrorPointsAtNomiSelector(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	_, entry := stageBuildTagMissingSelectorFixture(t)
	out, err := runNomi(t, cacheRoot, "run", entry)
	if err == nil {
		t.Fatalf("expected wrapper compile failure, got success:\n%s", out)
	}
	assertBadBuildTagSelectorDiagnostic(t, out, entry)
}

func TestRunFile_FFINumericWidthHelpersRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	projectRoot, entryPath := stageNumericWidthFixture(t, `import std/io

gopkg "widthbinding" as ffi

fn double_int(n: Int): Int go ffi.DoubleInt

fn add_u32(n: Int): Int go ffi.AddUint32

fn main() {
  io.print(double_int(21))
  io.print(add_u32(41))
}
`)
	out, err := runNomi(t, cacheRoot, "run", entryPath)
	if err != nil {
		t.Fatalf("nomi run: %v\nstdout/stderr:\n%s", err, out)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	want := []string{"42", "42"}
	if len(lines) != len(want) {
		t.Fatalf("expected %d output lines, got %d:\n%s", len(want), len(lines), out)
	}
	for i, line := range lines {
		if strings.TrimSpace(line) != want[i] {
			t.Fatalf("line %d: got %q, want %q\nfull output:\n%s", i+1, line, want[i], out)
		}
	}
	_ = projectRoot
}

func TestRunFile_FFINumericWidthHelperOverflow(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	projectRoot, entryPath := stageNumericWidthFixture(t, `import std/io

gopkg "widthbinding" as ffi

fn double_tiny(n: Int): Int go ffi.DoubleInt8

fn main() {
  io.print(double_tiny(128))
}
`)
	out, err := runNomi(t, cacheRoot, "run", entryPath)
	if err == nil {
		t.Fatalf("expected nomi run to fail, got output:\n%s", out)
	}
	if !strings.Contains(out, "unmarshal to int8") || !strings.Contains(out, "value 128 overflows") {
		t.Fatalf("expected int8 overflow in output, got:\n%s", out)
	}
	_ = projectRoot
}

func TestRunFile_FFIMaybeOKHelpersRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	projectRoot, entryPath := stageMaybeOKFixture(t)
	out, err := runNomi(t, cacheRoot, "run", entryPath)
	if err != nil {
		t.Fatalf("nomi run: %v\nstdout/stderr:\n%s", err, out)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	want := []string{"7", "yes:9", "missing"}
	if len(lines) != len(want) {
		t.Fatalf("expected %d output lines, got %d:\n%s", len(want), len(lines), out)
	}
	for i, line := range lines {
		if strings.TrimSpace(line) != want[i] {
			t.Fatalf("line %d: got %q, want %q\nfull output:\n%s", i+1, line, want[i], out)
		}
	}
	_ = projectRoot
}

func TestRunFile_FFIResultHelpersRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	projectRoot, entryPath := stageResultFixture(t)
	out, err := runNomi(t, cacheRoot, "run", entryPath)
	if err != nil {
		t.Fatalf("nomi run: %v\nstdout/stderr:\n%s", err, out)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	want := []string{"123", "5", "negative", "empty", "blank"}
	if len(lines) != len(want) {
		t.Fatalf("expected %d output lines, got %d:\n%s", len(want), len(lines), out)
	}
	for i, line := range lines {
		if strings.TrimSpace(line) != want[i] {
			t.Fatalf("line %d: got %q, want %q\nfull output:\n%s", i+1, line, want[i], out)
		}
	}
	_ = projectRoot
}

func TestRunFile_FFIStructProjectionRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	projectRoot, entryPath := stageStructProjectionFixture(t)
	out, err := runNomi(t, cacheRoot, "run", entryPath)
	if err != nil {
		t.Fatalf("nomi run: %v\nstdout/stderr:\n%s", err, out)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	// The last three lines are the identity assertions the original four
	// field reads were blind to: reading `p.address.city` back succeeds
	// even when the struct crossed the boundary with no Nomi type
	// identity, so only equality against a Nomi-built value and a
	// dispatched `Display` actually exercise it.
	want := []string{"Ada", "37", "London", "Oxford", "True", "True", "Address{city: London}"}
	if len(lines) != len(want) {
		t.Fatalf("expected %d output lines, got %d:\n%s", len(want), len(lines), out)
	}
	for i, line := range lines {
		if strings.TrimSpace(line) != want[i] {
			t.Fatalf("line %d: got %q, want %q\nfull output:\n%s", i+1, line, want[i], out)
		}
	}
	_ = projectRoot
}

// TestSyntaxErrorRefusedByRun is the hard edge of the LSP's
// resilient parsing (parser.ParseResilient). The editor recovers from a syntax
// error inside a function body so completion still has scopes to offer;
// `nomi run` must not. They go through parser.Parse, which stops
// at the first error and produces no AST, and this pins both the refusal
// and the exact diagnostic — the shape a recovered parse leaking into
// the command would break.
func TestSyntaxErrorRefusedByRun(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	dir := t.TempDir()
	entry := filepath.Join(dir, "main.nomi")
	// `|x| n + ` with the operand not yet typed, as mid-edit in an editor.
	mustWrite(t, entry, `import std/io

fn compute(n: Int): Int {
  total = n * 2
  f = |x| n + 
}

fn main() {
  io.print("hi")
}
`)
	// The diagnostic names the file, line and column, as compilers do.
	wantDiag := "error: unexpected token RBRACE \"}\"\n --> " + entry + ":6:1\n"

	out, err := runNomi(t, t.TempDir(), "run", entry)
	if err == nil {
		t.Fatalf("nomi run accepted a file with a syntax error:\n%s", out)
	}
	if !strings.Contains(out, wantDiag) {
		t.Fatalf("nomi run: expected %q, got:\n%s", wantDiag, out)
	}

	// Positive control: the same file with the operand supplied runs, so the
	// refusal above is about the syntax error rather than about the fixture
	// failing for some other reason.
	fixed := filepath.Join(dir, "fixed.nomi")
	mustWrite(t, fixed, `import std/io

fn compute(n: Int): Int {
  f = |x: Int| n + x
  f(2)
}

fn main() {
  io.print("hi")
}
`)
	if out, err := runNomi(t, t.TempDir(), "run", fixed); err != nil {
		t.Fatalf("the control fixture must run: %v\n%s", err, out)
	}
}

// TestRunCommand_TasksProject drives testdata/ffi_modules/tasks through
// `nomi run`: its REPL loop, reading commands from stdin, and the tasks.db it
// persists between runs through the sibling sqlite module's Go binding.
func TestRunCommand_TasksProject(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	root := repoRoot(t)
	entry := filepath.Join(root, "cmd", "nomi", "testdata", "ffi_modules", "tasks", "main.nomi")

	runDir := t.TempDir()
	runOut, err := runNomiInDir(t, cacheRoot, runDir, "add ship build\nlist\nquit\n", "run", entry)
	if err != nil {
		t.Fatalf("nomi run testdata/ffi_modules/tasks: %v\nstdout/stderr:\n%s", err, runOut)
	}
	for _, want := range []string{"tasks. type 'help' for commands.", "added [ ] 1 ship build", "bye."} {
		if !strings.Contains(runOut, want) {
			t.Fatalf("expected %q in tasks output, got:\n%s", want, runOut)
		}
	}
	if _, err := os.Stat(filepath.Join(runDir, "tasks.db")); err != nil {
		t.Fatalf("expected tasks.db in run cwd: %v", err)
	}

	secondOut, err := runNomiInDir(t, cacheRoot, runDir, "list\nquit\n", "run", entry)
	if err != nil {
		t.Fatalf("nomi run testdata/ffi_modules/tasks second run: %v\nstdout/stderr:\n%s", err, secondOut)
	}
	if !strings.Contains(secondOut, "ship build") {
		t.Fatalf("expected persisted task in second run, got:\n%s", secondOut)
	}
}

// TestRunCommand_ModuleProjectEntryPoints runs both entry points that
// testdata/modules/todo's nomi.toml lists. Each imports a file under an
// `internal/` directory it may reach, and main also imports a sibling Nomi
// module, stringkit, through a go.mod replace with no Go code on either side.
func TestRunCommand_ModuleProjectEntryPoints(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	todo := filepath.Join(repoRoot(t), "cmd", "nomi", "testdata", "modules", "todo")
	for _, c := range []struct{ entry, want string }{
		{"main.nomi", "  1  [x] buy milk\n  2  [ ] write the design doc\n 10  [ ] walk the dog\n2 open\n"},
		{filepath.Join("tools", "seed.nomi"), "  1  [ ] read the Nomi tour\n  2  [ ] write a module\n  3  [ ] publish it\n"},
	} {
		out, err := runNomi(t, cacheRoot, "run", filepath.Join(todo, c.entry))
		if err != nil {
			t.Fatalf("nomi run testdata/modules/todo/%s: %v\nstdout/stderr:\n%s", c.entry, err, out)
		}
		if out != c.want {
			t.Fatalf("nomi run testdata/modules/todo/%s printed:\n%s\nwant:\n%s", c.entry, out, c.want)
		}
	}
	entries, _ := os.ReadDir(cacheRoot)
	if len(entries) != 0 {
		t.Errorf("a project with no Go code built %d FFI cache entries; it should take the fast path", len(entries))
	}
}

func TestCheckCommand_PureNomiSingleFile_FastPath(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	dir := t.TempDir()
	entry := filepath.Join(dir, "main.nomi")
	mustWrite(t, entry, `fn main() {}
`)
	out, err := runNomi(t, cacheRoot, "check", entry)
	if err != nil {
		t.Fatalf("nomi check: %v\nstdout/stderr:\n%s", err, out)
	}
	if !strings.Contains(out, "ok ") || !strings.Contains(out, "main.nomi") {
		t.Fatalf("expected ok check output, got:\n%s", out)
	}
	entries, _ := os.ReadDir(cacheRoot)
	if len(entries) != 0 {
		t.Errorf("expected empty cache root for fast-path check; got %d entries", len(entries))
	}
}

// A tail `else if` chain whose every arm returns once panicked `nomi run`
// with an ir.Lint violation. `nomi check` and `nomi run` both accept it.
func TestCheckAndRun_ReturningElseIfChain(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	entry := filepath.Join(t.TempDir(), "main.nomi")
	mustWrite(t, entry, `import std/io

fn sign(x: Int): Int {
  if x < 0 {
    return -1
  } else if x == 0 {
    return 0
  } else {
    return 1
  }
}

fn main() {
  io.print(sign(-5))
  io.print(sign(0))
  io.print(sign(5))
}
`)
	out, err := runNomi(t, cacheRoot, "check", entry)
	if err != nil || !strings.Contains(out, "ok ") {
		t.Fatalf("nomi check: %v\n%s", err, out)
	}
	out, err = runNomi(t, cacheRoot, "run", entry)
	if err != nil || out != "-1\n0\n1\n" {
		t.Fatalf("nomi run: %v\n%s", err, out)
	}
}

func TestCheckCommand_FFIRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	projectRoot, entryPath := stageEchoFixture(t)
	out, err := runNomi(t, cacheRoot, "check", entryPath)
	if err != nil {
		t.Fatalf("nomi check: %v\nstdout/stderr:\n%s", err, out)
	}
	if !strings.Contains(out, "ok ") || !strings.Contains(out, "main.nomi") {
		t.Fatalf("expected ok check output, got:\n%s", out)
	}
	if strings.Contains(out, "HELLO") {
		t.Fatalf("nomi check executed main:\n%s", out)
	}
	entries, _ := os.ReadDir(cacheRoot)
	if len(entries) == 0 {
		t.Errorf("expected at least one cache entry after FFI check; got 0")
	}
	_ = projectRoot
}

func TestCheckCommand_DirectoryPureNomi(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "main.nomi"), `import helper

fn main() {
  helper.touch()
}
`)
	mustWrite(t, filepath.Join(dir, "helper.nomi"), `pub fn touch() {}
`)
	out, err := runNomi(t, cacheRoot, "check", dir)
	if err != nil {
		t.Fatalf("nomi check dir: %v\nstdout/stderr:\n%s", err, out)
	}
	for _, want := range []string{
		"ok " + filepath.Join(dir, "main.nomi"),
		"ok " + filepath.Join(dir, "helper.nomi") + " (through main.nomi)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("nomi check output lacks %q:\n%s", want, out)
		}
	}
	entries, _ := os.ReadDir(cacheRoot)
	if len(entries) != 0 {
		t.Errorf("expected empty cache root for pure directory check; got %d entries", len(entries))
	}
}

// A directory check judges a file another file in the directory imports
// through that importer, as `nomi run` loads it. A helper that reads an
// application field is valid only under an entry whose boot returns the
// application, so it is not checked on its own; an error in a helper is still
// reported, by its importer's check; and a file nothing imports is checked on
// its own.
func TestCheckCommand_DirectoryChecksHelpersThroughImporters(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "settings.nomi"), `pub struct Settings {
    context: Context

    tag: String
}
`)
	mustWrite(t, filepath.Join(dir, "reader.nomi"), `import settings.Settings

pub fn read(): String {
    Settings.tag
}
`)
	mustWrite(t, filepath.Join(dir, "main.nomi"), `import {
    std/io
    reader
    settings.Settings
}

pub fn boot(): Settings {
    Settings{context: Context.root(), tag: "root"}
}

fn main() {
    io.print(reader.read())
}
`)
	// On its own, reader.nomi is an error: no entry boot returns Settings.
	if out, err := runNomi(t, cacheRoot, "check", filepath.Join(dir, "reader.nomi")); err == nil {
		t.Fatalf("nomi check passed reader.nomi on its own; the directory case below proves nothing:\n%s", out)
	}
	out, err := runNomi(t, cacheRoot, "check", dir)
	if err != nil {
		t.Fatalf("nomi check dir: %v\n%s", err, out)
	}
	for _, want := range []string{
		"ok " + filepath.Join(dir, "main.nomi"),
		"ok " + filepath.Join(dir, "reader.nomi") + " (through main.nomi)",
		"ok " + filepath.Join(dir, "settings.nomi") + " (through main.nomi)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("nomi check output lacks %q:\n%s", want, out)
		}
	}

	// A type error in the helper is its importer's failure, located in the
	// helper.
	mustWrite(t, filepath.Join(dir, "reader.nomi"), `import settings.Settings

pub fn read(): String {
    Settings.tag + 1
}
`)
	// A file nothing imports is checked on its own.
	mustWrite(t, filepath.Join(dir, "orphan.nomi"), `fn lonely(): Int {
    "not an int"
}
`)
	out, err = runNomi(t, cacheRoot, "check", dir)
	if err == nil {
		t.Fatalf("nomi check passed a directory with a type error in a helper:\n%s", out)
	}
	for _, want := range []string{
		"FAIL " + filepath.Join(dir, "main.nomi"),
		"reader.nomi:4:",
		"FAIL " + filepath.Join(dir, "orphan.nomi"),
	} {
		if !strings.Contains(out, want) {
			t.Errorf("nomi check output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, filepath.Join(dir, "reader.nomi")+" (through") {
		t.Errorf("nomi check reported reader.nomi ok through an importer that failed:\n%s", out)
	}
}

// `nomi check` takes test files: a directory's test files are checked with
// its other files, and each case is lowered as `nomi test` lowers it, so a
// type error in a case and a case `nomi test` would report BLOCKED are both
// errors. A module's test files are checked with its entries.
func TestCheckCommand_TestFiles(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "main.nomi"), `fn main() {}
`)
	mustWrite(t, filepath.Join(dir, "passes_test.nomi"), `test "passes" {
  assert 1 + 1 == 2
}
`)
	// skip_odd's `continue` cannot be lowered inside a callback; the case
	// type-checks, so its error is the lowering's.
	mustWrite(t, filepath.Join(dir, "lowering_test.nomi"), `fn skip_odd(n: Int): Int {
  if n % 2 == 1 {
    continue
  }
  n
}

test "evens" {
  assert Iter.map([1, 2], |x| skip_odd(x)) |> Iter.to_list() == [2]
}
`)
	mustWrite(t, filepath.Join(dir, "types_test.nomi"), `test "collect" {
  dbg Result.collect([])
  assert True
}
`)
	out, err := runNomi(t, cacheRoot, "check", dir)
	if err == nil {
		t.Fatalf("nomi check passed a directory with a type error and a blocked case:\n%s", out)
	}
	for _, want := range []string{
		"ok " + filepath.Join(dir, "main.nomi"),
		"ok " + filepath.Join(dir, "passes_test.nomi"),
		"FAIL " + filepath.Join(dir, "lowering_test.nomi"),
		"this call to `skip_odd` is not supported yet, so test \"evens\" cannot run",
		"FAIL " + filepath.Join(dir, "types_test.nomi"),
		"the element type of `[]` is not determined",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("nomi check output lacks %q:\n%s", want, out)
		}
	}

	// One test file, checked on its own, exits as an ordinary file does.
	if out, err := runNomi(t, cacheRoot, "check", filepath.Join(dir, "passes_test.nomi")); err != nil {
		t.Fatalf("nomi check on a passing test file: %v\n%s", err, out)
	}

	// A module checks its test files with its entries.
	mustWrite(t, filepath.Join(dir, "nomi.toml"), "[module]\nname = \"checktests\"\nentry_points = [\"main\"]\n")
	out, _ = runNomi(t, cacheRoot, "check", dir)
	for _, want := range []string{
		"ok " + filepath.Join(dir, "main.nomi"),
		"ok " + filepath.Join(dir, "passes_test.nomi"),
		"FAIL " + filepath.Join(dir, "types_test.nomi"),
	} {
		if !strings.Contains(out, want) {
			t.Errorf("nomi check on a module lacks %q:\n%s", want, out)
		}
	}
}

func TestCheckCommand_DirectoryFFI(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	projectRoot, _ := stageEchoFixture(t)
	out, err := runNomi(t, cacheRoot, "check", projectRoot)
	if err != nil {
		t.Fatalf("nomi check dir: %v\nstdout/stderr:\n%s", err, out)
	}
	if !strings.Contains(out, "main.nomi") {
		t.Fatalf("expected FFI entry in check output, got:\n%s", out)
	}
	if strings.Contains(out, "HELLO") {
		t.Fatalf("nomi check executed main:\n%s", out)
	}
	entries, _ := os.ReadDir(cacheRoot)
	if len(entries) == 0 {
		t.Errorf("expected at least one cache entry after FFI directory check; got 0")
	}
}

func TestCheckCommand_GoBindings(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	root := repoRoot(t)
	dir := filepath.Join(root, "cmd", "nomi", "testdata", "go_bindings")
	out, err := runNomi(t, cacheRoot, "check", dir)
	if err != nil {
		t.Fatalf("nomi check testdata/go_bindings: %v\nstdout/stderr:\n%s", err, out)
	}
	if !strings.Contains(out, "ok ") || !strings.Contains(out, "main.nomi") {
		t.Fatalf("expected ok check output, got:\n%s", out)
	}
}

func TestCheckCommand_FFIWrapperCompileErrorPointsAtNomiSelector(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	projectRoot, entry := stageBuildTagMissingSelectorFixture(t)
	out, err := runNomi(t, cacheRoot, "check", projectRoot)
	if err == nil {
		t.Fatalf("expected wrapper compile failure, got success:\n%s", out)
	}
	assertBadBuildTagSelectorDiagnostic(t, out, entry)
}

func TestCheckCommand_SQLLiterals(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	root := repoRoot(t)
	dir := filepath.Join(root, "cmd", "nomi", "testdata", "ffi_modules", "sql_literals")
	out, err := runNomi(t, cacheRoot, "check", dir)
	if err != nil {
		t.Fatalf("nomi check testdata/ffi_modules/sql_literals: %v\nstdout/stderr:\n%s", err, out)
	}
	if !strings.Contains(out, "ok ") || !strings.Contains(out, "sql.nomi") {
		t.Fatalf("expected ok check output, got:\n%s", out)
	}
}

// TestCheckCommand_TasksProject checks a module that imports two sibling
// modules through go.mod replaces: one with a Go binding (sqlite) and one
// with a typed literal (sql_literals).
func TestCheckCommand_TasksProject(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	root := repoRoot(t)
	dir := filepath.Join(root, "cmd", "nomi", "testdata", "ffi_modules", "tasks")
	out, err := runNomi(t, cacheRoot, "check", dir)
	if err != nil {
		t.Fatalf("nomi check testdata/ffi_modules/tasks: %v\nstdout/stderr:\n%s", err, out)
	}
	if !strings.Contains(out, "ok ") || !strings.Contains(out, "main.nomi") {
		t.Fatalf("expected ok check output, got:\n%s", out)
	}
}

func TestTestCommand_FFIRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	projectRoot, _ := stageEchoFixture(t)
	testPath := filepath.Join(projectRoot, "main_test.nomi")
	mustWrite(t, testPath, `gopkg "echobinding" as ffi

fn echo_upper(s: String): String go ffi.EchoUpper

test "source-declared Go binding is available in tests" {
  assert echo_upper("hello") == "HELLO"
}
`)
	out, err := runNomi(t, cacheRoot, "test", testPath)
	if err != nil {
		t.Fatalf("nomi test: %v\nstdout/stderr:\n%s", err, out)
	}
	if !strings.Contains(out, "main_test.nomi :: source-declared Go binding is available in tests") {
		t.Fatalf("expected FFI test case output, got:\n%s", out)
	}
	if !strings.Contains(out, "1 passed, 0 failed") {
		t.Fatalf("expected passing summary, got:\n%s", out)
	}
	entries, _ := os.ReadDir(cacheRoot)
	if len(entries) == 0 {
		t.Errorf("expected at least one cache entry after FFI test; got 0")
	}
}

func TestTestCommand_GoBindings(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	root := repoRoot(t)
	dir := filepath.Join(root, "cmd", "nomi", "testdata", "go_bindings")
	out, err := runNomi(t, cacheRoot, "test", dir)
	if err != nil {
		t.Fatalf("nomi test testdata/go_bindings: %v\nstdout/stderr:\n%s", err, out)
	}
	for _, want := range []string{
		"query parsing returns a map of lists",
		"URL parsing returns a Nomi struct",
		"bytes cross to Go and back",
		"hex decoding returns a Result",
		"8 passed, 0 failed",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in go-bindings test output, got:\n%s", want, out)
		}
	}
}

func TestTestCommand_StdlibRegex(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	dir := t.TempDir()
	testPath := filepath.Join(dir, "main_test.nomi")
	mustWrite(t, testPath, "import {\n"+
		"  std/regex.Regex\n"+
		"}\n\n"+
		"test \"regex literals compile and match\" {\n"+
		"  digits = try Regex`\\d+`\n"+
		"  assert Regex.pattern(digits) == `\\d+`\n"+
		"  assert Regex.match?(digits, \"room 42\")\n"+
		"  refute Regex.match?(digits, \"room\")\n"+
		"}\n\n"+
		"test \"regex search helpers return Nomi shapes\" {\n"+
		"  digits = try Regex`\\d+`\n"+
		"  assert Regex.find(digits, \"a12b34\") == Some(\"12\")\n"+
		"  assert Regex.find(digits, \"abc\") == None\n"+
		"  assert Regex.find_all(digits, \"a12b34\") == [\"12\", \"34\"]\n"+
		"  assert Regex.replace_all(digits, \"a12b34\", \"#\") == \"a#b#\"\n"+
		"}\n\n"+
		"test \"invalid regex literals return Err\" {\n"+
		"  assert Err(_msg) = Regex`[`\n"+
		"}\n")
	out, err := runNomi(t, cacheRoot, "test", testPath)
	if err != nil {
		t.Fatalf("nomi test stdlib regex: %v\nstdout/stderr:\n%s", err, out)
	}
	for _, want := range []string{
		"regex literals compile and match",
		"regex search helpers return Nomi shapes",
		"invalid regex literals return Err",
		"3 passed, 0 failed",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in stdlib regex test output, got:\n%s", want, out)
		}
	}

	mainPath := filepath.Join(dir, "main.nomi")
	mustWrite(t, mainPath, "import {\n"+
		"  std/io\n"+
		"  std/regex.Regex\n"+
		"}\n\n"+
		"fn main(): Result<Unit, String> {\n"+
		"  digits = try Regex`\\d+`\n"+
		"  io.inspect(digits)\n"+
		"  Ok(Unit)\n"+
		"}\n")
	out, err = runNomi(t, cacheRoot, "run", mainPath)
	if err != nil {
		t.Fatalf("nomi run stdlib regex inspect: %v\nstdout/stderr:\n%s", err, out)
	}
	if !strings.Contains(out, "Regex`\\d+`") {
		t.Fatalf("expected regex Debug output, got:\n%s", out)
	}
}

func TestTestCommand_SQLLiterals(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	root := repoRoot(t)
	dir := filepath.Join(root, "cmd", "nomi", "testdata", "ffi_modules", "sql_literals")
	out, err := runNomi(t, cacheRoot, "test", dir)
	if err != nil {
		t.Fatalf("nomi test testdata/ffi_modules/sql_literals: %v\nstdout/stderr:\n%s", err, out)
	}
	for _, want := range []string{
		"a literal with no slots is its own SQL",
		"each slot becomes a placeholder and an argument",
		"3 passed, 0 failed",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in sql-literals test output, got:\n%s", want, out)
		}
	}
}

// TestTestCommand_FFIModuleProjects runs the tests of the sqlite binding
// (a Go module with a third-party dependency) and of the tasks module that
// imports it and sql_literals, each through the generated FFI wrapper.
func TestTestCommand_FFIModuleProjects(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	root := repoRoot(t)
	for _, c := range []struct {
		dir  string
		want []string
	}{
		{"sqlite", []string{
			"a NULL column reads as an empty string",
			"closing twice is harmless, and a closed database answers errors",
			"5 passed, 0 failed",
		}},
		{"tasks", []string{
			"added tasks are numbered from 1 and stamped by the clock",
			"a failed save is an error",
			"9 passed, 0 failed",
		}},
	} {
		dir := filepath.Join(root, "cmd", "nomi", "testdata", "ffi_modules", c.dir)
		out, err := runNomi(t, cacheRoot, "test", dir)
		if err != nil {
			t.Fatalf("nomi test testdata/ffi_modules/%s: %v\nstdout/stderr:\n%s", c.dir, err, out)
		}
		for _, want := range c.want {
			if !strings.Contains(out, want) {
				t.Fatalf("expected %q in %s test output, got:\n%s", want, c.dir, out)
			}
		}
	}
}

func TestTestCommand_RunsGroups(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	dir := t.TempDir()
	testPath := filepath.Join(dir, "math_test.nomi")
	mustWrite(t, testPath, `tests "math" {
  setup {base: 2}

  test "assertions", {base} {
    assert base + 2 == 4
    refute base == 5
  }

}

tests "sibling" {
  setup (2, 3)

  test "setup is bound by a tuple pattern", (base, extra) {
    assert base + extra == 5
  }
}
`)
	out, err := runNomi(t, cacheRoot, "test", dir)
	if err != nil {
		t.Fatalf("nomi test: %v\nstdout/stderr:\n%s", err, out)
	}
	if !strings.Contains(out, "ok ") || !strings.Contains(out, "math / assertions") || !strings.Contains(out, "sibling / setup is bound by a tuple pattern") {
		t.Fatalf("unexpected nomi test output:\n%s", out)
	}
	if !strings.Contains(out, "2 passed, 0 failed") {
		t.Fatalf("expected passing summary, got:\n%s", out)
	}
}

func TestTestCommand_DefaultsNomiEnvToTest(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	dir := t.TempDir()
	testPath := filepath.Join(dir, "env_test.nomi")
	mustWrite(t, testPath, `import std/compiler

test "nomi test defaults NOMI_ENV" {
  assert compiler.run(
    """
    import {
      std/io
    }

    struct Env {
  context: Context

      name: String
    }

    fn boot(startup: Startup): Env {
      name =
        case Map.get(startup.env, "NOMI_ENV") {
          Some(value) -> value
          None -> "missing"
        }

      Env{context: Context.root(), name}
    }

    fn main() {
      io.print(Env.name)
    }
    """
  ) == Ok("test\n")
}
`)
	cmd := exec.Command(nomiBin, "test", dir)
	cmd.Env = append(withoutEnv(os.Environ(), "NOMI_ENV"), "NOMI_FFIRUN_CACHE_ROOT="+cacheRoot)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		t.Fatalf("nomi test: %v\nstdout/stderr:\n%s", err, buf.String())
	}
	if !strings.Contains(buf.String(), "1 passed, 0 failed") {
		t.Fatalf("expected passing summary, got:\n%s", buf.String())
	}
}

func TestTestCommand_FileTestsSeePrivateMembers(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	dir := t.TempDir()
	testPath := filepath.Join(dir, "module_test.nomi")
	mustWrite(t, testPath, `fn secret_add(a: Int, b: Int): Int {
  a + b
}

test "uses private helper" {
  assert secret_add(2, 3) == 5
}
`)
	out, err := runNomi(t, cacheRoot, "test", dir)
	if err != nil {
		t.Fatalf("nomi test: %v\nstdout/stderr:\n%s", err, out)
	}
	if !strings.Contains(out, "uses private helper") || !strings.Contains(out, "1 passed, 0 failed") {
		t.Fatalf("unexpected module test output:\n%s", out)
	}
}

func TestTestCommand_FailureExitsNonZero(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	dir := t.TempDir()
	testPath := filepath.Join(dir, "failure_test.nomi")
	mustWrite(t, testPath, `test "fails" {
  assert 1 == 2
}
`)
	out, err := runNomi(t, cacheRoot, "test", dir)
	if err == nil {
		t.Fatalf("expected nomi test to fail, output:\n%s", out)
	}
	if !strings.Contains(out, "FAIL ") || !strings.Contains(out, "fails") || !strings.Contains(out, "assertion failed") {
		t.Fatalf("unexpected failure output:\n%s", out)
	}
	if !strings.Contains(out, "assert 1 == 2") {
		t.Fatalf("expected assertion expression, got:\n%s", out)
	}
	if strings.Contains(out, "actual: False") {
		t.Fatalf("did not expect redundant boolean actual value, got:\n%s", out)
	}
	if !strings.Contains(out, "0 passed, 1 failed") {
		t.Fatalf("expected failing summary, got:\n%s", out)
	}
}

// TestTestCommand_FinalCheckFailureResultFailsTest: a test body whose final
// value is `Err(AssertionFailure)`, and whose final statement is not itself an
// assertion, fails with that failure's report. A final check that passes
// passes the case, and a final `if` or `case` decides by its arm's value.
func TestTestCommand_FinalCheckFailureResultFailsTest(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	dir := t.TempDir()
	testPath := filepath.Join(dir, "failure_test.nomi")
	mustWrite(t, testPath, `import std/testing

test "fails" {
  assert True
  testing.check(1 == 2)
}

test "passes" {
  assert True
  testing.check(1 == 1)
}

test "arm fails" {
  assert True
  if 1 == 1 {
    testing.check(2 == 3)
  } else {
    testing.check(True)
  }
}

test "arm passes" {
  assert True
  case 3 {
    3 -> testing.check(3 == 3)
    _ -> testing.check(False)
  }
}
`)
	out, err := runNomi(t, cacheRoot, "test", dir)
	if err == nil {
		t.Fatalf("expected nomi test to fail, output:\n%s", out)
	}
	if !strings.Contains(out, "line 5: check failed") ||
		!strings.Contains(out, "check 1 == 2") ||
		!strings.Contains(out, "line 16: check failed") ||
		!strings.Contains(out, "check 2 == 3") ||
		!strings.Contains(out, ":: passes") ||
		!strings.Contains(out, ":: arm passes") ||
		!strings.Contains(out, "2 passed, 2 failed") {
		t.Fatalf("expected final check failure to fail the test, got:\n%s", out)
	}
}

func TestTestCommand_FinalRefuteCheckFailureResultPasses(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	dir := t.TempDir()
	testPath := filepath.Join(dir, "refute_check_test.nomi")
	mustWrite(t, testPath, `import std/testing

test "refute check failure" {
  refute testing.check(1 == 2)
}
`)
	out, err := runNomi(t, cacheRoot, "test", dir)
	if err != nil {
		t.Fatalf("nomi test: %v\nstdout/stderr:\n%s", err, out)
	}
	if !strings.Contains(out, "ok ") ||
		!strings.Contains(out, "refute check failure") ||
		!strings.Contains(out, "1 passed, 0 failed") {
		t.Fatalf("expected final refute check failure to pass, got:\n%s", out)
	}
}

func TestTestCommand_FinalDomainErrDoesNotFailTest(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	dir := t.TempDir()
	testPath := filepath.Join(dir, "domain_err_test.nomi")
	mustWrite(t, testPath, `test "domain err is just a value" {
  assert True
  Err("boom")
}
`)
	out, err := runNomi(t, cacheRoot, "test", dir)
	if err != nil {
		t.Fatalf("nomi test: %v\nstdout/stderr:\n%s", err, out)
	}
	if !strings.Contains(out, "ok ") ||
		!strings.Contains(out, "domain err is just a value") ||
		!strings.Contains(out, "1 passed, 0 failed") {
		t.Fatalf("expected final domain Err to be treated as an ordinary value, got:\n%s", out)
	}
}

func TestTestCommand_RefuteFailureReportsExpression(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	dir := t.TempDir()
	testPath := filepath.Join(dir, "failure_test.nomi")
	mustWrite(t, testPath, `test "fails" {
  refute 1 == 1
}
`)
	out, err := runNomi(t, cacheRoot, "test", dir)
	if err == nil {
		t.Fatalf("expected nomi test to fail, output:\n%s", out)
	}
	if !strings.Contains(out, "FAIL ") || !strings.Contains(out, "fails") || !strings.Contains(out, "refute failed") {
		t.Fatalf("unexpected refute failure output:\n%s", out)
	}
	if !strings.Contains(out, "refute 1 == 1") {
		t.Fatalf("expected refute expression, got:\n%s", out)
	}
	if !strings.Contains(out, "0 passed, 1 failed") {
		t.Fatalf("expected failing summary, got:\n%s", out)
	}
}

func TestTestCommand_AssertionFailureShowsBindingContext(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	dir := t.TempDir()
	testPath := filepath.Join(dir, "failure_test.nomi")
	mustWrite(t, testPath, `test "fails" {
  passes = 1 == 2
  assert passes
}
`)
	out, err := runNomi(t, cacheRoot, "test", dir)
	if err == nil {
		t.Fatalf("expected nomi test to fail, output:\n%s", out)
	}
	if !strings.Contains(out, "assert passes") ||
		!strings.Contains(out, "defined as:") ||
		!strings.Contains(out, "1 == 2") {
		t.Fatalf("expected assertion binding context, got:\n%s", out)
	}
	if strings.Contains(out, "where passes = False") {
		t.Fatalf("did not expect redundant binding bool value, got:\n%s", out)
	}
}

func TestTestCommand_TryEarlyReturnShowsSourceAndValue(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	dir := t.TempDir()
	testPath := filepath.Join(dir, "failure_test.nomi")
	mustWrite(t, testPath, `fn parse_id(_text: String): Result<Int, String> {
  Err("expected a numeric id")
}

test "formats parsed id" {
  id = try parse_id("42x")
  assert id == 42
}
`)
	out, err := runNomi(t, cacheRoot, "test", dir)
	if err == nil {
		t.Fatalf("expected nomi test to fail, output:\n%s", out)
	}
	if !strings.Contains(out, "line 6: test returned early") ||
		!strings.Contains(out, `try parse_id("42x")`) ||
		!strings.Contains(out, "returned:") ||
		!strings.Contains(out, `Err("expected a numeric id")`) {
		t.Fatalf("expected try early-return context, got:\n%s", out)
	}
}

func TestTestCommand_AssertionFailureShowsPipelineValues(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	dir := t.TempDir()
	testPath := filepath.Join(dir, "failure_test.nomi")
	mustWrite(t, testPath, `

test "fails" {
  passes =
    [1, 2, 3]
    |> Iter.any?(|n| n == 4)

  assert passes
}
`)
	out, err := runNomi(t, cacheRoot, "test", dir)
	if err == nil {
		t.Fatalf("expected nomi test to fail, output:\n%s", out)
	}
	if !strings.Contains(out, "pipeline values:") ||
		!strings.Contains(out, "[1, 2, 3]") ||
		!strings.Contains(out, "Iter.any?") ||
		!strings.Contains(out, "= False") {
		t.Fatalf("expected assertion pipeline values, got:\n%s", out)
	}
}

func TestTestCommand_AssertionFailureShowsComparisonValues(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	dir := t.TempDir()
	testPath := filepath.Join(dir, "failure_test.nomi")
	mustWrite(t, testPath, `test "fails" {
  a = 4
  assert 12345 == a
}
`)
	out, err := runNomi(t, cacheRoot, "test", dir)
	if err == nil {
		t.Fatalf("expected nomi test to fail, output:\n%s", out)
	}
	if !strings.Contains(out, "values:") ||
		!strings.Contains(out, "12345") ||
		!strings.Contains(out, "a") ||
		!strings.Contains(out, "= 4") {
		t.Fatalf("expected assertion comparison values, got:\n%s", out)
	}
}

func TestTestCommand_AssertionFailureSuppressesLiteralCallArgsInComparison(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	dir := t.TempDir()
	testPath := filepath.Join(dir, "failure_test.nomi")
	mustWrite(t, testPath, `

fn parse_id(s: String): Result<Int, String> {
  case String.to_int(s) {
    Some(id) -> Ok(id)
    None -> Err("expected a numeric id")
  }
}

test "fails" {
  assert parse_id("43") == Ok(42)
}
`)
	out, err := runNomi(t, cacheRoot, "test", dir)
	if err == nil {
		t.Fatalf("expected nomi test to fail, output:\n%s", out)
	}
	if !strings.Contains(out, "parse_id(\"43\")") ||
		!strings.Contains(out, "= Ok(43)") {
		t.Fatalf("expected comparison values, got:\n%s", out)
	}
	if strings.Contains(out, "\"43\"\n        = \"43\"") ||
		strings.Contains(out, "\n      42\n        = 42") ||
		strings.Contains(out, "Ok(42)\n        = Ok(42)") {
		t.Fatalf("did not expect obvious expected values in comparison report, got:\n%s", out)
	}
}

func TestTestCommand_AssertionFailureShowsLogicalDecider(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	dir := t.TempDir()
	testPath := filepath.Join(dir, "failure_test.nomi")
	mustWrite(t, testPath, `

fn parse_id(s: String): Result<Int, String> {
  case String.to_int(s) {
    Some(id) -> Ok(id)
    None -> Err("expected a numeric id")
  }
}

test "fails" {
  a = False
  assert parse_id("42") == Ok(42) and a
}
`)
	out, err := runNomi(t, cacheRoot, "test", dir)
	if err == nil {
		t.Fatalf("expected nomi test to fail, output:\n%s", out)
	}
	if !strings.Contains(out, "parse_id(\"42\")") ||
		!strings.Contains(out, "= Ok(42)") ||
		!strings.Contains(out, "a") ||
		!strings.Contains(out, "= False") {
		t.Fatalf("expected logical decider values, got:\n%s", out)
	}
	if strings.Contains(out, "parse_id(\"42\") == Ok(42)\n        = True") {
		t.Fatalf("did not expect redundant whole comparison value, got:\n%s", out)
	}
}

func TestTestCommand_AssertionFailureShowsOrPredicateDecider(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	dir := t.TempDir()
	testPath := filepath.Join(dir, "failure_test.nomi")
	mustWrite(t, testPath, `

test "fails" {
  left = False
  assert left or Maybe.some?(None)
}
`)
	out, err := runNomi(t, cacheRoot, "test", dir)
	if err == nil {
		t.Fatalf("expected nomi test to fail, output:\n%s", out)
	}
	if !strings.Contains(out, "left") ||
		!strings.Contains(out, "= False") ||
		!strings.Contains(out, "Maybe.some?(None)\n        = False") {
		t.Fatalf("expected or decider values, got:\n%s", out)
	}
	if strings.Contains(out, "None\n        = None") {
		t.Fatalf("did not expect self-evident variant arg value, got:\n%s", out)
	}
}

func TestTestCommand_AssertionFailureShowsNegatedPredicateInput(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	dir := t.TempDir()
	testPath := filepath.Join(dir, "failure_test.nomi")
	mustWrite(t, testPath, `

test "fails" {
  value: Maybe<Int> = None
  assert !Maybe.none?(value)
}
`)
	out, err := runNomi(t, cacheRoot, "test", dir)
	if err == nil {
		t.Fatalf("expected nomi test to fail, output:\n%s", out)
	}
	if !strings.Contains(out, "value") ||
		!strings.Contains(out, "= None") {
		t.Fatalf("expected negated predicate input value, got:\n%s", out)
	}
}

func TestTestCommand_RefuteFailureShowsPredicateInput(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	dir := t.TempDir()
	testPath := filepath.Join(dir, "failure_test.nomi")
	mustWrite(t, testPath, `

test "fails" {
  outcome = Ok(42)
  refute Result.ok?(outcome)
}
`)
	out, err := runNomi(t, cacheRoot, "test", dir)
	if err == nil {
		t.Fatalf("expected nomi test to fail, output:\n%s", out)
	}
	if !strings.Contains(out, "refute failed") ||
		!strings.Contains(out, "outcome") ||
		!strings.Contains(out, "= Ok(42)") {
		t.Fatalf("expected refute predicate input value, got:\n%s", out)
	}
}

func TestTestCommand_AssertionFailureShowsPredicateCallValues(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	dir := t.TempDir()
	testPath := filepath.Join(dir, "failure_test.nomi")
	mustWrite(t, testPath, `

test "fails" {
  body = "created"
  assert String.contains?(body, "ok")
}
`)
	out, err := runNomi(t, cacheRoot, "test", dir)
	if err == nil {
		t.Fatalf("expected nomi test to fail, output:\n%s", out)
	}
	if !strings.Contains(out, "values:") ||
		!strings.Contains(out, "body") ||
		!strings.Contains(out, "= \"created\"") ||
		!strings.Contains(out, "\"ok\"") ||
		!strings.Contains(out, "= \"ok\"") {
		t.Fatalf("expected assertion predicate call values, got:\n%s", out)
	}
}

func TestTestCommand_AssertionFailureDoesNotLeakCallbackComparisons(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	dir := t.TempDir()
	testPath := filepath.Join(dir, "failure_test.nomi")
	mustWrite(t, testPath, `

test "fails" {
  items = [1, 2, 3]
  assert Iter.any?(items, |n| n == 4)
}
`)
	out, err := runNomi(t, cacheRoot, "test", dir)
	if err == nil {
		t.Fatalf("expected nomi test to fail, output:\n%s", out)
	}
	if !strings.Contains(out, "values:") ||
		!strings.Contains(out, "items") ||
		!strings.Contains(out, "= [1, 2, 3]") {
		t.Fatalf("expected direct predicate input value, got:\n%s", out)
	}
	if strings.Contains(out, "n\n        = 1") ||
		strings.Contains(out, "n\n        = 2") ||
		strings.Contains(out, "n\n        = 3") {
		t.Fatalf("did not expect callback-local values in assertion report, got:\n%s", out)
	}
}

func TestTestCommand_AssertionFailureShowsNestedCallValues(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	dir := t.TempDir()
	testPath := filepath.Join(dir, "failure_test.nomi")
	mustWrite(t, testPath, `

test "fails" {
  items = [1, 2]
  expected = 3
  assert Iter.count(items) == expected
}
`)
	out, err := runNomi(t, cacheRoot, "test", dir)
	if err == nil {
		t.Fatalf("expected nomi test to fail, output:\n%s", out)
	}
	if !strings.Contains(out, "values:") ||
		!strings.Contains(out, "items") ||
		!strings.Contains(out, "= [1, 2]") ||
		!strings.Contains(out, "Iter.count(items)") ||
		!strings.Contains(out, "= 2") ||
		!strings.Contains(out, "expected") ||
		!strings.Contains(out, "= 3") {
		t.Fatalf("expected assertion nested call values, got:\n%s", out)
	}
}

func TestTestCommand_AssertionFailureShowsCaseScrutineeValue(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	dir := t.TempDir()
	testPath := filepath.Join(dir, "failure_test.nomi")
	mustWrite(t, testPath, `

fn parse_id(s: String): Result<Int, String> {
  case String.to_int(s) {
    Some(id) -> Ok(id)
    None -> Err("expected a numeric id")
  }
}

test "fails" {
  assert case parse_id("42") {
    Ok(id) -> id == 43
    Err(_) -> False
  }
}
`)
	out, err := runNomi(t, cacheRoot, "test", dir)
	if err == nil {
		t.Fatalf("expected nomi test to fail, output:\n%s", out)
	}
	if !strings.Contains(out, "values:") ||
		!strings.Contains(out, "parse_id(\"42\")") ||
		!strings.Contains(out, "= Ok(42)") ||
		!strings.Contains(out, "id") ||
		!strings.Contains(out, "= 42") ||
		!strings.Contains(out, "43") {
		t.Fatalf("expected assertion case scrutinee value, got:\n%s", out)
	}
}

func TestTestCommand_LineFilterRunsSingleTest(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	dir := t.TempDir()
	testPath := filepath.Join(dir, "line_filter_test.nomi")
	mustWrite(t, testPath, `test "first" {
  assert True
}

test "second" {
  assert True
}
`)
	out, err := runNomi(t, cacheRoot, "test", testPath, "--line", "5")
	if err != nil {
		t.Fatalf("nomi test: %v\nstdout/stderr:\n%s", err, out)
	}
	if strings.Contains(out, "first") || !strings.Contains(out, "second") {
		t.Fatalf("expected only second test to run, got:\n%s", out)
	}
	if !strings.Contains(out, "1 passed, 0 failed") {
		t.Fatalf("expected single passing summary, got:\n%s", out)
	}
}

func TestTestCommand_LineFilterRunsStdlibAttachedTest(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	intPath := filepath.Join(repoRoot(t), "std", "int.nomi")
	line := lineNumberContaining(t, intPath, "//! assert Int.to_string(42)")
	out, err := runNomi(t, cacheRoot, "test", intPath, "--line", strconv.Itoa(line))
	if err != nil {
		t.Fatalf("nomi test: %v\nstdout/stderr:\n%s", err, out)
	}
	if !strings.Contains(out, "to_string //! test") {
		t.Fatalf("expected stdlib attached assert to run, got:\n%s", out)
	}
	if !strings.Contains(out, "1 passed, 0 failed") {
		t.Fatalf("expected single passing summary, got:\n%s", out)
	}
}

// copyStdTree copies this checkout's std/ into dir/std and answers
// the copy's path.
func copyStdTree(t *testing.T, dir string) string {
	t.Helper()
	src := filepath.Join(repoRoot(t), "std")
	dst := filepath.Join(dir, "std")
	err := filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatalf("copy std: %v", err)
	}
	return dst
}

// replaceInFile replaces the one occurrence of old in path with new.
func replaceInFile(t *testing.T, path, old, new string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(data), old); n != 1 {
		t.Fatalf("%s holds %d copies of %q, want 1", path, n, old)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(data), old, new, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
}

// runNomiWithStd is runNomi with this binary's standard library at stdPath
// (NOMI_STD_PATH), or with no override when stdPath is empty.
func runNomiWithStd(t *testing.T, cacheRoot, stdPath string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(nomiBin, args...)
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "NOMI_STD_PATH=") {
			env = append(env, kv)
		}
	}
	env = append(env, "NOMI_FFIRUN_CACHE_ROOT="+cacheRoot)
	if stdPath != "" {
		env = append(env, "NOMI_STD_PATH="+stdPath)
	}
	cmd.Env = env
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}

// TestTestCommand_StdlibFileIsTestedAsItIsOnDisk: `nomi test <std file>` tests
// the file on disk, not the copy embedded in the binary. An edited `//!` prompt
// is the prompt that runs, and an edited function body is the body the
// module's prompts call. The standard library here is a copy of this
// checkout's, named by NOMI_STD_PATH, so the edits touch no tracked file.
func TestTestCommand_StdlibFileIsTestedAsItIsOnDisk(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	stdPath := copyStdTree(t, t.TempDir())
	calendar := filepath.Join(stdPath, "calendar.nomi")
	const prompt = "//! assert try Time.new(8, 30) == try Time\"08:30:00\""
	line := strconv.Itoa(lineNumberContaining(t, calendar, prompt))

	out, err := runNomiWithStd(t, cacheRoot, stdPath, "test", calendar, "--line", line)
	if err != nil || !strings.Contains(out, "1 passed, 0 failed") {
		t.Fatalf("the unedited copy's prompt does not pass (err %v):\n%s", err, out)
	}

	t.Run("edited prompt", func(t *testing.T) {
		replaceInFile(t, calendar, "Time.new(8, 30) ==", "Time.new(8, 31) ==")
		defer replaceInFile(t, calendar, "Time.new(8, 31) ==", "Time.new(8, 30) ==")
		out, err := runNomiWithStd(t, cacheRoot, stdPath, "test", calendar, "--line", line)
		if err == nil {
			t.Fatalf("an edited prompt that no longer holds passed, so the embedded one ran:\n%s", out)
		}
		if !strings.Contains(out, "assert try Time.new(8, 31) == try Time\"08:30:00\"") ||
			!strings.Contains(out, "0 passed, 1 failed") {
			t.Fatalf("the edited prompt did not fail as itself:\n%s", out)
		}
	})

	t.Run("edited body", func(t *testing.T) {
		const body = "Ok(Time{hour, minute, second, nanosecond})"
		replaceInFile(t, calendar, body, "Ok(Time{hour, minute: minute + 1, second, nanosecond})")
		defer replaceInFile(t, calendar, "Ok(Time{hour, minute: minute + 1, second, nanosecond})", body)
		out, err := runNomiWithStd(t, cacheRoot, stdPath, "test", calendar, "--line", line)
		if err == nil {
			t.Fatalf("a prompt calling an edited body passed, so the embedded body ran:\n%s", out)
		}
		if !strings.Contains(out, "= Time{hour: 8, minute: 31, nanosecond: 0, second: 0}") ||
			!strings.Contains(out, "0 passed, 1 failed") {
			t.Fatalf("the prompt did not fail on the edited body's answer:\n%s", out)
		}
	})
}

// TestStdlibFileOfAnotherCheckoutIsRefused: a file in another checkout's
// std/ is not this binary's stdlib module. `nomi test`, `nomi check` and
// `nomi run` refuse it with one line naming both standard libraries, rather
// than analyzing it as a user project (which reported the manifest's reserved
// name and `Ordering` against `Ordering`).
func TestStdlibFileOfAnotherCheckoutIsRefused(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	other := copyStdTree(t, t.TempDir())
	file := filepath.Join(other, "duration.nomi")
	if resolved, err := filepath.EvalSymlinks(file); err == nil {
		file = resolved
	}
	for _, cmd := range []string{"test", "check", "run"} {
		t.Run(cmd, func(t *testing.T) {
			out, err := runNomiWithStd(t, cacheRoot, "", cmd, file)
			if err == nil {
				t.Fatalf("nomi %s on another checkout's std file exited 0:\n%s", cmd, out)
			}
			want := file + " is in the standard library of another Nomi checkout (" + filepath.Dir(filepath.Dir(file)) + ")"
			if !strings.Contains(out, want) || !strings.Contains(out, "this nomi's standard library is ") {
				t.Fatalf("nomi %s was not refused as another checkout's std file; want %q in:\n%s", cmd, want, out)
			}
			if strings.Contains(out, "is reserved for the standard library") || strings.Contains(out, "Ordering") {
				t.Fatalf("nomi %s analyzed the file as a user project:\n%s", cmd, out)
			}
		})
	}
}

func TestTestCommand_LineFilterRunsStdlibImplAttachedTests(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	durationPath := filepath.Join(repoRoot(t), "std", "duration.nomi")
	cases := []struct {
		name       string
		lineNeedle string
		outNeedle  string
	}{
		{
			name:       "extern impl",
			lineNeedle: "//! assert Duration.to_string(Duration.seconds(5))",
			outNeedle:  "impl / to_string //! test",
		},
		{
			name:       "nomi impl",
			lineNeedle: "//! assert Duration.inspect(Duration.seconds(5))",
			outNeedle:  "impl / inspect //! test",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			line := lineNumberContaining(t, durationPath, tc.lineNeedle)
			out, err := runNomi(t, cacheRoot, "test", durationPath, "--line", strconv.Itoa(line))
			if err != nil {
				t.Fatalf("nomi test: %v\nstdout/stderr:\n%s", err, out)
			}
			if !strings.Contains(out, tc.outNeedle) {
				t.Fatalf("expected stdlib impl attached assert to run, got:\n%s", out)
			}
			if !strings.Contains(out, "1 passed, 0 failed") {
				t.Fatalf("expected single passing summary, got:\n%s", out)
			}
		})
	}
}

func lineNumberContaining(t *testing.T, path, needle string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	for i, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, needle) {
			return i + 1
		}
	}
	t.Fatalf("missing %q in %s", needle, path)
	return 0
}

func TestTestCommand_LineFilterDoesNotRunUnselectedAttachedTests(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	dir := t.TempDir()
	testPath := filepath.Join(dir, "attached_line_filter_test.nomi")
	mustWrite(t, testPath, `//! assert answer() == 41
fn answer(): Int {
  42
}

//! assert False
fn unrelated(): Int {
  0
}
`)
	out, err := runNomi(t, cacheRoot, "test", testPath, "--line", "1")
	if err == nil {
		t.Fatalf("expected selected attached assertion to fail, got:\n%s", out)
	}
	if strings.Contains(out, "unrelated") {
		t.Fatalf("unselected attached test should not run:\n%s", out)
	}
	if !strings.Contains(out, "answer //! test line 1") ||
		!strings.Contains(out, "assertion failed") {
		t.Fatalf("expected selected attached assertion failure, got:\n%s", out)
	}
}

func TestTestCommand_LineFilterRunsAttachedTestFromBodyLine(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	dir := t.TempDir()
	testPath := filepath.Join(dir, "attached_body_line_filter_test.nomi")
	mustWrite(t, testPath, `//! value = answer()
//! assert value == 41
fn answer(): Int {
  42
}

//! assert False
fn unrelated(): Int {
  0
}
`)
	out, err := runNomi(t, cacheRoot, "test", testPath, "--line", "2")
	if err == nil {
		t.Fatalf("expected selected attached assertion to fail, got:\n%s", out)
	}
	if strings.Contains(out, "unrelated") {
		t.Fatalf("unselected attached test should not run:\n%s", out)
	}
	if !strings.Contains(out, "answer //! test lines 1-2") ||
		!strings.Contains(out, "assertion failed") {
		t.Fatalf("expected selected attached assertion failure, got:\n%s", out)
	}
}

func TestTestCommand_LineFilterChecksUnselectedTestBodies(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	dir := t.TempDir()
	testPath := filepath.Join(dir, "line_filter_checks_unselected_test.nomi")
	mustWrite(t, testPath, `test "selected" {
  assert True
}

test "unselected bad analysis" {
  Missing.value
  assert True
}
`)
	out, err := runNomi(t, cacheRoot, "test", testPath, "--line", "1")
	if err == nil {
		t.Fatalf("expected unselected analysis error to fail, got:\n%s", out)
	}
	if !strings.Contains(out, "Missing") ||
		!strings.Contains(out, "error: undefined type or variant 'Missing'\n --> "+testPath+":6:3\n") {
		t.Fatalf("expected unselected analysis error, got:\n%s", out)
	}
}

func TestTestCommand_LineFilterUsesImportsFromUnselectedTests(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	dir := t.TempDir()
	testPath := filepath.Join(dir, "line_filter_imports_test.nomi")
	mustWrite(t, testPath, `import {
  std/comparable.Direction
  std/comparable.Ordering.{Less}
}

test "selected" {
  assert Display.to_string(Direction.Ascending) == "Ascending"
}

test "unselected uses Less" {
  assert Less == Less
}
`)
	out, err := runNomi(t, cacheRoot, "test", testPath, "--line", "6")
	if err != nil {
		t.Fatalf("nomi test: %v\nstdout/stderr:\n%s", err, out)
	}
	if strings.Contains(out, "unused") {
		t.Fatalf("imports used by unselected tests should count as used:\n%s", out)
	}
	if !strings.Contains(out, "selected") ||
		strings.Contains(out, "unselected uses Less") ||
		!strings.Contains(out, "1 passed, 0 failed") {
		t.Fatalf("expected only selected test to run, got:\n%s", out)
	}
}

func TestTestCommand_DuplicateTestNamesFail(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	dir := t.TempDir()
	testPath := filepath.Join(dir, "duplicates_test.nomi")
	mustWrite(t, testPath, `test "same" {
  assert True
}

test "same" {
  assert True
}
`)
	out, err := runNomi(t, cacheRoot, "test", testPath)
	if err == nil {
		t.Fatalf("expected nomi test to fail, output:\n%s", out)
	}
	if !strings.Contains(out, `duplicate test name "same"`) ||
		!strings.Contains(out, "line 5") ||
		!strings.Contains(out, "first declared at line 1") ||
		!strings.Contains(out, "0 passed, 1 failed") {
		t.Fatalf("expected duplicate test name failure, got:\n%s", out)
	}
}

func TestTestCommand_LineFilterRunsGroup(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	dir := t.TempDir()
	testPath := filepath.Join(dir, "line_filter_group_test.nomi")
	mustWrite(t, testPath, `tests "math" {
  test "one" {
    assert True
  }

  test "two" {
    assert True
  }
}
`)
	out, err := runNomi(t, cacheRoot, "test", testPath, "--line", "0")
	if err != nil {
		t.Fatalf("nomi test: %v\nstdout/stderr:\n%s", err, out)
	}
	if !strings.Contains(out, "math / one") || !strings.Contains(out, "math / two") {
		t.Fatalf("expected both tests-block children to run, got:\n%s", out)
	}
	if !strings.Contains(out, "2 passed, 0 failed") {
		t.Fatalf("expected tests-block passing summary, got:\n%s", out)
	}
}

func TestTestCommand_SetupDeferCleansUpPerTest(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	dir := t.TempDir()
	testPath := filepath.Join(dir, "setup_defer_test.nomi")
	mustWrite(t, testPath, `import std/io

struct Conn {
  label: String
}

fn close(conn: Conn): Unit {
  io.print("close " + conn.label)
}

tests "setup cleanup" {
  setup {
    conn = Conn{label: "db"}
    defer close(conn)
    {conn: conn}
  }

  test "one", {conn} {
    io.print("body one")
    assert conn.label == "db"
  }

  test "two", {conn} {
    io.print("body two")
    assert conn.label == "db"
  }
}

`)
	out, err := runNomi(t, cacheRoot, "test", dir)
	if err != nil {
		t.Fatalf("nomi test: %v\nstdout/stderr:\n%s", err, out)
	}
	bodyOne := strings.Index(out, "body one")
	closeOne := strings.Index(out, "close db")
	bodyTwo := strings.Index(out, "body two")
	closeTwo := strings.LastIndex(out, "close db")
	if bodyOne < 0 || closeOne < 0 || bodyTwo < 0 || closeTwo < 0 {
		t.Fatalf("expected setup defer body/cleanup output, got:\n%s", out)
	}
	if !(bodyOne < closeOne && closeOne < bodyTwo && bodyTwo < closeTwo) {
		t.Fatalf("expected setup defer cleanup after each test body, got:\n%s", out)
	}
	if strings.Count(out, "close db") != 2 {
		t.Fatalf("expected deferred call to run once per test, got:\n%s", out)
	}
	if !strings.Contains(out, "2 passed, 0 failed") {
		t.Fatalf("expected passing summary, got:\n%s", out)
	}
}

func TestTestCommand_RunsTestsInOrdinaryModuleFile(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	dir := t.TempDir()
	testPath := filepath.Join(dir, "main.nomi")
	mustWrite(t, testPath, `fn main() {
  Unit
}

test "inline module test" {
  assert 1 + 1 == 2
}
`)
	out, err := runNomi(t, cacheRoot, "test", testPath)
	if err != nil {
		t.Fatalf("nomi test: %v\nstdout/stderr:\n%s", err, out)
	}
	if !strings.Contains(out, "main.nomi :: inline module test") {
		t.Fatalf("expected inline test to run from ordinary module file, got:\n%s", out)
	}
	if !strings.Contains(out, "1 passed, 0 failed") {
		t.Fatalf("expected passing summary, got:\n%s", out)
	}
}

func TestTestCommand_DirectoryDiscoversTestsInOrdinaryModules(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "math.nomi"), `fn add(a: Int, b: Int): Int {
  a + b
}

test "inline add test" {
  assert add(2, 3) == 5
}
`)
	mustWrite(t, filepath.Join(dir, "helper.nomi"), `fn helper(): Int {
  42
}
`)
	out, err := runNomi(t, cacheRoot, "test", dir)
	if err != nil {
		t.Fatalf("nomi test: %v\nstdout/stderr:\n%s", err, out)
	}
	if !strings.Contains(out, "math.nomi :: inline add test") {
		t.Fatalf("expected directory test discovery to include ordinary module tests, got:\n%s", out)
	}
	if strings.Contains(out, "helper.nomi") {
		t.Fatalf("expected ordinary module without tests to be ignored, got:\n%s", out)
	}
	if !strings.Contains(out, "1 passed, 0 failed") {
		t.Fatalf("expected passing summary, got:\n%s", out)
	}
}

// TestTestCommand_VMCorpus runs `nomi test tests` on the VM. Every case must
// pass: a blocked case is one the VM cannot run (its reason is the BLOCKED
// line of the same run), and a failing case is a wrong answer. The run must
// also cover every case corpus.expect declares, so a case the VM's plan drops
// cannot pass unnoticed. TestExpectation_Corpus checks each case's output.
func TestTestCommand_VMCorpus(t *testing.T) {
	checkVMTestRun(t, "tests", "corpus")
}

// TestTestCommand_VMStdlib runs `nomi test std` on the VM: every stdlib
// module's `//!` prompt cases, built in the module's own scope against the
// cached stdlib lowering (internal/irbuild/stdtests.go), under
// TestTestCommand_VMCorpus's rule. TestExpectation_Stdlib checks each case's
// output.
func TestTestCommand_VMStdlib(t *testing.T) {
	checkVMTestRun(t, "std", "stdlib")
}

// checkVMTestRun runs `nomi test` over the repository directory dir and
// requires no failed or blocked case and exactly the case count the
// expectation population declares.
func checkVMTestRun(t *testing.T, dir, population string) {
	t.Helper()
	if testing.Short() {
		t.Skip("integration; -short")
	}
	root := repoRoot(t)
	out, _ := runNomi(t, t.TempDir(), "test", filepath.Join(root, dir))
	summary := regexp.MustCompile(`test result: \S+\. (\d+) passed, (\d+) failed(?:, (\d+) blocked)?`).
		FindStringSubmatch(termcolor.StripANSI(out))
	if summary == nil {
		t.Fatalf("no summary line in `nomi test %s`:\n%s", dir, out)
	}
	count := func(s string) int {
		n, _ := strconv.Atoi(s)
		return n
	}
	passed, failed, blocked := count(summary[1]), count(summary[2]), count(summary[3])
	if failed != 0 || blocked != 0 {
		t.Fatalf("`nomi test %s` reads %d passed, %d failed, %d blocked; every case must pass.\n\n"+
			"BLOCKED lines and failures:\n%s", dir, passed, failed, blocked, vmCorpusNonPassing(out))
	}
	recorded, err := expectation.Load(population)
	if err != nil {
		t.Fatal(err)
	}
	if total := recorded.TotalCases(); passed == 0 || passed != total {
		t.Fatalf("`nomi test %s` passed %d cases and %s.expect declares %d; "+
			"regenerate the expectations if cases were added or removed, "+
			"otherwise a case is missing from the VM's plan", dir, passed, population, total)
	}
}

// vmCorpusNonPassing is the run's output minus its `ok` lines.
func vmCorpusNonPassing(out string) string {
	var keep []string
	for _, line := range strings.Split(termcolor.StripANSI(out), "\n") {
		if !strings.HasPrefix(line, "ok ") {
			keep = append(keep, line)
		}
	}
	return strings.Join(keep, "\n")
}

func TestColorizeNomiLinePreservesStringLiteralSource(t *testing.T) {
	t.Setenv("NOMI_COLOR", "always")
	line := `String.contains?(diagnostic.message, "type 'List' has no member 'empty'x")`
	got := termcolor.StripANSI(termcolor.NomiLineFor(os.Stdout, line))
	if got != line {
		t.Fatalf("colorized line did not preserve source\nwant: %q\n got: %q", line, got)
	}
}

func TestRunCommand_TestFileErrors(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	dir := t.TempDir()
	testPath := filepath.Join(dir, "example_test.nomi")
	mustWrite(t, testPath, `test "passes" {
  assert 1 == 1
}
`)
	out, err := runNomi(t, cacheRoot, "run", testPath)
	if err == nil {
		t.Fatalf("expected nomi run on *_test.nomi to fail, output:\n%s", out)
	}
	if !strings.Contains(out, "is a test file") || !strings.Contains(out, "nomi test") {
		t.Fatalf("expected test-file guidance, got:\n%s", out)
	}
}

// `nomi run` on a file with no `fn main` fails with the text `nomi build`
// gives, rather than running nothing and exiting 0. A file that declares
// tests is pointed at `nomi test`.
func TestRunCommand_NoMainErrors(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	dir := t.TempDir()
	library := filepath.Join(dir, "library.nomi")
	mustWrite(t, library, "pub fn one(): Int {\n  1\n}\n")
	suite := filepath.Join(dir, "suite.nomi")
	mustWrite(t, suite, "fn one(): Int {\n  1\n}\n\ntest \"one\" {\n  assert one() == 1\n}\n")

	out, err := runNomi(t, cacheRoot, "run", library)
	if err == nil {
		t.Fatalf("nomi run on a file with no fn main exited 0:\n%s", out)
	}
	want := "nomi run: " + library + " declares no `fn main`, so there is no program to run\n"
	if out != want {
		t.Fatalf("nomi run on a file with no fn main printed:\n%s\nwant:\n%s", out, want)
	}

	out, err = runNomi(t, cacheRoot, "run", suite)
	if err == nil {
		t.Fatalf("nomi run on a file of tests exited 0:\n%s", out)
	}
	want = "nomi run: " + suite + " declares no `fn main`, so there is no program to run; " +
		"to run its tests, use `nomi test " + suite + "`\n"
	if out != want {
		t.Fatalf("nomi run on a file of tests printed:\n%s\nwant:\n%s", out, want)
	}

	// `nomi check` does not need a main.
	if out, err := runNomi(t, cacheRoot, "check", library); err != nil {
		t.Fatalf("nomi check on a library file: %v\n%s", err, out)
	}
}

func TestRunCommand_AssertionsOutsideTestsRun(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	dir := t.TempDir()
	path := filepath.Join(dir, "main.nomi")
	mustWrite(t, path, `import std/assertions.AssertionFailure

fn main(): Result<Unit, AssertionFailure> {
  assert 1 + 1 == 2
  refute 1 == 2
  Ok(Unit)
}
`)
	out, err := runNomi(t, cacheRoot, "run", path)
	if err != nil {
		t.Fatalf("nomi run: %v\nstdout/stderr:\n%s", err, out)
	}
}

func TestRunCommand_AssertionFailureShowsComparisonValues(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	dir := t.TempDir()
	path := filepath.Join(dir, "main.nomi")
	mustWrite(t, path, `import std/assertions.AssertionFailure

fn main(): Result<Unit, AssertionFailure> {
  a = 4
  assert 12345 == a
  Ok(Unit)
}
`)
	out, err := runNomi(t, cacheRoot, "run", path)
	if err == nil {
		t.Fatalf("expected nomi run to fail, output:\n%s", out)
	}
	if !strings.Contains(out, "assert 12345 == a") ||
		!strings.Contains(out, "values:") ||
		!strings.Contains(out, "= 4") {
		t.Fatalf("expected assertion comparison values, got:\n%s", out)
	}
}

func TestRunCommand_AssertionFailureSuppressesObviousLiteralValues(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	dir := t.TempDir()
	path := filepath.Join(dir, "main.nomi")
	mustWrite(t, path, `import std/assertions.AssertionFailure

fn main(): Result<Unit, AssertionFailure> {
  assert 1 == 2
  Ok(Unit)
}
`)
	out, err := runNomi(t, cacheRoot, "run", path)
	if err == nil {
		t.Fatalf("expected nomi run to fail, output:\n%s", out)
	}
	if !strings.Contains(out, "assert 1 == 2") {
		t.Fatalf("expected assertion expression, got:\n%s", out)
	}
	if strings.Contains(out, "values:") {
		t.Fatalf("expected obvious literal values to be suppressed, got:\n%s", out)
	}
}

func TestRunCommand_AssertionFailureCanBeHandledAsResult(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	dir := t.TempDir()
	path := filepath.Join(dir, "main.nomi")
	mustWrite(t, path, `import std/assertions.AssertionFailure
import std/io

fn validate(): Result<Unit, AssertionFailure> {
  assert 1 == 2
  Ok(Unit)
}

fn main() {
  case validate() {
    Err(_) -> io.print("caught")
    Ok(_) -> io.print("ok")
  }
}
`)
	out, err := runNomi(t, cacheRoot, "run", path)
	if err != nil {
		t.Fatalf("nomi run: %v\nstdout/stderr:\n%s", err, out)
	}
	if !strings.Contains(out, "caught") || strings.Contains(out, "ok") {
		t.Fatalf("expected handled assertion result, got:\n%s", out)
	}
}

// runNomi invokes the test-built nomi binary with the supplied args.
// Captures stdout + stderr together so a diagnostic written to
// stderr (the normal case for load-time errors) is visible. Sets
// NOMI_FFIRUN_CACHE_ROOT so tests get isolated cache dirs.
func runNomi(t *testing.T, cacheRoot string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(nomiBin, args...)
	cmd.Env = append(os.Environ(), "NOMI_FFIRUN_CACHE_ROOT="+cacheRoot)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}

// runNomiInDir is runNomi with a working directory and stdin, for a program
// that reads commands and writes files beside itself.
func runNomiInDir(t *testing.T, cacheRoot, dir, stdin string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(nomiBin, args...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Env = append(os.Environ(), "NOMI_FFIRUN_CACHE_ROOT="+cacheRoot)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}

func runBinary(path string, args ...string) (string, error) {
	cmd := exec.Command(path, args...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}

// runBinaryInDir is runBinary with a working directory, for a binary whose
// output depends on files it reads or writes beside itself — the same reason
// runNomiInDir exists next to runNomi.
func runBinaryInDir(dir, path string, args ...string) (string, error) {
	cmd := exec.Command(path, args...)
	cmd.Dir = dir
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}

func withoutEnv(env []string, name string) []string {
	prefix := name + "="
	filtered := make([]string, 0, len(env))
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

// stageEchoFixture creates a tempdir project that requires an `echobinding`
// Go module and declares a Nomi binding to EchoUpper. Returns
// (projectRoot, entryPath).
func stageEchoFixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	nomiRoot := repoRoot(t)

	bindingDir := filepath.Join(root, "echobinding")
	if err := os.MkdirAll(bindingDir, 0o755); err != nil {
		t.Fatalf("mkdir echobinding: %v", err)
	}
	bindingSrc := `package echobinding

import "strings"

func EchoUpper(s string) string {
	return strings.ToUpper(s)
}
`
	mustWrite(t, filepath.Join(bindingDir, "echo.go"), bindingSrc)
	mustWrite(t, filepath.Join(bindingDir, "go.mod"), `module echobinding

go 1.26.3
`)

	mustWrite(t, filepath.Join(root, "go.mod"), fmt.Sprintf(`module testproject

go 1.26.3

require echobinding v0.0.0

require github.com/nomi-language/nomi v0.0.0

replace echobinding => ./echobinding

replace github.com/nomi-language/nomi => %s
`, nomiRoot))
	mustWrite(t, filepath.Join(root, "nomi.toml"), `[module]
name = "testproject"
entry_points = ["main"]
`)

	entry := filepath.Join(root, "main.nomi")
	mustWrite(t, entry, `import std/io

gopkg "echobinding" as ffi

fn echo_upper(s: String): String go ffi.EchoUpper

fn main() {
  io.print(echo_upper("hello"))
}
	`)
	return root, entry
}

// stageCrossModuleSqliteFixture stages a two-Nomi-module project that holds
// testdata/ffi_modules/sqlite's opaque connection handle in its own struct,
// naming it through the module qualifier.
//
// # Why it points at the repo's real binding rather than a stub
//
// The claim under test is that a user's Go-backed handle crosses a Nomi module
// boundary, and the module graph is half of that. testdata/ffi_modules/sqlite is a
// separate Go module whose package imports `modernc.org/sqlite`, so the
// generated artifact's go.mod must require and replace it by absolute path and
// resolve that module's own requires — which renderGoMod does by mirroring the
// user module's replaces absolutized. A synthetic binding with no third-party
// dependency would exercise the mirror and skip the graph.
//
// # Every module-qualified position the fixture uses, and why each is here
//
//	pub struct Store { conn: sqlite.Conn }              the field
//	pub fn open_raw(...): Result<sqlite.Conn, String>   the return under a prelude
//	                                                    enum, tasks/store.nomi's shape
//
// Those are the two spellings this test covers, reproduced in a fixture it
// owns so a change to the tasks fixture cannot silently stop covering them.
//
// # No lambdas, and that is deliberate rather than stylistic
//
// `Iter.map` over `List<List<String>>` with an inferred lambda parameter hits
// an unrelated open gap (`lambda parameter with no inferred type`, then `Iter
// over an unlowered source`). Writing
// the row walk with `Iter.at` and `case` keeps the test aimed at the handle
// instead of failing for a reason it does not measure.
func stageCrossModuleSqliteFixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	nomiRoot := repoRoot(t)
	binding := filepath.Join(nomiRoot, "cmd", "nomi", "testdata", "ffi_modules", "sqlite")

	mustWrite(t, filepath.Join(root, "go.mod"), fmt.Sprintf(`module sqlitedriver

go 1.26.3

require (
	nomi v0.0.0
	sqlite v0.0.0
)

replace github.com/nomi-language/nomi => %s

replace sqlite => %s
`, nomiRoot, binding))
	mustWrite(t, filepath.Join(root, "nomi.toml"), `[module]
name = "sqlite_driver"
entry_points = ["main"]
`)

	mustWrite(t, filepath.Join(root, "store.nomi"), `import {
  sqlite/sqlite
}

pub struct Store {
  conn: sqlite.Conn
}

pub fn open_raw(path: String): Result<sqlite.Conn, String> {
  sqlite.open(path)
}

pub fn open_store(path: String): Result<Store, String> {
  conn = try open_raw(path)
  case sqlite.exec(conn, "CREATE TABLE IF NOT EXISTS notes (id INTEGER PRIMARY KEY AUTOINCREMENT, title TEXT NOT NULL)", []) {
    Err(e) -> {
      sqlite.close(conn)
      Err("create table: ${e}")
    }
    Ok(_) -> Ok(Store{conn})
  }
}

pub fn add(store: Store, title: String): Result<Unit, String> {
  sqlite.exec(store.conn, "INSERT INTO notes (title) VALUES (?)", [title])
}

pub fn cell(store: Store, query: String): Result<String, String> {
  case sqlite.query_all(store.conn, query, []) {
    Err(e) -> Err(e)
    Ok(rows) ->
      case Iter.at(rows, 0) {
        None -> Err("no rows")
        Some(row) ->
          case Iter.at(row, 0) {
            None -> Err("no columns")
            Some(c) -> Ok(c)
          }
      }
  }
}

pub fn close_store(store: Store): Unit {
  sqlite.close(store.conn)
}
`)

	entry := filepath.Join(root, "main.nomi")
	mustWrite(t, entry, `import {
  std/io
  store
}

fn main(): Result<Unit, String> {
  s = try store.open_store("driver.db")
  _ = try store.add(s, "first")
  _ = try store.add(s, "second")
  count = try store.cell(s, "SELECT COUNT(*) FROM notes")
  head = try store.cell(s, "SELECT title FROM notes ORDER BY id LIMIT 1")
  io.print("count ${count}")
  io.print("head ${head}")
  store.close_store(s)
  Ok(Unit)
}
`)
	return root, entry
}

func stageByteFixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	nomiRoot := repoRoot(t)

	bindingDir := filepath.Join(root, "bytebinding")
	if err := os.MkdirAll(bindingDir, 0o755); err != nil {
		t.Fatalf("mkdir bytebinding: %v", err)
	}
	mustWrite(t, filepath.Join(bindingDir, "bytes.go"), `package bytebinding

func AddPrefix(data []byte) []byte {
	out := append([]byte("go:"), data...)
	return out
}

func First(data []byte) byte {
	if len(data) == 0 {
		return 0
	}
	return data[0]
}

func Singleton(b byte) []byte {
	return []byte{b}
}
`)
	mustWrite(t, filepath.Join(bindingDir, "go.mod"), `module bytebinding

go 1.26.3
`)
	mustWrite(t, filepath.Join(root, "go.mod"), fmt.Sprintf(`module byteproject

go 1.26.3

require bytebinding v0.0.0

require github.com/nomi-language/nomi v0.0.0

replace bytebinding => ./bytebinding

replace github.com/nomi-language/nomi => %s
`, nomiRoot))
	mustWrite(t, filepath.Join(root, "nomi.toml"), `[module]
name = "byteproject"
entry_points = ["main"]
`)

	entry := filepath.Join(root, "main.nomi")
	mustWrite(t, entry, `import {
  std/io
}

gopkg "bytebinding" as ffi

fn add_prefix(data: Bytes): Bytes go ffi.AddPrefix

fn first(data: Bytes): Byte go ffi.First

fn singleton(b: Byte): Bytes go ffi.Singleton

fn main(): Result<Unit, String> {
  prefixed = add_prefix(String.to_bytes("hi"))
  text = try Bytes.to_string(prefixed)
  io.print(text)
  io.print(Byte.to_int(first(prefixed)))

  // Byte.from_int answers a Maybe, and try propagates into the enclosing
  // boundary's own kind -- a Result here -- so this one is a case, not a try.
  case Byte.from_int(65) {
    Some(a) -> io.print(try Bytes.to_string(singleton(a)))
    None -> io.print("not a byte")
  }

  Ok(Unit)
}
`)
	return root, entry
}

func stageMapFixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	nomiRoot := repoRoot(t)

	bindingDir := filepath.Join(root, "mapbinding")
	if err := os.MkdirAll(bindingDir, 0o755); err != nil {
		t.Fatalf("mkdir mapbinding: %v", err)
	}
	mustWrite(t, filepath.Join(bindingDir, "maps.go"), `package mapbinding

func Counts() map[string]int64 {
	return map[string]int64{"apples": 2, "oranges": 3}
}

func AddTotals(items map[string]int64) map[string]int64 {
	total := int64(0)
	for _, n := range items {
		total += n
	}
	out := make(map[string]int64, len(items)+1)
	for k, v := range items {
		out[k] = v
	}
	out["total"] = total
	return out
}
`)
	mustWrite(t, filepath.Join(bindingDir, "go.mod"), `module mapbinding

go 1.26.3
`)
	mustWrite(t, filepath.Join(root, "go.mod"), fmt.Sprintf(`module mapproject

go 1.26.3

require mapbinding v0.0.0

require github.com/nomi-language/nomi v0.0.0

replace mapbinding => ./mapbinding

replace github.com/nomi-language/nomi => %s
`, nomiRoot))
	mustWrite(t, filepath.Join(root, "nomi.toml"), `[module]
name = "mapproject"
entry_points = ["main"]
`)

	entry := filepath.Join(root, "main.nomi")
	mustWrite(t, entry, `import {
  std/io
}

gopkg "mapbinding" as ffi

fn counts(): Map<String, Int> go ffi.Counts

fn add_totals(items: Map<String, Int>): Map<String, Int> go ffi.AddTotals

fn main() {
  counted = counts()
  apples = case Map.get(counted, "apples") {
    Some(value) -> value
    None -> 0
  }
  io.print(apples)

  with_total = add_totals(counted)
  total = case Map.get(with_total, "total") {
    Some(value) -> value
    None -> 0
  }
  io.print(total)
}
`)
	return root, entry
}

func stageNumericWidthFixture(t *testing.T, source string) (string, string) {
	t.Helper()
	root := t.TempDir()
	nomiRoot := repoRoot(t)

	bindingDir := filepath.Join(root, "widthbinding")
	if err := os.MkdirAll(bindingDir, 0o755); err != nil {
		t.Fatalf("mkdir widthbinding: %v", err)
	}
	mustWrite(t, filepath.Join(bindingDir, "widths.go"), `package widthbinding

func DoubleInt(n int) int {
	return n * 2
}

func AddUint32(n uint32) uint32 {
	return n + 1
}

func DoubleInt8(n int8) int8 {
	return n * 2
}
`)
	mustWrite(t, filepath.Join(bindingDir, "go.mod"), `module widthbinding

go 1.26.3
`)
	mustWrite(t, filepath.Join(root, "go.mod"), fmt.Sprintf(`module widthproject

go 1.26.3

require widthbinding v0.0.0

require github.com/nomi-language/nomi v0.0.0

replace widthbinding => ./widthbinding

replace github.com/nomi-language/nomi => %s
`, nomiRoot))
	mustWrite(t, filepath.Join(root, "nomi.toml"), `[module]
name = "widthproject"
entry_points = ["main"]
`)

	entry := filepath.Join(root, "main.nomi")
	mustWrite(t, entry, source)
	return root, entry
}

func stageMaybeOKFixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	nomiRoot := repoRoot(t)

	bindingDir := filepath.Join(root, "maybeokbinding")
	if err := os.MkdirAll(bindingDir, 0o755); err != nil {
		t.Fatalf("mkdir maybeokbinding: %v", err)
	}
	mustWrite(t, filepath.Join(bindingDir, "maybe.go"), `package maybeokbinding

import "strconv"

func Lookup(key string) (int64, bool) {
	if key == "present" {
		return 7, true
	}
	return 0, false
}

func LookupMaybe(key string) *int64 {
	value, ok := Lookup(key)
	if !ok {
		return nil
	}
	return &value
}

func Describe(value int64, ok bool) string {
	if !ok {
		return "missing"
	}
	return "yes:" + strconv.FormatInt(value, 10)
}

func DescribeMaybe(value *int64) string {
	if value == nil {
		return Describe(0, false)
	}
	return Describe(*value, true)
}
`)
	mustWrite(t, filepath.Join(bindingDir, "go.mod"), `module maybeokbinding

go 1.26.3
`)
	mustWrite(t, filepath.Join(root, "go.mod"), fmt.Sprintf(`module maybeokproject

go 1.26.3

require maybeokbinding v0.0.0

require github.com/nomi-language/nomi v0.0.0

replace maybeokbinding => ./maybeokbinding

replace github.com/nomi-language/nomi => %s
`, nomiRoot))
	mustWrite(t, filepath.Join(root, "nomi.toml"), `[module]
name = "maybeokproject"
entry_points = ["main"]
`)

	entry := filepath.Join(root, "main.nomi")
	mustWrite(t, entry, `import std/io

gopkg "maybeokbinding" as ffi

fn lookup(key: String): Maybe<Int> go ffi.LookupMaybe

fn describe(value: Maybe<Int>): String go ffi.DescribeMaybe

fn main() {
  present = lookup("present")
  case present {
    Some(n) -> io.print(n)
    None -> io.print(0)
  }

  io.print(describe(Some(9)))
  io.print(describe(None))
}
`)
	return root, entry
}

func stageResultFixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	nomiRoot := repoRoot(t)

	bindingDir := filepath.Join(root, "resultbinding")
	if err := os.MkdirAll(bindingDir, 0o755); err != nil {
		t.Fatalf("mkdir resultbinding: %v", err)
	}
	mustWrite(t, filepath.Join(bindingDir, "result.go"), `package resultbinding

import (
	"errors"
	"strconv"
)

func Parse(s string) (int64, error) {
	return strconv.ParseInt(s, 10, 64)
}

func ValidateNonEmpty(s string) error {
	if s == "" {
		return errors.New("empty")
	}
	return nil
}

func ParsePositive(s string) (int64, error) {
	n, err := Parse(s)
	if err != nil {
		return 0, err
	}
	if n < 0 {
		return 0, errors.New("negative")
	}
	return n, nil
}

func ValidateCustom(s string) error {
	if s == "" {
		return errors.New("blank")
	}
	return nil
}
`)
	mustWrite(t, filepath.Join(bindingDir, "go.mod"), `module resultbinding

go 1.26.3
`)
	mustWrite(t, filepath.Join(root, "go.mod"), fmt.Sprintf(`module resultproject

go 1.26.3

require resultbinding v0.0.0

require github.com/nomi-language/nomi v0.0.0

replace resultbinding => ./resultbinding

replace github.com/nomi-language/nomi => %s
`, nomiRoot))
	mustWrite(t, filepath.Join(root, "nomi.toml"), `[module]
name = "resultproject"
entry_points = ["main"]
`)

	entry := filepath.Join(root, "main.nomi")
	mustWrite(t, entry, `import std/io

gopkg "resultbinding" as ffi

fn parse_direct(s: String): Result<Int, String> go ffi.Parse

fn parse_positive(s: String): Result<Int, String> go ffi.ParsePositive

fn validate_nonempty(s: String): Result<Unit, String> go ffi.ValidateNonEmpty

fn validate_custom(s: String): Result<Unit, String> go ffi.ValidateCustom

fn main(): Result<Unit, String> {
  io.print(try parse_direct("123"))
  io.print(try parse_positive("5"))

  negative = case parse_positive("-1") {
    Ok(_) -> "ok"
    Err(message) -> message
  }
  io.print(negative)

  empty = case validate_nonempty("") {
    Ok(_) -> "ok"
    Err(message) -> message
  }
  io.print(empty)

  blank = case validate_custom("") {
    Ok(_) -> "ok"
    Err(message) -> message
  }
  io.print(blank)

  Ok(Unit)
}
`)
	return root, entry
}

func stageStructProjectionFixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	nomiRoot := repoRoot(t)

	bindingDir := filepath.Join(root, "structbinding")
	if err := os.MkdirAll(bindingDir, 0o755); err != nil {
		t.Fatalf("mkdir structbinding: %v", err)
	}
	mustWrite(t, filepath.Join(bindingDir, "structs.go"), `package structbinding

type Address struct {
	City string
}

type User struct {
	Name string
	Age  int64
}

type Profile struct {
	Name    string
	Age     int64
	Address Address
}

func LoadUser(name string) User {
	return User{Name: name, Age: 36}
}

func LoadProfile(name string) Profile {
	return Profile{
		Name: name,
		Age: 36,
		Address: Address{City: "London"},
	}
}

func FindProfile(name string) (Profile, error) {
	return Profile{
		Name: name,
		Age: 37,
		Address: Address{City: "Oxford"},
	}, nil
}

func Birthday(user User) User {
	user.Age += 1
	return user
}
`)
	mustWrite(t, filepath.Join(bindingDir, "go.mod"), `module structbinding

go 1.26.3
`)
	mustWrite(t, filepath.Join(root, "go.mod"), fmt.Sprintf(`module structproject

go 1.26.3

require structbinding v0.0.0

require github.com/nomi-language/nomi v0.0.0

replace structbinding => ./structbinding

replace github.com/nomi-language/nomi => %s
`, nomiRoot))
	mustWrite(t, filepath.Join(root, "nomi.toml"), `[module]
name = "structproject"
entry_points = ["main"]
`)

	entry := filepath.Join(root, "main.nomi")
	mustWrite(t, entry, `import std/io

gopkg "structbinding" as ffi

struct Address {
  city: String
}

derive Equatable for Address
derive Display for Address

struct User {
  name: String
  age: Int
}

struct Profile {
  name: String
  age: Int
  address: Address
}

derive Equatable for Profile

fn load_user(name: String): User go ffi.LoadUser

fn load_profile(name: String): Profile go ffi.LoadProfile

fn find_profile(name: String): Result<Profile, String> go ffi.FindProfile

fn birthday(user: User): User go ffi.Birthday

fn main() {
  user = load_user("Ada") |> birthday()
  profile = load_profile(user.name)
  found = find_profile(user.name)
  io.print(user.name)
  io.print(user.age)
  io.print(profile.address.city)
  case found {
    Ok(p) -> io.print(p.address.city)
    Err(e) -> io.print(e)
  }
  // A Go-returned struct must be indistinguishable from a Nomi-built
  // one: dispatch is keyed on the value's type identity, so a struct
  // marshaled without it compares structurally against a TypeName that
  // can never match and silently answers False. Nested struct fields
  // are checked too — stamping only the top level moves the same bug
  // one level down.
  io.print(profile == Profile{
    name: "Ada",
    age: 36,
    address: Address{city: "London"},
  })
  io.print(profile.address == Address{city: "London"})
  io.print(Display.to_string(profile.address))
}
`)
	return root, entry
}

func stageBuildTagMissingSelectorFixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	nomiRoot := repoRoot(t)

	bindingDir := filepath.Join(root, "hiddenbinding")
	mustWrite(t, filepath.Join(bindingDir, "present.go"), `package hiddenbinding

func Present() string {
	return "present"
}
`)
	mustWrite(t, filepath.Join(bindingDir, "hidden.go"), `//go:build nomi_never

package hiddenbinding

func Hidden() string {
	return "hidden"
}
`)
	mustWrite(t, filepath.Join(bindingDir, "go.mod"), `module hiddenbinding

go 1.26.3
`)

	mustWrite(t, filepath.Join(root, "go.mod"), fmt.Sprintf(`module badselector

go 1.26.3

require hiddenbinding v0.0.0

require github.com/nomi-language/nomi v0.0.0

replace hiddenbinding => ./hiddenbinding

replace github.com/nomi-language/nomi => %s
`, nomiRoot))
	mustWrite(t, filepath.Join(root, "nomi.toml"), `[module]
name = "badselector"
entry_points = ["main"]
`)
	entry := filepath.Join(root, "main.nomi")
	mustWrite(t, entry, `import std/io

gopkg "hiddenbinding" as ffi

fn hidden(): String go ffi.Hidden

fn main() {
  io.print(hidden())
}
`)
	return root, entry
}

func assertBadBuildTagSelectorDiagnostic(t *testing.T, out, entry string) {
	t.Helper()
	wantLoc := "main.nomi:5:"
	for _, want := range []string{wantLoc, "undefined: ffi.Hidden"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected wrapper diagnostic to contain %q, got:\n%s", want, out)
		}
	}
	for _, avoid := range []string{"# command-line-arguments", "main.go:"} {
		if strings.Contains(out, avoid) {
			t.Fatalf("expected wrapper diagnostic to hide %q, got:\n%s", avoid, out)
		}
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
