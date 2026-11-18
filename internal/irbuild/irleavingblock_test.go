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
	verifyLambdaProgram(t, `import std::io

struct P
    a: Int
    b: Int
end

fn calls(x: Int) -> Int
    x
end

fn bind(n: Int) -> Int
    _x = begin
        io.print("bind")
        case n
            0 -> return 1
            _ -> return 2
        end
    end
end

fn bare_return(n: Int) -> Int
    _x = begin
        io.print("bare return")
        return n + 1
    end
end

fn operand(n: Int) -> Int
    1 + begin
        io.print("operand")
        if n > 0 then return 3 else return 4
    end
end

fn argument(n: Int) -> Int
    calls(begin
        io.print("argument")
        if n > 0 then return 5 else return 6
    end)
end

fn field(n: Int) -> Int
    _p = P{a: 1, b: begin
        io.print("field")
        case n
            0 -> return 7
            _ -> return 8
        end
    end}
end

fn item(n: Int) -> Int
    _xs = [calls(1), begin
        io.print("item")
        case n
            0 -> return 9
            _ -> return 10
        end
    end]
end

fn nested(n: Int) -> Int
    _x = begin
        io.print("outer")
        _y = begin
            io.print("inner")
            case n
                0 -> return 11
                _ -> return 12
            end
        end
    end
end

fn tail_block(n: Int) -> Int
    begin
        io.print("tail block")
        case n
            0 -> return 13
            _ -> return 14
        end
    end
end

fn arms(n: Int) -> Int
    y = case n
        0 ->
            io.print("arm")
            _z = begin
                io.print("arm block")
                if n == 0 then return 15 else return 16
            end
        _ -> n
    end
    if y > 100 then
        begin
            io.print("if branch")
            _w = begin
                return y + 1
            end
        end
    else
        y
    end
end

fn main()
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
end
`, "bind\n1\nbind\n2\nbare return\n6\noperand\n3\nargument\n6\nfield\n7\nitem\n10\n"+
		"outer\ninner\n11\ntail block\n14\narm\narm block\n15\nif branch\n201\n3\n")
}

// A block that leaves by `return` runs its deferred calls on the way out,
// most recent first, then the enclosing body's, as a `return` written there
// does.
func TestIRLeavingBlock_Defer(t *testing.T) {
	verifyLambdaProgram(t, `import std::io

fn deferred(n: Int) -> Int
    defer io.print("outer defer")
    _x = begin
        defer io.print("inner defer 1")
        defer io.print("inner defer 2")
        io.print("deferred")
        begin
            defer io.print("innermost defer")
            case n
                0 -> return 1
                _ -> return 2
            end
        end
    end
end

fn main()
    io.inspect(deferred(0))
    io.inspect(deferred(1))
end
`, "deferred\ninnermost defer\ninner defer 2\ninner defer 1\nouter defer\n1\n"+
		"deferred\ninnermost defer\ninner defer 2\ninner defer 1\nouter defer\n2\n")
}

// The same in a lambda's body, where `return` leaves the lambda; in an
// Iter.map and an Iter.filter callback, whose arms leave by `break` and
// `continue`; and in an Iter.loop body, where `return v` is the next state
// and `break v` ends the loop.
func TestIRLeavingBlock_LambdasAndLoops(t *testing.T) {
	verifyLambdaProgram(t, `import std::io

fn lambda(n: Int) -> Int
    f = fn m: Int ->
        _x = begin
            io.print("lambda")
            case m
                0 -> return 10
                _ -> return 20
            end
        end
    end
    f(n)
end

fn mapped(xs: List<Int>) -> List<Int>
    Iter.map(xs, fn x ->
        _y = begin
            io.print("map ${x}")
            case x
                1 -> continue
                3 -> break 30
                _ -> return x * 10
            end
        end
    end)
    |> Iter.to_list()
end

fn filtered(xs: List<Int>) -> List<Int>
    Iter.filter(xs, fn x ->
        begin
            io.print("filter ${x}")
            if x == 2 then
                continue
            else
                if x > 3 then break True else return True
            end
        end
    end)
    |> Iter.to_list()
end

fn looped() -> Int
    Iter.loop(0, fn i ->
        _x = begin
            io.print("iteration ${i}")
            case i
                2 -> break i * 100
                _ -> return i + 1
            end
        end
    end)
end

fn main()
    io.inspect(lambda(1))
    io.inspect(mapped([0, 1, 2, 3, 4]))
    io.inspect(filtered([1, 2, 3, 4, 5]))
    io.inspect(looped())
end
`, "lambda\n20\nmap 0\nmap 1\nmap 2\nmap 3\n[0, 20, 30]\n"+
		"filter 1\nfilter 2\nfilter 3\nfilter 4\n[1, 3, 4]\n"+
		"iteration 0\niteration 1\niteration 2\n200\n")
}

