package vm_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/vmhost"
)

// A Map.get or Vector.get whose Maybe is only taken apart (`case` arms,
// `?`) keeps it as a payload and a tag in registers: a Some allocates
// nothing. The count is per activation over a loop doing one lookup each.
func TestMaybeFuse_AMatchedLookupAllocatesNothing(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"Vector.get", `import std::io

fn down(n: Int, v: Vector<Int>) -> Int
    if n < 1 then
        0
    else
        x = case Vector.get(v, n %% 4)
            Some(x) -> x
            None -> 0
        end
        down(n - 1, v) + x
    end
end

fn main()
    down(%d, Vector[1000, 2000, 3000]) |> io.print()
end
`},
		{"Map.get", `import std::io

fn down(n: Int, m: Map<Int, Int>) -> Int
    if n < 1 then
        0
    else
        x = case Map.get(m, n %% 4)
            Some(x) -> x
            None -> 0
        end
        down(n - 1, m) + x
    end
end

fn main()
    down(%d, [(0, 1000), (1, 2000), (2, 3000)] |> Iter.to_map()) |> io.print()
end
`},
		{"Map.get, keys past 255", `import std::io

fn down(n: Int, m: Map<Int, Int>) -> Int
    if n < 1 then
        0
    else
        x = case Map.get(m, 1000 + n %% 4)
            Some(x) -> x
            None -> 0
        end
        down(n - 1, m) + x
    end
end

fn main()
    down(%d, [(1000, 1000), (1001, 2000), (1002, 3000)] |> Iter.to_map()) |> io.print()
end
`},
		{"Map.get, String keys", `import std::io

fn down(n: Int, m: Map<String, Int>, keys: Vector<String>) -> Int
    if n < 1 then
        0
    else
        k = Vector.get(keys, n %% 4)!
        x = case Map.get(m, k)
            Some(x) -> x
            None -> 0
        end
        down(n - 1, m, keys) + x
    end
end

fn main()
    keys = Vector["zero", "one", "two", "three"]
    down(%d, [("zero", 1000), ("one", 2000), ("two", 3000)] |> Iter.to_map(), keys) |> io.print()
end
`},
		{"?", `import std::io

fn get(v: Vector<Int>, i: Int) -> Maybe<Int>
    x = Vector.get(v, i)?
    y = Vector.get(v, i + 1)?
    Some(x + y)
end

fn down(n: Int, v: Vector<Int>) -> Int
    if n < 1 then
        0
    else
        case get(v, n %% 2)
            Some(x) -> down(n - 1, v) + x
            None -> down(n - 1, v)
        end
    end
end

fn main()
    down(%d, Vector[1000, 2000, 3000]) |> io.print()
end
`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// get builds its own Some(x + y) for the caller: one
			// allocation. Every index hits, so neither `?` leaves.
			want := 0.0
			if tc.name == "?" {
				want = 1
			}
			if per := allocsPerActivation(t, tc.src); per > want+0.1 {
				t.Errorf("one matched lookup allocates %.2f times, want %.1f. The likely causes: "+
					"the crossing compiling to opHost rather than opHostM (a use fuseMaybes does "+
					"not accept), or its fast path falling back to the intrinsic", per, want)
			}
		})
	}
}

// The lookups compile to the fused opcodes, and none of their uses runs a
// handler.
func TestMaybeFuse_AMatchedLookupCompilesToRegisters(t *testing.T) {
	p, err := vmhost.LoadSource("main", fusedProgram)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"at", "name", "plus", "twice", "scalars"} {
		listing, err := p.Disassemble(name)
		if err != nil {
			t.Fatal(err)
		}
		ops := opcodesOf(listing)
		if !ops["hostm"] || ops["host"] || ops["matchv"] || ops["projp"] || ops["ir"] && strings.Contains(listing, "; try") {
			t.Errorf("%s's lookup does not compile to the fused opcodes:\n%s", name, listing)
		}
	}
	// A Maybe that leaves the function keeps its record.
	listing, err := p.Disassemble("kept")
	if err != nil {
		t.Fatal(err)
	}
	if ops := opcodesOf(listing); ops["hostm"] || !ops["host"] {
		t.Errorf("kept returns its Maybe and still fuses it:\n%s", listing)
	}
}

// opcodesOf is the set of opcodes a disassembly listing holds.
func opcodesOf(listing string) map[string]bool {
	ops := map[string]bool{}
	for _, line := range strings.Split(listing, "\n")[1:] {
		if f := strings.Fields(line); len(f) > 1 {
			ops[f[1]] = true
		}
	}
	return ops
}

// Fused lookups answer what the record did: hits, misses, a negative and a
// big index, a big key, a key compared through its kernels, `?` on both
// variants, and a Maybe matched twice.
func TestMaybeFuse_AFusedLookupAnswersAsTheRecordDid(t *testing.T) {
	p, err := vmhost.LoadSource("main", fusedProgram)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatal(err)
	}
	want := `10 30 -1 -1 -1 -1
one three hundred ? ?
3 0
Some(30) Some(50) None None
Some(20) None 7
one/oneone none/N
big zero 2
min two and a half 1
70k - -1
- zero -1
`
	if out.String() != want {
		t.Errorf("output:\n%s\nwant:\n%s", out.String(), want)
	}
}

