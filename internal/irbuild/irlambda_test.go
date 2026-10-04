package irbuild

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/vm"
)

// TestIRLambda_CompleteProgramsRunOnTheVM runs a program with
// lambdas on the VM and requires the expected output.
func TestIRLambda_CompleteProgramsRunOnTheVM(t *testing.T) {
	const src = `import std/io
fn main() {
 offset = 2
 add = |x: Int| x + offset
 twice = |x: Int| add(x) + add(x)
 io.print(twice(19))
}
`
	const want = "42\n"
	verifyLambdaProgram(t, src, want)
}

func TestIRLambda_NestedBooleanArms(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"nested scopes and effects", `import std/io
fn main() {
 base = 10
 choose = |a: Bool, b: Bool| {
  if a {
   amount = base + 1
   if b {
    answer = amount + 1
    io.print("both")
    answer
   } else {
    answer = amount + 2
    io.print("first")
    answer
   }
  } else {
   amount = base + 3
   if b { amount + 1 } else { amount + 2 }
  }
 }
 io.print(choose(True, True))
 io.print(choose(True, False))
 io.print(choose(False, True))
 io.print(choose(False, False))
 io.print(base)
}
`, "both\n12\nfirst\n13\n14\n15\n10\n"},
		{"else if and escaping captures", `import std/io
fn main() {
 choose = |x: Int| {
  if x < 0 {
   amount = 1
   |y: Int| y + amount
  } else if x == 0 {
   amount = 2
   |y: Int| y + amount
  } else if x == 1 {
   amount = 3
   |y: Int| y + amount
  } else {
   amount = 4
   |y: Int| y + amount
  }
 }
 a = choose(-1)
 b = choose(0)
 c = choose(1)
 d = choose(2)
 io.print(a(10))
 io.print(b(10))
 io.print(c(10))
 io.print(d(10))
 io.print(a(20))
}
`, "11\n12\n13\n14\n21\n"},
		{"nested unit", `import std/io
fn main() {
 emit = |a: Bool, b: Bool| {
  if a {
   if b { io.print("both") } else { io.print("first") }
  } else if b { io.print("second") } else { io.print("neither") }
 }
 emit(True, True)
 emit(True, False)
 emit(False, True)
 emit(False, False)
}
`, "both\nfirst\nsecond\nneither\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}

// verifyLambdaProgram runs src on the VM and requires want.
func verifyLambdaProgram(t *testing.T, src, want string, extraFiles ...map[string]string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "main.nomi")
	for _, files := range extraFiles {
		for name, content := range files {
			file := filepath.Join(filepath.Dir(path), name)
			if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := os.WriteFile(path, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := Analyze(path)
	if err != nil {
		t.Fatal(err)
	}
	res, _, err := GenerateIR(p)
	if err != nil {
		t.Fatal(err)
	}
	var entry *ir.Module
	for _, m := range res.IR {
		for _, f := range m.Funcs() {
			if f.Name() == "main" {
				entry = m
			}
		}
	}
	if entry == nil {
		t.Fatal("the program's main was not retained")
	}
	var out bytes.Buffer
	booted, err := vm.NewProgram(entry, res.IRModules(), &out).Boot()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := booted.Run("main"); err != nil {
		t.Fatal(err)
	}
	if out.String() != want {
		t.Fatalf("VM output %q; want %q", out.String(), want)
	}
	if got := vmReference(path); got.exit != 0 || got.stdout != want || got.stderr != "" {
		t.Fatalf("vm command: %s; want %q", got, want)
	}
}

func TestIRLambda_CaptureAndCallSemantics(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"factory activations", `import std/io
fn adder(n: Int): (Int) -> Int { |x: Int| x + n }
fn main() {
 a = adder(3)
 b = adder(7)
 io.print(a(1))
 io.print(b(1))
 io.print(a(2))
}`, "4\n8\n5\n"},
		{"parameter shadow", `import std/io
fn main() {
 x = 100
 f = |x: Int| x + 1
 io.print(x)
 io.print(f(2))
}`, "100\n3\n"},
		{"argument order", `import std/io
fn mark(x: Int): Int { dbg x }
fn main() {
 f = |a: Int, b: Int| a - b
 io.print(f(mark(7), mark(2)))
}`, "dbg line 2: x = 7\ndbg line 2: x = 2\n5\n"},
		{"capture parameter", `import std/io
fn apply(n: Int): Int {
 f = |x: Int| x + n + n
 f(2)
}
fn main() { io.print(apply(3)) }`, "8\n"},
		{"discard parameter", `import std/io
fn main() {
 f = |_x: Int| 9
 io.print(f(2))
}`, "9\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}

func TestIRLambda_HigherOrderBodies(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"named function and captured parameter", `import std/io
fn double(x: Int): Int { x * 2 }
fn wrap(f: (Int) -> Int): (Int) -> Int { |x: Int| f(x) + 1 }
fn main() {
 f = wrap(double)
 io.print(f(3))
}`, "7\n"},
		{"higher order lambda", `import std/io
fn main() {
 apply = |f: (Int) -> Int, x: Int| f(x) + 1
 io.print(apply(|n| n * 3, 4))
}`, "13\n"},
		{"callee before argument", `import std/io
fn make(): (Int) -> Int {
 io.print("callee")
 |x: Int| x + 1
}
fn arg(): Int {
 io.print("argument")
 4
}
fn main() { io.print(make()(arg())) }`, "callee\nargument\n5\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}

func TestIRLambda_StatementBodies(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"single expression on later line", `import std/io
fn main() {
 f = |x: Int| {
  x
 }
 io.print(f(3))
}`, "3\n"},
		{"multiline arithmetic", `import std/io
fn main() {
 f = |x: Int| {
  y = (x +
   1)
  y *
   2
 }
 io.print(f(3))
}`, "8\n"},
		{"conditional closure", `import std/io
fn make(n: Int): (Int) -> Int {
 if n > 0 {
  |x: Int| {
   y = x + n
   y
  }
 } else {
  |x: Int| {
   y = x - n
   y
  }
 }
}
fn main() { io.print(make(2)(3)); io.print(make(-2)(3)) }`, "5\n5\n"},
		{"capture and effects", `import std/io
fn main() {
 base = 3
 callback = |x: Int| {
  y = x + base
  io.print(y)
  y * 2
 }
 io.print(callback(4))
}`, "7\n14\n"},
		{"inline final callback", `import std/io
fn apply(x: Int, f: (Int) -> Int): Int { f(x) }
fn main() {
 io.print(apply(2, |x: Int| {
  y = x + 1
  y * 2
 }))
}`, "6\n"},
		{"callback before later argument", `import std/io
fn apply(f: (Int) -> Int, x: Int): Int { f(x) }
fn main() {
 n = 4
 io.print(apply(|x: Int| {
  y = x + 1
  y
 },
 n))
}`, "5\n"},
		{"nested escaping closure", `import std/io
fn make(base: Int): (Int) -> (Int) -> Int {
 |x: Int| {
  subtotal = base + x
  |y: Int| {
   result = subtotal + y
   result
  }
 }
}
fn main() {
 add = make(2)(3)
 io.print(add(4))
}`, "9\n"},
		{"returned local", `import std/io
fn main() {
 f = |x: Int| {
  y = x + 1
  io.print("ready")
  y
 }
 io.print(f(5))
}`, "ready\n6\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}

func TestIRLambda_ControlFlow(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"unit conditional", `import std/io
fn main() {
 emit = |flag: Bool| if flag { io.print("yes") } else { io.print("no") }
 emit(True)
 emit(False)
}`, "yes\nno\n"},
		{"captured conditional", `import std/io
fn main() {
 cutoff = 3
 classify = |x: Int| {
  adjusted = x + 1
  if adjusted > cutoff { "high" } else { "low" }
 }
 io.print(classify(1))
 io.print(classify(5))
}`, "low\nhigh\n"},
		{"only selected arm runs", `import std/io
fn yes(): Int { io.print("yes"); 1 }
fn no(): Int { io.print("no"); 2 }
fn main() {
 select = |flag: Bool| if flag { yes() } else { no() }
 io.print(select(True))
 io.print(select(False))
}`, "yes\n1\nno\n2\n"},
		{"integer case", `import std/io
fn main() {
 classify = |x: Int| {
  case x {
   0 -> "zero"
   1 -> "one"
   _ -> "other"
  }
 }
 io.print(classify(0))
 io.print(classify(1))
 io.print(classify(9))
}`, "zero\none\nother\n"},
		{"string case with capture", `import std/io
fn main() {
 suffix = "!"
 classify = |word: String| case word {
  "yes" -> "accepted" + suffix
  _ -> "unknown" + suffix
 }
 io.print(classify("yes"))
 io.print(classify("no"))
}`, "accepted!\nunknown!\n"},
		{"escaping conditional", `import std/io
fn make(cutoff: Int): (Int) -> String {
 |x: Int| if x > cutoff { "high" } else { "low" }
}
fn main() {
 f = make(3)
 io.print(f(2))
 io.print(f(4))
}`, "low\nhigh\n"},
		{"conditional returns callable", `import std/io
fn main() {
 choose = |flag: Bool| if flag { |x: Int| x + 1 } else { |x: Int| x - 1 }
 io.print(choose(True)(5))
 io.print(choose(False)(5))
}`, "6\n4\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}

func TestIRLambda_StatementArms(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"local shadows sibling capture", `import std/io
fn main() {
 outer = 10
 choose = |flag: Bool| if flag {
  answer = outer + 1
  answer
 } else {
  outer = 3
  outer
 }
 io.print(choose(True))
 io.print(choose(False))
 io.print(outer)
}`, "11\n3\n10\n"},
		{"sibling locals and shared capture", `import std/io
fn main() {
 base = 10
 choose = |flag: Bool| if flag {
  answer = base + 1
  io.print("left")
  answer
 } else {
  answer = base + 2
  io.print("right")
  answer
 }
 io.print(choose(True))
 io.print(choose(False))
}`, "left\n11\nright\n12\n"},
		{"escaping branch local", `import std/io
fn main() {
 choose = |flag: Bool| if flag {
  amount = 2
  |x: Int| x + amount
 } else {
  amount = 5
  |x: Int| x + amount
 }
 left = choose(True)
 right = choose(False)
 io.print(left(10))
 io.print(right(10))
 io.print(left(20))
}`, "12\n15\n22\n"},
		{"case blocks", `import std/io
fn main() {
 base = 3
 choose = |x: Int| case x {
  0 -> {
   answer = base + 1
   io.print("zero")
   answer
  }
  _ -> {
   answer = base + x
   io.print("other")
   answer
  }
 }
 io.print(choose(0))
 io.print(choose(4))
}`, "zero\n4\nother\n7\n"},
		{"unit arms", `import std/io
fn main() {
 emit = |flag: Bool| if flag {
  message = "left"
  io.print(message)
 } else {
  message = "right"
  io.print(message)
 }
 emit(True)
 emit(False)
}`, "left\nright\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}

func TestIRLambda_ArmBindingAndCaptureIdentity(t *testing.T) {
	p, err := AnalyzeSource("main.nomi", `import std/io
fn main() {
 base = 10
 f = |flag: Bool| if flag { answer = base + 1; answer } else { answer = base + 2; answer }
 io.print(f(True))
}`)
	if err != nil {
		t.Fatal(err)
	}
	res, _, err := GenerateIR(p)
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, mod := range res.IR {
		for _, f := range mod.Funcs() {
			for _, b := range f.Blocks() {
				for _, in := range b.Instrs() {
					closure, ok := in.(*ir.FuncValue)
					if !ok {
						continue
					}
					found++
					if closure.NumCaptures() != 1 {
						t.Fatalf("captures = %d; want one shared parent binding", closure.NumCaptures())
					}
					var locals []*ir.Symbol
					for _, arm := range closure.Body().Blocks() {
						for _, in := range arm.Instrs() {
							if bind, ok := in.(*ir.Bind); ok && bind.Sym().Name() == "answer" {
								locals = append(locals, bind.Sym())
							}
						}
					}
					if len(locals) != 2 || locals[0] == locals[1] {
						t.Fatal("sibling declarations must have distinct symbols")
					}
				}
			}
		}
	}
	if found != 1 {
		t.Fatalf("retained %d closures; want 1", found)
	}
}
