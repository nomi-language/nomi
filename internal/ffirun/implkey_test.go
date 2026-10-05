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

// Two programs that depend on implOwnerKey reading the receiver's base name
// and the interface's instantiation. They are the end-to-end half of the
// key-level agreement checks in internal/hostpair.
//
// They go through `go run` on the generated wrapper rather than through
// DiscoverInScope, because a wrong key fails at REGISTRATION and
// at CALL, not at discovery. A discovery-level assertion would have agreed
// with the defect: two exports under one key is a perfectly well-formed
// DiscoveredPackage.

// stageImplKeyProject writes a project whose Go binding lives in a nested
// module, mirroring TestFFIRun_SourceBindingRoundTrip's layout.
func stageImplKeyProject(t *testing.T, goSrc, nomiSrc string) (projectRoot, entryPath string) {
	t.Helper()
	projectRoot = t.TempDir()
	nomiRoot := repoRoot(t)

	bindingDir := filepath.Join(projectRoot, "implbinding")
	if err := os.MkdirAll(bindingDir, 0o755); err != nil {
		t.Fatalf("mkdir implbinding: %v", err)
	}
	mustWriteHelper(t, filepath.Join(bindingDir, "bind.go"), goSrc)
	mustWriteHelper(t, filepath.Join(bindingDir, "go.mod"), `module implbinding

go 1.26.3
`)
	mustWriteHelper(t, filepath.Join(projectRoot, "go.mod"), fmt.Sprintf(`module impltest

go 1.26.3

require implbinding v0.0.0

require github.com/nomi-language/nomi v0.0.0

replace implbinding => ./implbinding

replace github.com/nomi-language/nomi => %s
`, nomiRoot))
	mustWriteHelper(t, filepath.Join(projectRoot, "nomi.toml"), `[module]
name = "impltest"
entry_points = ["main"]

`)
	entryPath = filepath.Join(projectRoot, "main.nomi")
	mustWriteHelper(t, entryPath, nomiSrc)
	return projectRoot, entryPath
}

func runImplKeyProject(t *testing.T, entryPath, mode string) string {
	t.Helper()
	res, err := Prepare(entryPath)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if res.FastPath {
		t.Fatal("expected FastPath=false (source binding discovered)")
	}
	cmd := exec.Command("go", "run", "-mod=mod", res.WrapperPath, wrapperModeFlag, mode, entryPath)
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
	return strings.TrimSpace(stdout.String())
}

// TestFFIRun_TwoGenericInterfaceImplsRegisterTwoKeys is the program that did
// not start.
//
// Two `impl Add<X, Score> for Score` blocks, each binding `add` to a different
// Go function. Before the fix both landed on the key `Score.add` and the
// wrapper's second registration failed:
//
//	nomi run: FFI export registration for "Score.add" in package "addbinding"
//	failed (declared at main.nomi:22:51):
//	RegisterExternFunc("Score.add"): name already registered
//
// The two Go functions compute DIFFERENT results on purpose. Two distinct keys
// would satisfy a check that only asked whether the program started; the
// output is what says each declaration reached its own Go function rather than
// both reaching one of them.
func TestFFIRun_TwoGenericInterfaceImplsRegisterTwoKeys(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test invokes go run; skipped in -short mode")
	}
	t.Setenv("NOMI_FFIRUN_CACHE_ROOT", t.TempDir())

	_, entryPath := stageImplKeyProject(t, `package implbinding

type Score struct {
	Points int64
}

type Bonus struct {
	Points int64
}

// AddScore is deliberately plain addition and AddBonus deliberately is not, so
// the program's output distinguishes which binding each impl reached.
func AddScore(lhs Score, rhs Score) Score {
	return Score{Points: lhs.Points + rhs.Points}
}

func AddBonus(lhs Score, rhs Bonus) Score {
	return Score{Points: lhs.Points + rhs.Points*10}
}
`, `import {
  std/io
}

gopkg "implbinding" as go_add

pub struct Score {
  points: Int
}

pub struct Bonus {
  points: Int
}

impl Add<Score, Score> for Score {
  fn add(lhs: Score, rhs: Score): Score go go_add.AddScore
}

impl Add<Bonus, Score> for Score {
  fn add(lhs: Score, rhs: Bonus): Score go go_add.AddBonus
}

fn main() {
  io.print(Int.to_string((Score{points: 1} + Score{points: 2}).points))
  io.print(Int.to_string((Score{points: 1} + Bonus{points: 2}).points))
}
`)

	got := runImplKeyProject(t, entryPath, "vmrun")
	const want = "3\n21"
	if got != want {
		t.Fatalf("output = %q, want %q — 3 is AddScore and 21 is AddBonus, so a wrong value means the two impls share one binding", got, want)
	}
}

