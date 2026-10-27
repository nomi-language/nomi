package vmhost_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// subtestsCountReport runs src's tests as `nomi test` reports them, with no
// colour, and answers the cases and the report.
func subtestsCountReport(t *testing.T, src string) ([]vmhost.CaseResult, string) {
	t.Helper()
	t.Setenv("NOMI_COLOR", "never")
	const name = "rows_test.nomi"
	p, err := vmhost.LoadSource(name, src)
	if err != nil {
		t.Fatalf("the front end rejects the tests: %v", err)
	}
	var buf bytes.Buffer
	cases := p.Cases(&buf, vmhost.TestOptions{})
	rep := vmhost.NewTestReport(&buf)
	p.Test(&buf, rep, name, vmhost.TestOptions{}, func(n string) string { return n })
	rep.Summary()
	return cases, buf.String()
}

// A table of nine rows of which one fails counts as nine cases, eight passed
// and one failed, and the FAIL block still names the failing row.
func TestSubtestRowsCountAsCases_OneFailingRow(t *testing.T) {
	cases, out := subtestsCountReport(t, `test "squares" {
    subtests 1..=9, n {
        assert n * n != 49
    }
}
`)
	if len(cases) != 1 || cases[0].Cases != 9 || cases[0].FailedCases != 1 || !cases[0].Subtests {
		t.Fatalf("want one test of 9 cases with 1 failed, got %+v", cases)
	}
	if !strings.HasPrefix(out, "FAIL squares\n") || !strings.Contains(out, "1 of 9 subtests failed") ||
		!strings.Contains(out, "subtest 7") {
		t.Fatalf("the failing row's report changed:\n%s", out)
	}
	if !strings.HasSuffix(out, "test result: FAILED. 8 passed, 1 failed\n") {
		t.Fatalf("want 8 passed and 1 failed:\n%s", out)
	}
}

// Two tables in one test count their rows summed, and a passing test names
// its count on its ok line, beside a test with no table, which is one case.
func TestSubtestRowsCountAsCases_TwoTablesInOneTest(t *testing.T) {
	cases, out := subtestsCountReport(t, `test "two tables" {
    subtests [1, 2, 3], n {
        assert n > 0
    }
    subtests ["a", "b"], s {
        assert s != ""
    }
}

test "one row" {
    subtests [1], n {
        assert n == 1
    }
}

test "plain" {
    assert 1 + 1 == 2
}
`)
	if len(cases) != 3 || cases[0].Cases != 5 || cases[1].Cases != 1 || cases[2].Cases != 1 || cases[2].Subtests {
		t.Fatalf("want 5, 1 and 1 cases, got %+v", cases)
	}
	want := "ok two tables (5 cases)\n" +
		"ok one row (1 case)\n" +
		"ok plain\n" +
		"test result: ok. 7 passed, 0 failed\n"
	if out != want {
		t.Fatalf("report:\n%s\nwant:\n%s", out, want)
	}
}

// A failure after a table's rows passed is one more case, and it failed.
func TestSubtestRowsCountAsCases_AFailureAfterTheRows(t *testing.T) {
	cases, out := subtestsCountReport(t, `test "rows then assert" {
    subtests [1, 2, 3], n {
        assert n > 0
    }
    assert 1 == 2
}
`)
	if len(cases) != 1 || cases[0].Cases != 4 || cases[0].FailedCases != 1 {
		t.Fatalf("want 4 cases with 1 failed, got %+v", cases)
	}
	if !strings.HasSuffix(out, "test result: FAILED. 3 passed, 1 failed\n") {
		t.Fatalf("want 3 passed and 1 failed:\n%s", out)
	}
}
