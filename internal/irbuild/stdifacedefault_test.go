package irbuild

import (
	"testing"

	"github.com/nomi-language/nomi/internal/frontend"
)

// discreteDay is a program type implementing std/discrete's Discrete, whose
// `steps_between` is an open Nomi default declared in std (returning None):
// the impl writes only `next`.
const discreteDay = `pub struct Day
    n: Int
end

impl Discrete for Day
    fn next(d: Day) -> Maybe<Day>
        if d.n < 7 then Some(Day{n: d.n + 1}) else None
    end
end
`

// A default of a std interface reaches a program's implementor that does
// not write it, by every spelling: type-qualified, interface-qualified,
// through a generic bound, and on a type another file declares. The
// default is std's body, lowered in std/discrete (stdifacedefault.go);
// each spelling declined before ("qualified call, Type.method").
func TestStdIfaceDefault_ProgramImplementorInherits(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const main = `import std::io

` + discreteDay + `
fn steps<T>(a: T, b: T) -> Maybe<Int> where T: Discrete
    Discrete.steps_between(a, b)
end

fn main()
    io.print(Day.steps_between(Day{n: 1}, Day{n: 3}))
    io.print(Discrete.steps_between(Day{n: 1}, Day{n: 3}))
    io.print(steps(Day{n: 2}, Day{n: 5}))
end
`
	verifyLambdaProgram(t, main, "None\nNone\nNone\n")

	t.Run("the type and impl in another file", func(t *testing.T) {
		verifyLambdaProgram(t, `import std::io
import days.Day

fn main()
    io.print(Day.steps_between(Day{n: 1}, Day{n: 3}))
    io.print(Discrete.steps_between(Day{n: 1}, Day{n: 3}))
end
`, "None\nNone\n", map[string]string{"days.nomi": discreteDay})
	})
}

// An impl that writes an open default overrides it: the written function is
// the call's by each spelling, the default is not.
func TestStdIfaceDefault_OpenOverrideWins(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	verifyLambdaProgram(t, `import std::io

struct Step
    n: Int
end

impl Discrete for Step
    fn next(s: Step) -> Maybe<Step>
        Some(Step{n: s.n + 1})
    end

    fn steps_between(start: Step, stop: Step) -> Maybe<Int>
        Some(stop.n - start.n)
    end
end

fn steps<T>(a: T, b: T) -> Maybe<Int> where T: Discrete
    Discrete.steps_between(a, b)
end

fn main()
    io.print(Step.steps_between(Step{n: 1}, Step{n: 4}))
    io.print(Discrete.steps_between(Step{n: 1}, Step{n: 4}))
    io.print(steps(Step{n: 2}, Step{n: 9}))
end
`, "Some(3)\nSome(3)\nSome(7)\n")
}

// The instance links by name when each module is encoded apart: the
// instance's symbol is named from the interface function it is an instance
// of, with its receiver (linkName).
func TestStdIfaceDefault_LinksThroughObjects(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	defer LinkThroughObjects()()
	verifyLambdaProgram(t, `import std::io

`+discreteDay+`
fn main()
    io.print(Day.steps_between(Day{n: 1}, Day{n: 3}))
end
`, "None\n")
}

// The front end accepts each of these programs: the tests above hold the
// builder to lowering them, so they must fail if the checker starts to
// reject one rather than pass vacuously.
func TestStdIfaceDefault_FrontEndAccepts(t *testing.T) {
	src := discreteDay + `
fn main()
    _ = Day.steps_between(Day{n: 1}, Day{n: 3})
    _ = Discrete.steps_between(Day{n: 1}, Day{n: 3})
end
`
	if _, err := frontend.New(frontend.Config{}).CheckSource("main", src, frontend.Mode{}); err != nil {
		t.Fatalf("the front end rejects this, so the lowering tests above say nothing: %v", err)
	}
}

// A std body a program builds for itself (it reaches an interface default,
// `Int.max`) whose parameter default is no literal: `Decimal.format`'s
// `max_fraction` defaults to `Int.max(Decimal.scale(d), min_fraction)`, and
// its `mode` to `RoundingMode.HalfEven`. A call that leaves either out
// reaches the arity wrapper or default accessor the instance's gen built in
// std/decimal (stdUnretainedDefaultedCall), linked in memory and through
// objects by name.
func TestStdIfaceDefault_UnretainedBodyFillsADefaultThatIsNoLiteral(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = "import std::io\n\nfn main()\n" +
		"    io.print(Decimal.format(1234.50d, Locale`en`))\n" +
		"    io.print(Decimal.format(0.125d, Locale`en`, max_fraction: 2))\n" +
		"    io.print(Decimal.format(12d, Locale`fr`, 2))\n" +
		"    io.print(Decimal.format(1234.5d, Locale`en`, grouping: False))\n" +
		"    io.print(Decimal.format_percent(0.125d, Locale`en`))\nend\n"
	const want = "1,234.50\n0.12\n12,00\n1234.5\n12.5%\n"
	verifyLambdaProgram(t, src, want)
	t.Run("linked through objects", func(t *testing.T) {
		defer LinkThroughObjects()()
		verifyLambdaProgram(t, src, want)
	})
}
