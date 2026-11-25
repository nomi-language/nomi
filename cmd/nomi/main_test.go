package main

import (
	"bytes"
	"fmt"
	"github.com/nomi-language/nomi/internal/expectation"
	"github.com/nomi-language/nomi/internal/irbuild"
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

// plantedEnv, set in a child's environment, makes the test binary run as
// `nomi` with every call to `planted_decline` declined by the IR builder
// (irbuild.PlantDeclineForTest), so a test about how `nomi run`, `nomi test`
// and `nomi check` report a blocked program needs no real lowering gap.
// withPlantedDecline sets it to 1. Any other value is the callee whose calls
// decline instead (withPlantedDeclineOf).
const plantedEnv = "NOMI_TEST_RUN_AS_NOMI_WITH_PLANTED_DECLINE"

// plantedReason is the builder's reason for a planted decline.
const plantedReason = "a planted decline"

// testBin is this test binary, which runs as `nomi` under plantedEnv.
var testBin string

func TestMain(m *testing.M) {
	if callee := os.Getenv(plantedEnv); callee != "" {
		if callee == "1" {
			callee = "planted_decline"
		}
		irbuild.PlantDeclineForTest(callee, plantedReason)
		main()
		os.Exit(0)
	}
	os.Exit(runTests(m))
}

// runTests builds `nomi` into a temporary directory, runs the tests and
// removes the directory, answering the exit code. It is apart from TestMain
// because os.Exit runs no deferred call: the directory holds the binary and
// every artifact its children cache, hundreds of megabytes a run.
func runTests(m *testing.M) int {
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "TestMain: %v\n", err)
		return 1
	}
	testBin = self
	tmp, err := os.MkdirTemp("", "nomi-test-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "TestMain: mkdir: %v\n", err)
		return 1
	}
	defer os.RemoveAll(tmp)
	// The package's directory, from this file's path: a fuzzing worker
	// starts in the directory its coordinator had moved to, the repository
	// root.
	_, file, _, _ := runtime.Caller(0)
	pkgDir := filepath.Dir(file)
	binPath := exe(filepath.Join(tmp, "nomi"))
	build := exec.Command("go", "build", "-o", binPath, ".")
	build.Dir = pkgDir
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "TestMain: build nomi: %v\n%s", err, out)
		return 1
	}
	// The CLI tests name corpus files, fixtures and scripts relative to the
	// repository root, which is also the module root.
	if err := os.Chdir(filepath.Join(pkgDir, "..", "..")); err != nil {
		fmt.Fprintf(os.Stderr, "TestMain: chdir to the repository root: %v\n", err)
		return 1
	}
	nomiBin = binPath
	// No child keeps its artifacts in the real cache: every child process
	// inherits these (artifactCacheEnv for one that inherits nothing).
	for _, kv := range artifactCacheEnv(tmp) {
		k, v, _ := strings.Cut(kv, "=")
		_ = os.Setenv(k, v)
	}
	return m.Run()
}

// exe is path with the executable suffix of this system: a test that
// builds an executable and runs it by path names it so, since Windows runs
// only a file whose name ends in .exe.
func exe(path string) string {
	if runtime.GOOS == "windows" {
		return path + ".exe"
	}
	return path
}

// artifactCacheEnv puts the artifact cache under dir, so a child never
// writes the user's. It holds the std bundle the built nomi caches
// (internal/artifactcache): the first child lowers std and writes it, the
// rest decode it.
func artifactCacheEnv(dir string) []string {
	return []string{"NOMI_ARTIFACT_CACHE=" + filepath.Join(dir, "artifacts")}
}

// withPlantedDecline makes nomiBin this test binary run as `nomi` with a
// planted decline (plantedEnv) until t ends.
func withPlantedDecline(t *testing.T) {
	withPlantedDeclineOf(t, "1")
}

