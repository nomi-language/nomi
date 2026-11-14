package vmhost_test

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/internal/irbuild"
	"github.com/nomi-language/nomi/vmhost"
)

// `nomi check` reports a body the compiler accepts and cannot lower as an
// error at its source, worded for the author, instead of leaving it to
// `nomi run` to refuse as BLOCKED, with advice for the declines whose cause
// the author can act on. Each case blocks on a planted decline carrying the
// builder reason the advice is keyed by. The fixture must keep
// type-checking: if the front end started rejecting it, the error below
// would be a type error and this test would say nothing about lowering.
func TestCheck_ReportsWhatTheCompilerCannotLower(t *testing.T) {
	path := writeProgram(t, `import std::io

fn planted_decline(n: Int) -> Int
  n
end

fn evens(xs: List<Int>) -> Int
  planted_decline(Iter.count(xs))
end

fn unused() -> Int
  planted_decline(0)
end

fn main()
  io.print(evens([1, 2]))
end
`)
	cases := []struct {
		reason string
		hint   string
	}{
		{"an iter-sensitive function passed where its break or continue cannot be caught: is_big",
			"runs as a callback only when the iteration function is named with its owner, as in `Iter.find(xs, is_big)`"},
	}
	for _, c := range cases {
		restore := irbuild.PlantDeclineForTest("planted_decline", c.reason)
		err := vmhost.Check(path)
		restore()
		var ds frontend.Diagnostics
		if !errors.As(err, &ds) {
			t.Fatalf("Check answered %v; want diagnostics", err)
		}
		if len(ds) != 1 {
			t.Fatalf("got %d diagnostics, want 1 (an unreached body is not reported):\n%v", len(ds), err)
		}
		d := ds[0]
		const message = "this call to `planted_decline` is not supported yet, so `fn evens` cannot run"
		if d.Path != path || d.Line != 8 || d.Col != 3 || d.Message != message {
			t.Errorf("diagnostic = %s:%d:%d %q; want line 8 col 3 %q", d.Path, d.Line, d.Col, d.Message, message)
		}
		if len(d.Hints) != 2 || !strings.Contains(d.Hints[0], c.hint) || d.Hints[1] != gapHint {
			t.Errorf("hints %q; want one containing %q, then the gap hint", d.Hints, c.hint)
		}
		for _, internal := range []string{"*ast.", "builder", "not retained", "iter-sensitive", "ident bound"} {
			if strings.Contains(err.Error(), internal) {
				t.Errorf("the report names compiler internals (%q):\n%v", internal, err)
			}
		}
	}
}

// A body the run never reaches is not reported, and a program whose every
// reached body lowers checks clean.
func TestCheck_ALoweredProgramChecksClean(t *testing.T) {
	path := writeProgram(t, `import std::io

fn add(a: Int, b: Int) -> Int
  a + b
end

fn main()
  io.print(Iter.reduce([1, 2, 3], 0, fn acc, n -> add(acc, n)))
end
`)
	if err := vmhost.Check(path); err != nil {
		t.Fatalf("Check rejected a program that runs: %v", err)
	}
}

// When `main` itself cannot be lowered, the report is at the expression it
// stopped at: under plantDecline, the call to `planted_decline`.
func TestCheck_ReportsADeclinedMainAtTheExpression(t *testing.T) {
	plantDecline(t)
	path := writeProgram(t, `import std::io

fn planted_decline(a: Int) -> Int
    a
end

fn main()
    u = planted_decline(1)
    io.inspect(u)
end
`)
	err := vmhost.Check(path)
	var ds frontend.Diagnostics
	if !errors.As(err, &ds) || len(ds) != 1 {
		t.Fatalf("Check answered %v; want one diagnostic", err)
	}
	if d := ds[0]; d.Line != 8 || d.Col != 9 || d.Message != "this call to `planted_decline` is not supported yet, so `fn main` cannot run" {
		t.Errorf("got %d:%d %q", d.Line, d.Col, d.Message)
	}
}

