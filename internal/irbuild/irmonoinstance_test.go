package irbuild

import (
	"io"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/vm"
)

func TestIRMonoInstance_DistinctDeclarations(t *testing.T) {
	const src = `import std/io
fn instance_pair_sum<T>(pair: (T, T)): T where T: Add<T, T> { pair.0 + pair.1 }
fn main() {
  io.print(instance_pair_sum((1, 2)))
  io.print(instance_pair_sum(("a", "b")))
  io.print(instance_pair_sum((3, 4)))
}`
	p, err := AnalyzeSource("main.nomi", src)
	if err != nil {
		t.Fatal(err)
	}
	res, _, err := GenerateIR(p)
	if err != nil {
		t.Fatal(err)
	}
	var instances []*ir.Func
	var owner *ir.Module
	for _, mod := range res.IR {
		for _, fn := range mod.Funcs() {
			if fn.Name() == "instance_pair_sum" {
				instances = append(instances, fn)
				owner = mod
			}
		}
	}
	if len(instances) != 2 {
		t.Fatalf("retained %d instances, want 2", len(instances))
	}
	if instances[0].Sym() == instances[1].Sym() {
		t.Fatal("two generic instances share one callee identity")
	}
	calls := map[*ir.Symbol]bool{}
	for _, mod := range res.IR {
		for _, fn := range mod.Funcs() {
			for _, b := range fn.Blocks() {
				for _, in := range b.Instrs() {
					if c, ok := in.(*ir.Call); ok && c.Callee() != nil && c.Callee().Name() == "instance_pair_sum" {
						calls[c.Callee()] = true
					}
				}
			}
		}
	}
	if len(calls) != 2 || !calls[instances[0].Sym()] || !calls[instances[1].Sym()] {
		t.Fatalf("retained calls and retained bodies disagree: %d called symbols", len(calls))
	}
	m := vm.NewProgram(owner, res.IRModules(), io.Discard)
	for i, tc := range []struct{ arg, want any }{
		{int64(21), int64(42)},
		{"a", "aa"},
	} {
		got, err := vmRunSymV(m, instances[i].Sym(), &probeTuple{items: []any{tc.arg, tc.arg}})
		want := tc.want
		if err != nil || got != want {
			t.Fatalf("instance %d: result=%v error=%v; want %v", i, got, err, want)
		}
	}
}

func TestIRMonoInstance_CorpusDeclarationsUnique(t *testing.T) {
	_, files := corpusAnalysis(t)
	seenModules := map[*ir.Module]bool{}
	for _, file := range files {
		if file.Res == nil {
			continue
		}
		for _, mod := range file.Res.IRModules() {
			if seenModules[mod] {
				continue
			}
			seenModules[mod] = true
			seen := map[*ir.Symbol]*ir.Func{}
			for _, fn := range mod.Funcs() {
				if fn.Sym() == nil {
					continue
				}
				if prior := seen[fn.Sym()]; prior != nil {
					t.Errorf("%s: %s and %s share one declaration", file.Rel, prior.Name(), fn.Name())
				}
				seen[fn.Sym()] = fn
			}
		}
	}
	if len(seenModules) == 0 {
		t.Fatal("no retained modules inspected")
	}
}
