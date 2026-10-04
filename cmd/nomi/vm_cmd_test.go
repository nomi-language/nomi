package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/termcolor"
)

// The CLI surfaces of `nomi run`, `nomi test` and the REPL over a program the
// VM cannot fully run.

// vmBlockedProgram reaches a function the IR builder declines (a literal of a
// struct whose field is a Map of function values), from `main` and from
// one test case.
const vmBlockedProgram = `import std/io

fn count(n: Int, acc: Int): Int {
  if n == 0 {
    acc
  } else {
    weigh(Node{f: Map.empty()}) + 3
  }
}

fn main() {
  io.print("before")
  io.print(Int.to_string(count(3, 0)))
}

test "counts" {
  assert count(3, 0) == 3
}

test "adds" {
  assert 1 + 2 == 3
}

struct Node {
  f: Map<String, (Int) -> Int>
}

fn weigh(_d: Node): Int {
  0
}
`

func TestRunCommand_VMRefusesABlockedProgram(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	entry := filepath.Join(t.TempDir(), "main.nomi")
	mustWrite(t, entry, vmBlockedProgram)

	out, err := runNomi(t, t.TempDir(), "run", entry)
	if err == nil {
		t.Fatalf("nomi run on a blocked program exited 0:\n%s", out)
	}
	if !strings.Contains(out, "BLOCKED ") ||
		!strings.Contains(out, "[count] not retained: a struct literal: Node") {
		t.Fatalf("nomi run did not name the blocked function and its reason:\n%s", out)
	}
	if strings.Contains(out, "before") {
		t.Fatalf("a blocked program ran part of main:\n%s", out)
	}
	if !strings.HasSuffix(out, "the VM cannot run this program\n") {
		t.Fatalf("the refusal does not end with its closing line:\n%s", out)
	}
}

func TestTestCommand_VMReportsBlockedCases(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	entry := filepath.Join(t.TempDir(), "main.nomi")
	mustWrite(t, entry, vmBlockedProgram)

	out, err := runNomi(t, t.TempDir(), "test", entry)
	plain := termcolor.StripANSI(out)
	if err == nil {
		t.Fatalf("nomi test with a blocked case exited 0:\n%s", plain)
	}
	for _, want := range []string{
		":: counts [count] not retained: a struct literal: Node\n",
		"ok " + entry + " :: adds\n",
		"test result: BLOCKED. 1 passed, 0 failed, 1 blocked\n",
	} {
		if !strings.Contains(plain, want) {
			t.Fatalf("nomi test output lacks %q:\n%s", want, plain)
		}
	}

	// An all-green run has no blocked count in the summary.
	out, err = runNomi(t, t.TempDir(), "test", entry, "--line", "20")
	plain = termcolor.StripANSI(out)
	if err != nil || !strings.HasSuffix(plain, "test result: ok. 1 passed, 0 failed\n") {
		t.Fatalf("nomi test --line 20 on the VM: %v\n%s", err, plain)
	}
}

func TestReplCommand_VMBanner(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	home := t.TempDir()
	cmd := exec.Command(nomiBin)
	cmd.Env = append(cmd.Environ(), "HOME="+home)
	cmd.Stdin = strings.NewReader("1 + 2\n")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("nomi: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), replVMBanner) || !strings.Contains(string(out), "\n3\n") {
		t.Fatalf("the VM REPL printed:\n%s", out)
	}
	// The banner opens with the line `nomi --version` prints, not a version
	// of its own.
	version, err := exec.Command(nomiBin, "--version").Output()
	if err != nil {
		t.Fatalf("nomi --version: %v", err)
	}
	if !strings.HasPrefix(string(out), string(version)) {
		t.Fatalf("the REPL banner does not open with `nomi --version`'s line %q:\n%s", version, out)
	}

}

// A flag a command does not take fails with "unknown flag" naming it, rather
// than being read as a path or starting the REPL.
func TestCommands_RejectUnknownFlags(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	entry := filepath.Join(t.TempDir(), "main.nomi")
	mustWrite(t, entry, "fn main() {}\n")
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"run", "--interpret", entry}, `nomi run: unknown flag "--interpret"`},
		{[]string{"run", "-x", entry}, `nomi run: unknown flag "-x"`},
		{[]string{"test", "--interpret", entry}, `nomi test: unknown flag "--interpret"`},
		{[]string{"test", entry, "-v"}, `nomi test: unknown flag "-v"`},
		{[]string{"test", entry, entry}, `nomi test: unexpected argument`},
		{[]string{"check", "--interpret", entry}, `nomi check: unknown flag "--interpret"`},
		{[]string{"check", entry, entry}, `nomi check: unexpected argument`},
		{[]string{"--interpret"}, `nomi: unknown flag "--interpret"`},
		{[]string{"frobnicate"}, `nomi: unknown command "frobnicate"`},
		{[]string{"fmt", "-x", entry}, "flag provided but not defined: -x"},
	}
	for _, c := range cases {
		cmd := exec.Command(nomiBin, c.args...)
		cmd.Stdin = strings.NewReader("")
		out, err := cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(out), c.want) {
			t.Errorf("nomi %s: want failure naming %q, got %v:\n%s", strings.Join(c.args, " "), c.want, err, out)
		}
	}
	// A flag after the file is the program's own argument.
	if out, err := exec.Command(nomiBin, "run", entry, "--verbose").CombinedOutput(); err != nil {
		t.Fatalf("nomi run <file> --verbose: %v\n%s", err, out)
	}
}

