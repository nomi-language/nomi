package vmhost

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/vm"
)

// plantedTarget is the one function name the planted hook panics on. It is
// distinctive so a test elsewhere in this package that runs while the hook is
// installed compiles nothing it names.
const plantedTarget = "planted_compile_panic_target"

// plantCompilePanic makes the VM's bytecode compiler panic on plantedTarget
// until the test ends.
func plantCompilePanic(t *testing.T) {
	t.Helper()
	vm.CompileHook = func(f *ir.Func) {
		if f.Name() == plantedTarget {
			panic("planted compile panic")
		}
	}
	t.Cleanup(func() { vm.CompileHook = nil })
}

// wantInternalError requires err to be the planted panic as an InternalError.
func wantInternalError(t *testing.T, how string, err error) {
	t.Helper()
	var ice *InternalError
	if !errors.As(err, &ice) {
		t.Fatalf("%s: err = %v (%T), want an *InternalError", how, err, err)
	}
	msg := err.Error()
	for _, want := range []string{
		"internal compiler error: vm: compiling " + plantedTarget + " to bytecode: planted compile panic",
		"please report it at https://github.com/nomi-language/nomi/issues",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("%s: message %q does not contain %q", how, msg, want)
		}
	}
	if len(ice.Stack) == 0 {
		t.Errorf("%s: the InternalError carries no stack", how)
	}
}

const plantedProgram = `import std/io
import std/tasks.Task

fn planted_compile_panic_target(n: Int): Int {
    n + 1
}

fn direct(): Int {
    planted_compile_panic_target(1)
}

fn through_callback(): List<Int> {
    [1, 2] |> Iter.map(planted_compile_panic_target) |> Iter.to_list()
}

fn through_task(): String {
    outcome = concurrent {
        t = Task.spawn(|| planted_compile_panic_target(1))
        Task.outcome(t)
    }
    case outcome {
        .Completed(_) -> "completed"
        .Cancelled -> "cancelled"
        .Failed(_) -> "failed"
    }
}

fn fault(n: Int): Int {
    10 / n
}

fn main() {
    io.print("before")
    io.print(direct())
}

test "a direct call" {
    assert direct() == 2
}

test "a callback" {
    assert through_callback() == [2, 3]
}

test "a task observes nothing" {
    assert through_task() == "completed"
}

test "an unrelated case passes" {
    assert 1 + 1 == 2
}
`

// TestCompilePanic_IsAnInternalErrorOnEveryPath plants a panic in the VM's
// bytecode compiler and requires every way of running a program to answer it
// as an InternalError: never a crash, never a Nomi fault a program could
// observe, never a BLOCKED machine limit.
func TestCompilePanic_IsAnInternalErrorOnEveryPath(t *testing.T) {
	plantCompilePanic(t)
	ctx := context.Background()

	load := func(t *testing.T) *Program {
		t.Helper()
		p, err := LoadSource("main.nomi", plantedProgram, WithOutput(io.Discard))
		if err != nil {
			t.Fatalf("the program does not load: %v", err)
		}
		return p
	}

	t.Run("nomi run", func(t *testing.T) {
		var out bytes.Buffer
		err := load(t).Run(ctx, &out, nil, false)
		wantInternalError(t, "Run", err)
		if out.String() != "before\n" {
			t.Errorf("output %q, want the line printed before the planted call", out.String())
		}
	})

	t.Run("Call", func(t *testing.T) {
		for _, name := range []string{"direct", "through_callback", "through_task"} {
			_, err := load(t).Call(ctx, name)
			wantInternalError(t, "Call "+name, err)
		}
	})

	t.Run("Evaluate", func(t *testing.T) {
		_, err := load(t).Evaluate(ctx, "direct", EvalLimits{Steps: 1_000_000})
		wantInternalError(t, "Evaluate", err)
	})

	t.Run("nomi test", func(t *testing.T) {
		results := load(t).Cases(io.Discard, TestOptions{})
		if len(results) != 4 {
			t.Fatalf("%d case results, want 4", len(results))
		}
		for _, r := range results[:3] {
			if r.Blocked != nil {
				t.Errorf("%s is BLOCKED (%v), want an InternalError", r.Name, r.Blocked)
				continue
			}
			wantInternalError(t, r.Name, r.Err)
		}
		if last := results[3]; last.Err != nil || last.Blocked != nil {
			t.Errorf("%s: err %v, blocked %v; a case that never reaches the planted function passes",
				last.Name, last.Err, last.Blocked)
		}
	})

	t.Run("REPL", func(t *testing.T) {
		s := NewSession(io.Discard, io.Discard)
		p, err := s.Load("fn planted_compile_panic_target(): Int {\n    1\n}\n\nfn main() {\n    _ = planted_compile_panic_target()\n}\n")
		if err != nil {
			t.Fatalf("the input does not load: %v", err)
		}
		wantInternalError(t, "Session.Run", s.Run(ctx, p))
	})

	t.Run("a real fault stays a fault", func(t *testing.T) {
		_, err := load(t).Call(ctx, "fault", int64(0))
		if err == nil {
			t.Fatal("10 / 0 answered no error")
		}
		var ice *InternalError
		if errors.As(err, &ice) {
			t.Fatalf("a Nomi fault answered as an InternalError: %v", err)
		}
		if _, blocked := IsBlocked(err); blocked {
			t.Fatalf("a Nomi fault answered as BLOCKED: %v", err)
		}
	})
}