// TestFFIRun_GenericReceiverImplIsCallable is the dead registration, made
// observable by calling it.
//
// A key of `Box<T>.origin` would name a key no call crosses under; the call
// crosses under `Box.origin`. The key-level assertion lives in
// internal/hostpair. This test is the consequence, not the diagnosis.
//
// `origin()` takes no arguments and returns Int because a generic receiver's
// other methods have `Box<T>` in their signatures and the FFI boundary does not
// project a generic struct. That is a separate limit; the nullary shape is
// enough to exercise the key.
func TestFFIRun_GenericReceiverImplIsCallable(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test invokes go run; skipped in -short mode")
	}
	t.Setenv("NOMI_FFIRUN_CACHE_ROOT", t.TempDir())

	_, entryPath := stageImplKeyProject(t, `package implbinding

func Origin() int64 { return 7 }
`, `import {
  std/io
}

gopkg "implbinding" as go_box

pub opaque struct Box<T> {
  item: T
}

impl Box<T> {
  pub fn origin(): Int go go_box.Origin
}

fn main() {
  io.print(Int.to_string(Box.origin()))
}
`)

	got := runImplKeyProject(t, entryPath, "vmrun")
	if got != "7" {
		t.Fatalf("output = %q, want 7", got)
	}
}

// TestFFIRun_GoBoundImplFunctionsRunOnTheVM: a Go-bound function in an
// interface impl (two operator impls on one receiver, and Display), in an
// inherent impl, and in an inherent impl on a generic receiver, reached
// type-qualified, interface-qualified, through `+` and through
// interpolation. Each crosses to its own Go function under the key the
// wrapper registers it by.
func TestFFIRun_GoBoundImplFunctionsRunOnTheVM(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test invokes go run; skipped in -short mode")
	}
	t.Setenv("NOMI_FFIRUN_CACHE_ROOT", t.TempDir())

	_, entryPath := stageImplKeyProject(t, `package implbinding

type Score struct {
	Points int64
}

type Bonus struct {
	Points int64
}

func AddScore(lhs Score, rhs Score) Score { return Score{Points: lhs.Points + rhs.Points} }

func AddBonus(lhs Score, rhs Bonus) Score { return Score{Points: lhs.Points + rhs.Points*10} }

func Describe(s Score) string { return "described" }

func Render(s Score) string { return "rendered" }

func Label(n int64) string { return "label" }
`, `import {
  std/io
}

gopkg "implbinding" as go_add

pub struct Score {
  points: Int
}

pub struct Bonus {
  points: Int
}

pub opaque struct Box<T> {
  item: T
}

impl Add<Score, Score> for Score {
  fn add(lhs: Score, rhs: Score): Score go go_add.AddScore
}

impl Add<Bonus, Score> for Score {
  fn add(lhs: Score, rhs: Bonus): Score go go_add.AddBonus
}

impl Score {
  pub fn describe(s: Score): String go go_add.Describe
}

impl Display for Score {
  fn to_string(s: Score): String go go_add.Render
}

impl Box<T> {
  pub fn label(n: Int): String go go_add.Label
}

fn main() {
  a = Score.add(Score{points: 1}, Score{points: 2})
  io.print(a.points)
  b = Add.add(Score{points: 1}, Bonus{points: 2})
  io.print(b.points)
  s: Score = Score{points: 1} + Score{points: 5}
  io.print(s.points)
  io.print(Score.describe(a))
  io.print(Display.to_string(a))
  io.print("${a}")
  io.print(Box.label(3))
}
`)

	got := runImplKeyProject(t, entryPath, "vmrun")
	const want = "3\n21\n6\ndescribed\nrendered\nrendered\nlabel"
	if got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}
