package irbuild

import (
	"testing"
)

func TestIRLoop_CompletePrograms(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"iteration-and-loops.md:L41", `fn main() {
  // Countdown — break when n reaches 0.
  countdown = Iter.loop(fn n = 5 -> {
    if n == 0 { break n }
    n - 1
  })
  dbg countdown

  // Tuple state — first Fibonacci pair where the second is > 100.
  #(a, b) = Iter.loop(fn state = #(0, 1) -> {
    #(x, y) = state
    if y > 100 { break #(x, y) }
    #(y, x + y)
  })
  dbg a
  dbg b
}`, "dbg line 7: countdown = 0\ndbg line 15: a = 89\ndbg line 16: b = 144\n"},
		{"guards, return advance and repeated calls", `import std/io
fn count_to(limit: Int) -> Int {
  Iter.loop(fn n = 0 -> {
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
  total = a + Iter.loop(fn k = 1 -> {
    if k > 50 { break k - 1 }
    k * 2
  })
  dbg a
  dbg b
  dbg total
  label = Iter.loop(fn s = "a" -> {
    if String.length(s) >= 3 { break "${s}!" }
    "${s}b"
  })
  io.print(label)
}`, "stop at 4\nstop at 5\ndbg line 22: a = 40\ndbg line 23: b = 50\ndbg line 24: total = 103\nabb!\n"},
		{"operand positions", `import std/io
fn f() -> Int {
  io.print("f")
  3
}
fn g(x: Int, y: Int) -> Int { x - y }
fn main() {
  total = f() + Iter.loop(fn k = 1 -> {
    io.print("k")
    if k > 5 { break k }
    k + 2
  })
  dbg total
  dbg Iter.loop(fn k = f() -> {
    if k > 5 { break k }
    k + 2
  })
  h = g(Iter.loop(fn k = 1 -> {
    if k > 5 { break k }
    k + 2
  }), f())
  io.print("${h} ${Iter.loop(fn s = "x" -> {
    if s == "xxx" { break s }
    "${s}x"
  })}")
}`, "f\nk\nk\nk\nk\ndbg line 13: total = 10\nf\ndbg line 14:\n  Iter.loop(fn k = f() -> {\n      if k > 5 { break k }\n      k + 2\n  })\n  = 7\nf\n4 xxx\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}

func TestIRLoop_RetainsLoopFunctions(t *testing.T) {
	p, err := AnalyzeSource("main.nomi", `fn count_to(limit: Int) -> Int {
  Iter.loop(fn n = 0 -> {
    if n >= limit { break n }
    n + 1
  })
}
fn main() { _ = count_to(3) + count_to(4) }`)
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

func TestIRLoop_BranchingTailAndFirstBreak(t *testing.T) {
	// A branching tail writes the tail slot from two arms, and a final `break
	// v` leaves on the first iteration.
	verifyLambdaProgram(t, "import std/io\nfn run() -> Int {\nIter.loop(fn n = 0 -> {\n if n > 3 { break n }\n if n > 1 { n + 2 } else { n + 1 }\n })\n}\n"+
		"fn first_break() -> Int {\nIter.loop(fn n = 7 -> { break n })\n}\nfn main() {\n io.print(run())\n io.print(first_break())\n}\n", "4\n7\n")
}

// A final `return v` is the next state, as a tail value is (vm-maps-iters).
func TestIRLoop_FinalReturnIsTheNextState(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
fn run() -> Int {
  Iter.loop(fn n = 0 -> {
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
  n = Iter.loop(fn i = 0 -> {
    if i == 600000 { break i }
    i + 1
  })
  io.print(n)
}
`, "600000\n")
}

// A loop whose state is Unit runs for its effects: a bare `break`, `break u`,
// `continue`, a guard, a final `return u` and a tail whose value is Unit, a
// discard-named state, an inferred `= Unit` seed, the loop's Unit answer
// bound and printed, and a loop in a lambda.
func TestIRLoop_UnitState(t *testing.T) {
	verifyLambdaProgram(t, `import std/channels.{Channel, Receiver, Sender}
import std/io

fn feed(items: List<String>) -> Receiver<String> {
    ch = Channel.buffered<String>(10)
    Iter.each(items, fn s -> {
        _ = Sender.send(ch.sender, s)
    })
    Sender.close(ch.sender)
    ch.receiver
}

fn break_state(input: Receiver<String>) {
    Iter.loop(fn u: Unit = Unit -> {
        case Receiver.receive(input) {
            Some("skip") -> continue
            Some(t) -> io.print(t)
            None -> break u
        }
    })
}

fn bare_break(input: Receiver<String>) {
    Iter.loop(fn _u: Unit = Unit -> {
        case Receiver.receive(input) {
            Some(t) -> io.print("bare ${t}")
            None -> break
        }
    })
}

fn guards(input: Receiver<String>) -> Unit {
    Iter.loop(fn u = Unit -> {
        t = Receiver.receive(input)
        if t == None { break u }
        if t == Some("skip") { continue }
        io.print("guarded ${t}")
        return u
    })
}

fn main() {
    break_state(feed(["a", "skip", "b"]))
    bare_break(feed(["c"]))
    r = guards(feed(["d", "skip", "e"]))
    io.inspect(r)
    run = fn items: List<String> -> {
        input = feed(items)
        Iter.loop(fn _: Unit = Unit -> {
            case Receiver.receive(input) {
                Some(t) -> io.print("lambda ${t}")
                None -> break
            }
        })
    }
    run(["f"])
}
`, "a\nb\nbare c\nguarded Some(d)\nguarded Some(e)\nUnit\nlambda f\n")
}

// A stateless loop has state Unit and lowers as a Unit-state loop does,
// whatever ends its body: `break`, `continue`, a `case` whose arms all leave,
// a Unit tail, an `if` with no `else`. Its value is Unit, and it lowers in a
// lambda too.
func TestIRLoop_StatelessLoopEndings(t *testing.T) {
	verifyLambdaProgram(t, `import {
    std/channels.{Channel, Receiver, Sender}
    std/io
}

fn fill(xs: List<Int>) -> Receiver<Int> {
    ch: Channel<Int> = Channel.buffered(10)
    Iter.each(xs, fn x -> {
        _ = Sender.send(ch.sender, x)
    })
    Sender.close(ch.sender)
    ch.receiver
}

fn main() {
    // Ends in break: runs once.
    Iter.loop(fn -> {
        io.print("once")
        break
    })
    // Ends in continue.
    inbox = fill([1, 2, 3])
    Iter.loop(fn -> {
        Some(n) = Receiver.receive(inbox) else { break }
        if n == 3 { break Unit }
        io.print("continue ${n}")
        continue
    })
    // A case whose arms all leave.
    inbox2 = fill([4, 5, 0, 6])
    Iter.loop(fn -> {
        case Receiver.receive(inbox2) {
            Some(0) -> break
            Some(n) -> {
                io.print("case ${n}")
                continue
            }
            None -> break
        }
    })
    // A Unit tail, and an if with no else ending the body.
    inbox3 = fill([7, 8])
    u = Iter.loop(fn -> {
        Some(n) = Receiver.receive(inbox3) else { break }
        io.print("tail ${n}")
        if n == 7 { io.print("seven") }
    })
    io.inspect(u)
    // In a lambda.
    drain = fn label: String, inbox: Receiver<Int> -> Iter.loop(fn -> {
        Some(n) = Receiver.receive(inbox) else { break }
        io.print("${label} ${n}")
    })
    drain("lambda", fill([9, 10]))
}
`, "once\ncontinue 1\ncontinue 2\ncase 4\ncase 5\ntail 7\nseven\ntail 8\nUnit\nlambda 9\nlambda 10\n")
}
