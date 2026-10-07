package vmhost

import (
	"bytes"
	"strings"
	"testing"
)

// blockBindingTests binds blocks whose values branch inside test bodies,
// which the test-body builder lowers.
const blockBindingTests = `test "an if tail" {
    v = {
        a = 1
        if a > 0 { 1 } else { 2 }
    }
    assert v == 1
}

test "a case tail" {
    v = {
        a = Some(3)
        case a {
            Some(n) -> n * 2
            None -> 0
        }
    }
    assert v == 6
}

test "a block tail whose own tail branches" {
    v = {
        a = 2
        {
            w = {
                b = a + 1
                if b > 2 { "big" } else { "small" }
            }
            w + "!"
        }
    }
    assert v == "big!"
}
`

func TestBlockBindingBranches_RunInTests(t *testing.T) {
	t.Setenv("NOMI_COLOR", "never")
	const name = "blocks_test.nomi"
	p, err := LoadSource(name, blockBindingTests)
	if err != nil {
		t.Fatalf("lowering: %v", err)
	}
	var buf bytes.Buffer
	rep := NewTestReport(&buf)
	p.Test(&buf, rep, name, TestOptions{}, func(n string) string { return TestName(name, n) })
	failed := rep.Summary()
	out := buf.String()
	if failed || strings.Contains(out, "BLOCKED") || strings.Count(out, "ok ") != 3 {
		t.Fatalf("every case must run and pass:\n%s", out)
	}
}

// A block-valued binding that cannot lower is reported at the statement
// inside it that declined, not at the enclosing function. A block whose
// value has a type declared inside it is a known gap
// (gapBlockLocalTypeEscapesItsBlock); if it starts lowering, this test needs
// another construct the builder declines at a statement.
func TestBlockBindingDecline_IsReportedAtTheStatement(t *testing.T) {
	src := `import std/io

fn main() {
    v = {
        w = {
            struct Loc {
                n: Int
            }
            Loc{n: 1}
        }
        io.inspect(w)
        if True { 1 } else { 2 }
    }
    io.inspect(v)
}
`
	o := checkLowers(t.TempDir(), src)
	if !o.accepted {
		t.Fatalf("the front end rejects this, so it no longer tests a decline's position: %s", o.rejection)
	}
	if o.internal != "" {
		t.Fatalf("the compiler panics:\n%s", o.internal)
	}
	if len(o.declines) != 1 {
		t.Fatalf("want one decline, got %q", o.declines)
	}
	want := "main.nomi:5:9: its body is not supported yet, so `fn main` cannot run"
	if !strings.Contains(o.declines[0], want) {
		t.Fatalf("decline:\n%s\nwant it to contain %q", o.declines[0], want)
	}
}
