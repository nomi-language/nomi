package analysis_test

import (
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// TestStdlibIterTypeArgsSolve pins the general ImplTypeArgs registry's
// Iter element type for the built-in sources — the cases the deleted
// `next`-shape extractor used to hard-code in unify.go. Under the push
// protocol the element type is solved from `each_while`'s `yield: (T) -> Bool`
// parameter rather than a `Maybe<(T, self)>` return, and `Seq` (the lone
// carrier behind every lazy adapter) has to solve through the nested function
// type in its `run` field. The behavioral iter.* tests in
// checker_stdlib_test.go drive these through the unifier; this localizes
// a regression to "the solved template is wrong" rather than the unifier.
func TestStdlibIterTypeArgsSolve(t *testing.T) {
	lib := loadStdlibOnce()
	fa := &analysis.FileAnalysis{}
	analysis.AttachStdlibProjectImpls(fa, lib.Files)
	pi := fa.ProjectImpls
	if pi == nil {
		t.Fatal("no ProjectImpls built")
	}
	want := map[string]string{
		"List":        "T",
		"Vector":      "T",
		"Seq":         "T",
		"Set":         "T",
		"Map":         "(K, V)",
		"Range":       "T",
		"StepByRange": "T",
		"String":      "String",
	}
	wantReceiver := map[string]string{
		"List":        "List<T>",
		"Vector":      "Vector<T>",
		"Seq":         "Seq<T>",
		"Set":         "Set<T>",
		"Map":         "Map<K, V>",
		"Range":       "Range<T>",
		"StepByRange": "StepByRange<T, S>",
		"String":      "String",
	}
	for typeName, wantArg := range want {
		info := pi.ImplTypeArgs[typeName]["Iter"]
		if info == nil {
			t.Errorf("%s: no (Iter) ImplTypeArgs entry", typeName)
			continue
		}
		if len(info.Args) != 1 {
			t.Errorf("%s: expected 1 Iter type arg, got %d", typeName, len(info.Args))
			continue
		}
		if info.Args[0].String() != wantArg {
			t.Errorf("%s: Iter element type = %q, want %q", typeName, info.Args[0].String(), wantArg)
		}
		if info.Receiver == nil || info.Receiver.String() != wantReceiver[typeName] {
			got := "<nil>"
			if info.Receiver != nil {
				got = info.Receiver.String()
			}
			t.Errorf("%s: Iter receiver = %q, want %q", typeName, got, wantReceiver[typeName])
		}
	}
}

func TestStdlibAddTypeArgSetsSolve(t *testing.T) {
	lib := loadStdlibOnce()
	fa := &analysis.FileAnalysis{}
	analysis.AttachStdlibProjectImpls(fa, lib.Files)
	pi := fa.ProjectImpls
	if pi == nil {
		t.Fatal("no ProjectImpls built")
	}
	cases := []struct {
		typeName string
		rhs      string
		out      string
	}{
		{"Duration", "Duration", "Duration"},
		{"Instant", "Duration", "Instant"},
		{"DateTime", "Duration", "DateTime"},
	}
	for _, tc := range cases {
		set := pi.ImplTypeArgSets[tc.typeName]["Add"]
		if len(set) == 0 {
			t.Errorf("%s: no Add ImplTypeArgSets entry", tc.typeName)
			continue
		}
		found := false
		for _, info := range set {
			if len(info.Args) == 2 && info.Args[0].String() == tc.rhs && info.Args[1].String() == tc.out {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: missing Add<%s, %s> entry; got %v", tc.typeName, tc.rhs, tc.out, set)
		}
	}
}

// TestGeneralInterfaceBareTypeArgSolves is the Stage-2 driver: a generic
// NON-iter user interface with a default method returning bare `T` must
// solve `T` from the concrete receiver, with no annotation. The binding form
// (`x = Chooser.pick(...)`) gives the checker no expected type to push inward,
// so `T` can only come from unifying the `Pair<Int>` receiver against
// `Chooser<T>`. Today that binding fails ("type is not locally determined")
// because only `iter` gets type-arg binding from a concrete receiver (the
// hardcoded `iface.Name == "iter"` unify special-case). Goes green once
// unifyInterfaceAgainstConcrete consults the general ImplTypeArgs registry.
func TestGeneralInterfaceBareTypeArgSolves(t *testing.T) {
	_, errs := checkSourceWithStdlib(`import std/io

interface Chooser<T> {
  fn first(p: self): T

  fn pick(p: self): T {
    Chooser.first(p)
  }
}

struct Pair<E> {
  a: E
  b: E
}

impl Chooser for Pair<E> {
  fn first(p: Pair<E>): E {
    p.a
  }
}

fn f() {
  x = Chooser.pick(Pair{a: 1, b: 2})
  io.print(x)
}

`)
	expectNoStdlibErrors(t, errs)
}