// withPlantedDeclineOf is withPlantedDecline with every call to callee
// declined, a function std itself calls: the child keeps no std bundle,
// so it lowers std with the plant in place.
func withPlantedDeclineOf(t *testing.T, callee string) {
	t.Setenv(plantedEnv, callee)
	if callee != "1" {
		t.Setenv("NOMI_ARTIFACT_CACHE", "off")
	}
	bin := nomiBin
	nomiBin = testBin
	t.Cleanup(func() { nomiBin = bin })
}

// plantDecline makes the builder decline every call to `planted_decline`
// in this process until t ends (irbuild.PlantDeclineForTest), for a test of
// the in-process REPL.
func plantDecline(t *testing.T) {
	t.Cleanup(irbuild.PlantDeclineForTest("planted_decline", plantedReason))
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
	if err := os.WriteFile(entry, []byte(`import std::io
fn main()
  io.print("ok")
end
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
	// `fn x -> n + ` with the operand not yet typed, as mid-edit in an editor.
	mustWrite(t, entry, `import std::io

fn compute(n: Int) -> Int
  total = n * 2
  f = fn x -> n + 
end

fn main()
  io.print("hi")
end
`)
	// The diagnostic names the file, line and column, as compilers do.
	wantDiag := "error: unexpected `end`\n --> " + entry + ":6:1\n"

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
	mustWrite(t, fixed, `import std::io

fn compute(n: Int) -> Int
  f = fn x: Int -> n + x
  f(2)
end

fn main()
  io.print("hi")
end
`)
	if out, err := runNomi(t, t.TempDir(), "run", fixed); err != nil {
		t.Fatalf("the control fixture must run: %v\n%s", err, out)
	}
}

// TestRunCommand_PackageProjectEntryPoints runs both entry points of
// testdata/modules/todo: main, which its nomi.toml lists, and the tool
// tools/seed, which it need not list. Each imports a file under an
// `internal/` directory it may reach, and main also imports a sibling Nomi
// package, stringkit, which nomi.toml names by its directory.
func TestRunCommand_PackageProjectEntryPoints(t *testing.T) {
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
}

func TestCheckCommand_PureNomiSingleFile_FastPath(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	dir := t.TempDir()
	entry := filepath.Join(dir, "main.nomi")
	mustWrite(t, entry, `fn main() end
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
	mustWrite(t, entry, `import std::io

fn sign(x: Int) -> Int
  if x < 0 then
    return -1
  else if x == 0 then
    return 0
  else
    return 1
  end
end

fn main()
  io.print(sign(-5))
  io.print(sign(0))
  io.print(sign(5))
end
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

func TestCheckCommand_DirectoryPureNomi(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "main.nomi"), `import helper

fn main()
  helper.touch()
end
`)
	mustWrite(t, filepath.Join(dir, "helper.nomi"), `pub fn touch() end
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
	mustWrite(t, filepath.Join(dir, "settings.nomi"), `pub struct Settings
    context: Context

    tag: String
end
`)
	mustWrite(t, filepath.Join(dir, "reader.nomi"), `import settings.Settings

pub fn read() -> String
    Settings.tag
end
`)
	mustWrite(t, filepath.Join(dir, "main.nomi"), `import
    std::io
    reader
    settings.Settings
end

pub fn boot() -> Result<Settings, String>
    Ok(Settings{context: Context.root(), tag: "root"})
end

fn main()
    io.print(reader.read())
end
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

	// A type error in the helper fails its importer's check, and the
	// failure is labelled by the file that holds the error, not the importer.
	mustWrite(t, filepath.Join(dir, "reader.nomi"), `import settings.Settings

pub fn read() -> String
    Settings.tag + 1
end
`)
	// A file nothing imports is checked on its own.
	mustWrite(t, filepath.Join(dir, "orphan.nomi"), `fn lonely() -> Int
    "not an int"
end
`)
	out, err = runNomi(t, cacheRoot, "check", dir)
	if err == nil {
		t.Fatalf("nomi check passed a directory with a type error in a helper:\n%s", out)
	}
	for _, want := range []string{
		"FAIL " + filepath.Join(dir, "reader.nomi") + "\n",
		"reader.nomi:4:",
		"FAIL " + filepath.Join(dir, "orphan.nomi") + "\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("nomi check output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "FAIL "+filepath.Join(dir, "main.nomi")) {
		t.Errorf("nomi check labelled the importer, which holds no error:\n%s", out)
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
	withPlantedDecline(t)
	cacheRoot := t.TempDir()
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "main.nomi"), `fn main() end
`)
	mustWrite(t, filepath.Join(dir, "passes_test.nomi"), `test "passes"
  assert 1 + 1 == 2
end
`)
	// The case type-checks and declines at its call to `planted_decline`
	// (withPlantedDecline), so its error is the lowering's.
	mustWrite(t, filepath.Join(dir, "lowering_test.nomi"), `fn planted_decline(n: Int) -> Int
  n
end

test "evens"
  assert Iter.map([1, 2], fn x -> planted_decline(x)) |> Iter.to_list() == [1, 2]
end
`)
	mustWrite(t, filepath.Join(dir, "types_test.nomi"), `test "collect"
  dbg Result.collect([])
  assert True
end
`)
	out, err := runNomi(t, cacheRoot, "check", dir)
	if err == nil {
		t.Fatalf("nomi check passed a directory with a type error and a blocked case:\n%s", out)
	}
	for _, want := range []string{
		"ok " + filepath.Join(dir, "main.nomi"),
		"ok " + filepath.Join(dir, "passes_test.nomi"),
		"FAIL " + filepath.Join(dir, "lowering_test.nomi"),
		"this call to `planted_decline` is not supported yet, so test \"evens\" cannot run",
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
	mustWrite(t, filepath.Join(dir, "nomi.toml"), "[package]\nname = \"checktests\"\nentry_points = [\"main\"]\n")
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

func TestCheckCommand_SQLLiterals(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	root := repoRoot(t)
	dir := filepath.Join(root, "cmd", "nomi", "testdata", "modules", "sql_literals")
	out, err := runNomi(t, cacheRoot, "check", dir)
	if err != nil {
		t.Fatalf("nomi check testdata/modules/sql_literals: %v\nstdout/stderr:\n%s", err, out)
	}
	if !strings.Contains(out, "ok ") || !strings.Contains(out, "sql.nomi") {
		t.Fatalf("expected ok check output, got:\n%s", out)
	}
}

func TestTestCommand_StdlibRegex(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	dir := t.TempDir()
	testPath := filepath.Join(dir, "main_test.nomi")
	mustWrite(t, testPath, "import\n"+
		"  std::regex.Regex\n"+
		"end\n\n"+
		"test \"regex literals compile and match\"\n"+
		"  digits = Regex`\\d+`\n"+
		"  assert Regex.pattern(digits) == `\\d+`\n"+
		"  assert String.contains(\"room 42\", digits)\n"+
		"  refute String.contains(\"room\", digits)\n"+
		"end\n\n"+
		"test \"regex search helpers return Nomi shapes\"\n"+
		"  digits = Regex`\\d+`\n"+
		"  assert String.find(\"a12b34\", digits) == Some(\"12\")\n"+
		"  assert String.find(\"abc\", digits) == None\n"+
		"  assert String.find_all(\"a12b34\", digits) == [\"12\", \"34\"]\n"+
		"  assert String.replace(\"a12b34\", digits, \"#\") == \"a#b#\"\n"+
		"end\n\n"+
		"test \"an invalid pattern compiles to Err\"\n"+
		"  assert Err(_msg) = Regex.compile(\"[\")\n"+
		"end\n")
	out, err := runNomi(t, cacheRoot, "test", testPath)
	if err != nil {
		t.Fatalf("nomi test stdlib regex: %v\nstdout/stderr:\n%s", err, out)
	}
	for _, want := range []string{
		"regex literals compile and match",
		"regex search helpers return Nomi shapes",
		"an invalid pattern compiles to Err",
		"3 passed, 0 failed",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in stdlib regex test output, got:\n%s", want, out)
		}
	}

	mainPath := filepath.Join(dir, "main.nomi")
	mustWrite(t, mainPath, "import\n"+
		"  std::io\n"+
		"  std::regex.Regex\n"+
		"end\n\n"+
		"fn main() -> Result<Unit, String>\n"+
		"  digits = Regex`\\d+`\n"+
		"  io.inspect(digits)\n"+
		"  Ok(Unit)\n"+
		"end\n")
	out, err = runNomi(t, cacheRoot, "run", mainPath)
	if err != nil {
		t.Fatalf("nomi run stdlib regex inspect: %v\nstdout/stderr:\n%s", err, out)
	}
	if !strings.Contains(out, "Regex`\\d+`") {
		t.Fatalf("expected regex Debug output, got:\n%s", out)
	}

	// An invalid backtick pattern is a compile error, from `nomi check` and
	// from `nomi test` alike.
	badPath := filepath.Join(dir, "bad_test.nomi")
	mustWrite(t, badPath, "import std::regex.Regex\n\n"+
		"test \"an invalid backtick literal does not compile\"\n"+
		"  assert String.contains(\"[\", Regex`[`)\n"+
		"end\n")
	for _, cmd := range []string{"check", "test"} {
		out, err = runNomi(t, cacheRoot, cmd, badPath)
		if err == nil {
			t.Fatalf("nomi %s admits an invalid backtick regex:\n%s", cmd, out)
		}
		if want := "bad_test.nomi:4:31"; !strings.Contains(out, want) || !strings.Contains(out, "typed literal Regex`[` is invalid: error parsing regexp: missing closing ]: `[`") {
			t.Fatalf("nomi %s: expected the literal's error at %s, got:\n%s", cmd, want, out)
		}
	}
}

func TestTestCommand_SQLLiterals(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	root := repoRoot(t)
	dir := filepath.Join(root, "cmd", "nomi", "testdata", "modules", "sql_literals")
	out, err := runNomi(t, cacheRoot, "test", dir)
	if err != nil {
		t.Fatalf("nomi test testdata/modules/sql_literals: %v\nstdout/stderr:\n%s", err, out)
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

func TestTestCommand_RunsGroups(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	dir := t.TempDir()
	testPath := filepath.Join(dir, "math_test.nomi")
	mustWrite(t, testPath, `group "math"
  setup {base: 2}

  test "assertions", {base:}
    assert base + 2 == 4
    refute base == 5
  end

end

group "sibling"
  setup (2, 3)

  test "setup is bound by a tuple pattern", (base, extra)
    assert base + extra == 5
  end
end
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
	mustWrite(t, testPath, `import std::compiler

test "nomi test defaults NOMI_ENV"
  assert compiler.run(
    """
    import
      std::io
    end

    struct Env
      context: Context
      name: String
    end

    fn boot(startup: Startup) -> Result<Env, String>
      name =
        case Map.get(startup.env, "NOMI_ENV")
          Some(value) -> value
          None -> "missing"
        end

      Ok(Env{context: Context.root(), name:})
    end

    fn main()
      io.print(Env.name)
    end
    """
  ) == Ok("test\n")
end
`)
	cmd := exec.Command(nomiBin, "test", dir)
	cmd.Env = append(withoutEnv(os.Environ(), "NOMI_ENV"), "NOMI_RUNNER_CACHE_ROOT="+cacheRoot)
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
	mustWrite(t, testPath, `fn secret_add(a: Int, b: Int) -> Int
  a + b
end

test "uses private helper"
  assert secret_add(2, 3) == 5
end
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
	mustWrite(t, testPath, `test "fails"
  assert 1 == 2
end
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

// TestTestCommand_MultiLineStringFailureIsALineDiff: a failed `==` over two
// multi-line Strings reports a line diff, expected (the right operand) as `-`
// and actual (the left) as `+`, in place of the two operands' rows, and the
// `"""` literal is not printed a second time: the assertion's echo writes it
// as the lines it spans. Each expected line carries the source line the
// literal writes it on.
func TestTestCommand_MultiLineStringFailureIsALineDiff(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	dir := t.TempDir()
	testPath := filepath.Join(dir, "bank_test.nomi")
	mustWrite(t, testPath, `fn transcript() -> String
    "balance?\nYour balance is 500\n\ndeposited 25\nnew balance 525\nbye  \n"
end

test "deposit"
    output = transcript()
    assert output == """
        balance?
        Your balance: 500

        deposited 25
        new balance 525
        bye
        """
end
`)
	out, err := runNomi(t, cacheRoot, "test", dir)
	if err == nil {
		t.Fatalf("expected nomi test to fail, output:\n%s", out)
	}
	want := `  line 7: assertion failed
    assert output == """ lines 8-13 """
    expected """ ... """ (-) and actual output (+) differ

         8   │ balance?
         9 - │ Your balance: 500
           + │ Your balance is 500
        10   │
        11   │ deposited 25
        12   │ new balance 525
        13 - │ bye
             │ \ no newline at end
           + │ bye  $
             │ ($ ends a line that ends in whitespace)
test result: FAILED. 0 passed, 1 failed
`
	if !strings.HasSuffix(out, want) {
		t.Fatalf("got:\n%s\nwant it to end with:\n%s", out, want)
	}
}

// TestTestCommand_FailedReplayNumbersTheScript: a failed `io.replay` names
// what it compared, echoes the assertion with the script collapsed to the
// lines it spans, numbers each script line with its line in the file,
// shows a changed empty line as `(empty line)`, and keeps each input line's
// indentation in the text column.
func TestTestCommand_FailedReplayNumbersTheScript(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "bank_test.nomi"), `import std::io

fn main()
    io.print("")
    io.print("What is the starting balance?")
    balance = io.read_line() |> Result.with_default(None) |> Option.with_default("0") |> String.to_int() |> Option.with_default(0)
    io.print("Your starting balance is ${balance}")
    prompt(balance)
end

fn prompt(balance: Int)
    io.print("")
    io.print("Enter a deposit (e.g. deposit 50) or withdrawal (e.g. withdraw 10)")
    case io.read_line()
        Ok(Some(line)) -> case String.split(line, " ")
            ["deposit", n] -> case String.to_int(n)
                Some(amount) ->
                    io.print("${amount} deposited, new balance is ${balance + amount}")
                    prompt(balance + amount)
                None -> Unit
            end
            _ -> prompt(balance)
        end
        _ -> Unit
    end
end

test "deposits"
    assert io.replay(
        """
        What is the starting balance?
            > 500
        Your starting balance is 500

        Enter a deposit (e.g. deposit 50) or withdrawal (e.g. withdraw 10)

            > deposit 25
        25 deposited, new balance is 525

        Enter a deposit (e.g. deposit 50) or withdrawal (e.g. withdraw 10)
            > deposit 5
        5 deposited, new balance is 530

        Enter a deposit (e.g. deposit 50) or withdrawal (e.g. withdraw 10)
            > deposit nope
        nope is not a number

        Enter a deposit (e.g. deposit 50) or withdrawal (e.g. withdraw 10)
        """,
        main,
    )
end
`)
	out, err := runNomi(t, cacheRoot, "test", dir)
	if err == nil {
		t.Fatalf("expected nomi test to fail, output:\n%s", out)
	}
	want := `    io.replay: the transcript (-) and what main printed (+) differ

           + │ (empty line)
        31   │ What is the starting balance?
        32   │     > 500
        33   │ Your starting balance is 500
        34   │
        35   │ Enter a deposit (e.g. deposit 50) or withdrawal (e.g. withdraw 10)
        36 - │ (empty line)
        37   │     > deposit 25
        38   │ 25 deposited, new balance is 525
        39   │
             │   ... 3 lines match ...
        43   │
        44   │ Enter a deposit (e.g. deposit 50) or withdrawal (e.g. withdraw 10)
        45   │     > deposit nope
        46 - │ nope is not a number
        47 - │ (empty line)
        48 - │ Enter a deposit (e.g. deposit 50) or withdrawal (e.g. withdraw 10)
test result: FAILED. 0 passed, 1 failed
`
	// The echo writes the script as the lines the diff numbers rather than
	// repeating it above the diff.
	echo := "  line 29: the transcript differs from the script\n" +
		"    assert io.replay(\"\"\" lines 31-48 \"\"\", main)\n" +
		"    io.replay: the transcript"
	if !strings.HasSuffix(out, want) || !strings.Contains(out, echo) {
		t.Fatalf("got:\n%s\nwant it to end with:\n%s", out, want)
	}
}

// TestTestCommand_LiteralOperandRowsAndDiffBounds: a literal operand's row is
// dropped, since the assertion line shows it, while a name bound to a
// constant keeps its row; a comparison that is only part of the subject, a
// `refute`, and two one-line Strings keep their rows and show no diff.
func TestTestCommand_LiteralOperandRowsAndDiffBounds(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	dir := t.TempDir()
	testPath := filepath.Join(dir, "rows_test.nomi")
	mustWrite(t, testPath, `test "literal"
    n = 999
    assert n == 1_000
end

test "bound constant"
    n = 999
    limit = 1_000
    assert n == limit
end

test "part of the subject"
    out = "a\nb"
    assert out == "a\nc" and True
end

test "refute"
    out = "a\nb"
    refute out == "a\nb"
end

test "one line"
    out = "abc "
    assert out == "abc"
end
`)
	out, err := runNomi(t, cacheRoot, "test", dir)
	if err == nil {
		t.Fatalf("expected nomi test to fail, output:\n%s", out)
	}
	for _, want := range []string{
		"    assert n == 1_000\n    values:\n      n\n        = 999\nFAIL ",
		"    assert n == limit\n    values:\n      n\n        = 999\n      limit\n        = 1000\nFAIL ",
		"    assert out == \"a\\nc\" and True\n    values:\n      out\n        = \"a\\nb\"\n      out == \"a\\nc\"\n        = False\nFAIL ",
		"    refute out == \"a\\nb\"\n    values:\n      out\n        = \"a\\nb\"\nFAIL ",
		"    assert out == \"abc\"\n    values:\n      out\n        = \"abc \"\ntest result:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not contain\n%s\ngot:\n%s", want, out)
		}
	}
	if strings.Contains(out, "diff (") {
		t.Errorf("none of these failures is a diff:\n%s", out)
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
	mustWrite(t, testPath, `import std::testing

test "fails"
  assert True
  testing.check(1 == 2)
end

test "passes"
  assert True
  testing.check(1 == 1)
end

test "arm fails"
  assert True
  if 1 == 1 then
    testing.check(2 == 3)
  else
    testing.check(True)
  end
end

test "arm passes"
  assert True
  case 3
    3 -> testing.check(3 == 3)
    _ -> testing.check(False)
  end
end
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
	mustWrite(t, testPath, `import std::testing

test "refute check failure"
  refute testing.check(1 == 2)
end
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
	mustWrite(t, testPath, `test "domain err is just a value"
  assert True
  Err("boom")
end
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
	mustWrite(t, testPath, `test "fails"
  refute 1 == 1
end
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
	mustWrite(t, testPath, `test "fails"
  passes = 1 == 2
  assert passes
end
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
	mustWrite(t, testPath, `fn parse_id(_text: String) -> Result<Int, String>
  Err("expected a numeric id")
end

test "formats parsed id"
  id = parse_id("42x")?
  assert id == 42
end
`)
	out, err := runNomi(t, cacheRoot, "test", dir)
	if err == nil {
		t.Fatalf("expected nomi test to fail, output:\n%s", out)
	}
	if !strings.Contains(out, "line 6: test returned early") ||
		!strings.Contains(out, `parse_id("42x")?`) ||
		!strings.Contains(out, "returned:") ||
		!strings.Contains(out, `Err("expected a numeric id")`) {
		t.Fatalf("expected early?-return context, got:\n%s", out)
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

test "fails"
  passes =
    [1, 2, 3]
    |> Iter.any_match(fn n -> n == 4)

  assert passes
end
`)
	out, err := runNomi(t, cacheRoot, "test", dir)
	if err == nil {
		t.Fatalf("expected nomi test to fail, output:\n%s", out)
	}
	if !strings.Contains(out, "pipeline values:") ||
		!strings.Contains(out, "[1, 2, 3]") ||
		!strings.Contains(out, "Iter.any_match") ||
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
	mustWrite(t, testPath, `test "fails"
  a = 4
  assert 12345 == a
end
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

fn parse_id(s: String) -> Result<Int, String>
  case String.to_int(s)
    Some(id) -> Ok(id)
    None -> Err("expected a numeric id")
  end
end

test "fails"
  assert parse_id("43") == Ok(42)
end
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

fn parse_id(s: String) -> Result<Int, String>
  case String.to_int(s)
    Some(id) -> Ok(id)
    None -> Err("expected a numeric id")
  end
end

test "fails"
  a = False
  assert parse_id("42") == Ok(42) and a
end
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

test "fails"
  left = False
  assert left or Option.is_some(None)
end
`)
	out, err := runNomi(t, cacheRoot, "test", dir)
	if err == nil {
		t.Fatalf("expected nomi test to fail, output:\n%s", out)
	}
	if !strings.Contains(out, "left") ||
		!strings.Contains(out, "= False") ||
		!strings.Contains(out, "Option.is_some(None)\n        = False") {
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

test "fails"
  value: Option<Int> = None
  assert not Option.is_none(value)
end
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

test "fails"
  outcome = Ok(42)
  refute Result.is_ok(outcome)
end
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

test "fails"
  body = "created"
  assert String.contains(body, "ok")
end
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

test "fails"
  items = [1, 2, 3]
  assert Iter.any_match(items, fn n -> n == 4)
end
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

test "fails"
  items = [1, 2]
  expected = 3
  assert Iter.count(items) == expected
end
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

fn parse_id(s: String) -> Result<Int, String>
  case String.to_int(s)
    Some(id) -> Ok(id)
    None -> Err("expected a numeric id")
  end
end

test "fails"
  assert case parse_id("42")
    Ok(id) -> id == 43
    Err(_) -> False
  end
end
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
	mustWrite(t, testPath, `test "first"
  assert True
end

test "second"
  assert True
end
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
	env = append(env, "NOMI_RUNNER_CACHE_ROOT="+cacheRoot)
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
	const prompt = "//! assert Time.new(8, 30)? == Time\"08:30:00\""
	line := strconv.Itoa(lineNumberContaining(t, calendar, prompt))

	out, err := runNomiWithStd(t, cacheRoot, stdPath, "test", calendar, "--line", line)
	if err != nil || !strings.Contains(out, "1 passed, 0 failed") {
		t.Fatalf("the unedited copy's prompt does not pass (err %v):\n%s", err, out)
	}

	t.Run("edited prompt", func(t *testing.T) {
		replaceInFile(t, calendar, "Time.new(8, 30)? ==", "Time.new(8, 31)? ==")
		defer replaceInFile(t, calendar, "Time.new(8, 31)? ==", "Time.new(8, 30)? ==")
		out, err := runNomiWithStd(t, cacheRoot, stdPath, "test", calendar, "--line", line)
		if err == nil {
			t.Fatalf("an edited prompt that no longer holds passed, so the embedded one ran:\n%s", out)
		}
		if !strings.Contains(out, "assert Time.new(8, 31)? == Time\"08:30:00\"") ||
			!strings.Contains(out, "0 passed, 1 failed") {
			t.Fatalf("the edited prompt did not fail as itself:\n%s", out)
		}
	})

	t.Run("edited body", func(t *testing.T) {
		const body = "minute:     component(minute),"
		const edited = "minute:     component(minute + 1),"
		replaceInFile(t, calendar, body, edited)
		defer replaceInFile(t, calendar, edited, body)
		out, err := runNomiWithStd(t, cacheRoot, stdPath, "test", calendar, "--line", line)
		if err == nil {
			t.Fatalf("a prompt calling an edited body passed, so the embedded body ran:\n%s", out)
		}
		if !strings.Contains(out, "= 08:31:00") ||
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
fn answer() -> Int
  42
end

//! assert False
fn unrelated() -> Int
  0
end
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
fn answer() -> Int
  42
end

//! assert False
fn unrelated() -> Int
  0
end
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
	mustWrite(t, testPath, `test "selected"
  assert True
end

test "unselected bad analysis"
  Missing.value
  assert True
end
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
	mustWrite(t, testPath, `import
  std::comparable.Direction
  std::comparable.Ordering.{Less}
end

test "selected"
  assert Display.to_string(Direction.Ascending) == "Ascending"
end

test "unselected uses Less"
  assert Less == Less
end
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
	mustWrite(t, testPath, `test "same"
  assert True
end

test "same"
  assert True
end
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
	mustWrite(t, testPath, `group "math"
  test "one"
    assert True
  end

  test "two"
    assert True
  end
end
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
	mustWrite(t, testPath, `import std::io

struct Conn
  label: String
end

fn close(conn: Conn) -> Unit
  io.print("close " + conn.label)
end

group "setup cleanup"
  setup begin
    conn = Conn{label: "db"}
    defer close(conn)
    {conn:}
  end

  test "one", {conn:}
    io.print("body one")
    assert conn.label == "db"
  end

  test "two", {conn:}
    io.print("body two")
    assert conn.label == "db"
  end
end

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
	mustWrite(t, testPath, `fn main()
  Unit
end

test "inline module test"
  assert 1 + 1 == 2
end
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
	mustWrite(t, filepath.Join(dir, "math.nomi"), `fn add(a: Int, b: Int) -> Int
  a + b
end

test "inline add test"
  assert add(2, 3) == 5
end
`)
	mustWrite(t, filepath.Join(dir, "helper.nomi"), `fn helper() -> Int
  42
end
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
	t.Parallel()
	checkVMTestRun(t, "tests", "corpus")
}

// TestTestCommand_VMStdlib runs `nomi test std` on the VM: every stdlib
// module's `//!` prompt cases, built in the module's own scope against the
// cached stdlib lowering (internal/irbuild/stdtests.go), under
// TestTestCommand_VMCorpus's rule. TestExpectation_Stdlib checks each case's
// output.
func TestTestCommand_VMStdlib(t *testing.T) {
	t.Parallel()
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
	line := `String.contains(diagnostic.message, "type 'List' has no member 'empty'x")`
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
	mustWrite(t, testPath, `test "passes"
  assert 1 == 1
end
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
	mustWrite(t, library, "pub fn one() -> Int\n  1\nend\n")
	suite := filepath.Join(dir, "suite.nomi")
	mustWrite(t, suite, "fn one() -> Int\n  1\nend\n\ntest \"one\"\n  assert one() == 1\nend\n")

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
	mustWrite(t, path, `import std::assertions.AssertionFailure

fn main() -> Result<Unit, AssertionFailure>
  assert 1 + 1 == 2
  refute 1 == 2
  Ok(Unit)
end
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
	mustWrite(t, path, `import std::assertions.AssertionFailure

fn main() -> Result<Unit, AssertionFailure>
  a = 4
  assert 12345 == a
  Ok(Unit)
end
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
	mustWrite(t, path, `import std::assertions.AssertionFailure

fn main() -> Result<Unit, AssertionFailure>
  assert 1 == 2
  Ok(Unit)
end
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
	mustWrite(t, path, `import std::assertions.AssertionFailure
import std::io

fn validate() -> Result<Unit, AssertionFailure>
  assert 1 == 2
  Ok(Unit)
end

fn main()
  case validate()
    Err(_) -> io.print("caught")
    Ok(_) -> io.print("ok")
  end
end
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
// NOMI_RUNNER_CACHE_ROOT so a test's runner builds get an isolated cache.
func runNomi(t *testing.T, cacheRoot string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(nomiBin, args...)
	cmd.Env = append(os.Environ(), "NOMI_RUNNER_CACHE_ROOT="+cacheRoot)
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
	cmd.Env = append(os.Environ(), "NOMI_RUNNER_CACHE_ROOT="+cacheRoot)
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

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
