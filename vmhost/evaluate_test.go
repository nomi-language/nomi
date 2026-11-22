package vmhost

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

const evaluateSource = `import std::io

fn answer() -> Int
    Iter.reduce(1..=4, 0, fn acc, n -> acc + n)
end

fn spin(n: Int) -> Int
    if n < 0 then
        n
    else
        spin(n + 1)
    end
end

fn forever() -> Int
    spin(0)
end

fn endless() -> Int
    Iter.repeat(1)
    |> Iter.filter(fn x ->
        if x < 0 then break
        True
    end)
    |> Iter.count()
end

fn loud() -> Int
    io.print("evaluated")
    1
end

fn doubled() -> String
    String.repeat("ab", 2)
end

fn main()
    io.print("${answer()} ${forever()} ${endless()} ${loud()} ${doubled()}")
end
`

func evaluateProgram(t *testing.T) *Program {
	t.Helper()
	p, err := LoadSource("evaluate", evaluateSource)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestEvaluate_AnswersAPureFunctionsValue(t *testing.T) {
	v, err := evaluateProgram(t).Evaluate(context.Background(), "answer", EvalLimits{Steps: 10_000})
	if err != nil || v != int64(10) {
		t.Fatalf("answer = %v, %v; want 10", v, err)
	}
}

func TestEvaluate_StopsAtItsStepLimit(t *testing.T) {
	// Time the evaluation alone: the first load in a process also analyzes
	// the stdlib, which takes seconds under -race.
	p := evaluateProgram(t)
	start := time.Now()
	_, err := p.Evaluate(context.Background(), "forever", EvalLimits{Steps: 100_000})
	if !errors.Is(err, ErrEvalLimit) {
		t.Fatalf("a function that never returns answered %v, want ErrEvalLimit", err)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("stopping took %v", took)
	}
}

func TestEvaluate_BoundsAnIterationDrivenInGo(t *testing.T) {
	_, err := evaluateProgram(t).Evaluate(context.Background(), "endless", EvalLimits{Steps: 100_000})
	if !errors.Is(err, ErrEvalLimit) {
		t.Fatalf("counting an endless iteration answered %v, want ErrEvalLimit", err)
	}
}

func TestEvaluate_StopsAtItsDeadline(t *testing.T) {
	_, err := evaluateProgram(t).Evaluate(context.Background(), "forever",
		EvalLimits{Steps: 1 << 62, Deadline: time.Now().Add(20 * time.Millisecond)})
	if !errors.Is(err, ErrEvalLimit) {
		t.Fatalf("a function past its deadline answered %v, want ErrEvalLimit", err)
	}
}

func TestEvaluate_RefusesAFunctionWithEffects(t *testing.T) {
	var out strings.Builder
	p, err := LoadSource("evaluate", evaluateSource, WithOutput(&out))
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Evaluate(context.Background(), "loud", EvalLimits{Steps: 10_000})
	var effectful *Effectful
	if !errors.As(err, &effectful) {
		t.Fatalf("a function that prints answered %v, want *Effectful", err)
	}
	if want := []string{"loud calls io.print"}; strings.Join(effectful.Effects, "|") != strings.Join(want, "|") {
		t.Errorf("effects = %q, want %q", effectful.Effects, want)
	}
	if out.Len() != 0 {
		t.Errorf("the refused function ran: %q", out.String())
	}
}

// A function that calls one whose cost grows with its argument is refused
// as unbounded, not as having effects: the limits cannot stop one call
// into Go.
func TestEvaluate_RefusesAFunctionWhoseCostIsUnbounded(t *testing.T) {
	_, err := evaluateProgram(t).Evaluate(context.Background(), "doubled", EvalLimits{Steps: 10_000})
	var unbounded *Unbounded
	if !errors.As(err, &unbounded) {
		t.Fatalf("a function that repeats a string answered %v, want *Unbounded", err)
	}
	var effectful *Effectful
	if errors.As(err, &effectful) {
		t.Errorf("a function that repeats a string answered %v, an *Effectful", err)
	}
	if want := []string{"doubled calls strings.String.repeat"}; strings.Join(unbounded.Calls, "|") != strings.Join(want, "|") {
		t.Errorf("calls = %q, want %q", unbounded.Calls, want)
	}
}
