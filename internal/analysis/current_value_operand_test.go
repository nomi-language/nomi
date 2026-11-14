package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// A `..` written as an operand in a field's or key's new value is the value
// already there (current_value.go). Each case marks with ‸ where its one
// diagnostic is reported and gives its whole text. A case fails when the
// source is admitted.
func TestCurrentValueOperand_Diagnostics(t *testing.T) {
	const outside = "`..` means the value already at a field or key of an update, and this `..` is not in one\n" +
		"help: write `..` in a field's new value, as in `{..user, visits: .. + 1}`, or name the value here"
	const missingInt = "a missing key leaves `..` no Int to read\n" +
		"help: the map may lack the key, and only a collection has a value to start from there, the empty one; to give a missing key a value, use `Map.update(m, key, default, f)`"
	const keeps = "a field or key whose value is `..` keeps the value it holds; leave it out"
	cases := []struct{ name, body, want string }{
		{"a binding", "  _a = ‸.. + 1", outside},
		{"a call argument", "  _a = String.trim(‸..)", outside},
		{"a binding after an update", "  _a = {..ada(), name: .. + \"!\"}\n  _b = ‸.. + 1", outside},
		{"a named struct literal's field", `  _a = Address{street: ‸.., city: "x"}`, outside},
		{"a plain struct literal's field", `  _a = {street: ‸.., city: "x"}`, outside},
		{"a patch passed to another function's Partial<T>", `  _a = take({name: ‸.. + "!"})`, outside},
		{"a map entry before every spread", "  m = {\"a\" => 1}\n  _a = {\"b\" => ‸.., ..m}", outside},
		{"an Int at a key the map may lack", "  m = {\"a\" => 1}\n  _a = {..m, \"a\" => ‸.. + 1}", missingInt},
		{"an Int under a key the map may lack", "  m = {\"a\" => {\"b\" => 1}}\n  _a = {..m, \"a\" => {.., \"b\" => ‸.. + 1}}", missingInt},
		{"a field whose value is `..`", `  _a = {..ada(), name: ‸..}`, keeps},
		{"a whole value at the field is the field's type", `  _a = {..ada(), address: {street: ‸.., city: "x"}}`,
			"field 'street' of Address: expected String, got Address"},
		{"the wrong type", `  _a = {..ada(), name: .. ‸+ 1}`, "binary + type mismatch: String vs Int"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := currentValueDecls + "fn main()\n" + tc.body + "\nend\n"
			line, col := markerPos(t, src)
			src = strings.Replace(src, "‸", "", 1)
			_, errs := checkSourceWithStdlib(src)
			if len(errs) == 0 {
				t.Fatalf("the front end admits this")
			}
			for _, e := range errs {
				if e.Line == line && e.Col == col && diagText(e) == tc.want {
					if len(errs) != 1 {
						t.Fatalf("want one diagnostic, got %d: %s", len(errs), allText(errs))
					}
					return
				}
			}
			t.Fatalf("want at %d:%d\n  %s\ngot\n  %s", line, col, tc.want, allText(errs))
		})
	}
}

// The operand forms the rules admit.
func TestCurrentValueOperand_Admitted(t *testing.T) {
	for name, body := range map[string]string{
		"arithmetic and a call":            `  _a = {..ada(), name: String.trim(..) + "!"}`,
		"nested updates":                   `  _a = {..ada(), address: {.., street: String.trim(..), city: .. + "!"}}`,
		"a pipe":                           `  _a = {..ada(), tags: .. |> Iter.filter(fn t -> t != "x") |> Iter.to_list()}`,
		"a lambda":                         `  _a = {..ada(), name: Some("x") |> Maybe.map(fn s -> s + ..) |> Maybe.with_default("")}`,
		"Struct.update":                    `  _a = Struct.update(ada(), {name: .. + "!", address: {.., city: .. + "!"}})`,
		"a list at a key the map may lack": "  m = {\"a\" => [1]}\n  _a = {..m, \"a\" => .. + [2], \"b\" => [0, ..]}",
		"a vector":                         `  _a = {..ada(), recent: Vector.push(.., 1)}`,
		"an if":                            `  _a = {..ada(), name: if .. == "" then "?" else ..}`,
		"a line end":                       "  _a = {\n    ..ada(),\n    name: String.trim(\n      ..\n    ),\n  }",
	} {
		t.Run(name, func(t *testing.T) {
			src := currentValueDecls + "fn main()\n" + body + "\nend\n"
			if _, errs := checkSourceWithStdlib(src); len(errs) != 0 {
				t.Fatalf("the front end rejects this: %s", allText(errs))
			}
		})
	}
}

