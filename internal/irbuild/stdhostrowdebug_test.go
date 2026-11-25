package irbuild

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/rt"
)

// hostRowValues binds `v` to a value of a std host type, by the type's
// name in stdHostSpecs, for TestStdHostTypeRowsRenderAsDebug: the lines a
// test body starts with, and the import they need.
var hostRowValues = map[string]struct{ imports, bind string }{
	"Byte":    {bind: "v: Byte = 200"},
	"Bytes":   {bind: "a: Byte = 97\n    b: Byte = 98\n    v = Bytes.from_list([a, b])"},
	"Context": {bind: "v = Context.root()"},
	"Decimal": {bind: "v = 1.50d"},
	"Dynamic": {imports: "import std::json.Json\n", bind: "v = Json.to_dynamic(Json.String(\"hello\"))"},
	"Regex":   {imports: "import std::regex.Regex\n", bind: "v = Regex`\\d+`"},
}

// TestStdHostTypeRowsRenderAsDebug: a value of every std host type with a
// hand-written `impl Debug` (stdHostSpecs, each row whose type std writes
// an `impl Debug` for in Nomi or as a host fn) reads in a failed
// assertion's `values:` row and in a subtest's row, at the top and in a
// tuple, as `Debug.inspect` renders it (spec §17 *Failure reports*): a
// Regex as the typed literal that builds it, a Decimal as `1.50d` and a
// Dynamic as the value it holds. A new host type with an
// `impl Debug` fails here until hostRowValues can build one.
func TestStdHostTypeRowsRenderAsDebug(t *testing.T) {
	written := map[string]bool{}
	for id, n := range stdMachineImpls(t) {
		if iface, _, _ := strings.Cut(id.Impl, "<"); iface != "Debug" || id.Name != "inspect" {
			continue
		}
		if _, isFunc := n.(*ast.FuncDef); isFunc && isSynthesized(n) {
			continue
		}
		for _, spec := range stdHostSpecs {
			if id.Owner == spec.nomi && (spec.origin == "" || spec.origin == id.Module) {
				written[spec.nomi] = true
			}
		}
	}
	var names []string
	for name := range written {
		if _, ok := hostRowValues[name]; !ok {
			t.Errorf("std host type %s has a hand-written impl Debug and hostRowValues builds no value of it", name)
			continue
		}
		names = append(names, name)
	}
	for name := range hostRowValues {
		if !written[name] {
			t.Errorf("hostRowValues names %s, which is no std host type with a hand-written impl Debug", name)
		}
	}
	sort.Strings(names)
	if len(names) < 4 {
		t.Fatalf("found %d std host types with a hand-written impl Debug (%v), which is too few to check anything", len(names), names)
	}

	// Each test fails on purpose: its assertion's values show `v`, and
	// its subtest's row holds `v` in a tuple beside its Debug text.
	var imports, tests strings.Builder
	for _, name := range names {
		val := hostRowValues[name]
		imports.WriteString(val.imports)
		fmt.Fprintf(&tests, "\ntest %q\n    %s\n    rows = [(v, Debug.inspect(v))]\n"+
			"    subtests rows, (x, text)\n        assert Debug.inspect(x) == text + \"!\"\n    end\nend\n",
			name, val.bind)
	}
	src := imports.String() + tests.String()
	obs := vmReference(writeTemp(t, src))
	if obs.exit != 1 || obs.stderr != "" {
		t.Fatalf("the program does not fail its %d tests as written: %s\n%s", len(names), obs, src)
	}

	// A case's report: its header, then a values block whose first row is
	// `x`, then `Debug.inspect(x)`.
	cases := strings.Split(obs.stdout, "FAIL ")[1:]
	if len(cases) != len(names) {
		t.Fatalf("%d tests failed, want %d:\n%s", len(cases), len(names), obs.stdout)
	}
	for i, c := range cases {
		name := names[i]
		lines := strings.Split(c, "\n")
		var header string
		values := map[string]string{}
		for j, line := range lines {
			line = strings.TrimSpace(line)
			if rest, ok := strings.CutPrefix(line, "subtest "); ok {
				header, _, _ = strings.Cut(rest, ": line ")
			}
			if rest, ok := strings.CutPrefix(line, "= "); ok && j > 0 {
				values[strings.TrimSpace(lines[j-1])] = rest
			}
		}
		lit := values["Debug.inspect(x)"]
		if lit == "" {
			t.Errorf("%s: the report shows no Debug.inspect(x) row:\n%s", name, c)
			continue
		}
		// lit is the Debug text as a String literal; the value's row is
		// that text unquoted, which RowText's literal of it gives back.
		row := values["x"]
		if rt.StringLiteral(row) != lit {
			t.Errorf("%s: its values row reads %s, and Debug.inspect renders %s", name, row, lit)
		}
		if want := "(" + row + ", " + lit + ")"; header != want {
			t.Errorf("%s: its subtest row reads %s, want %s", name, header, want)
		}
	}
	t.Logf("%d std host types with a hand-written impl Debug: %v", len(names), names)
}
