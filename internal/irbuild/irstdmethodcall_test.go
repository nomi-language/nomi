package irbuild

import (
	"testing"
)

func TestIRStdMethodCall_EffectsAndSiblingCaller(t *testing.T) {
	verifyLambdaProgram(t, `import { std/io std/instant.Instant lib }
fn mark(n: Int): Int { io.print(n) return n }
fn main() {
  io.inspect(Instant.before?(
    Instant.from_seconds(mark(1)),
    Instant.from_seconds(mark(2) + 3)
  ))
  io.print(lib.convert(7))
}
`, "1\n2\nTrue\n7\n", map[string]string{
		"lib.nomi": "import std/duration.Duration\npub fn convert(n: Int): Int { Duration.as_hours(Duration.hours(n)) }",
	})
}

func TestIRStdMethodCall_LocalReceiverKeepsPrecedence(t *testing.T) {
	verifyLambdaProgram(t, `import std/io
struct Duration { n: Int }
impl Duration { fn as_hours(d: Duration): Int { d.n } }
fn main() { io.print(Duration.as_hours(Duration{n: 9})) }
`, "9\n")
}

func TestIRStdMethodCall_DeclinedBuildKeepsNativeEmission(t *testing.T) {
	p, err := AnalyzeSource("main.nomi", "import std/duration.Duration\nstruct Point { x: Int }\nstruct Outer { p: List<Point>\n hooks: Map<String, (Int) -> Int> = Map.empty() }\nfn main() { n = Duration.as_hours(Duration.hours(2)) dbg Outer{p: [Point{x: n}]} return }")
	if err != nil {
		t.Fatal(err)
	}
	retained, _, err := GenerateIR(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, mod := range retained.IR {
		for _, fn := range mod.Funcs() {
			if fn.Name() == "main" {
				t.Fatal("nominal Debug must decline after resolving stdlib calls")
			}
		}
	}
}

func TestIRStdMethodCall_HostBodiesStayOutsideRetainedCalls(t *testing.T) {
	p, err := AnalyzeSource("main.nomi", "import std/io\nfn upper(c: Context): Context { Context.with_value(c, Some(1)) }\nfn main() { _ = upper(Context.root()) io.print(\"x\") }")
	if err != nil {
		t.Fatal(err)
	}
	res, _, err := GenerateIR(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, mod := range res.IR {
		for _, fn := range mod.Funcs() {
			if fn.Name() == "upper" {
				t.Fatal("host call was retained as a Nomi body")
			}
		}
	}
}

func TestIRStdMethodCall_RefinementsAndConversions(t *testing.T) {
	verifyLambdaProgram(t, `import { std/io std/int.PositiveInt std/duration.Duration std/instant.Instant }
fn present(v: Maybe<PositiveInt>): Bool { case v { Some(_) -> True None -> False } }
fn main() {
  io.inspect(present(Int.to_positive(5)))
  io.inspect(present(Int.to_positive(0)))
  io.print(Duration.as_hours(Duration.hours(3)))
  io.print(Instant.to_seconds(Instant.from_seconds(42)))
}
`, "True\nFalse\n3\n42\n")
}