// checkAndRun answers what `nomi check` and `nomi run` report for the
// program at path, under NOMI_DEBUG_LOWERING so the lowering's reason is in
// both. Each must be one diagnostic, and the two the same.
func checkAndRun(t *testing.T, path string) frontend.Diagnostic {
	t.Helper()
	t.Setenv("NOMI_DEBUG_LOWERING", "1")
	checked := vmhost.Check(path)
	var ds frontend.Diagnostics
	if !errors.As(checked, &ds) || len(ds) != 1 {
		t.Fatalf("Check answered %v; want one diagnostic", checked)
	}
	p, err := vmhost.Load(path)
	if err != nil {
		t.Fatalf("the front end rejects this, so nothing below is tested: %v", err)
	}
	ran := p.Run(context.Background(), io.Discard, nil, false)
	if ran == nil || ran.Error() != checked.Error() {
		t.Fatalf("Run answered:\n%v\nCheck answered:\n%v", ran, checked)
	}
	return ds[0]
}

// A body that declines three calls below `main` is reported at its own
// expression, with how the program reaches it and, on request, its own
// reason: the report leads to the body, not to main's call.
func TestCheck_ReportsTheBodyThreeCallsDeep(t *testing.T) {
	plantDecline(t)
	path := writeProgram(t, `import std::io

fn planted_decline(n: Int) -> Int
    n
end

fn evaluate(n: Int) -> Int
    planted_decline(n) + 1
end

fn run(n: Int) -> Int
    evaluate(n) * 2
end

fn run_prompt(n: Int) -> Int
    run(n) - 3
end

fn main()
    io.print(run_prompt(4))
end
`)
	d := checkAndRun(t, path)
	const message = "this call to `planted_decline` is not supported yet, so `fn evaluate` cannot run"
	if d.Line != 8 || d.Col != 5 || d.Message != message {
		t.Errorf("diagnostic = %d:%d %q; want 8:5 %q", d.Line, d.Col, d.Message, message)
	}
	want := []string{
		"the program reaches it through `main` → `run_prompt` → `run` → `evaluate`",
		gapHint,
		"the lowering's reason: [evaluate] a planted decline",
	}
	if !slices.Equal(d.Hints, want) {
		t.Errorf("hints\n%q\nwant\n%q", d.Hints, want)
	}
}

// A call to a function whose signature the lowering refused is reported at
// that signature's refused parameter type, naming the call, the type the
// refusal comes from and its declaration, not at the caller's call: each of
// run_prompt, run and evaluate takes an Interp, which holds a struct the
// builder refuses (irbuild.PlantTypeDeclineForTest), so main's call to
// run_prompt is where the lowering stops.
func TestCheck_ReportsARefusedSignatureAtItsType(t *testing.T) {
	t.Cleanup(irbuild.PlantTypeDeclineForTest("Planted", plantedReason))
	path := writeProgram(t, `import std::io

struct Planted
    n: Int
end

struct Interp
    held: Maybe<Planted>
    n: Int
end

fn evaluate(i: Interp) -> Int
    i.n + 1
end

fn run(i: Interp) -> Int
    evaluate(i) * 2
end

fn run_prompt(i: Interp) -> Int
    run(i) - 3
end

fn main()
    io.print(run_prompt(Interp{held: None, n: 1}))
end
`)
	d := checkAndRun(t, path)
	const message = "the type `Interp` is not supported yet, so `fn main` cannot run"
	if d.Path != path || d.Line != 20 || d.Col != 18 || d.Message != message {
		t.Errorf("diagnostic = %s:%d:%d %q; want 20:18 %q", d.Path, d.Line, d.Col, d.Message, message)
	}
	want := []string{
		"`fn main` calls `fn run_prompt`, whose parameter `i` has this type",
		"`Interp` is not supported because of the type `Planted` it holds, declared at " + path + ":3:8",
		gapHint,
		"the lowering's reason: [main] a direct callee that is not lowerable: run_prompt; [run_prompt] " +
			plantedReason + ": Planted (blocks Interp)",
	}
	if !slices.Equal(d.Hints, want) {
		t.Errorf("hints\n%q\nwant\n%q", d.Hints, want)
	}
}

// gapHint is the last hint of every unsupported diagnostic.
const gapHint = "this is a gap in Nomi, not a mistake in your code; please report it at https://github.com/nomi-language/nomi/issues"