// Each `..` operand has the type of the field or key it belongs to: the
// innermost one whose value holds it.
func TestCurrentValueOperand_Types(t *testing.T) {
	src := currentValueDecls + "fn main()\n" +
		"  _a = {..ada(), address: {.., street: String.trim(..)}, tags: .. + [\"x\"], name: Some(\"x\") |> Maybe.map(fn s -> s + ..) |> Maybe.with_default(\"\")}\n" +
		"end\n"
	fa, nodes, errs := checkSourceNodesWithStdlib(src)
	if len(errs) != 0 {
		t.Fatalf("the front end rejects this: %s", allText(errs))
	}
	var got []string
	for _, n := range nodes {
		ast.Inspect(n, func(n ast.Node) bool {
			if cv, ok := n.(*ast.CurrentValue); ok {
				ty := fa.ExprTypes[cv]
				if ty == nil {
					got = append(got, "nil")
				} else {
					got = append(got, analysis.ResolveTypeVar(ty).String())
				}
			}
			return true
		})
	}
	want := "String Address List<String> String"
	if strings.Join(got, " ") != want {
		t.Errorf("types = %q, want %q", strings.Join(got, " "), want)
	}
}

// A `..` in a field's or key's value belongs to its update whatever the
// types, so an update that cannot be checked reports only what stopped it,
// never that its `..` is outside an update. Each case gives the one error
// it reports.
func TestCurrentValueOperand_InAnUpdateThatFails(t *testing.T) {
	cases := []struct{ name, head, body, want string }{
		{"a base of an unknown type", "",
			"fn helper(r: Missing) -> Person\n  {..r, name: Some(..) |> Maybe.with_default(\"\")}\nend\n",
			`unknown type "Missing"`},
		{"a base from a module that is not there", "import std::no_such_module\n\n",
			"fn helper(r: no_such_module.Req) -> Person\n  {..r, name: String.trim(..)}\nend\n",
			`unknown type "no_such_module.Req"`},
		{"an undefined base", "",
			"fn helper() -> Person\n  {..nobody, name: String.trim(..), address: {.., city: .. + \"!\"}}\nend\n",
			"undefined variable 'nobody'"},
		{"a field the struct lacks", "",
			"fn helper() -> Person\n  {..ada(), title: String.trim(..)}\nend\n",
			"Person has no field 'title'"},
		{"a map base of an unknown type", "",
			"fn helper(m: Missing) -> Int\n  _a = {..m, \"a\" => .. + 1}\n  1\nend\n",
			`unknown type "Missing"`},
		{"Struct.update of an undefined value", "",
			"fn helper() -> Person\n  Struct.update(nobody, {name: .. + \"!\"})\nend\n",
			"undefined variable 'nobody'"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.head + currentValueDecls + tc.body + "fn main()\n  _a = 1\nend\n"
			_, errs := checkSourceWithStdlib(src)
			if len(errs) == 0 {
				t.Fatalf("the front end admits this")
			}
			for _, e := range errs {
				if strings.Contains(e.Message, "is not in one") {
					t.Fatalf("a `..` in a failed update is reported outside one:\n%s", allText(errs))
				}
			}
			if len(errs) != 1 || !strings.Contains(errs[0].Message, tc.want) {
				t.Fatalf("want the one error %q, got\n%s", tc.want, allText(errs))
			}
		})
	}
}
