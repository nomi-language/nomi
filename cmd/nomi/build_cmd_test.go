package main

// `nomi build`: a runner binary with the program's linked IR appended. See
// build.go.

import (
	"bytes"
	"debug/elf"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/expectation"
	"github.com/nomi-language/nomi/vmrunner"
)

// dbgRefusal is the header of a build refusal that lists at least one `dbg`.
var dbgRefusal = regexp.MustCompile(`^nomi build: (\d+ todos? and )?\d+ dbgs? remains?:\n`)

// observedRun is one process's whole observable output.
type observedRun struct {
	stdout, stderr string
	exit           int
}

func (o observedRun) transcript() string { return expectation.Transcript(o.stdout, o.stderr) }

// buildEnv is the environment every command in these tests runs with: an
// isolated FFI cache, no colour, and no NOMI_ENV, as the golden recorder pins.
func buildEnv(cacheRoot string) []string {
	return append(withoutEnv(withoutEnv(os.Environ(), "NOMI_ENV"), "NOMI_COLOR"),
		"NOMI_FFIRUN_CACHE_ROOT="+cacheRoot, "NOMI_COLOR=never")
}

// runProcess runs path with args in dir, over stdin, with env, and answers
// its streams and exit status.
func runProcess(t *testing.T, env []string, dir, stdin, path string, args ...string) observedRun {
	t.Helper()
	cmd := exec.Command(path, args...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdin = strings.NewReader(stdin)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	o := observedRun{stdout: out.String(), stderr: errOut.String()}
	if err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("running %s: %v", path, err)
		}
		o.exit = exitErr.ExitCode()
	}
	return o
}

// TestBuildCommand_TheBinaryRunsAsNomiRunDoes builds small programs and runs
// each binary beside `nomi run` of the same source: stdout, stderr and exit
// status must be identical, including a boot reading its arguments and
// environment, a failed assertion and a fault.
func TestBuildCommand_TheBinaryRunsAsNomiRunDoes(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; builds a runner; -short")
	}
	cacheRoot := t.TempDir()
	env := append(buildEnv(cacheRoot), "NOMI_BUILD_TEST=configured")
	programs := map[string]string{
		"startup.nomi": `import {
 std/io
}

struct Settings {
  context: Context
}

fn boot(startup: Startup): Settings {
  io.print(Map.get(startup.env, "NOMI_BUILD_TEST"))
  io.print(startup.args)
  Settings{context: Context.root()}
}

fn main() {
  io.print("main ran")
}
`,
		"assertion.nomi": `import {
  std/assertions.AssertionFailure
  std/io
}

fn main(): Result<Unit, AssertionFailure> {
  io.print("before")
  x = 2
  assert x + 1 == 4
  Ok(Unit)
}
`,
		"fault.nomi": `import std/io

fn divide(a: Int, b: Int): Int {
  a / b
}

fn main() {
  io.print("before")
  io.print(divide(1, 0))
}
`,
		// std/compiler's hosts link the front end, so this program's runner
		// is the nomi_compiler variant.
		"compiler.nomi": `import {
  std/compiler
  std/io
}

fn main() {
  io.print(Debug.inspect(compiler.run("import std/io\n\nfn main() {\n  io.print(\"nested\")\n}\n")))
  io.print(Debug.inspect(compiler.check("fn main() {\n  missing\n}\n")))
}
`,
		"stdin.nomi": `import std/io

fn main() {
  case io.read_line() {
    Ok(line) -> io.print("read: ${line}")
    Err(e) -> io.print("no line: ${e}")
  }
}
`,
	}
	for name, src := range programs {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			entry := filepath.Join(dir, name)
			mustWrite(t, entry, src)
			bin := filepath.Join(t.TempDir(), "app")
			if b := runProcess(t, env, dir, "", nomiBin, "build", entry, "-o", bin); b.exit != 0 {
				t.Fatalf("nomi build: exit %d\n%s", b.exit, b.transcript())
			}
			args := []string{"first", "--literal", "two words"}
			want := runProcess(t, env, dir, "a line\n", nomiBin, append([]string{"run", entry}, args...)...)
			got := runProcess(t, env, dir, "a line\n", bin, args...)
			if got != want {
				t.Fatalf("the built binary differs from nomi run\n--- nomi run (exit %d) ---\n%s\n--- binary (exit %d) ---\n%s",
					want.exit, want.transcript(), got.exit, got.transcript())
			}
			if name == "startup.nomi" && want.stdout != "Some(configured)\n[first, --literal, two words]\nmain ran\n" {
				t.Fatalf("the startup program printed %q; this test compares the wrong thing", want.stdout)
			}
			if name == "compiler.nomi" && !strings.Contains(want.stdout, "nested") {
				t.Fatalf("the compiler program printed %q; this test compares the wrong thing", want.transcript())
			}
		})
	}
}

