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

// A block whose tail is an `if` or `case` every arm of which leaves, or a
// `return`, `break` or `continue`, leaves itself: it ends the block holding
// it, as the region does, and nothing writes its value. Before, such a block
// used as a value declined with "this block is not supported yet".

// A named function's block leaving by `return` as a binding's value, an
// operand of `+`, a call argument, a struct field, a list item, nested two
// deep, a function's tail and inside an arm block; each main call reaches a
// different arm.
func TestIRLeavingBlock_Returns(t *testing.T) {
	verifyLambdaProgram(t, `import std/io

struct P {
    a: Int
    b: Int
}

fn calls(x: Int) -> Int {
    x
}

fn bind(n: Int) -> Int {
    x = {
        io.print("bind")
        case n {
            0 -> return 1
            _ -> return 2
        }
    }
    io.inspect(x)
    x
}

fn bare_return(n: Int) -> Int {
    x = {
        io.print("bare return")
        return n + 1
    }
    io.inspect(x)
    x
}

fn operand(n: Int) -> Int {
    1 + {
        io.print("operand")
        if n > 0 { return 3 } else { return 4 }
    }
}

fn argument(n: Int) -> Int {
    calls({
        io.print("argument")
        if n > 0 { return 5 } else { return 6 }
    })
}

fn field(n: Int) -> Int {
    p = #P{a: 1, b: {
        io.print("field")
        case n {
            0 -> return 7
            _ -> return 8
        }
    }}
    p.a
}

fn item(n: Int) -> Int {
    xs = [calls(1), {
        io.print("item")
        case n {
            0 -> return 9
            _ -> return 10
        }
    }]
    io.inspect(xs)
    0
}

fn nested(n: Int) -> Int {
    x = {
        io.print("outer")
        y = {
            io.print("inner")
            case n {
                0 -> return 11
                _ -> return 12
            }
        }
        io.inspect(y)
        y
    }
    x
}

fn tail_block(n: Int) -> Int {
    {
        io.print("tail block")
        case n {
            0 -> return 13
            _ -> return 14
        }
    }
}

fn arms(n: Int) -> Int {
    y = case n {
        0 -> {
            io.print("arm")
            z = {
                io.print("arm block")
                if n == 0 { return 15 } else { return 16 }
            }
            z
        }
        _ -> n
    }
    if y > 100 {
        {
            io.print("if branch")
            w = {
                return y + 1
            }
            w
        }
    } else {
        y
    }
}

fn main() {
    io.inspect(bind(0))
    io.inspect(bind(1))
    io.inspect(bare_return(5))
    io.inspect(operand(1))
    io.inspect(argument(0))
    io.inspect(field(0))
    io.inspect(item(1))
    io.inspect(nested(0))
    io.inspect(tail_block(1))
    io.inspect(arms(0))
    io.inspect(arms(200))
    io.inspect(arms(3))
}
`, "bind\n1\nbind\n2\nbare return\n6\noperand\n3\nargument\n6\nfield\n7\nitem\n10\n"+
		"outer\ninner\n11\ntail block\n14\narm\narm block\n15\nif branch\n201\n3\n")
}

// A block that leaves by `return` runs its deferred calls on the way out,
// most recent first, then the enclosing body's, as a `return` written there
// does.
func TestIRLeavingBlock_Defer(t *testing.T) {
	verifyLambdaProgram(t, `import std/io

fn deferred(n: Int) -> Int {
    defer io.print("outer defer")
    x = {
        defer io.print("inner defer 1")
        defer io.print("inner defer 2")
        io.print("deferred")
        {
            defer io.print("innermost defer")
            case n {
                0 -> return 1
                _ -> return 2
            }
        }
    }
    io.print("never")
    x
}

fn main() {
    io.inspect(deferred(0))
    io.inspect(deferred(1))
}
`, "deferred\ninnermost defer\ninner defer 2\ninner defer 1\nouter defer\n1\n"+
		"deferred\ninnermost defer\ninner defer 2\ninner defer 1\nouter defer\n2\n")
}

