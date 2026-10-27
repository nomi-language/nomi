package rt

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// TestTypedNilTestFailureIsAPass pins the one hazard of Test.Fn returning an
// interface, and it fails on the answer rather than on a type.
//
// A nil *AssertionFailure boxed into an interface compares non-nil, so a plain
// `error` return would let a passing case be reported as a failure with no
// failure in it. Test.Fn returns TestFailure, an interface, because a `try`
// propagating inside a test body ends the case as an EarlyReturnFailure. The
// nil-receiver Failure() method is what keeps a nil failure of either concrete
// type reading as a pass.
//
// The mutation this defends against is deleting either `if e == nil` guard, or
// changing runTest to `return failure` instead of `return failure.Failure()`.
// Either makes this test report a failure for a body that produced none, which
// in a real run would be every case failing.
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
			if _, _, err := runTest(fr, Test{Name: tc.name, Fn: tc.fn}); err != nil {
				t.Fatalf("a body that produced no failure was reported as failing: %v", err)
			}
			var b bytes.Buffer
			rep := NewTestReporter(&b)
			_, _, err := runTest(fr, Test{Name: tc.name, Fn: tc.fn})
			rep.Result(tc.name, err, "", CaseCount{})
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
	_, _, err := runTest(fr, Test{Name: "try propagates", Fn: body})
	if err == nil {
		t.Fatal("an EarlyReturnFailure returned from a body did not reach the reporter")
	}
	var b bytes.Buffer
	rep := NewTestReporter(&b)
	rep.Result("try propagates", err, "", CaseCount{})
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
	rep.Result("f :: a", nil, "", CaseCount{})
	if failed := rep.Summary(); failed {
		t.Fatal("a clean run reported failure")
	}
	if got, want := clean.String(), "ok f :: a\ntest result: ok. 1 passed, 0 failed\n"; got != want {
		t.Fatalf("clean report %q, want %q", got, want)
	}

	var b bytes.Buffer
	rep = NewTestReporter(&b)
	rep.Result("f :: a", nil, "", CaseCount{})
	rep.Blocked("f :: b", []string{"f:3:5: this call to `g` is not supported yet, so `fn h` cannot run\nf:3:5: help: pass `g`", "[h] not retained: x"}, "")
	if failed := rep.Summary(); !failed {
		t.Fatal("a run with a blocked case did not fail")
	}
	want := "ok f :: a\n" +
		"BLOCKED f :: b f:3:5: this call to `g` is not supported yet, so `fn h` cannot run\n" +
		"  f:3:5: help: pass `g`\n" +
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
	rep.Result("f :: a", errString("boom"), "", CaseCount{})
	rep.Blocked("f :: b", nil, "")
	rep.Summary()
	if !strings.HasSuffix(b.String(), "test result: FAILED. 0 passed, 1 failed, 1 blocked\n") {
		t.Fatalf("a failing run with a blocked case reported:\n%s", b.String())
	}
}

// A test that ran `subtests` tables counts each row as a case: a passing one
// prints its count on its ok line, a failing one counts its failed rows as
// failed cases and the rest as passed, and the summary adds cases, not tests.
func TestSubtestRowsAreCountedAsCases(t *testing.T) {
	rowFailure := func(n int) error {
		failed := make([]SubtestFailure, n)
		for i := range failed {
			failed[i] = SubtestFailure{Row: "0", Failure: &AssertionFailure{Line: 4, Keyword: "assert", Expr: "false"}}
		}
		return SubtestsFailure(3, "rows", 9, failed)
	}
	for _, tc := range []struct {
		name  string
		rows  int
		err   error
		count CaseCount
	}{
		{"no table, passed", 0, nil, CaseCount{Cases: 1}},
		{"no table, failed", 0, errString("boom"), CaseCount{Cases: 1, Failed: 1}},
		{"one row", 1, nil, CaseCount{Subtests: true, Cases: 1}},
		{"nine rows", 9, nil, CaseCount{Subtests: true, Cases: 9}},
		{"one of nine failed", 9, rowFailure(1), CaseCount{Subtests: true, Cases: 9, Failed: 1}},
		{"a fault after the rows", 9, errString("line 7: division by zero"), CaseCount{Subtests: true, Cases: 10, Failed: 1}},
	} {
		if got := CountCases(tc.rows, tc.err); got != tc.count {
			t.Errorf("%s: CountCases = %+v, want %+v", tc.name, got, tc.count)
		}
	}

	var b bytes.Buffer
	rep := NewTestReporter(&b)
	rep.Result("f :: phrases", nil, "", CountCases(9, nil))
	rep.Result("f :: one", nil, "", CountCases(1, nil))
	rep.Result("f :: plain", nil, "", CaseCount{})
	if failed := rep.Summary(); failed {
		t.Fatal("a passing run reported failure")
	}
	want := "ok f :: phrases (9 cases)\n" +
		"ok f :: one (1 case)\n" +
		"ok f :: plain\n" +
		"test result: ok. 11 passed, 0 failed\n"
	if got := b.String(); got != want {
		t.Fatalf("report:\n%q\nwant:\n%q", got, want)
	}

	b.Reset()
	rep = NewTestReporter(&b)
	err := rowFailure(1)
	rep.Result("f :: phrases", err, "", CountCases(9, err))
	if failed := rep.Summary(); !failed {
		t.Fatal("a run with a failed row did not fail")
	}
	if !strings.HasPrefix(b.String(), "FAIL f :: phrases\n") || !strings.Contains(b.String(), "1 of 9 subtests failed") {
		t.Fatalf("a failed row's report lost its FAIL line or its rows:\n%s", b.String())
	}
	if !strings.HasSuffix(b.String(), "test result: FAILED. 8 passed, 1 failed\n") {
		t.Fatalf("a test of 9 rows with 1 failed reported:\n%s", b.String())
	}
}

