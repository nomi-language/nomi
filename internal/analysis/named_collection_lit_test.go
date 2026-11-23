package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// `List[...]` and `Map{...}` as literals are each an error at the name,
// spanning it, whose help shows the literal without the name and whose
// fix deletes the name. A literal too long for a line is told to drop the
// name instead.
func TestNamedCollectionLit_IsRefusedAtTheName(t *testing.T) {
	long := strings.Repeat(`"a long element of the list", `, 5)
	var longEntries string
	for _, k := range []string{"a", "b", "c", "d"} {
		longEntries += `"long key ` + k + ` of the map" => "and its value", `
	}
	for _, tc := range []struct{ name, head, kind, body, help string }{
		{"list elements", "List", "list", "_a = List[1, 2]", "write `[1, 2]`"},
		{"an empty list", "List", "list", "_a: List<Int> = List[]", "write `[]`"},
		{"a list spread", "List", "list", "xs = [1]\n  _a = List[..xs, 3]", "write `[..xs, 3]`"},
		{"a list nested in a named literal", "List", "list", "_a = Vector[List[1], [2]]", "write `[1]`"},
		{"a list argument", "List", "list", "_a = List.length(List[1, 2])", "write `[1, 2]`"},
		{"a list where a refinement is expected", "List", "list", "_a: NonEmptyList<Int> = List[1, 2]", "write `[1, 2]`"},
		{"a list too long for a line", "List", "list", "_a = List[" + long + `"z"]`, "write the literal without `List`"},
		{"map entries", "Map", "map", `_a = Map{"a" => 1, "b" => 2}`, "write `{\"a\" => 1, \"b\" => 2}`"},
		{"an empty map", "Map", "map", "_a: Map<String, Int> = Map{}", "write `{}`"},
		{"a map nested in a named literal", "Map", "map", `_a = Vector[Map{"a" => 1}, {"b" => 2}]`, "write `{\"a\" => 1}`"},
		{"a map argument", "Map", "map", `_a = Map.size(Map{"a" => 1})`, "write `{\"a\" => 1}`"},
		{"an empty map argument", "Map", "map", "_a: Map<String, Int> = Map.put(Map{}, \"a\", 1)", "write `{}`"},
		{"a map where a refinement is expected", "Map", "map", `_a: NonEmptyMap<String, Int> = Map{"a" => 1}`, "write `{\"a\" => 1}`"},
		{"a map too long for a line", "Map", "map", "_a = Map{" + longEntries + `"z" => "z"}`, "write the literal without `Map`"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := "fn main()\n  " + tc.body + "\nend\n"
			_, errs := checkSourceWithStdlib(src)
			if len(errs) == 0 {
				t.Fatalf("the checker admits this, and a %s literal has no named form", tc.kind)
			}
			if len(errs) != 1 {
				t.Fatalf("want one error, got %d: %v", len(errs), errs)
			}
			e := errs[0]
			if want := "a " + tc.kind + " literal has no name in front"; e.Message != want {
				t.Fatalf("message %q, want %q", e.Message, want)
			}
			if len(e.Hints) != 1 || e.Hints[0] != tc.help {
				t.Fatalf("help %q, want %q", e.Hints, tc.help)
			}
			if e.Code != analysis.NamedLitCode {
				t.Errorf("code %q, want %q", e.Code, analysis.NamedLitCode)
			}
			open := map[string]string{"List": "[", "Map": "{"}[tc.head]
			line, col := sourcePos(t, src, tc.head+open)
			end := col + len(tc.head)
			if e.Line != line || e.Col != col || e.EndLine != line || e.EndCol != end {
				t.Errorf("span %d:%d-%d:%d, want %d:%d-%d:%d", e.Line, e.Col, e.EndLine, e.EndCol, line, col, line, end)
			}
			title := "Remove `" + tc.head + "`"
			if e.Fix == nil || e.Fix.Title != title || len(e.Fix.Edits) != 1 {
				t.Fatalf("fix %+v, want one edit titled %s", e.Fix, title)
			}
			ed := e.Fix.Edits[0]
			if ed.Text != "" || ed.Start.Line != line || ed.Start.Col != col || ed.End.Line != line || ed.End.Col != end {
				t.Errorf("edit %+v, want `%s` deleted", ed, tc.head)
			}
		})
	}
}

// The literals that do name a collection, a type or a variant keep their
// names, and `List` and `Map` keep their other meanings: a type, and a
// module.
func TestNamedCollectionLit_OtherNamesAndMeaningsAreAdmitted(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"a vector", "fn main()\n  _a = Vector[1, 2]\nend\n"},
		{"a set", "fn main()\n  _a = Set[1, 2]\nend\n"},
		{"an empty set", "fn main()\n  _a: Set<Int> = Set[]\nend\n"},
		{"a vector with a spread", "fn main()\n  v = Vector[1]\n  _a = Vector[..v, 2]\nend\n"},
		{"a distinct type over a list", "type Ids List<Int>\n\nfn main()\n  _a = Ids[1, 2]\nend\n"},
		{"a variant with a list payload", "enum Bag\n  Items(List<Int>)\n  Empty\nend\n\nfn main()\n  _a = Bag.Items[1, 2]\nend\n"},
		{"the unnamed list", "fn main()\n  _a = [1, 2]\nend\n"},
		{"List as a type", "fn main()\n  _a: List<Int> = [1, 2]\nend\n"},
		{"List as a module", "fn main()\n  _a = List.length([1, 2])\nend\n"},
		{"a distinct type over a map", "type Kvs Map<String, Int>\n\nfn main()\n  _a = Kvs{\"a\" => 1}\nend\n"},
		{"an empty distinct type over a map", "type Kvs Map<String, Int>\n\nfn main()\n  _a = Kvs{}\nend\n"},
		{"a variant with a map payload", "enum Json\n  Obj(Map<String, Int>)\n  Null\nend\n\nfn main()\n  _a = Json.Obj{\"a\" => 1}\nend\n"},
		{"a struct", "struct Point\n  x: Int\nend\n\nfn main()\n  _a = Point{x: 1}\nend\n"},
		{"the unnamed map", "fn main()\n  _a = {\"a\" => 1}\nend\n"},
		{"the unnamed empty map", "fn main()\n  _a: Map<String, Int> = {}\nend\n"},
		{"an unnamed empty map argument", "fn main()\n  _a: Map<String, Int> = Map.put({}, \"a\", 1)\nend\n"},
		{"an unnamed map with a spread", "fn main()\n  m = {\"a\" => 1}\n  _a = {..m, \"b\" => 2}\nend\n"},
		{"Map as a type", "fn main()\n  _a: Map<String, Int> = {\"a\" => 1}\nend\n"},
		{"Map as a module", "fn main()\n  _a = Map.size({\"a\" => 1})\nend\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, errs := checkSourceWithStdlib(tc.src); len(errs) != 0 {
				t.Fatalf("the checker rejects this: %v", errs)
			}
		})
	}
}
