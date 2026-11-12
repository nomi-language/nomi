package vmhost_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// A test binds a Unit setup value as any Unit binding does, and a setup whose
// final statement is `assert x` or `refute x` has the assertion's value, the
// judged subject (spec §17), as the checker types it: True, False or the
// Maybe, not Unit. A setup ending in a statement, an empty one and one whose
// `return` is bare are Unit.
func TestRun_SetupValuesTestsBind(t *testing.T) {
	const name = "main_test.nomi"
	p, err := vmhost.LoadSource(name, `import std::io

fn flag() -> Bool
  False
end

group "asserted"
  setup begin
    io.print("setup a")
    assert 1 == 1
  end

  test "binds", v
    assert v
  end

  test "binds nothing"
    assert True
  end

  test "discards", _
    assert True
  end
end

group "printed"
  setup io.print("setup b")

  test "binds", u
    x = io.print("x")
    assert u == x
  end
end

group "statement"
  setup begin
    io.print("setup c")
  end

  test "binds", w
    assert w == Unit
  end
end

group "maybe subject"
  setup begin
    assert Some(2)
  end

  test "binds the subject", m
    assert m == Some(2)
  end
end

group "failing tail"
  setup begin
    assert flag()
  end

  test "never runs its body", v
    io.print("unreachable")
    assert v
  end
end

group "returning"
  setup begin
    if flag() then
      return False
    end
    refute flag()
  end

  test "binds the refuted value", v
    refute v
  end
end

group "returning unit"
  setup begin
    if flag() then
      return
    end
    io.print("setup d")
  end

  test "binds unit", u
    assert u == Unit
  end
end

group "empty"
  setup begin end

  test "binds unit", u
    assert u == Unit
  end
end
`)
	if err != nil {
		t.Fatalf("the front end rejects the tests: %v", err)
	}
	var buf bytes.Buffer
	rep := vmhost.NewTestReport(&buf)
	p.Test(&buf, rep, name, vmhost.TestOptions{}, func(n string) string { return vmhost.TestName(name, n) })
	rep.Summary()
	out := buf.String()
	// Each setup's output is reported under the case it ran for.
	if strings.Count(out, "\n  setup a\n") != 3 ||
		!strings.Contains(out, "ok main_test.nomi :: printed / binds\n  setup b\n  x\n") ||
		!strings.Contains(out, "ok main_test.nomi :: statement / binds\n  setup c\n") ||
		!strings.Contains(out, "ok main_test.nomi :: returning unit / binds unit\n  setup d\n") ||
		strings.Contains(out, "BLOCKED") || strings.Contains(out, "unreachable") ||
		!strings.Contains(out, "FAIL main_test.nomi :: failing tail / never runs its body\n  line 57: assertion failed\n    assert flag()\n") ||
		!strings.Contains(out, "9 passed, 1 failed") {
		t.Fatalf("want every setup to run, nine passing cases and the failing tail:\n%s", out)
	}
}