// The JSON report gives each test record its count of cases, a failed one
// how many of them failed, and counts cases in its summary.
func TestSubtestRowsAreCountedInTheJSONReport(t *testing.T) {
	var b bytes.Buffer
	rep := NewTestReporterFormat(&b, TestFormatJSON)
	rep.ResultAt(TestLocation{File: "/a_test.nomi", Line: 3, EndLine: 9}, "a_test.nomi :: phrases", nil, "", CountCases(9, nil))
	err := SubtestsFailure(12, "rows", 4, []SubtestFailure{{Row: "1", Failure: &AssertionFailure{Line: 13, Keyword: "assert", Expr: "false"}}})
	rep.ResultAt(TestLocation{File: "/a_test.nomi", Line: 11, EndLine: 15}, "a_test.nomi :: words", err, "", CountCases(4, err))
	rep.ResultAt(TestLocation{File: "/a_test.nomi", Line: 17, EndLine: 19}, "a_test.nomi :: plain", nil, "", CaseCount{})
	rep.BlockedAt(TestLocation{File: "/a_test.nomi", Line: 21, EndLine: 23}, "a_test.nomi :: blocked", []string{"x"}, "")
	rep.Summary()
	lines := strings.Split(strings.TrimSuffix(b.String(), "\n"), "\n")
	if len(lines) != 5 {
		t.Fatalf("want 5 records, got:\n%s", b.String())
	}
	for i, want := range []string{
		`"status":"passed","cases":9,"message":""`,
		`"status":"failed","cases":4,"failed_cases":1,"message":`,
		`"status":"passed","cases":1,"message":""`,
		`"status":"blocked","cases":1,"message":"x"`,
	} {
		if !strings.Contains(lines[i], want) {
			t.Errorf("record %d lacks %s:\n%s", i, want, lines[i])
		}
	}
	if !strings.Contains(lines[0], `"report":"ok a_test.nomi :: phrases (9 cases)\n"`) {
		t.Errorf("a passing table's report does not name its count:\n%s", lines[0])
	}
	if got, want := lines[4], `{"type":"summary","passed":13,"failed":1,"blocked":1}`; got != want {
		t.Errorf("summary %s, want %s", got, want)
	}
}

// A case's rows are what its `subtests` statements ran, summed; one that
// runs inside another's row adds nothing, and nothing outside a case counts.
func TestEnterSubtestsCountsTheCasesRows(t *testing.T) {
	fr := NewFrame(context.Background())
	EnterSubtests(fr)(5) // outside every case
	_, rows, err := runTest(fr, Test{Name: "two tables", Fn: func(fr *Frame) TestFailure {
		EnterSubtests(fr)(3)
		outer := EnterSubtests(fr)
		EnterSubtests(fr)(7) // inside a row of outer
		outer(4)
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if rows != 7 {
		t.Fatalf("the case ran %d rows, want 3 + 4 = 7", rows)
	}
}

type errString string

func (e errString) Error() string { return string(e) }
