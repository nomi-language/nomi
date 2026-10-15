package vmhost

import (
	"bytes"
	"context"
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

// A block-valued binding that cannot lower is reported at the innermost
// construct inside it that declined, the call in the inner block, not at
// the enclosing function. The call is to `planted_decline` (plantDecline).
func TestBlockBindingDecline_IsReportedAtTheStatement(t *testing.T) {
	plantDecline(t)
	src := `import std/io

fn planted_decline(a: Int): Int {
    a
}

fn main() {
    v = {
        w = {
            u = planted_decline(1)
            u
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
	want := "main.nomi:10:17: this call to `planted_decline` is not supported yet, so `fn main` cannot run"
	if !strings.Contains(o.declines[0], want) {
		t.Fatalf("decline:\n%s\nwant it to contain %q", o.declines[0], want)
	}
}

// A binding at the end of a block shadows the outer name for the rest of
// that block only, so when nothing in the block reads it the front end
// reports it as never read. Before, it leaked into the enclosing scope:
// the check passed, the IR builder declined the body, and `nomi check`
// said the body was not supported yet.
func TestBlockBinding_UnreadShadowAtTheEndIsNeverRead(t *testing.T) {
	_, err := LoadSource("main.nomi", `import std/io

fn main() {
    total = 1
    if True {
        total = total + 1
    }
    io.print("${total}")
}
`)
	if err == nil {
		t.Fatal("the front end admits an inner binding nothing reads")
	}
	msg := err.Error()
	for _, want := range []string{"binding 'total' is never read", "main.nomi:6:9"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the error does not contain %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "not supported yet") {
		t.Errorf("the error is the lowering's, not the check's:\n%s", msg)
	}
}

// A block may end in a binding: one the block reads, or a `_` one. The
// block's value is then Unit, and the program runs.
func TestBlockBinding_ABlockEndingInABindingRuns(t *testing.T) {
	p, err := LoadSource("main.nomi", `import std/io

fn helper(n: Int) {
    if n > 0 {
        _x = n + 1
    } else {
        _y = n
    }
}

fn main() {
    total = 1
    if True {
        total = total + 1
        io.print("inner ${total}")
    }
    _w = {
        _z = 3
    }
    helper(1)
    f = || {
        _q = 4
    }
    f()
    case total {
        1 -> {
            _r = 1
        }
        _ -> Unit
    }
    concurrent {
        _c = 1
    }
    io.print("${total}")
}
`)
	if err != nil {
		t.Fatalf("the front end rejects the program, or it does not lower: %v", err)
	}
	var out bytes.Buffer
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatalf("run: %v\n%s", err, out.String())
	}
	if got, want := out.String(), "inner 2\n1\n"; got != want {
		t.Errorf("output: got %q, want %q", got, want)
	}
}