// A test body's block that leaves by `return`, bound, as a statement block's
// tail binding, and as a call argument, and a bound `case` whose arms all
// return: each case ends at the `return`, after its deferred calls.
func TestIRLeavingBlock_TestBodies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "leaving_block_test.nomi")
	src := `import std::io

test "a bound block that returns"
    assert 1 == 1
    n = 1
    _x = begin
        defer io.print("bound defer")
        io.print("bound block")
        case n
            0 -> return
            _ -> return
        end
    end
end

test "a block statement whose binding returns"
    assert 1 == 1
    begin
        defer io.print("block defer")
        io.print("block")
        _y = if 1 > 0 then
            io.print("then")
            return
        else
            return
        end
    end
end

test "a call argument block that returns"
    assert 1 == 1
    io.inspect(begin
        io.print("argument")
        return
    end)
end

test "a bound case that returns"
    assert 1 == 1
    n = 0
    _x = case n
        0 ->
            io.print("arm")
            return
        _ -> return
    end
end
`
	if err := os.WriteFile(path, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := Analyze(path)
	if err != nil {
		t.Fatalf("the front end rejects the fixture, so nothing below is tested: %v", err)
	}
	declined := map[string]string{}
	p.Declines.Observed = func(fn, reason string) { declined[fn] = reason }
	res, _, err := GenerateIR(p)
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

// A test statement whose value holds an `if` every arm of which returns
// ends the case there, after the operands before it: `dbg`'s operand, a
// `defer`'s argument, which registers nothing, and a destructure's value.
func TestIRLeavingBlock_TestStatementOperands(t *testing.T) {
	path := filepath.Join(t.TempDir(), "leaving_operand_test.nomi")
	src := `import std::io

fn mark(n: Int) -> Int
    io.print("mark ${n}")
    n
end

fn note(a: Int, b: Int) -> Unit
    io.print("note ${a} ${b}")
end

fn pair(a: Int, b: Int) -> (Int, Int)
    (a, b)
end

test "a dbg whose operand leaves"
    assert 1 == 1
    c = True
    io.print("dbg")
    dbg(if c then return else return)
end

test "a defer whose argument leaves"
    assert 1 == 1
    c = True
    defer note(mark(1), if c then return else return)
end

test "a destructure whose value leaves"
    assert 1 == 1
    c = True
    (_a, _b) = pair(mark(2), if c then return else return)
end
`
	if err := os.WriteFile(path, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := Analyze(path)
	if err != nil {
		t.Fatalf("the front end rejects the fixture, so nothing below is tested: %v", err)
	}
	declined := map[string]string{}
	p.Declines.Observed = func(fn, reason string) { declined[fn] = reason }
	res, _, err := GenerateIR(p)
	if err != nil {
		t.Fatal(err)
	}
	var module *ir.Module
	for _, m := range res.IR {
		if m.Name() == path {
			module = m
		}
	}
	if module == nil || len(module.Tests()) != 3 {
		t.Fatalf("not every case was retained (declines: %v)", declined)
	}
	var out bytes.Buffer
	exit, reason := vm.NewProgram(module, res.IRModules(), &out).RunTests(path)
	if reason != "" {
		t.Fatalf("the VM could not run the cases: %s", reason)
	}
	want := strings.ReplaceAll(`ok PATH :: a dbg whose operand leaves
  dbg
ok PATH :: a defer whose argument leaves
  mark 1
ok PATH :: a destructure whose value leaves
  mark 2
test result: ok. 3 passed, 0 failed
`, "PATH", path)
	if exit != 0 || out.String() != want {
		t.Fatalf("exit %d, report:\n%s", exit, out.String())
	}
}
