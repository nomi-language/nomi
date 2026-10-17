package analysis_test

import (
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// Map update by spread: `%{..base, key => value}`, which means
// `Map.merge(base, %{key => value})`. The result has the base's type, and
// each entry's key and value must have its key and value types.

const mapSpreadDecls = `struct P {
  x: Int
}
struct Cfg {
  name: String
  prefs: Map<String, String>
}
`

func TestCheck_MapSpread_Accepts(t *testing.T) {
	cases := map[string]string{
		"a new key":            "  m = %{\"a\" => 1}\n  _a: Map<String, Int> = %{..m, \"b\" => 2}\n",
		"a key the base holds": "  m = %{\"a\" => 1}\n  _a: Map<String, Int> = %{..m, \"a\" => 2}\n",
		"maybe values":         "  m: Map<String, Maybe<Int>> = %{\"a\" => Some(1)}\n  _a: Map<String, Maybe<Int>> = %{..m, \"b\" => None}\n",
		"list values":          "  m = %{\"a\" => [1]}\n  _a: Map<String, List<Int>> = %{..m, \"b\" => []}\n",
		"an empty base":        "  _a: Map<String, Int> = %{..Map.empty(), \"a\" => 1}\n",
		"an unannotated empty": "  e = %{..Map.empty(), \"a\" => 1}\n  _a: Map<String, Int> = e\n",
		"nested in a struct":   "  c = Cfg{name: \"x\", prefs: %{\"theme\" => \"light\"}}\n  _a: Cfg = {..c, prefs: %{..c.prefs, \"theme\" => \"dark\"}}\n",
		"nested in a value":    "  m = %{\"o\" => %{\"k\" => 1}}\n  i = %{\"k\" => 2}\n  _a: Map<String, Map<String, Int>> = %{..m, \"o\" => %{..i, \"j\" => 3}}\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(mapSpreadDecls + "fn main() {\n" + body + "}\n")
			if len(errs) != 0 {
				t.Fatalf("errors = %v, want none", errs)
			}
		})
	}
}

func TestCheck_MapSpread_AcceptsAGenericBase(t *testing.T) {
	_, errs := checkSourceWithStdlib(`fn with_default<K, V>(m: Map<K, V>, k: K, v: V) -> Map<K, V> {
  %{..m, k => v}
}

fn main() {
  _a: Map<Int, String> = with_default(%{1 => "one"}, 2, "two")
}
`)
	if len(errs) != 0 {
		t.Fatalf("errors = %v, want none", errs)
	}
}

// Each refusal is one error, with its exact message and position. `{..m}`
// parses as the struct form; the base's type words the error for a map.
func TestCheck_MapSpread_Refuses(t *testing.T) {
	cases := map[string]struct {
		body      string
		want      string
		hint      string
		line, col int
	}{
		"no entries": {"  m = %{\"a\" => 1}\n  _a = %{..m}\n",
			"a map spread with no entries is its base value; write `m`", "", 10, 10},
		"a call base with no entries": {"  _a = %{..Map.empty()}\n",
			"a map spread with no entries is its base value; write the value itself", "", 9, 10},
		"a wrong key": {"  m = %{\"a\" => 1}\n  _a = %{..m, 1 => 2}\n",
			"map spread key type mismatch: expected String, got Int", "", 10, 15},
		"a wrong value": {"  m = %{\"a\" => 1}\n  _a = %{..m, \"b\" => \"s\"}\n",
			"map spread value type mismatch: expected Int, got String", "", 10, 22},
		"a struct base": {"  p = P{x: 1}\n  _a = %{..p, \"b\" => 2}\n",
			"map spread `..` requires a map, got P", "", 10, 10},
		"struct fields over a map": {"  m = %{\"a\" => 1}\n  _a = {..m, b: 2}\n",
			"struct spread `..` requires a struct, got Map<String, Int>", "a map update is written `%{..base, key => value}`", 10, 9},
		"a repeated key": {"  m = %{\"a\" => 1}\n  _a = %{..m, \"b\" => 1, \"b\" => 2}\n",
			"key `\"b\"` is given twice in this map literal; the later entry would replace the earlier", "", 10, 25},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(mapSpreadDecls + "fn main() {\n" + c.body + "}\n")
			if len(errs) != 1 {
				t.Fatalf("errors = %v, want exactly %q", errs, c.want)
			}
			e := errs[0]
			if e.Message != c.want || e.Line != c.line || e.Col != c.col {
				t.Errorf("error = %d:%d %q, want %d:%d %q", e.Line, e.Col, e.Message, c.line, c.col, c.want)
			}
			if got := hintsOf(e); got != c.hint {
				t.Errorf("hint = %q, want %q", got, c.hint)
			}
		})
	}
}

func hintsOf(e analysis.TypeError) string {
	if len(e.Hints) == 0 {
		return ""
	}
	return e.Hints[0]
}
