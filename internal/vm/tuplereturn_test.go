package vm_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/vmhost"
)

// A tuple a function builds and returns, taken apart by its caller, crosses
// the call in parts: the callee's opRetP copies its components to the
// register stack and the caller's deliver moves them into its own, so the
// call allocates no record. Ints past 255 included, which a box would
// allocate for.
func TestTupleReturn_APairTakenApartAllocatesNothing(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"pair", `import std::io

fn pair(i: Int) -> (Int, Int)
    (i, i * 1000)
end

fn down(n: Int) -> Int
    if n < 1 then
        0
    else
        (a, b) = pair(n)
        down(n - 1) + a + b
    end
end

fn main()
    down(%d) |> io.print()
end
`},
		{"recursive pair", `import std::io

fn fibs(n: Int) -> (Int, Int)
    if n == 0 then
        (0, 1)
    else
        (a, b) = fibs(n - 1)
        (b, (a + b) %% 1000003)
    end
end

fn main()
    (a, _) = fibs(%d)
    io.print(a)
end
`},
		{"String and Int", `import std::io

fn named(i: Int, names: Vector<String>) -> (String, Int)
    (Vector.get(names, i %% 2)!, i * 1000)
end

fn down(n: Int, names: Vector<String>) -> Int
    if n < 1 then
        0
    else
        (s, x) = named(n, names)
        down(n - 1, names) + (if s == "one" then 1 else 3) + x
    end
end

fn main()
    down(%d, Vector["one", "three"]) |> io.print()
end
`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if per := allocsPerActivation(t, tc.src); per > 0.1 {
				t.Errorf("a pair returned and taken apart allocates %.2f times, want 0. The likely causes: "+
					"the callee returning with opRet rather than opRetP (its tuple not split), or "+
					"the caller's tuple not split (a call splitTuples does not accept)", per)
			}
		})
	}
}

// A tuple returned in parts reads as the record would have everywhere: a
// caller that keeps it whole, a caller that splits it from a callee that
// built a record, a call from Go (main's interpolation through a host), a
// callee whose deferred calls run after its return, big Ints, Strings and
// references, and a pair recursion.
func TestTupleReturn_AReturnInPartsAnswersAsTheRecordDid(t *testing.T) {
	p, err := vmhost.LoadSource("main", tupleReturnProgram)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"pair": "retp", "deferred": "retp", "apart": "movw"} {
		listing, err := p.Disassemble(name)
		if err != nil {
			t.Fatal(err)
		}
		if ops := opcodesOf(listing); !ops[want] || ops["make"] {
			t.Errorf("%s does not compile its tuple to %s:\n%s", name, want, listing)
		}
	}
	var out bytes.Buffer
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatal(err)
	}
	want := `3 (1, 2) (5, 6)
1208925819614629174706176 x [1] 2
deferred 7
deferred 7
(7, 8) 15
(55, 89) 55
(1, b)
`
	if out.String() != want {
		t.Errorf("output:\n%s\nwant:\n%s", out.String(), want)
	}
}

const tupleReturnProgram = `import std::io

fn pair(i: Int) -> (Int, Int)
    (i, i + 1)
end

fn apart(i: Int) -> Int
    (a, b) = pair(i)
    a + b
end

fn kept(t: (Int, Int)) -> (Int, Int)
    t
end

fn from_record(i: Int) -> Int
    (a, b) = kept((i, i + 1))
    a + b
end

fn mixed(n: Int, s: String, xs: List<Int>) -> (Int, String, List<Int>)
    (n * n, s, xs)
end

fn deferred(i: Int) -> (Int, Int)
    defer io.print("deferred ${i}")
    (i, i + 1)
end

fn fibs(n: Int) -> (Int, Int)
    if n == 0 then
        (0, 1)
    else
        (a, b) = fibs(n - 1)
        (b, a + b)
    end
end

fn whole(i: Int) -> (Int, String)
    (i, "b")
end

fn main()
    whole_pair = pair(1)
    io.print("${apart(1)} ${whole_pair} ${pair(5)}")
    (big, s, xs) = mixed(1099511627776, "x", [1])
    io.print("${big} ${s} ${xs} ${from_record(0) + 1}")
    d = deferred(7)
    (d0, d1) = deferred(7)
    io.print("${d} ${d0 + d1}")
    (f, _) = fibs(10)
    io.print("${fibs(10)} ${f}")
    io.print("${whole(1)}")
end
`

const tupleReturnLocalSrc = `import std::io

fn pair(i: Int) -> (Int, String)
  (i, "x")
end

fn main()
  t = pair(1)
  (n, s) = t
  io.print("${n} ${s}")
end
`

// A stopped frame shows a tuple a call returned in parts as the tuple it
// is, and stepping out of the callee lands with it delivered.
func TestTupleReturn_AStoppedFrameShowsATupleReturnedInParts(t *testing.T) {
	s := debugProgram(t, tupleReturnLocalSrc)
	s.breakAt(9)
	s.run()
	st := s.stop()
	s.at(st, vm.StopBreakpoint, "main:9", `t: (Int, String) = (1, "x")`)
	s.d.Continue()
	if err := s.finish(); err != nil {
		t.Fatal(err)
	}
	if got := s.out.String(); got != "1 x\n" {
		t.Fatalf("output %q", got)
	}
	for name, want := range map[string]string{"pair": "retp", "main": "movw"} {
		listing, err := s.p.Disassemble(name)
		if err != nil {
			t.Fatal(err)
		}
		if ops := opcodesOf(listing); !ops[want] || ops["make"] {
			t.Errorf("%s does not carry its tuple in parts, so this test shows nothing:\n%s", name, listing)
		}
	}
}
