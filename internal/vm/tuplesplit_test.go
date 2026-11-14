package vm_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/vmhost"
)

// `case (a, b)` over a tuple the function builds keeps the tuple as its
// components in registers: matching it allocates nothing.
func TestTupleSplit_ATupleCaseAllocatesNothing(t *testing.T) {
	per := allocsPerActivation(t, `import std::io

fn pick(a: Int, b: Int) -> Int
    case (a, b)
        (0, 0) -> 1
        (0, _) -> 2
        (_, 0) -> 3
        _ -> 4
    end
end

fn down(n: Int) -> Int
    if n < 1 then 0 else down(n - 1) + pick(n %% 2, n %% 3)
end

fn main()
    down(%d) |> io.print()
end
`)
	if per > 0.1 {
		t.Errorf("one tuple case allocates %.2f times, want 0. The likely cause: the subject's "+
			"tuple not split (a use splitTuples does not accept), so opMake builds it", per)
	}
}

// A tuple a call returns and the caller takes apart crosses in parts (opRetP
// in the callee, moves in the caller), and a tuple the function builds and
// takes apart compiles to moves.
func TestTupleSplit_ComponentReadsCompileToTypedOpcodes(t *testing.T) {
	p, err := vmhost.LoadSource("main", splitProgram)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"apart": "movw", "pair": "retp", "pick": "movw", "swap": "movs"} {
		listing, err := p.Disassemble(name)
		if err != nil {
			t.Fatal(err)
		}
		ops := opcodesOf(listing)
		if !ops[want] || ops["make"] || strings.Contains(listing, "= slot ") {
			t.Errorf("%s does not compile its tuple to %s:\n%s", name, want, listing)
		}
	}
}

// Split tuples answer what the record did: Ints past int64, strings,
// references, Unit, a component read in a loop after its operand's register
// was written again, and a tuple read whole keeps its record.
func TestTupleSplit_ASplitTupleAnswersAsTheRecordDid(t *testing.T) {
	p, err := vmhost.LoadSource("main", splitProgram)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatal(err)
	}
	want := `1 2 3 4
1208925819614629174706177 b/a
[1, 2] True
10
(1, x)
`
	if out.String() != want {
		t.Errorf("output:\n%s\nwant:\n%s", out.String(), want)
	}
}

const splitProgram = `import std::io

fn pair(i: Int) -> (Int, Int)
    (i, i + 1)
end

fn apart(i: Int) -> Int
    (a, b) = pair(i)
    a + b
end

fn pick(a: Int, b: Int) -> Int
    case (a, b)
        (0, 0) -> 1
        (0, _) -> 2
        (_, 0) -> 3
        _ -> 4
    end
end

fn swap(a: String, b: String) -> String
    (x, y) = (b, a)
    "${x}/${y}"
end

fn big(n: Int) -> Int
    (a, b) = (n * n, 1)
    a + b
end

fn refs(xs: List<Int>) -> String
    (l, u) = (xs, Unit)
    _ = u
    "${l} ${u == Unit}"
end

fn walk(i: Int, acc: Int) -> Int
    t = (i, acc)
    if i == 0 then
        acc
    else
        (n, s) = t
        walk(n - 1, s + n)
    end
end

fn whole(i: Int) -> (Int, String)
    t = (i, "x")
    t
end

fn main()
    io.print("${pick(0, 0)} ${pick(0, 1)} ${pick(1, 0)} ${pick(1, 1)}")
    io.print("${big(1099511627776)} ${swap("a", "b")}")
    io.print(refs([1, 2]))
    io.print("${walk(4, 0)}")
    io.print("${whole(1)}")
end
`

const splitLocalSrc = `import std::io

fn main()
  a = 1
  t = (a, "x")
  (n, s) = t
  io.print("${n} ${s}")
end
`

// A stopped frame shows a split tuple as the tuple it is, rebuilt from its
// components' registers.
func TestTupleSplit_AStoppedFrameShowsASplitTuple(t *testing.T) {
	s := debugProgram(t, splitLocalSrc)
	s.breakAt(6)
	s.run()
	st := s.stop()
	s.at(st, vm.StopBreakpoint, "main:6", `a: Int = 1, t: (Int, String) = (1, "x")`)
	s.d.Continue()
	if err := s.finish(); err != nil {
		t.Fatal(err)
	}
	if got := s.out.String(); got != "1 x\n" {
		t.Fatalf("output %q", got)
	}
	listing, err := s.p.Disassemble("main")
	if err != nil {
		t.Fatal(err)
	}
	if opcodesOf(listing)["make"] {
		t.Errorf("main does not split its tuple, so this test shows nothing:\n%s", listing)
	}
}
