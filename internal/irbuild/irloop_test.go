package irbuild

import (
	"testing"
)

func TestIRLoop_CompletePrograms(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"iteration-and-loops.md:L41", `fn main(): Int {
  // Countdown — break when n reaches 0.
  countdown = Iter.loop(|n = 5| {
    if n == 0 { break n }
    n - 1
  })
  dbg countdown

  // Tuple state — first Fibonacci pair where the second is > 100.
  (a, b) = Iter.loop(|state = (0, 1)| {
    (x, y) = state
    if y > 100 { break (x, y) }
    (y, x + y)
  })
  dbg a
  dbg b
}`, "dbg line 7: countdown = 0\ndbg line 15: a = 89\ndbg line 16: b = 144\n"},
		{"guards, return advance and repeated calls", `import std/io
fn count_to(limit: Int): Int {
  Iter.loop(|n = 0| {
    step = n + 1
    if n < 0 { continue }
    if n >= limit {
      io.print("stop at ${n}")
      break n * 10
    }
    if n == 2 { return n + 2 }
    step
  })
}

fn main() {
  a = count_to(3)
  b = count_to(5)
  total = a + Iter.loop(|k = 1| {
    if k > 50 { break k - 1 }
    k * 2
  })
  dbg a
  dbg b
  dbg total
  label = Iter.loop(|s = "a"| {
    if String.length(s) >= 3 { break "${s}!" }
    "${s}b"
  })
  io.print(label)
}`, "stop at 4\nstop at 5\ndbg line 22: a = 40\ndbg line 23: b = 50\ndbg line 24: total = 103\nabb!\n"},
		{"operand positions", `import std/io
fn f(): Int {
  io.print("f")
  3
}
fn g(x: Int, y: Int): Int { x - y }
fn main() {
  total = f() + Iter.loop(|k = 1| {
    io.print("k")
    if k > 5 { break k }
    k + 2
  })
  dbg total
  dbg Iter.loop(|k = f()| {
    if k > 5 { break k }
    k + 2
  })
  h = g(Iter.loop(|k = 1| {
    if k > 5 { break k }
    k + 2
  }), f())
  io.print("${h} ${Iter.loop(|s = "x"| {
    if s == "xxx" { break s }
    "${s}x"
  })}")
}`, "f\nk\nk\nk\nk\ndbg line 13: total = 10\nf\ndbg line 14:\n  Iter.loop(|k = f()| {\n      if k > 5 { break k }\n      k + 2\n  })\n  = 7\nf\n4 xxx\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}

func TestIRLoop_RetainsLoopFunctions(t *testing.T) {
	p, err := AnalyzeSource("main.nomi", `fn count_to(limit: Int): Int {
  Iter.loop(|n = 0| {
    if n >= limit { break n }
    n + 1
  })
}
fn main(): Int { count_to(3) + count_to(4) }`)
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := GenerateIR(p)
	if err != nil {
		t.Fatal(err)
	}
	retained := map[string]bool{}
	for _, mod := range got.IR {
		for _, f := range mod.Funcs() {
			retained[f.Name()] = true
		}
	}
	if !retained["count_to"] || !retained["main"] {
		t.Fatalf("retained %v; want count_to and main", retained)
	}
}

func TestIRLoop_UnsupportedShapesPreserveEmission(t *testing.T) {
	// A branching tail writes the tail slot from two arms, and a final `break
	// v` leaves on the first iteration.
	verifyLambdaProgram(t, "import std/io\nfn run(): Int {\nIter.loop(|n = 0| {\n if n > 3 { break n }\n if n > 1 { n + 2 } else { n + 1 }\n })\n}\n"+
		"fn first_break(): Int {\nIter.loop(|n = 7| { break n })\n}\nfn main() {\n io.print(run())\n io.print(first_break())\n}\n", "4\n7\n")
	for _, body := range []string{
		// A break inside a value conditional.
		"Iter.loop(|n = 0| {\n x = if n > 3 { break n } else { n }\n x + 1\n })",
		// A loop inside a lambda.
		"(|| Iter.loop(|n = 0| {\n if n > 3 { break n }\n n + 1\n }))()",
		// A destructuring state parameter.
		"(a, _) = Iter.loop(|(x, y): (Int, Int) = (0, 1)| {\n if y > 3 { break (x, y) }\n (y, x + y)\n })\na",
	} {
		p, err := AnalyzeSource("main.nomi", "fn run(): Int {\n"+body+"\n}\nfn main(): Int { run() }")
		if err != nil {
			t.Fatal(err)
		}
		got, _, err := GenerateIR(p)
		if err != nil {
			t.Fatal(err)
		}
		for _, mod := range got.IR {
			for _, f := range mod.Funcs() {
				if f.Name() == "run" {
					t.Fatalf("unsupported loop was retained:\n%s", body)
				}
			}
		}
	}
}

// A final `return v` is the next state, as a tail value is (vm-maps-iters).
func TestIRLoop_FinalReturnIsTheNextState(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn run(): Int {
  Iter.loop(|n = 0| {
    if n > 3 { break n }
    return n + 1
  })
}
fn main() {
  io.print(run())
}
`, "4\n")
}

// A loop longer than any transfer bound the machine might impose: the VM runs
// it to its break.
func TestIRLoop_LongLoopRunsToItsBreak(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn main() {
  n = Iter.loop(|i = 0| {
    if i == 600000 { break i }
    i + 1
  })
  io.print(n)
}
`, "600000\n")
}
