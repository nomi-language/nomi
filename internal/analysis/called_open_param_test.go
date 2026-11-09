package analysis_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// A callback's parameter typed by a callee type parameter nothing has solved
// yet, and then called, is a function: the call solves the parameter's
// type to a function of its arguments' types, returning what the call's
// context needs. `Actor.start(workers, 0, fn s, f -> f(s))` makes M
// `(Int) -> Int`; the call used to be typed Unit, and the handler was
// refused as `(Int, _) -> Unit`.

const calledOpenParamActor = `import {
    std::actors.Actor
    std::supervisors.{Restart, Supervisor}
}

fn main() {
    workers = Supervisor.new(max_running: 1, restart: Restart.Transient)
    counter = Actor.start(workers, 0, fn s, f -> f(s))
    _ = Actor.cast(counter, fn n -> n + 1)
}
`

func TestCalledOpenParam_Accepted(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"an actor of function messages", calledOpenParamActor},
		{"a parameter solved by its argument first", `fn main() {
    xs: List<(Int) -> String> = [fn n -> "${n}"]
    _ys = Iter.map(xs, fn g -> g(1)) |> Iter.to_list()
}
`},
		{"a later argument checked against the solved type", `fn run<M>(h: (Int, M) -> Int, m: M) -> Int {
    h(1, m)
}

fn main() {
    _n = run(fn s, f -> f(s), fn n -> n * 2)
}
`},
		{"a call of several arguments", `fn fold<S, M>(state: S, h: (S, M) -> S, m: M) -> S {
    h(state, m)
}

fn main() {
    _s = fold("a", fn s, f -> f(s, 3), fn s, n -> "${s}${n}")
}
`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expectClean(t, checkWithStdlib(tc.src))
		})
	}
}

// The solved type is checked like any other: the actor's messages are
// `(Int) -> Int`, so a message of another type is refused.
func TestCalledOpenParam_SolvedTypeHoldsTheCall(t *testing.T) {
	src := strings.Replace(calledOpenParamActor, "fn n -> n + 1", `fn n -> "${n}"`, 1)
	errs := checkWithStdlib(src)
	if len(errs) == 0 {
		t.Fatal("the checker admits a `(Int) -> String` message for an actor of `(Int) -> Int`")
	}
}

func TestCalledOpenParam_Rejected(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		want      []string
	}{
		{"no type expected anywhere", `fn main() {
    h = fn g -> g(1)
    _ = h
}
`, []string{"2:12 cannot infer type for parameter g\n" +
			"help: annotate the parameter with its type, as in `fn g: T -> ...`; a later use of the lambda does not give it one"}},
		{"a second call with another argument type", `fn run<M>(h: (Int, M) -> Int, m: M) -> Int {
    h(1, m)
}

fn main() {
    _n = run(fn s, f -> {
        _ = f(s)
        f("x")
    }, fn n -> n)
}
`, []string{"8:11 argument 1: expected Int, got String"}},
		{"a result nothing fixes", `fn keep<M>(h: (Int, M) -> Int) -> Int {
    _ = h
    0
}

fn main() {
    _b = keep(fn s, f -> {
        _ = f(s)
        s
    })
}
`, []string{"7:15 the type argument M of `keep` is not determined: annotate the value passed in, or give the call its type arguments, as in `keep<M>(…)`\n" +
			"help: `keep` needs to know M, and nothing in the call or in how its result is used fixes it"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			errs := checkWithStdlib(tc.src)
			got := calledOpenParamDiags(errs)
			if strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
				t.Fatalf("errors:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(tc.want, "\n"))
			}
		})
	}
}

func calledOpenParamDiags(errs []analysis.TypeError) []string {
	got := make([]string, len(errs))
	for i, e := range errs {
		got[i] = fmt.Sprintf("%d:%d %s", e.Line, e.Col, diagText(e))
	}
	return got
}
