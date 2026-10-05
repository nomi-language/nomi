package analysis_test

import (
	"strings"
	"testing"
)

// A `return value` in a group's setup is a value of the setup, joined with its
// tail as a lambda's returns are: the case's pattern binds the joined type.
func TestSetupReturn_TypesTheSetupValue(t *testing.T) {
	errs := checkWithStdlib(`
tests "g" {
  setup {
    x = 1
    if x > 0 {
      return "positive"
    }
    "other"
  }

  test "t", v {
    assert v == 2
  }
}
`)
	if len(errs) != 1 || !strings.Contains(errs[0].Message, "String vs Int") {
		t.Fatalf("want the one error that v is a String, got %v", errs)
	}
}

// A setup whose only exit is a `return` has the returned value's type.
func TestSetupReturn_ReturnOnly(t *testing.T) {
	errs := checkWithStdlib(`
tests "g" {
  setup {
    x = 1
    return x + 1
  }

  test "t", v {
    assert v == 2
  }
}
`)
	if len(errs) != 0 {
		t.Fatalf("want no errors, got %v", errs)
	}
	errs = checkWithStdlib(`
tests "g" {
  setup {
    return "s"
  }

  test "t", v {
    assert v == 2
  }
}
`)
	if len(errs) != 1 || !strings.Contains(errs[0].Message, "String vs Int") {
		t.Fatalf("want the one error that v is a String, got %v", errs)
	}
}

// A return whose value disagrees with the setup's tail is an error at the
// return.
func TestSetupReturn_DisagreeingReturnIsAnError(t *testing.T) {
	errs := checkWithStdlib(`
tests "g" {
  setup {
    if True {
      return 1
    }
    "x"
  }

  test "t", v {
    assert v == "x"
  }
}
`)
	if len(errs) != 1 || !strings.Contains(errs[0].Message, "return type mismatch") {
		t.Fatalf("want one return type mismatch, got %v", errs)
	}
}
