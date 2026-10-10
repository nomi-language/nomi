package irbuild

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/vm"
)

// A block whose tail never finishes, an `Iter.loop` with no `break` or a
// block ending in one, ends the block that holds it, as the loop itself
// does: nothing after it is lowered and nothing writes the block's value.
// Each program leaves by a trap after its exact output.
func TestIRLoopEndless_BlockEndingInALoop(t *testing.T) {
	// The block statement ending main, nested two deep.
	verifyEndlessTrap(t, `import std/io

fn main() {
    zero = 0
    io.print("main")
    {
        io.print("start")
        {
            io.print("inner")
            Iter.loop(|| {
                io.print("again")
                io.print(1 / zero)
            })
        }
    }
}
`, "line 12: division by zero", "main\nstart\ninner\nagain\n")

	// A block value bound to a name, with code after it that never runs,
	// and a block ending an Int function.
	verifyEndlessTrap(t, `import std/io

fn tail_block(): Int {
    {
        io.print("tail block")
        Iter.loop(|n = 0| {
            if n == 1 { io.print(1 / (n - 1)) }
            n + 1
        })
    }
}

fn main() {
    x = {
        io.print("value block")
        _ = tail_block()
        Iter.loop(|| {
            io.print("never")
        })
    }
    io.inspect(x)
}
`, "line 7: division by zero", "value block\ntail block\n")

	// Arms that are blocks ending in a block that never finishes, in a
	// binding's `case` and a statement `if`, and a block that never
	// finishes as an operand. Each main reaches a different one.
	pick := `import std/io

fn pick(flag: Int): Int {
    y = case flag {
        0 -> {
            io.print("arm 0")
            {
                Iter.loop(|n = 0| {
                    if n == 1 { io.print(1 / (n - 1)) }
                    n + 1
                })
            }
        }
        _ -> flag * 10
    }
    io.inspect(y)
    if y > 15 {
        {
            io.print("if branch")
            Iter.loop(|| {
                io.print(1 / (y - y))
            })
        }
    } else {
        io.print("else")
    }
    y
}
`
	verifyEndlessTrap(t, pick+`
fn main() {
    io.inspect(pick(1) + pick(0))
}
`, "line 9: division by zero", "10\nelse\narm 0\n")
	verifyEndlessTrap(t, pick+`
fn main() {
    io.inspect(pick(1) + pick(2))
}
`, "line 21: division by zero", "10\nelse\n20\nif branch\n")
	verifyEndlessTrap(t, pick+`
fn main() {
    io.inspect(pick(1) + {
        io.print("operand")
        Iter.loop(|n = 0| {
            if n == 1 { io.print(1 / (n - 1)) }
            n + 1
        })
    })
}
`, "line 34: division by zero", "10\nelse\noperand\n")

	// A lambda's body, a loop body and a concurrent body, each ending in a
	// block that never finishes.
	verifyEndlessTrap(t, `import std/io

fn main() {
    zero = 0
    f = |label: String| {
        io.print(label)
        {
            Iter.loop(|| {
                io.print("lambda loop")
                io.print(1 / zero)
            })
        }
    }
    total = Iter.loop(|i = 0| {
        if i == 2 { break i }
        io.print("outer ${i}")
        i + 1
    })
    io.inspect(total)
    concurrent {
        io.print("concurrent")
        {
            f("lambda")
        }
    }
}
`, "line 10: division by zero", "outer 0\nouter 1\n2\nconcurrent\nlambda\nlambda loop\n")

	verifyEndlessTrap(t, `import std/io

fn main() {
    _ = Iter.loop(|i = 0| {
        io.print("outer ${i}")
        {
            io.print("inner block")
            Iter.loop(|n = 0| {
                if n == 1 { io.print(1 / (n - 1)) }
                n + 1
            })
        }
    })
}
`, "line 9: division by zero", "outer 0\ninner block\n")
}

// A test body's block statement, and a block value it binds, ending in a
// loop that never finishes: each case fails at its trap after the output
// before it, and nothing after the block runs.
func TestIRLoopEndless_TestBodyBlocks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "endless_block_test.nomi")
	src := `import std/io

test "a block statement" {
    assert 1 == 1
    {
        io.print("block")
        Iter.loop(|| {
            io.print(1 / 0)
        })
    }
}

test "a bound block" {
    assert 2 == 2
    x = {
        io.print("bound block")
        {
            Iter.loop(|n = 0| {
                if n == 1 { io.print(1 / (n - 1)) }
                n + 1
            })
        }
    }
    io.print("never")
    io.inspect(x)
}
`
	if err := os.WriteFile(path, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := Analyze(path)
	if err != nil {
		t.Fatalf("the front end rejects the fixture, so nothing below is tested: %v", err)
	}
	declined := map[string]string{}
	prev := IRDeclineObserved
	IRDeclineObserved = func(fn, reason string) { declined[fn] = reason }
	res, _, err := GenerateIR(p)
	IRDeclineObserved = prev
	if err != nil {
		t.Fatal(err)
	}
	var module *ir.Module
	for _, m := range res.IR {
		if m.Name() == path {
			module = m
		}
	}
	if module == nil || len(module.Tests()) != 2 {
		t.Fatalf("not every case was retained (declines: %v)", declined)
	}
	var out bytes.Buffer
	exit, reason := vm.NewProgram(module, res.IRModules(), &out).RunTests(path)
	if reason != "" {
		t.Fatalf("the VM could not run the cases: %s", reason)
	}
	report := out.String()
	if exit == 0 || strings.Contains(report, "never") {
		t.Fatalf("exit %d, report:\n%s", exit, report)
	}
	for _, want := range []string{
		"block\n", "line 8: division by zero",
		"bound block\n", "line 19: division by zero",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("the report lacks %q:\n%s", want, report)
		}
	}
}
