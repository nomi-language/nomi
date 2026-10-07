package analysis_test

import (
	"fmt"
	"strings"
	"testing"
)

// A test or group named by an empty or whitespace-only string is an error at
// the name: a report could not say which case it means.
func TestEmptyTestName_IsAnErrorAtTheName(t *testing.T) {
	errs := checkWithStdlib(`
test "" {
  assert 1 == 1
}

tests "  " {
  test "\t" {
    assert True
  }

  test "named" {
    assert True
  }
}

test "fine" {
  assert True
}
`)
	var got []string
	for _, e := range errs {
		got = append(got, fmt.Sprintf("%d:%d %s", e.Line, e.Col, e.Message))
	}
	want := []string{
		"2:6 a test name must not be empty; name the test for what it checks, such as `test \"parses a date\"`",
		"6:7 a `tests` group name must not be empty; name the group for what its tests share, such as `tests \"dates\"`",
		"7:8 a test name must not be empty; name the test for what it checks, such as `test \"parses a date\"`",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("errors:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
