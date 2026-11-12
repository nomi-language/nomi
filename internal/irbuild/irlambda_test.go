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
	const src = `import std::io
fn main()
 offset = 2
 add = fn x: Int -> x + offset
 twice = fn x: Int -> add(x) + add(x)
 io.print(twice(19))
end
`
	const want = "42\n"
	verifyLambdaProgram(t, src, want)
}

func TestIRLambda_NestedBooleanArms(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"nested scopes and effects", `import std::io
fn main()
 base = 10
 choose = fn a: Bool, b: Bool ->
  if a then
   amount = base + 1
   if b then
    answer = amount + 1
    io.print("both")
    answer
   else
    answer = amount + 2
    io.print("first")
    answer
   end
  else
   amount = base + 3
   if b then amount + 1 else amount + 2
  end
 end
 io.print(choose(True, True))
 io.print(choose(True, False))
 io.print(choose(False, True))
 io.print(choose(False, False))
 io.print(base)
end
`, "both\n12\nfirst\n13\n14\n15\n10\n"},
		{"else if and escaping captures", `import std::io
fn main()
 choose = fn x: Int ->
  if x < 0 then
   amount = 1
   fn y: Int -> y + amount
  else if x == 0 then
   amount = 2
   fn y: Int -> y + amount
  else if x == 1 then
   amount = 3
   fn y: Int -> y + amount
  else
   amount = 4
   fn y: Int -> y + amount
  end
 end
 a = choose(-1)
 b = choose(0)
 c = choose(1)
 d = choose(2)
 io.print(a(10))
 io.print(b(10))
 io.print(c(10))
 io.print(d(10))
 io.print(a(20))
end
`, "11\n12\n13\n14\n21\n"},
		{"nested unit", `import std::io
fn main()
 emit = fn a: Bool, b: Bool ->
  if a then
   if b then io.print("both") else io.print("first")
  else if b then
      io.print("second")
  else
      io.print("neither")
  end
 end
 emit(True, True)
 emit(True, False)
 emit(False, True)
 emit(False, False)
end
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
		{"factory activations", `import std::io
fn adder(n: Int) -> (Int) -> Int
    fn x: Int -> x + n
end
fn main()
 a = adder(3)
 b = adder(7)
 io.print(a(1))
 io.print(b(1))
 io.print(a(2))
end`, "4\n8\n5\n"},
		{"parameter shadow", `import std::io
fn main()
 x = 100
 f = fn x: Int -> x + 1
 io.print(x)
 io.print(f(2))
end`, "100\n3\n"},
		{"argument order", `import std::io
fn mark(x: Int) -> Int
    dbg x
end
fn main()
 f = fn a: Int, b: Int -> a - b
 io.print(f(mark(7), mark(2)))
end`, "dbg line 3: x = 7\ndbg line 3: x = 2\n5\n"},
		{"capture parameter", `import std::io
fn apply(n: Int) -> Int
 f = fn x: Int -> x + n + n
 f(2)
end
fn main()
    io.print(apply(3))
end`, "8\n"},
		{"discard parameter", `import std::io
fn main()
 f = fn _x: Int -> 9
 io.print(f(2))
end`, "9\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}

func TestIRLambda_HigherOrderBodies(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"named function and captured parameter", `import std::io
fn double(x: Int) -> Int
    x * 2
end
fn wrap(f: (Int) -> Int) -> (Int) -> Int
    fn x: Int -> f(x) + 1
end
fn main()
 f = wrap(double)
 io.print(f(3))
end`, "7\n"},
		{"higher order lambda", `import std::io
fn main()
 apply = fn f: (Int) -> Int, x: Int -> f(x) + 1
 io.print(apply(fn n -> n * 3, 4))
end`, "13\n"},
		{"callee before argument", `import std::io
fn make() -> (Int) -> Int
 io.print("callee")
 fn x: Int -> x + 1
end
fn arg() -> Int
 io.print("argument")
 4
end
fn main()
    io.print(make()(arg()))
end`, "callee\nargument\n5\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}

func TestIRLambda_StatementBodies(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"single expression on later line", `import std::io
fn main()
 f = fn x: Int ->
  x
 end
 io.print(f(3))
end`, "3\n"},
		{"multiline arithmetic", `import std::io
fn main()
 f = fn x: Int ->
  y = (x +
   1)
  y *
   2
 end
 io.print(f(3))
end`, "8\n"},
		{"conditional closure", `import std::io
fn make(n: Int) -> (Int) -> Int
 if n > 0 then
  fn x: Int ->
   y = x + n
   y
  end
 else
  fn x: Int ->
   y = x - n
   y
  end
 end
end
fn main()
    io.print(make(2)(3))
    io.print(make(-2)(3))
end`, "5\n5\n"},
		{"capture and effects", `import std::io
fn main()
 base = 3
 callback = fn x: Int ->
  y = x + base
  io.print(y)
  y * 2
 end
 io.print(callback(4))
end`, "7\n14\n"},
		{"inline final callback", `import std::io
fn apply(x: Int, f: (Int) -> Int) -> Int
    f(x)
end
fn main()
 io.print(apply(2, fn x: Int ->
  y = x + 1
  y * 2
 end))
end`, "6\n"},
		{"callback before later argument", `import std::io
fn apply(f: (Int) -> Int, x: Int) -> Int
    f(x)
end
fn main()
 n = 4
 io.print(apply(fn x: Int ->
  y = x + 1
  y
 end,
 n))
end`, "5\n"},
		{"nested escaping closure", `import std::io
fn make(base: Int) -> (Int) -> (Int) -> Int
 fn x: Int ->
  subtotal = base + x
  fn y: Int ->
   result = subtotal + y
   result
  end
 end
end
fn main()
 add = make(2)(3)
 io.print(add(4))
end`, "9\n"},
		{"returned local", `import std::io
fn main()
 f = fn x: Int ->
  y = x + 1
  io.print("ready")
  y
 end
 io.print(f(5))
end`, "ready\n6\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}

func TestIRLambda_ControlFlow(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"unit conditional", `import std::io
fn main()
 emit = fn flag: Bool -> if flag then io.print("yes") else io.print("no")
 emit(True)
 emit(False)
end`, "yes\nno\n"},
		{"captured conditional", `import std::io
fn main()
 cutoff = 3
 classify = fn x: Int ->
  adjusted = x + 1
  if adjusted > cutoff then "high" else "low"
 end
 io.print(classify(1))
 io.print(classify(5))
end`, "low\nhigh\n"},
		{"only selected arm runs", `import std::io
fn yes() -> Int
    io.print("yes")
    1
end
fn no() -> Int
    io.print("no")
    2
end
fn main()
 select = fn flag: Bool -> if flag then yes() else no()
 io.print(select(True))
 io.print(select(False))
end`, "yes\n1\nno\n2\n"},
		{"integer case", `import std::io
fn main()
 classify = fn x: Int ->
  case x
   0 -> "zero"
   1 -> "one"
   _ -> "other"
  end
 end
 io.print(classify(0))
 io.print(classify(1))
 io.print(classify(9))
end`, "zero\none\nother\n"},
		{"string case with capture", `import std::io
fn main()
 suffix = "!"
 classify = fn word: String -> case word
  "yes" -> "accepted" + suffix
  _ -> "unknown" + suffix
 end
 io.print(classify("yes"))
 io.print(classify("no"))
end`, "accepted!\nunknown!\n"},
		{"escaping conditional", `import std::io
fn make(cutoff: Int) -> (Int) -> String
 fn x: Int -> if x > cutoff then "high" else "low"
end
fn main()
 f = make(3)
 io.print(f(2))
 io.print(f(4))
end`, "low\nhigh\n"},
		{"conditional returns callable", `import std::io
fn main()
 choose = fn flag: Bool -> if flag then (fn x: Int -> x + 1) else fn x: Int -> x - 1
 io.print(choose(True)(5))
 io.print(choose(False)(5))
end`, "6\n4\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}

func TestIRLambda_StatementArms(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"local shadows sibling capture", `import std::io
fn main()
 outer = 10
 choose = fn flag: Bool -> if flag then
  answer = outer + 1
  answer
 else
  outer = 3
  outer
 end
 io.print(choose(True))
 io.print(choose(False))
 io.print(outer)
end`, "11\n3\n10\n"},
		{"sibling locals and shared capture", `import std::io
fn main()
 base = 10
 choose = fn flag: Bool -> if flag then
  answer = base + 1
  io.print("left")
  answer
 else
  answer = base + 2
  io.print("right")
  answer
 end
 io.print(choose(True))
 io.print(choose(False))
end`, "left\n11\nright\n12\n"},
		{"escaping branch local", `import std::io
fn main()
 choose = fn flag: Bool -> if flag then
  amount = 2
  fn x: Int -> x + amount
 else
  amount = 5
  fn x: Int -> x + amount
 end
 left = choose(True)
 right = choose(False)
 io.print(left(10))
 io.print(right(10))
 io.print(left(20))
end`, "12\n15\n22\n"},
		{"case blocks", `import std::io
fn main()
 base = 3
 choose = fn x: Int -> case x
  0 ->
   answer = base + 1
   io.print("zero")
   answer
  _ ->
   answer = base + x
   io.print("other")
   answer
 end
 io.print(choose(0))
 io.print(choose(4))
end`, "zero\n4\nother\n7\n"},
		{"unit arms", `import std::io
fn main()
 emit = fn flag: Bool -> if flag then
  message = "left"
  io.print(message)
 else
  message = "right"
  io.print(message)
 end
 emit(True)
 emit(False)
end`, "left\nright\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}

func TestIRLambda_ArmBindingAndCaptureIdentity(t *testing.T) {
	p, err := AnalyzeSource("main.nomi", `import std::io
fn main()
 base = 10
 f = fn flag: Bool -> if flag then
     answer = base + 1
     answer
 else
     answer = base + 2
     answer
 end
 io.print(f(True))
end`)
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
