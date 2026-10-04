package irbuild

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
)

func TestIRInterfaceCall_ADeclinedBodyIsNotRetained(t *testing.T) {
	for _, tail := range []string{"", "dbg Outer{p: [Point{x: 1}]}"} {
		t.Run(tail, func(t *testing.T) {
			result := "Unit"
			if tail != "" {
				result = "Outer"
			}
			p, err := AnalyzeSource("main.nomi", `import std/io
type Token
struct Point { x: Int }
struct Outer {
  p: List<Point>
  hooks: Map<String, (Int) -> Int> = Map.empty()
}
interface Named { fn name(value: self): String }
impl Named for Token { fn name(value: Token): String { _ = value; "token" } }
fn main(): `+result+` { io.print(Named.name(Token)); `+tail+` }
`)
			if err != nil {
				t.Fatal(err)
			}
			got, _, err := GenerateIR(p)
			if err != nil {
				t.Fatal(err)
			}
			retained := false
			for _, mod := range got.IR {
				for _, f := range mod.Funcs() {
					if f.Name() == "main" {
						retained = true
					}
				}
			}
			if retained != (tail == "") {
				t.Fatalf("main retained=%v", retained)
			}
		})
	}
}

// An interface-typed parameter is an existential: the call through it is a
// DISPATCHED call selected by the runtime type, never a direct call to one
// implementation, and it prints the expected output.
func TestIRInterfaceCall_ErasedReceiverDispatches(t *testing.T) {
	src := `import std/io
type Token
struct Box { n: Int }
interface Named { fn name(value: self): String }
impl Named for Token { fn name(value: Token): String { _ = value; "token" } }
impl Named for Box { fn name(value: Box): String { "box " + Int.to_string(value.n) } }
fn describe(value: Named): String { Named.name(value) }
fn main() {
 io.print(describe(Token))
 io.print(describe(Box{n: 2}))
}
`
	p, err := AnalyzeSource("main.nomi", src)
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := GenerateIR(p)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, mod := range got.IR {
		for _, f := range mod.Funcs() {
			if f.Name() != "describe" {
				continue
			}
			found = true
			for _, b := range f.Blocks() {
				for _, in := range b.Instrs() {
					if c, isCall := in.(*ir.Call); isCall && c.Form() != ir.CalleeDispatched {
						t.Fatalf("describe calls %s, not a dispatched call", c)
					}
				}
			}
		}
	}
	if !found {
		t.Fatal("describe was not retained")
	}
	verifyLambdaProgram(t, src, "token\nbox 2\n")
}
