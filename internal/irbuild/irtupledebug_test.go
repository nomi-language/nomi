package irbuild

import "testing"

func TestIRTupleDebug_NominalChildrenKeepTheirDispatchBoundary(t *testing.T) {
	// A bare return after `dbg` makes main Unit; after `io.inspect` it would
	// do nothing, which the checker rejects.
	for _, output := range []string{"dbg pair\n  return", "io.inspect(pair)"} {
		p, err := AnalyzeSource("main.nomi", "import std/io\ntype Email String\nfn main() {\n  io.print(\"start\")\n  pair = (1, Email(\"a@b.com\"))\n  "+output+"\n}\n")
		if err != nil {
			t.Fatal(err)
		}
		res, _, err := GenerateIR(p)
		if err != nil {
			t.Fatal(err)
		}
		for _, mod := range res.IR {
			for _, fn := range mod.Funcs() {
				if fn.Name() == "main" {
					t.Fatalf("%s retained unsupported nominal Debug dispatch", output)
				}
			}
		}
	}
}

func TestIRTupleDebug_CompletePrograms(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"ordered components and transparent result", `import std/io
fn main(): Int {
  pair = ("Ada", 37)
  (name, score) = pair
  dbg pair
  io.inspect((name, score))
  score
}`, "dbg line 5: pair = (\"Ada\", 37)\n(\"Ada\", 37)\n"},
		{"nested tuple and list", `import std/io
fn main() {
  io.inspect(((1, True), ["a", "b"], (2.5, "c")))
}`, "((1, True), [\"a\", \"b\"], (2.5, \"c\"))\n"},
		{"effectful operand once", `import std/io
fn pair(): (Int, String) { io.print("pair") return (42, "x") }
fn main() {
  io.inspect(pair())
}`, "pair\n(42, \"x\")\n"},
		{"debug escaping", `import std/io
fn main() {
  io.inspect(("a\nb", "a\"b", "a\\b"))
}`, "(\"a\nb\", \"a\\\"b\", \"a\\\\b\")\n"},
	} {
		t.Run(tc.name, func(t *testing.T) { verifyLambdaProgram(t, tc.src, tc.want) })
	}
}
