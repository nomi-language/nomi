package vm_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// mandelbrot's escape test, as benchmarks/game/mandelbrot.nomi writes it.
const escapeLoop = `import std::io

fn bounded(zr: Float, zi: Float, cr: Float, ci: Float, left: Int) -> Bool
    tr = zr * zr
    ti = zi * zi

    if tr + ti > 4.0 then
        False
    else if left == 0 then
        True
    else
        bounded(tr - ti + cr, 2.0 * zr * zi + ci, cr, ci, left - 1)
    end
end

fn main()
    io.print("${bounded(0.0, 0.0, -0.5, 0.25, 50)} ${bounded(0.0, 0.0, 1.0, 1.0, 50)}")
end
`

// A tail-recursive loop's iteration is its own work: each comparison and the
// branch on it are one instruction, its constants are loaded once at entry
// (the self tail call restarts past them), and a jump to a jump to a return
// is the return. bounded's iteration is twelve instructions: four
// multiplies, three additions, a subtraction of Floats, one of Ints, two
// compare-and-branches and the self tail call's jump (opLoop).
func TestDispatch_ATailRecursiveLoopIsItsWork(t *testing.T) {
	p, err := vmhost.LoadSource("main", escapeLoop)
	if err != nil {
		t.Fatal(err)
	}
	listing, err := p.Disassemble("bounded")
	if err != nil {
		t.Fatal(err)
	}
	var ops []string
	for _, line := range strings.Split(listing, "\n")[1:] {
		if f := strings.Fields(line); len(f) > 1 {
			ops = append(ops, f[1])
		}
	}
	loads := 0
	for loads < len(ops) && ops[loads] == "loadw" {
		loads++
	}
	// The iteration: from the first instruction after the loads to the tail
	// call, leaving out the returns of the exits.
	var iteration []string
	for _, op := range ops[loads:] {
		if op == "ret" || op == "loadw" {
			continue
		}
		iteration = append(iteration, op)
		if op == "loop" {
			break
		}
	}
	want := "mulf mulf addf brcmpf brcmpi subf addf mulf mulf addf subi loop"
	if loads != 4 || strings.Join(iteration, " ") != want {
		t.Errorf("bounded loads %d constants at entry and iterates over %q, want 4 and %q:\n%s",
			loads, strings.Join(iteration, " "), want, listing)
	}
	if opcodesOf(listing)["jmp"] {
		t.Errorf("a jump survives where a return would do:\n%s", listing)
	}
	var out bytes.Buffer
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatal(err)
	}
	if out.String() != "True False\n" {
		t.Errorf("output %q", out.String())
	}
}

// A self tail call allocates nothing.
func TestDispatch_ASelfTailCallAllocatesNothing(t *testing.T) {
	per := allocsPerActivation(t, `import std::io

fn loop(n: Int, acc: Int, s: String, xs: List<Int>) -> Int
    if n < 1 then acc + String.length(s) + List.length(xs) else loop(n - 1, acc + n, s, xs)
end

fn main()
    loop(%d, 0, "ab", [1]) |> io.print()
end
`)
	if per > 0.1 {
		t.Errorf("one self tail call allocates %.2f times, want 0", per)
	}
}

// A self tail call carries every kind of operand into its parameters: Ints
// past int64, strings, references, more words than its fast path holds, and
// operands that are the parameters themselves in another order. A function
// with a deferred call takes the general path and runs it each time.
func TestDispatch_SelfTailCallsCarryEveryOperand(t *testing.T) {
	p, err := vmhost.LoadSource("main", `import std::io

fn grow(n: Int, x: Int) -> Int
    if n == 0 then x else grow(n - 1, x * 1000000007)
end

fn swap(n: Int, a: String, b: String, xs: List<String>) -> String
    if n == 0 then "${a}${b}${xs}" else swap(n - 1, b, a, [a, ..xs])
end

fn rotate(n: Int, a: Int, b: Int, c: Int, d: Int, e: Int, f: Int, g: Int, h: Int, i: Int) -> Int
    if n == 0 then a * 1 + b * 10 + c * 100 + d * 1000 else rotate(n - 1, i, a, b, c, d, e, f, g, h)
end

fn floats(n: Int, x: Float, y: Float) -> Float
    if n == 0 then x - y else floats(n - 1, y, x + 1.5)
end

fn deferred(n: Int) -> Int
    defer io.print("left ${n}")
    if n == 0 then 0 else deferred(n - 1)
end

fn main()
    io.print("${grow(5, 3)}")
    io.print(swap(3, "a", "b", []))
    io.print("${rotate(3, 1, 2, 3, 4, 5, 6, 7, 8, 9)}")
    io.print("${floats(5, 0.0, 0.0)}")
    io.print("${deferred(2)}")
end
`)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatal(err)
	}
	want := `3000000105000001470000010290000036015000050421
ba[a, b, a]
1987
-1.5
left 0
left 1
left 2
0
`
	if out.String() != want {
		t.Errorf("output:\n%s\nwant:\n%s", out.String(), want)
	}
}

const literalArms = `import std::io

fn complement(base: String) -> String
    case base
        "A" or "a" -> "T"
        "C" or "c" -> "G"
        "G" or "g" -> "C"
        "T" or "t" -> "A"
        _ -> "N"
    end
end

fn main()
    io.print("${complement("A")}${complement("x")}")
end
`

// A function with no tail call to itself loads each constant where its arm
// reads it: hoisting them to the entry (consthoist.go) would load the
// literals of every arm at each call, for a subject the first arm matches.
// So complement's first instruction is not a run of loads of all thirteen
// literals; it loads at most the two its first comparison reads.
func TestDispatch_AFunctionWithNoLoopLoadsItsConstantsWhereTheyAreRead(t *testing.T) {
	p, err := vmhost.LoadSource("main", literalArms)
	if err != nil {
		t.Fatal(err)
	}
	listing, err := p.Disassemble("complement")
	if err != nil {
		t.Fatal(err)
	}
	loads := 0
	for _, line := range strings.Split(listing, "\n")[1:] {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		if !strings.HasPrefix(f[1], "load") {
			break
		}
		loads++
	}
	if loads > 2 {
		t.Fatalf("complement loads %d constants before its first comparison, want at most 2:\n%s", loads, listing)
	}
}