const fusedLocalSrc = `import std::io

fn main()
  m: Map<Int, String> = [(1, "one")] |> Iter.to_map()
  got = Map.get(m, 1)
  miss = Map.get(m, 2)
  text = case got
    Some(s) -> s
    None -> "none"
  end
  other = case miss
    Some(s) -> s
    None -> "none"
  end
  io.print("${text} ${other}")
end
`

// A stopped frame shows a fused Maybe as the Maybe it is, rebuilt from its
// payload and tag registers.
func TestMaybeFuse_AStoppedFrameShowsAFusedMaybe(t *testing.T) {
	s := debugProgram(t, fusedLocalSrc)
	s.breakAt(7)
	s.run()
	st := s.stop()
	s.at(st, vm.StopBreakpoint, "main:7",
		`m: Map<Int, String> = {1 => "one"}, got: Maybe<String> = Some("one"), miss: Maybe<String> = None`)
	s.d.Continue()
	if err := s.finish(); err != nil {
		t.Fatal(err)
	}
	if got := s.out.String(); got != "one none\n" {
		t.Fatalf("output %q", got)
	}
	listing, err := s.p.Disassemble("main")
	if err != nil {
		t.Fatal(err)
	}
	if !opcodesOf(listing)["hostm"] {
		t.Errorf("main does not fuse its lookups, so this test shows nothing:\n%s", listing)
	}
}

const fusedProgram = `import std::io

fn at(v: Vector<Int>, i: Int) -> Int
    case Vector.get(v, i)
        Some(x) -> x
        None -> -1
    end
end

fn name(m: Map<Int, String>, k: Int) -> String
    case Map.get(m, k)
        Some(s) -> s
        None -> "?"
    end
end

fn pairs(m: Map<(Int, Int), Int>, a: Int, b: Int) -> Int
    case Map.get(m, (a, b))
        Some(x) -> x
        None -> 0
    end
end

fn plus(v: Vector<Int>, i: Int) -> Maybe<Int>
    x = Vector.get(v, i)?
    y = Vector.get(v, i + 1)?
    Some(x + y)
end

fn scalars(ints: Map<Int, String>, floats: Map<Float, String>, words: Map<String, Int>, k: Int, f: Float, w: String) -> String
    i = case Map.get(ints, k)
        Some(s) -> s
        None -> "-"
    end
    x = case Map.get(floats, f)
        Some(s) -> s
        None -> "-"
    end
    n = case Map.get(words, w)
        Some(n) -> n
        None -> -1
    end
    "${i} ${x} ${n}"
end

fn kept(v: Vector<Int>, i: Int) -> Maybe<Int>
    Vector.get(v, i)
end

fn twice(m: Map<Int, String>, k: Int) -> String
    got = Map.get(m, k)
    first = case got
        Some(s) -> s
        None -> "none"
    end
    second = case got
        None -> "N"
        Some(s) -> "${s}${s}"
    end
    "${first}/${second}"
end

fn main()
    v = Vector[10, 20, 30]
    big = 1180591620717411303424
    io.print("${at(v, 0)} ${at(v, 2)} ${at(v, 3)} ${at(v, -1)} ${at(v, big)} ${at(v, -big)}")
    m: Map<Int, String> = [(1, "one"), (300, "three hundred")] |> Iter.to_map()
    io.print("${name(m, 1)} ${name(m, 300)} ${name(m, 2)} ${name(m, big)}")
    pm: Map<(Int, Int), Int> = [((1, 2), 3)] |> Iter.to_map()
    io.print("${pairs(pm, 1, 2)} ${pairs(pm, 2, 1)}")
    io.print("${plus(v, 0)} ${plus(v, 1)} ${plus(v, 2)} ${plus(v, 5)}")
    io.print("${kept(v, 1)} ${kept(v, 9)} ${Maybe.with_default(Vector.get(v, 9), 7)}")
    io.print("${twice(m, 1)} ${twice(m, 5)}")
    ints: Map<Int, String> = [(big, "big"), (-9223372036854775808, "min"), (70000, "70k")] |> Iter.to_map()
    floats: Map<Float, String> = [(0.0, "zero"), (2.5, "two and a half")] |> Iter.to_map()
    words: Map<String, Int> = [("a", 1), ("bb", 2)] |> Iter.to_map()
    io.print(scalars(ints, floats, words, big, -0.0, "bb"))
    io.print(scalars(ints, floats, words, -9223372036854775808, 2.5, "a"))
    io.print(scalars(ints, floats, words, 70000, 1.0, "c"))
    io.print(scalars(ints, floats, words, big + 1, 0.0, ""))
end
`