// The same in a lambda's body, where `return` leaves the lambda; in an
// Iter.map and an Iter.filter callback, whose arms leave by `break` and
// `continue`; and in an Iter.loop body, where `return v` is the next state
// and `break v` ends the loop.
func TestIRLeavingBlock_LambdasAndLoops(t *testing.T) {
	verifyLambdaProgram(t, `import std/io

fn lambda(n: Int) -> Int {
    f = fn m: Int -> {
        x = {
            io.print("lambda")
            case m {
                0 -> return 10
                _ -> return 20
            }
        }
        io.inspect(x)
        m
    }
    f(n)
}

fn mapped(xs: List<Int>) -> List<Int> {
    Iter.map(xs, fn x -> {
        y = {
            io.print("map ${x}")
            case x {
                1 -> continue
                3 -> break 30
                _ -> return x * 10
            }
        }
        y
    })
    |> Iter.to_list()
}

fn filtered(xs: List<Int>) -> List<Int> {
    Iter.filter(xs, fn x -> {
        {
            io.print("filter ${x}")
            if x == 2 { continue } else { if x > 3 { break True } else { return True } }
        }
    })
    |> Iter.to_list()
}

fn looped() -> Int {
    Iter.loop(fn i = 0 -> {
        x = {
            io.print("iteration ${i}")
            case i {
                2 -> break i * 100
                _ -> return i + 1
            }
        }
        x
    })
}

fn main() {
    io.inspect(lambda(1))
    io.inspect(mapped([0, 1, 2, 3, 4]))
    io.inspect(filtered([1, 2, 3, 4, 5]))
    io.inspect(looped())
}
`, "lambda\n20\nmap 0\nmap 1\nmap 2\nmap 3\n[0, 20, 30]\n"+
		"filter 1\nfilter 2\nfilter 3\nfilter 4\n[1, 3, 4]\n"+
		"iteration 0\niteration 1\niteration 2\n200\n")
}

// A test body's block that leaves by `return`, bound, as a statement block's
// tail binding, and as a call argument, and a bound `case` whose arms all
// return: each case ends at the `return`, after its deferred calls, and
// nothing after it runs.
func TestIRLeavingBlock_TestBodies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "leaving_block_test.nomi")
	src := `import std/io

test "a bound block that returns" {
    n = 1
    x = {
        defer io.print("bound defer")
        io.print("bound block")
        case n {
            0 -> return
            _ -> return
        }
    }
    io.inspect(x)
    assert False
}

test "a block statement whose binding returns" {
    assert 1 == 1
    {
        defer io.print("block defer")
        io.print("block")
        y = if 1 > 0 {
            io.print("then")
            return
        } else {
            return
        }
        io.inspect(y)
    }
    io.print("never")
}

test "a call argument block that returns" {
    io.inspect({
        io.print("argument")
        return
    })
    assert False
}

test "a bound case that returns" {
    n = 0
    x = case n {
        0 -> {
            io.print("arm")
            return
        }
        _ -> return
    }
    io.inspect(x)
    assert False
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
	if module == nil || len(module.Tests()) != 4 {
		t.Fatalf("not every case was retained (declines: %v)", declined)
	}
	var out bytes.Buffer
	exit, reason := vm.NewProgram(module, res.IRModules(), &out).RunTests(path)
	if reason != "" {
		t.Fatalf("the VM could not run the cases: %s", reason)
	}
	want := strings.ReplaceAll(`ok PATH :: a bound block that returns
  bound block
  bound defer
ok PATH :: a block statement whose binding returns
  block
  then
  block defer
ok PATH :: a call argument block that returns
  argument
ok PATH :: a bound case that returns
  arm
test result: ok. 4 passed, 0 failed
`, "PATH", path)
	if exit != 0 || out.String() != want {
		t.Fatalf("exit %d, report:\n%s", exit, out.String())
	}
}
