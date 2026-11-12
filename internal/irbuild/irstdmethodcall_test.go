package irbuild

import (
	"testing"
)

func TestIRStdMethodCall_EffectsAndSiblingCaller(t *testing.T) {
	verifyLambdaProgram(t, `import
    std::io std::instant.Instant lib
end
fn mark(n: Int) -> Int
    io.print(n)
    return n
end
fn main()
  io.inspect(Instant.is_before(
    Instant.from_seconds(mark(1)),
    Instant.from_seconds(mark(2) + 3)
  ))
  io.print(lib.convert(7))
end
`, "1\n2\nTrue\n7\n", map[string]string{
		"lib.nomi": "import std::duration.Duration\npub fn convert(n: Int) -> Int\n    Duration.as_hours(Duration.hours(n))\nend",
	})
}

func TestIRStdMethodCall_LocalReceiverKeepsPrecedence(t *testing.T) {
	verifyLambdaProgram(t, `import std::io
struct Duration
    n: Int
end
impl Duration
    fn as_hours(d: Duration) -> Int
        d.n
    end
end
fn main()
    io.print(Duration.as_hours(Duration{n: 9}))
end
`, "9\n")
}

// A std method call's result builds a struct whose field holds a Map of
// function values.
func TestIRStdMethodCall_BesideAFunctionMapFieldRuns(t *testing.T) {
	verifyLambdaProgram(t, "import\n    std::io std::duration.Duration\nend\nstruct Point\n    x: Int\nend\nstruct Outer\n    p: List<Point>\n hooks: Map<String, (Int) -> Int> = {}\nend\nfn main()\n    n = Duration.as_hours(Duration.hours(2))\n    io.inspect(Outer{p: [Point{x: n}]})\nend\n",
		"Outer{p: [Point{x: 2}], hooks: {}}\n")
}

func TestIRStdMethodCall_HostBodiesStayOutsideRetainedCalls(t *testing.T) {
	p, err := AnalyzeSource("main.nomi", "import std::io\nfn upper(c: Context) -> Context\n    Context.with_value(c, Some(1))\nend\nfn main()\n    _ = upper(Context.root())\n    io.print(\"x\")\nend")
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
	verifyLambdaProgram(t, `import
    std::io std::int.PositiveInt std::duration.Duration std::instant.Instant
end
fn present(v: Maybe<PositiveInt>) -> Bool
    case v
        Some(_) -> True
        None -> False
    end
end
fn main()
  io.inspect(present(Int.to_positive(5)))
  io.inspect(present(Int.to_positive(0)))
  io.print(Duration.as_hours(Duration.hours(3)))
  io.print(Instant.to_seconds(Instant.from_seconds(42)))
end
`, "True\nFalse\n3\n42\n")
}
