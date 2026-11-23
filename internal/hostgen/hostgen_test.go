package hostgen

import (
	"strings"
	"testing"
)

type drifted struct {
	X int64
	Y int64
}

func withMap(map[string]float64) int64 { return 0 }
func withSlice([]float64) int64        { return 0 }
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
	const source = `import
  int.Int64
end

pub struct Point
  x: Int
  y: Int
  z: Int
end

pub host fn with_map(m: Map<String, Int>) -> Int
pub host fn drift(p: Point) -> Int
pub host fn wrong_scalar(x: Float) -> Int
pub host fn hidden_error(x: Int64) -> Int
pub host fn short(x: Int64, y: Int64) -> Int
pub host fn user_enum(x: Int64) -> Option<Option<Int>>
`
	// A closure has no name a generated file can call.
	closure := func(int64, int64) int64 { return 0 }
	cases := []struct {
		key  string
		fn   any
		want string
	}{
		{"t.with_map", withMap, "Go map[string]float64 has no spelling the generator writes"},
		{"t.with_map", withSlice, "a Map crosses as rt.Map[K, V]"},
		{"t.drift", drift, "declares field z and the Go struct has no field"},
		{"t.wrong_scalar", intToInt, "Go int64 does not project to Float"},
		{"t.hidden_error", withError, "must return Result<_, String>, not Int"},
		{"t.short", intToInt, "the declaration takes 2 parameter(s)"},
		{"t.user_enum", intToPtr, "no conversion from Go int64 to Option<Int>"},
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