// TestBuildCommand_DefaultOutputIsTheEntryName: like `go build`, no -o
// writes the entry's base name into the working directory.
func TestBuildCommand_DefaultOutputIsTheEntryName(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; builds a runner; -short")
	}
	dir := t.TempDir()
	entry := filepath.Join(dir, "greet.nomi")
	mustWrite(t, entry, "import std/io\n\nfn main() {\n  io.print(\"hi\")\n}\n")
	env := buildEnv(t.TempDir())
	if b := runProcess(t, env, dir, "", nomiBin, "build", entry); b.exit != 0 {
		t.Fatalf("nomi build: exit %d\n%s", b.exit, b.transcript())
	}
	if got := runProcess(t, env, t.TempDir(), "", filepath.Join(dir, "greet")); got.stdout != "hi\n" || got.exit != 0 {
		t.Fatalf("./greet printed %q, exit %d", got.transcript(), got.exit)
	}
}

// TestBuildCommand_RefusesWhatRunRefuses: a program the front end rejects is
// refused with exactly `nomi run`'s text, and nothing is written. A file with
// no `fn main` and a test file are refused by name.
func TestBuildCommand_RefusesWhatRunRefuses(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	env := buildEnv(t.TempDir())
	dir := t.TempDir()
	broken := filepath.Join(dir, "broken.nomi")
	mustWrite(t, broken, "import std/io\n\nfn main() {\n  io.print(undefined_name)\n}\n")
	library := filepath.Join(dir, "library.nomi")
	mustWrite(t, library, "pub fn double(x: Int): Int {\n  x * 2\n}\n")
	testFile := filepath.Join(dir, "thing_test.nomi")
	mustWrite(t, testFile, "test \"t\" {\n  assert 1 == 1\n}\n")

	bin := filepath.Join(t.TempDir(), "app")
	run := runProcess(t, env, dir, "", nomiBin, "run", broken)
	build := runProcess(t, env, dir, "", nomiBin, "build", broken, "-o", bin)
	if run.exit == 0 || build.exit != run.exit || build.stderr != run.stderr || build.stdout != "" {
		t.Fatalf("a front-end error: nomi run exit %d\n%s\nnomi build exit %d\n%s",
			run.exit, run.transcript(), build.exit, build.transcript())
	}
	for _, c := range []struct{ entry, want string }{
		{library, "library.nomi declares no `fn main`, so there is no program to build"},
		{testFile, "is a test file; use `nomi test"},
	} {
		b := runProcess(t, env, dir, "", nomiBin, "build", c.entry, "-o", bin)
		if b.exit == 0 || !strings.Contains(b.stderr, c.want) {
			t.Fatalf("nomi build %s: exit %d, want the refusal %q\n%s", filepath.Base(c.entry), b.exit, c.want, b.transcript())
		}
	}
	if _, err := os.Stat(bin); !os.IsNotExist(err) {
		t.Fatalf("a refused build left something at -o (%v)", err)
	}
}

// TestBuildCommand_NoGoToolchainSaysSo: with no runner cached and no `go` on
// PATH, the build names the missing toolchain and writes nothing.
func TestBuildCommand_NoGoToolchainSaysSo(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	dir := t.TempDir()
	entry := filepath.Join(dir, "hello.nomi")
	mustWrite(t, entry, "import std/io\n\nfn main() {\n  io.print(\"hi\")\n}\n")
	env := append(withoutEnv(buildEnv(t.TempDir()), "PATH"), "PATH="+t.TempDir())
	bin := filepath.Join(t.TempDir(), "hello")
	b := runProcess(t, env, dir, "", nomiBin, "build", entry, "-o", bin)
	if b.exit == 0 || !strings.Contains(b.stderr, "no Go toolchain: `nomi build` builds the runner") ||
		!strings.Contains(b.stderr, "`nomi run` needs no toolchain") {
		t.Fatalf("exit %d:\n%s", b.exit, b.transcript())
	}
	if _, err := os.Stat(bin); !os.IsNotExist(err) {
		t.Fatalf("a failed build left something at -o (%v)", err)
	}
}

