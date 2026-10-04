package irbuild

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/vm"
)

// Assertion subjects that are not a Bool (`Maybe`/`Result` shapes and
// `Assertable` values), assertions inside an ordinary `fn`, and a piped
// `testing.check`, run on the VM and compared against the golden record of
// `nomi test`, failing reports included.

// irAssertFixtureVM lowers the program at path, requires every declared case
// retained, runs them in the VM and requires the golden output.
func irAssertFixtureVM(t *testing.T, path string) *ir.Module {
	t.Helper()
	p, err := Analyze(path)
	if err != nil {
		t.Fatalf("the front end rejects the fixture, so nothing below is tested: %v", err)
	}
	res, _, err := GenerateIR(p)
	if err != nil {
		t.Fatal(err)
	}
	var module *ir.Module
	for _, m := range res.IR {
		if m.Name() == path {
			module = m
		}
	}
	if module == nil {
		t.Fatalf("%s retained nothing", path)
	}
	var out bytes.Buffer
	exit, reason := vm.NewProgram(module, res.IRModules(), &out).RunTests(path)
	if reason != "" {
		t.Fatalf("the VM could not run the cases: %s", reason)
	}
	if strings.Contains(out.String(), "BLOCKED") {
		t.Fatalf("a case is blocked:\n%s", out.String())
	}
	golden := goldenReference(t, path)
	if out.String() != golden.stdout || exit != golden.exit {
		t.Errorf("VM and golden output differ:\nVM (exit %d):\n%s\ngolden (exit %d):\n%s",
			exit, out.String(), golden.exit, golden.stdout)
	}
	return module
}

// TestIRAssertSubject_RepositoryFixturesRunWhole runs the deliberately red
// fixtures that pin the three judgements' words: every case retains and the
// transcript is the golden record byte for byte.
func TestIRAssertSubject_RepositoryFixturesRunWhole(t *testing.T) {
	for _, name := range []string{
		"assert_shape.nomi",              // Maybe/Result subjects, both keywords
		"assertable_subject.nomi",        // Assertable answers, defaults and empty reasons
		"assertable_subject_report.nomi", // Assertable rows, details and defined-as
		"check_shape_report.nomi",        // testing.check over a shape subject
		"assert_boundary.nomi",           // assert and `assert pat = v` in an ordinary fn
		"testing_check.nomi",             // testing.check's Result, unchanged
		"pipe_assert_report.nomi",
		"try_check_in_test.nomi", // `try testing.check` in a test body reports the check        // piped subjects, unchanged
	} {
		t.Run(name, func(t *testing.T) {
			abs, err := filepath.Abs(filepath.Join("testdata", name))
			if err != nil {
				t.Fatal(err)
			}
			irAssertFixtureVM(t, abs)
		})
	}
}

// TestIRAssertSubject_AssertableCarriesItsAnswer pins the node shape: an
// Assertable subject's `ir.Assert` carries the answer its `failure` impl
// returned, and a shape subject's carries none.
func TestIRAssertSubject_AssertableCarriesItsAnswer(t *testing.T) {
	abs, err := filepath.Abs(filepath.Join("testdata", "assertable_subject_report.nomi"))
	if err != nil {
		t.Fatal(err)
	}
	module := irAssertFixtureVM(t, abs)
	answered := 0
	for _, c := range module.Tests() {
		for _, b := range c.Fn().Blocks() {
			for _, in := range b.Instrs() {
				if a, ok := in.(*ir.Assert); ok && a.Answer() != ir.NoTemp {
					answered++
				}
			}
		}
	}
	if answered == 0 {
		t.Fatal("no retained assertion carries an Assertable answer")
	}
}

const irAssertSubjectSource = `
import {
  std/assertions.AssertionFailure
  std/testing
}

fn find(n: Int): Maybe<String> {
  if n == 1 { Some("Ada") } else { None }
}

fn ensure_small(n: Int): Result<Int, AssertionFailure> {
  limit = 10
  assert n < limit
  assert find(1)
  Ok(n)
}

fn checked(xs: List<Int>): Result<Bool, AssertionFailure> {
  held = try testing.check(xs |> Iter.any?(|x| x > 2))
  Ok(held)
}

test "an assert in an ordinary fn is its Err" {
  assert Ok(3) = ensure_small(3)
  assert Err(failure) = ensure_small(12)
  assert failure.reason == "assertion failed"
  assert failure.expression == "n < limit"
}

test "a failing assert in an ordinary fn reports through the caller" {
  assert Ok(12) = ensure_small(12)
}

test "a check over a pipe inside an ordinary fn" {
  assert checked([1, 2, 3]) == Ok(True)
  assert Err(failure) = checked([1, 2])
  assert failure.keyword == "check"
}

test "a failing piped check reports its pipeline" {
  result = testing.check(
    [1, 2]
    |> Iter.any?(|x| x > 2)
  )
  assert result == Ok(True)
}

test "a failing shape assertion shows the value" {
  refute find(1)
}
`

// TestIRAssertSubject_FnBoundaryAndPipedCheck runs assertions inside an
// ordinary fn (the failure is the fn's Err, read by the caller) and piped
// checks, passing and failing, against the golden record.
func TestIRAssertSubject_FnBoundaryAndPipedCheck(t *testing.T) {
	path := filepath.Join(t.TempDir(), "assert_subject_test.nomi")
	if err := os.WriteFile(path, []byte(irAssertSubjectSource), 0600); err != nil {
		t.Fatal(err)
	}
	irAssertFixtureVM(t, path)
}
