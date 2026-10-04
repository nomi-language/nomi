package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/termcolor"
	"github.com/nomi-language/nomi/rt"
)

// Unbounded recursion at the CLI is an ordinary fault: `nomi run` prints the
// positioned message and exits 1, and `nomi test` fails the case and runs the
// next.

const recursesForever = `import std/io

fn down(n: Int): Int {
  down(n + 1) + 1
}

fn main() {
  io.print("start")
  down(0) |> io.print()
}

test "recurses" {
  assert down(0) == 0
}

test "adds" {
  assert 1 + 2 == 3
}
`

func TestRunCommand_UnboundedRecursionIsAFaultOnTheVM(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	entry := filepath.Join(t.TempDir(), "main.nomi")
	mustWrite(t, entry, recursesForever)

	out, err := runNomi(t, t.TempDir(), "run", entry)
	if err == nil {
		t.Fatalf("nomi run on unbounded recursion exited 0:\n%s", out)
	}
	want := "start\n" + rt.CallDepthError(4).Error() + "\n"
	if out != want {
		t.Fatalf("nomi run printed\n%q\nwant\n%q", truncate(out), want)
	}
	if code := exitCode(err); code != 1 {
		t.Fatalf("nomi run exited %d, want 1 (a Go stack overflow exits 2)", code)
	}
}

func TestTestCommand_UnboundedRecursionFailsItsCaseOnTheVM(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	entry := filepath.Join(t.TempDir(), "main.nomi")
	mustWrite(t, entry, recursesForever)

	out, err := runNomi(t, t.TempDir(), "test", entry)
	plain := termcolor.StripANSI(out)
	if err == nil {
		t.Fatalf("nomi test with a failing case exited 0:\n%s", truncate(plain))
	}
	for _, want := range []string{
		rt.CallDepthError(4).Error(),
		"ok " + entry + " :: adds\n",
		"test result: FAILED. 1 passed, 1 failed\n",
	} {
		if !strings.Contains(plain, want) {
			t.Fatalf("nomi test output lacks %q:\n%s", want, truncate(plain))
		}
	}
	if strings.Contains(plain, "goroutine") {
		t.Fatalf("nomi test printed a Go stack trace:\n%s", truncate(plain))
	}
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	if e, ok := err.(interface{ ExitCode() int }); ok {
		return e.ExitCode()
	}
	return -1
}

func truncate(s string) string {
	if len(s) > 2000 {
		return s[:2000] + "\n..."
	}
	return s
}