// TestBuildCommand_CrossCompiles builds for linux/amd64 and reads the result
// as an ELF x86-64 executable with the image appended.
func TestBuildCommand_CrossCompiles(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; builds a runner; -short")
	}
	dir := t.TempDir()
	entry := filepath.Join(dir, "hello.nomi")
	mustWrite(t, entry, "import std/io\n\nfn main() {\n  io.print(\"hi\")\n}\n")
	bin := filepath.Join(t.TempDir(), "hello-linux")
	if b := runProcess(t, buildEnv(t.TempDir()), dir, "", nomiBin, "build", "--target", "linux/amd64", entry, "-o", bin); b.exit != 0 {
		t.Fatalf("nomi build --target linux/amd64: exit %d\n%s", b.exit, b.transcript())
	}
	f, err := elf.Open(bin)
	if err != nil {
		t.Fatalf("the linux/amd64 build is not an ELF file: %v", err)
	}
	defer f.Close()
	if f.Machine != elf.EM_X86_64 || f.Type != elf.ET_EXEC {
		t.Fatalf("the linux/amd64 build is %v %v", f.Machine, f.Type)
	}
	if _, err := vmrunner.ReadImage(bin); err != nil {
		t.Fatalf("the linux/amd64 build carries no image: %v", err)
	}
}

// TestBuildCommand_EveryProgramMatchesItsGoldenRecord is the plan's
// agreement trigger: every whole-program record of the corpus (a record with
// no declared test cases), built with `nomi build` and
// run as a binary, must match the record `nomi run` wrote byte for byte,
// exit status included. A program `nomi run` refuses must be refused by the
// build with the same text; a file with no `fn main` (whose record is empty)
// is refused as having nothing to build, and a program with a `dbg` (whose
// record shows it ran) is refused as listing one. There is no exclusion: a
// record the recorder did not run fails here.
func TestBuildCommand_EveryProgramMatchesItsGoldenRecord(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; builds every corpus program; -short")
	}
	built := buildMatchesRecords(t, nomiBin, buildEnv(t.TempDir()), []recordPopulation{
		{"corpus", "tests"},
	}, nil)
	if built == 0 {
		t.Fatal("no program was built, so this test compared nothing")
	}
	t.Logf("%d programs built", built)
}

// recordPopulation is a golden population and the directory its IDs are
// relative to.
type recordPopulation struct{ name, dir string }

// buildMatchesRecords builds every whole-program record of pops with nomi and
// holds each binary to its record (see
// TestBuildCommand_EveryProgramMatchesItsGoldenRecord). A build whose stderr
// passover accepts is not compared and not counted. It answers how many
// programs were built and run.
func buildMatchesRecords(t *testing.T, nomi string, env []string, pops []recordPopulation, passover func(stderr string) bool) int {
	t.Helper()
	root, err := expectation.Root()
	if err != nil {
		t.Fatal(err)
	}
	built := 0
	for _, pop := range pops {
		set, err := expectation.Load(pop.name)
		if err != nil {
			t.Fatal(err)
		}
		for _, rec := range set.Cases {
			if rec.Cases != 0 {
				continue
			}
			rec := rec
			entry := filepath.Join(repoRoot(t), pop.dir, filepath.FromSlash(rec.ID))
			t.Run(pop.name+"/"+rec.ID, func(t *testing.T) {
				bin := filepath.Join(t.TempDir(), "app")
				b := runProcess(t, env, t.TempDir(), "", nomi, "build", entry, "-o", bin)
				if strings.HasPrefix(rec.Transcript, "<not run>") {
					t.Fatalf("the recorder did not run this program, so the build has nothing to match:\n%s", rec.Transcript)
				}
				if b.exit != 0 && passover != nil && passover(b.stderr) {
					return
				}
				if b.exit != 0 {
					if strings.Contains(b.stderr, "declares no `fn main`") {
						if rec.Exit != 1 || !strings.Contains(rec.Transcript, "declares no `fn main`, so there is no program to run") {
							t.Fatalf("refused as having no main, but nomi run recorded exit %d:\n%s", rec.Exit, rec.Transcript)
						}
						return
					}
					// A program with a `dbg` runs (its record shows the dbg's
					// line) and is refused by the build, which lists it.
					if dbgRefusal.MatchString(b.stderr) {
						if rec.Exit != 0 || !strings.Contains(rec.Transcript, "dbg line ") {
							t.Fatalf("refused for a dbg, but nomi run recorded exit %d and no dbg line:\n%s", rec.Exit, rec.Transcript)
						}
						return
					}
					got := expectation.Normalize(b.transcript(), root)
					if b.exit != rec.Exit || got != rec.Transcript {
						t.Fatalf("nomi build refused (exit %d) and the record is exit %d:\n%s",
							b.exit, rec.Exit, expectation.LineDiff(rec.Transcript, got))
					}
					return
				}
				built++
				o := runProcess(t, env, t.TempDir(), "", bin)
				got := expectation.Normalize(o.transcript(), root)
				if o.exit != rec.Exit || got != rec.Transcript {
					t.Fatalf("the built binary exits %d, the record %d:\n%s",
						o.exit, rec.Exit, expectation.LineDiff(rec.Transcript, got))
				}
			})
		}
	}
	return built
}
