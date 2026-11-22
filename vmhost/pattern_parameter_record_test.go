package vmhost_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// A one-variant enum's struct-shaped variant, taken apart as the record of
// its fields (`One.Rec(r)`, `One.Rec({w:, h:})`), binds in a function
// parameter, a lambda parameter, a parameter's nested pattern and a test's
// setup pattern, as it does in a `case` arm and a binding.
func TestRun_AStructVariantsRecordBindsInEveryParameterPosition(t *testing.T) {
	t.Setenv("NOMI_COLOR", "never")
	const name = "record_param_test.nomi"
	p, err := vmhost.LoadSource(name, `enum One
    Rec {w: Int, h: Int}
end

fn whole(One.Rec(r)) -> Int
    r.w * r.h
end

fn fields(One.Rec({w:, h:})) -> Int
    w + h
end

fn nested((One.Rec(r), n): (One, Int)) -> Int
    r.w + n
end

test "function parameters"
    assert whole(One.Rec{w: 2, h: 3}) == 6
    assert fields(One.Rec{w: 2, h: 3}) == 5
    assert nested((One.Rec{w: 5, h: 0}, 1)) == 6
end

test "a lambda parameter"
    f = fn One.Rec(r) -> r.w - r.h
    assert f(One.Rec{w: 9, h: 3}) == 6
end

group "records"
    setup One.Rec{w: 2, h: 5}

    test "a setup pattern", One.Rec({w:, h:} as r)
        assert w * h == 10
        assert r.h == 5
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
	if out := buf.String(); strings.Contains(out, "BLOCKED") || !strings.Contains(out, "3 passed, 0 failed") {
		t.Fatalf("want three passing cases:\n%s", out)
	}
}
