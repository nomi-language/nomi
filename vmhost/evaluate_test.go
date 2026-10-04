package vmhost

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

const evaluateSource = `import std/io

fn answer(): Int {
    Iter.reduce(1..=4, |acc = 0, n| acc + n)
}

fn spin(n: Int): Int {
    if n < 0 {
        n
    } else {
        spin(n + 1)
    }
}

fn forever(): Int {
    spin(0)
}

fn endless(): Int {
    Iter.count(Iter.repeat(1))
}

fn loud(): Int {
    io.print("evaluated")
    1
}

fn main() {
    io.print("${answer()} ${forever()} ${endless()} ${loud()}")
}
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
	start := time.Now()
	_, err := evaluateProgram(t).Evaluate(context.Background(), "forever", EvalLimits{Steps: 100_000})
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
