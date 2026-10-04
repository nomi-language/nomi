package rt

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// TestTypedNilTestFailureIsAPass is the pin for the one hazard the widening of
// Test.Fn could have introduced, and it fails on the ANSWER rather than on a
// type.
//
// Test.Fn used to be `func(*Frame) *AssertionFailure`, typed concretely on
// purpose: a nil *AssertionFailure boxed into an interface compares NON-nil, so
// an `error` return would have let a PASSING case be reported as a failure with
// no failure in it. Widening it to TestFailure — needed because a `try`
// propagating inside a test body ends the case as an EarlyReturnFailure — has to
// carry that property rather than trade it away, and the nil-receiver Failure()
// method is how it is carried.
//
// The mutation this defends against is deleting either `if e == nil` guard, or
// changing runTest to `return failure` instead of `return failure.Failure()`.
// Either makes this test report a failure for a body that produced none, which
// in a real run would be EVERY case failing.
func TestTypedNilTestFailureIsAPass(t *testing.T) {
	fr := NewFrame(context.Background())

	var nilAssertion *AssertionFailure
	var nilEarly *EarlyReturnFailure
	for _, tc := range []struct {
		name string
		fn   func(*Frame) TestFailure
	}{
		{"untyped nil", func(*Frame) TestFailure { return nil }},
		{"typed nil *AssertionFailure", func(*Frame) TestFailure { return nilAssertion }},
		{"typed nil *EarlyReturnFailure", func(*Frame) TestFailure { return nilEarly }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := runTest(fr, Test{Name: tc.name, Fn: tc.fn}); err != nil {
				t.Fatalf("a body that produced no failure was reported as failing: %v", err)
			}
			var b bytes.Buffer
			rep := NewTestReporter(&b)
			rep.Result(tc.name, runTest(fr, Test{Name: tc.name, Fn: tc.fn}))
			if failed := rep.Summary(); failed {
				t.Fatalf("the reporter counted a failure:\n%s", b.String())
			}
			if !strings.HasPrefix(b.String(), "ok "+tc.name+"\n") {
				t.Fatalf("expected an ok line, got:\n%s", b.String())
			}
		})
	}
}

// TestEarlyReturnFailureReachesTheReporter pins that the second inhabitant of
// TestFailure actually renders — the widening is pointless if the reporter's
// early-return branch is unreachable from a lowered test body.
//
// The text is spelled out because it IS the contract: `nomi test` produces these
// exact bytes for a `try` that propagates in a test body. The program-level
// reference for this shape is internal/irbuild/testdata/try_in_test.nomi.
func TestEarlyReturnFailureReachesTheReporter(t *testing.T) {
	fr := NewFrame(context.Background())
	body := func(*Frame) TestFailure {
		return &EarlyReturnFailure{Line: 52, Expr: "try parse(-1)", Value: `Err("negative -1")`}
	}
	err := runTest(fr, Test{Name: "try propagates", Fn: body})
	if err == nil {
		t.Fatal("an EarlyReturnFailure returned from a body did not reach the reporter")
	}
	var b bytes.Buffer
	rep := NewTestReporter(&b)
	rep.Result("try propagates", err)
	if failed := rep.Summary(); !failed {
		t.Fatal("the reporter did not count the failure")
	}
	want := "FAIL try propagates\n" +
		"  line 52: test returned early\n" +
		"    try parse(-1)\n" +
		"    returned:\n" +
		"      Err(\"negative -1\")\n" +
		"test result: FAILED. 0 passed, 1 failed\n"
	if got := b.String(); got != want {
		t.Fatalf("report:\n%q\nwant:\n%q", got, want)
	}
}

// A blocked case is reported on its own lines, counted apart from passed and
// failed, and makes the run fail. A run with none prints the summary it
// always has, so an all-green run reads exactly as it does with no blocked
// support at all.
func TestBlockedCasesAreCountedApartAndOnlyWhenPresent(t *testing.T) {
	var clean bytes.Buffer
	rep := NewTestReporter(&clean)
	rep.Result("f :: a", nil)
	if failed := rep.Summary(); failed {
		t.Fatal("a clean run reported failure")
	}
	if got, want := clean.String(), "ok f :: a\ntest result: ok. 1 passed, 0 failed\n"; got != want {
		t.Fatalf("clean report %q, want %q", got, want)
	}

	var b bytes.Buffer
	rep = NewTestReporter(&b)
	rep.Result("f :: a", nil)
	rep.Blocked("f :: b", []string{"[g] not retained: a tail plan", "[h] not retained: x"})
	if failed := rep.Summary(); !failed {
		t.Fatal("a run with a blocked case did not fail")
	}
	want := "ok f :: a\n" +
		"BLOCKED f :: b [g] not retained: a tail plan\n" +
		"BLOCKED f :: b [h] not retained: x\n" +
		"test result: BLOCKED. 1 passed, 0 failed, 1 blocked\n"
	if got := b.String(); got != want {
		t.Fatalf("report:\n%q\nwant:\n%q", got, want)
	}
	if rep.BlockedCount() != 1 {
		t.Fatalf("BlockedCount = %d, want 1", rep.BlockedCount())
	}

	b.Reset()
	rep = NewTestReporter(&b)
	rep.Result("f :: a", errString("boom"))
	rep.Blocked("f :: b", nil)
	rep.Summary()
	if !strings.HasSuffix(b.String(), "test result: FAILED. 0 passed, 1 failed, 1 blocked\n") {
		t.Fatalf("a failing run with a blocked case reported:\n%s", b.String())
	}
}

type errString string

func (e errString) Error() string { return string(e) }