// `nomi test --format json` on the VM: a passing case is a "passed" record, a
// case the VM cannot run is a "blocked" record whose message is its reasons,
// and the summary counts it. The flags are accepted in any order.
func TestTestCommand_VMJSONFormatReportsBlocked(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	dir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	path := filepath.Join(dir, "main.nomi")
	mustWrite(t, path, vmBlockedProgram)

	stdout, _, err := runNomiSplit(t, t.TempDir(), dir, "test", "--format", "json", path)
	if err == nil {
		t.Fatalf("a run with a blocked case exited 0:\n%s", stdout)
	}
	records := parseJSONRecords(t, stdout)
	if len(records) != 3 {
		t.Fatalf("want two tests and a summary:\n%s", stdout)
	}
	blocked := records[0]
	if blocked.Type != "test" || blocked.File != path || blocked.Line != 16 || blocked.EndLine != 18 ||
		blocked.Status != "blocked" || blocked.Message == nil ||
		*blocked.Message != "[count] not retained: a struct literal: Node" {
		t.Errorf("blocked record = %+v", blocked)
	}
	if passed := records[1]; passed.Status != "passed" || passed.Line != 20 || passed.EndLine != 22 {
		t.Errorf("passed record = %+v", passed)
	}
	if s := records[2]; s.Type != "summary" || s.Passed != 1 || s.Failed != 0 || s.Blocked != 1 {
		t.Errorf("summary = %+v", s)
	}
}

// In json mode a case's own output goes to stderr on the VM too, so stdout
// carries only records.
func TestTestCommand_VMJSONFormatSendsOutputToStderr(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "print_test.nomi"), `import std/io

test "prints" {
  io.print("hello from a vm test")
  assert 1 + 1 == 2
}
`)
	stdout, stderr, err := runNomiSplit(t, t.TempDir(), dir, "test", "print_test.nomi", "--format", "json")
	if err != nil {
		t.Fatalf("nomi test --format json: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if stderr != "hello from a vm test\n" {
		t.Fatalf("stderr = %q", stderr)
	}
	records := parseJSONRecords(t, stdout)
	if len(records) != 2 || records[0].Status != "passed" || records[1].Passed != 1 {
		t.Fatalf("records:\n%s", stdout)
	}
}

// `Bytes.each_while` and `String.each_while` called by their type run on the
// VM's byte and grapheme views: each yields until the callback answers False,
// and answers False when stopped and True when the source ran out.
func TestRunCommand_EachWhileRunsDirectly(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	entry := filepath.Join(t.TempDir(), "main.nomi")
	mustWrite(t, entry, `import std/io

fn main() {
  b = String.to_bytes("abc")
  stopped = Bytes.each_while(b, |x| {
    io.print(Byte.to_int(x))
    Byte.to_int(x) < 98
  })
  io.print(stopped)
  io.print(Bytes.each_while(b, |_x| True))
  graphemes = String.each_while("héllo", |g| {
    io.print(g)
    g != "l"
  })
  io.print(graphemes)
  io.print(String.each_while("ab", |_g| True))
}
`)
	out, err := runNomi(t, t.TempDir(), "run", entry)
	want := "97\n98\nFalse\nTrue\nh\né\nl\nFalse\nTrue\n"
	if err != nil || out != want {
		t.Fatalf("nomi run: %v\ngot:\n%s\nwant:\n%s", err, out, want)
	}
}

// An empty block answers Unit: `fn main() {}` runs and prints nothing, and an
// empty function, lambda and branch are retained.
func TestRunCommand_EmptyBodiesAnswerUnit(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty.nomi")
	mustWrite(t, empty, "fn main() {}\n")
	out, err := runNomi(t, t.TempDir(), "run", empty)
	if err != nil || out != "" {
		t.Fatalf("nomi run on an empty main: %v\n%s", err, out)
	}

	others := filepath.Join(dir, "others.nomi")
	mustWrite(t, others, `import std/io

fn nothing() {}

fn main() {
  f = || {}
  f()
  if 1 == 1 {
  } else {
    io.print("no")
  }
  io.inspect(nothing())
}
`)
	out, err = runNomi(t, t.TempDir(), "run", others)
	if err != nil || out != "Unit\n" {
		t.Fatalf("nomi run on empty bodies: %v\n%s", err, out)
	}
}
