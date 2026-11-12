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
	verifyEndlessTrap(t, `import std::io

fn main()
    zero = 0
    io.print("main")
    begin
        io.print("start")
        begin
            io.print("inner")
            Iter.loop(fn ->
                io.print("again")
                io.print(1 / zero)
            end)
        end
    end
end
`, "line 12: division by zero", "main\nstart\ninner\nagain\n")

	// A block value bound to a name, with code after it that never runs,
	// and a block ending an Int function.
	verifyEndlessTrap(t, `import std::io

fn tail_block() -> Int
    begin
        io.print("tail block")
        Iter.loop(0, fn n ->
            if n == 1 then io.print(1 / (n - 1))
            n + 1
        end)
    end
end

fn main()
    x = begin
        io.print("value block")
        _ = tail_block()
        Iter.loop(fn ->
            io.print("never")
        end)
    end
    io.inspect(x)
end
`, "line 7: division by zero", "value block\ntail block\n")

	// Arms that are blocks ending in a block that never finishes, in a
	// binding's `case` and a statement `if`, and a block that never
	// finishes as an operand. Each main reaches a different one.
	pick := `import std::io

fn pick(flag: Int) -> Int
    y = case flag
        0 ->
            io.print("arm 0")
            begin
                Iter.loop(0, fn n ->
                    if n == 1 then io.print(1 / (n - 1))
                    n + 1
                end)
            end
        _ -> flag * 10
    end
    io.inspect(y)
    if y > 15 then
        begin
            io.print("if branch")
            Iter.loop(fn ->
                io.print(1 / (y - y))
            end)
        end
    else
        io.print("else")
    end
    y
end
`
	verifyEndlessTrap(t, pick+`
fn main()
    io.inspect(pick(1) + pick(0))
end
`, "line 9: division by zero", "10\nelse\narm 0\n")
	verifyEndlessTrap(t, pick+`
fn main()
    io.inspect(pick(1) + pick(2))
end
`, "line 20: division by zero", "10\nelse\n20\nif branch\n")
	verifyEndlessTrap(t, pick+`
fn main()
    io.inspect(pick(1) + begin
        io.print("operand")
        Iter.loop(0, fn n ->
            if n == 1 then io.print(1 / (n - 1))
            n + 1
        end)
    end)
end
`, "line 33: division by zero", "10\nelse\noperand\n")

	// A lambda's body, a loop body and a concurrent body, each ending in a
	// block that never finishes.
	verifyEndlessTrap(t, `import std::io

fn main()
    zero = 0
    f = fn label: String ->
        io.print(label)
        begin
            Iter.loop(fn ->
                io.print("lambda loop")
                io.print(1 / zero)
            end)
        end
    end
    total = Iter.loop(0, fn i ->
        if i == 2 then break i
        io.print("outer ${i}")
        i + 1
    end)
    io.inspect(total)
    concurrent
        io.print("concurrent")
        begin
            f("lambda")
        end
    end
end
`, "line 10: division by zero", "outer 0\nouter 1\n2\nconcurrent\nlambda\nlambda loop\n")

	verifyEndlessTrap(t, `import std::io

fn main()
    _ = Iter.loop(0, fn i ->
        io.print("outer ${i}")
        begin
            io.print("inner block")
            Iter.loop(0, fn n ->
                if n == 1 then io.print(1 / (n - 1))
                n + 1
            end)
        end
    end)
end
`, "line 9: division by zero", "outer 0\ninner block\n")
}

// A test body's block statement, and a block value it binds, ending in a
// loop that never finishes: each case fails at its trap after the output
// before it, and nothing after the block runs.
func TestIRLoopEndless_TestBodyBlocks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "endless_block_test.nomi")
	src := `import std::io

test "a block statement"
    assert 1 == 1
    begin
        io.print("block")
        Iter.loop(fn ->
            io.print(1 / 0)
        end)
    end
end

test "a bound block"
    assert 2 == 2
    x = begin
        io.print("bound block")
        begin
            Iter.loop(0, fn n ->
                if n == 1 then io.print(1 / (n - 1))
                n + 1
            end)
        end
    end
    io.print("never")
    io.inspect(x)
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
