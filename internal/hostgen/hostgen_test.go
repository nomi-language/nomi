package hostgen

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

type drifted struct {
	X int64
	Y int64
}

func withMap(map[string]float64) int64 { return 0 }
func drift(drifted) int64              { return 0 }
func intToInt(int64) int64             { return 0 }
func withError(int64) (int64, error)   { return 0, nil }
func intToPtr(int64) *int64            { return nil }
func twoInts(int64, int64) int64       { return 0 }

// TestUnadaptableBindingsFailGenerationByName plants each way a binding and
// its declaration can disagree and requires generation to refuse, naming the
// binding. An adapter the generator cannot write must be a build-time error,
// never a run-time one.
func TestUnadaptableBindingsFailGenerationByName(t *testing.T) {
	const source = `pub struct Point {
  x: Int
  y: Int
  z: Int
}

pub host fn with_map(m: Map<String, Int>): Int
pub host fn drift(p: Point): Int
pub host fn wrong_scalar(x: Float): Int
pub host fn hidden_error(x: Int): Int
pub host fn short(x: Int, y: Int): Int
pub host fn user_enum(x: Int): Maybe<Maybe<Int>>
`
	// A closure has no name a generated file can call.
	closure := func(int64, int64) int64 { return 0 }
	cases := []struct {
		key  string
		fn   any
		want string
	}{
		{"t.with_map", withMap, "value: Go float64 does not project to Int"},
		{"t.drift", drift, "declares field z and the Go struct has no field"},
		{"t.wrong_scalar", intToInt, "Go int64 does not project to Float"},
		{"t.hidden_error", withError, "must return Result<_, String>, not Int"},
		{"t.short", intToInt, "the declaration takes 2 parameter(s)"},
		{"t.user_enum", intToPtr, "no conversion from Go int64 to Maybe<Int>"},
		{"t.short", closure, "is not a top-level function"},
	}
	for _, c := range cases {
		tb := Table{
			Package: "x",
			Source: func(m string) ([]byte, bool) {
				if m == "t" {
					return []byte(source), true
				}
				return stdSource(m)
			},
			Funcs:    []FuncRow{{Name: c.key, Fn: c.fn}},
			ModuleOf: func(FuncRow) (string, error) { return "t", nil },
		}
		_, err := Generate(tb)
		if err == nil {
			t.Errorf("%s: generated an adapter the declaration does not describe", c.key)
			continue
		}
		if !strings.HasPrefix(err.Error(), c.key+": ") || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: refusal %q does not name the binding and say %q", c.key, err, c.want)
		}
	}

	// The control: the same declaration with a matching Go function generates.
	tb := Table{
		Package: "x",
		Source: func(m string) ([]byte, bool) {
			if m == "t" {
				return []byte(source), true
			}
			return stdSource(m)
		},
		Funcs:    []FuncRow{{Name: "t.short", Fn: twoInts}},
		ModuleOf: func(FuncRow) (string, error) { return "t", nil },
	}
	if _, err := Generate(tb); err != nil {
		t.Fatalf("a matching binding is refused: %v", err)
	}
}

// TestARefusedBindingLeavesNoHalfWrittenConversion: a refusal rolls the
// generator back, so a later binding needing the conversion the refusal
// abandoned is refused too, rather than accepted with a call to a method that
// was memoized and never written. internal/ffirun generates on after a
// refusal; without the rollback its wrapper failed to compile
// (`b.out6 undefined`).
func TestARefusedBindingLeavesNoHalfWrittenConversion(t *testing.T) {
	const source = `pub host fn first(x: Int): Maybe<Maybe<Int>>
pub host fn second(x: Int): Maybe<Maybe<Int>>
pub host fn fine(x: Int, y: Int): Int
`
	ms, err := LoadModules(func(m string) ([]byte, bool) {
		if m == "t" {
			return []byte(source), true
		}
		return stdSource(m)
	})
	if err != nil {
		t.Fatal(err)
	}
	var bindings []Binding
	for _, row := range []FuncRow{{Name: "t.first", Fn: intToPtr}, {Name: "t.second", Fn: intToPtr}, {Name: "t.fine", Fn: twoInts}} {
		hf, err := FindHostFunc(ms, "t", row.Name)
		if err != nil {
			t.Fatal(err)
		}
		bindings = append(bindings, Binding{Key: row.Name, Fn: row.Fn, Decl: hf})
	}
	g := New("x", ms.Resolver(), nil)
	var refused []string
	for _, b := range bindings {
		if err := g.Add(b); err != nil {
			refused = append(refused, b.Key)
		}
	}
	if strings.Join(refused, " ") != "t.first t.second" {
		t.Fatalf("refused %v, want t.first and t.second", refused)
	}
	if _, err := g.Source(""); err != nil {
		t.Fatalf("the file after two refusals does not format: %v", err)
	}
}

// Structure writes a recursive type out once and names it after, so it
// terminates; and with Unloaded set, a name no loaded module declares is a
// leaf rather than an error, so the declared fields around it still count.
func TestStructureOfARecursiveTypeOverAnUnloadedName(t *testing.T) {
	src := "pub struct Node {\n    label: String\n    wait: Duration\n    next: Maybe<Node>\n}\n"
	ms := NewModules(func(module string) ([]byte, bool) {
		if module == "tree" {
			return []byte(src), true
		}
		return nil, false
	})
	mod, err := ms.Load("tree")
	if err != nil {
		t.Fatal(err)
	}
	node := &ast.SimpleType{Name: "Node"}
	if _, err := ms.Resolver().Shape(mod, node); err == nil {
		t.Fatal("Duration resolved with no std loaded, so the Unloaded case below tests nothing")
	}
	res := ms.Resolver()
	res.Unloaded = true
	s, err := res.Shape(mod, node)
	if err != nil {
		t.Fatalf("Shape: %v", err)
	}
	got := s.Structure()
	for _, want := range []string{`"label"`, `"wait"`, `"?tree:Duration"`, `"next"`} {
		if !strings.Contains(got, want) {
			t.Errorf("Structure %s lacks %s", got, want)
		}
	}
	if n := strings.Count(got, `"tree.Node"`); n != 2 {
		t.Errorf("Structure %s names tree.Node %d times, want 2 (written out, then named)", got, n)
	}
}
