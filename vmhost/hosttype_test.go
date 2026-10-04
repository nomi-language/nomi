package vmhost_test

import (
	"context"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/hostadapt"
	"github.com/nomi-language/nomi/vmhost"

	"github.com/nomi-language/nomi/rt"
)

// A plain user `host type` (no `go` binding) is an opaque handle an
// embedder's host table creates and reads, as a plain `host fn` is a
// crossing it answers: the program holds values of it, passes them back,
// and never looks inside.
const hostTypeProgram = `host type Counter

host fn counter_new(start: Int): Counter

host fn counter_value(c: Counter): Int

host fn counter_bump(c: Counter): Counter

struct Held {
  counter: Counter
}

fn bump_twice(c: Counter): Counter {
  counter_bump(counter_bump(c))
}

pub fn run(start: Int): Int {
  c = counter_new(start)
  held = Held{counter: bump_twice(c)}
  all = [c, held.counter]
  Iter.reduce(all, |acc = 0, x| acc * 100 + counter_value(x))
}
`

type counter struct{ n int64 }

func counterTable(*hostadapt.Env) (map[string]hostadapt.Func, error) {
	return map[string]hostadapt.Func{
		"counter_new": func(_ *rt.Frame, args []rt.Value) (rt.Value, error) {
			return &counter{n: args[0].(int64)}, nil
		},
		"counter_value": func(_ *rt.Frame, args []rt.Value) (rt.Value, error) {
			return args[0].(*counter).n, nil
		},
		"counter_bump": func(_ *rt.Frame, args []rt.Value) (rt.Value, error) {
			return &counter{n: args[0].(*counter).n + 1}, nil
		},
	}, nil
}

func TestCall_APlainHostTypeIsAnEmbeddersHandle(t *testing.T) {
	p, err := vmhost.LoadSource("main", hostTypeProgram, vmhost.WithHosts(counterTable))
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.Call(context.Background(), "run", 5)
	if err != nil {
		t.Fatal(err)
	}
	if got != int64(507) {
		t.Fatalf("run(5) answered %v, want 507", got)
	}
}

// With no table answering its functions, the program is refused naming the
// functions; the type itself needs no registration.
func TestLoad_APlainHostTypeNeedsOnlyItsFunctions(t *testing.T) {
	_, err := vmhost.LoadSource("main", hostTypeProgram)
	if err == nil {
		t.Fatal("a program whose host functions nothing answers loaded")
	}
	if !strings.Contains(err.Error(), "3 externs declared but not registered") ||
		strings.Contains(err.Error(), "Counter\t") {
		t.Fatalf("load error %q: want the three functions and not the type", err)
	}
}
