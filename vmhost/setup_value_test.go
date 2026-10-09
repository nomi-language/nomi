package vmhost_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// A test binds a Unit setup value as any Unit binding does, and a setup whose
// final statement is `assert x` or `refute x` has the assertion's value, the
// judged subject (spec §36), as the checker types it: True, False or the
// Maybe, not Unit. A setup ending in a statement, an empty one and one whose
// `return` is bare are Unit.
func TestRun_SetupValuesTestsBind(t *testing.T) {
	const name = "main_test.nomi"
	p, err := vmhost.LoadSource(name, `import std/io

fn flag(): Bool {
  False
}

tests "asserted" {
  setup {
    io.print("setup a")
    assert 1 == 1
  }

  test "binds", v {
    assert v
  }

  test "binds nothing" {
    assert True
  }

  test "discards", _ {
    assert True
  }
}

tests "printed" {
  setup io.print("setup b")

  test "binds", u {
    x = io.print("x")
    assert u == x
  }
}

tests "statement" {
  setup {
    io.print("setup c")
  }

  test "binds", w {
    assert w == Unit
  }
}

tests "maybe subject" {
  setup {
    assert Some(2)
  }

  test "binds the subject", m {
    assert m == Some(2)
  }
}

tests "failing tail" {
  setup {
    assert flag()
  }

  test "never runs its body", v {
    io.print("unreachable")
    assert v
  }
}

tests "returning" {
  setup {
    if flag() {
      return False
    }
    refute flag()
  }

  test "binds the refuted value", v {
    refute v
  }
}

tests "returning unit" {
  setup {
    if flag() {
      return
    }
    io.print("setup d")
  }

  test "binds unit", u {
    assert u == Unit
  }
}

tests "empty" {
  setup {}

  test "binds unit", u {
    assert u == Unit
  }
}
`)
	if err != nil {
		t.Fatalf("the front end rejects the tests: %v", err)
	}
	var buf bytes.Buffer
	rep := vmhost.NewTestReport(&buf)
	p.Test(&buf, rep, name, vmhost.TestOptions{}, func(n string) string { return vmhost.TestName(name, n) })
	rep.Summary()
	out := buf.String()
	if !strings.HasPrefix(out, "setup a\nsetup a\nsetup a\nsetup b\nx\nsetup c\nsetup d\n") ||
		strings.Contains(out, "BLOCKED") || strings.Contains(out, "unreachable") ||
		!strings.Contains(out, "FAIL main_test.nomi :: failing tail / never runs its body\n  line 57: assertion failed\n    assert flag()\n") ||
		!strings.Contains(out, "9 passed, 1 failed") {
		t.Fatalf("want every setup to run, nine passing cases and the failing tail:\n%s", out)
	}
}
