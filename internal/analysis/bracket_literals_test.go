package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// literalTypes checks src and answers, in source order, the kind and type
// of the literal each binding whose name starts with `lit` is given.
func literalTypes(t *testing.T, src string) []string {
	t.Helper()
	fa, nodes, errs := checkSourceNodesWithStdlib(src + "\n")
	if len(errs) > 0 {
		var msgs []string
		for _, e := range errs {
			msgs = append(msgs, e.Error())
		}
		t.Fatalf("the checker rejects this:\n%s\n%s", src, strings.Join(msgs, "\n"))
	}
	var out []string
	for _, n := range nodes {
		ast.Inspect(n, func(n ast.Node) bool {
			b, ok := n.(*ast.Binding)
			if !ok || !strings.HasPrefix(b.Name, "lit") {
				return true
			}
			switch v := b.Value.(type) {
			case *ast.ListLit:
				out = append(out, b.Name+" "+v.Kind.String()+" "+analysis.ResolveTypeVar(fa.ExprTypes[v]).String())
			case *ast.MapLit:
				out = append(out, b.Name+" map "+analysis.ResolveTypeVar(fa.ExprTypes[v]).String())
			case *ast.StructLit:
				out = append(out, b.Name+" struct "+analysis.ResolveTypeVar(fa.ExprTypes[v]).String())
			}
			return true
		})
	}
	return out
}

// An unnamed bracket literal is the List, Vector or Set its expected type
// names, a distinct type over one included, which it builds; a List where
// nothing fixes it; and, with no collection expected, the collection of its
// first typed spread.
func TestBracketLiteral_KindFromExpectedType(t *testing.T) {
	src := `type Path Vector<Int>
type Tags Set<String>
type Ids List<Int>

fn main()
    v: Vector<Int> = [1, 2]
    s: Set<Int> = [1, 2]
    lit_list = [1, 2]
    lit_vector: Vector<Int> = [1, 2]
    lit_set: Set<Int> = [3]
    lit_path: Path = [1, 2]
    lit_tags: Tags = ["a"]
    lit_ids: Ids = [4]
    lit_empty_vector: Vector<String> = []
    lit_spread_vector = [..v, 3]
    lit_spread_set = [..s, 3]
    lit_spread_maybe = [..Some(1), 2]
    lit_named_vector = Vector[1, 2]
    lit_named_set = Set["a"]
    lit_named_path = Path[1]
    _ = (v, s, lit_list, lit_vector, lit_set, lit_path, lit_tags, lit_ids, lit_empty_vector)
    _ = (lit_spread_vector, lit_spread_set, lit_spread_maybe, lit_named_vector, lit_named_set, lit_named_path)
end`
	want := []string{
		"lit_list list List<Int>",
		"lit_vector vector Vector<Int>",
		"lit_set set Set<Int>",
		"lit_path vector Path",
		"lit_tags set Tags",
		"lit_ids list Ids",
		"lit_empty_vector vector Vector<String>",
		"lit_spread_vector vector Vector<Int>",
		"lit_spread_set set Set<Int>",
		"lit_spread_maybe list List<Int>",
		"lit_named_vector vector Vector<Int>",
		"lit_named_set set Set<String>",
		"lit_named_path vector Path",
	}
	if got := literalTypes(t, src); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// A brace literal is a map or a struct by its first entry; `{}` is the empty
// map, or the empty value of the map type expected; `Name{}` is what the
// name declares.
func TestBraceLiteral_MapOrStruct(t *testing.T) {
	src := `type Kvs Map<String, Int>

struct Point
    x: Int = 0
end

fn main()
    lit_map = {"a" => 1}
    lit_struct = {x: 1}
    lit_empty: Map<String, Int> = {}
    lit_kvs: Kvs = {"a" => 1}
    lit_empty_kvs: Kvs = {}
    lit_named_empty_kvs = Kvs{}
    lit_point = Point{}
    lit_std_map = Map{"b" => 2}
    _ = (lit_map, lit_struct, lit_empty, lit_kvs, lit_empty_kvs, lit_named_empty_kvs, lit_point, lit_std_map)
end`
	want := []string{
		"lit_map map Map<String, Int>",
		"lit_struct struct {x: Int}",
		"lit_empty map Map<String, Int>",
		"lit_kvs map Kvs",
		"lit_empty_kvs map Kvs",
		"lit_named_empty_kvs struct Kvs",
		"lit_point struct Point",
		"lit_std_map map Map<String, Int>",
	}
	if got := literalTypes(t, src); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// A bracket pattern takes the kind of its subject, and over a set its
// elements are constants: a name, `_` or another pattern there is an
// error, as is a `..` before the last element.
func TestBracketPattern_KindFromSubject(t *testing.T) {
	src := `fn f(xs: List<Int>, v: Vector<Int>, s: Set<String>) -> Int
    a = case xs
        [x, ..] -> x
        [] -> 0
    end
    b = case v
        [.., z] -> z
        [] -> 0
    end
    c = case s
        ["admin", ..rest] -> Set.size(rest)
        _ -> 0
    end
    a + b + c
end`
	_, nodes, errs := checkSourceNodesWithStdlib(src + "\n")
	if len(errs) > 0 {
		t.Fatalf("the checker rejects this: %v", errs)
	}
	var kinds []string
	for _, n := range nodes {
		ast.Inspect(n, func(n ast.Node) bool {
			if p, ok := n.(*ast.ListPattern); ok {
				kinds = append(kinds, p.Kind.String())
			}
			return true
		})
	}
	if got := strings.Join(kinds, " "); got != "list list vector vector set" {
		t.Errorf("pattern kinds = %q", got)
	}
	for src, want := range map[string]string{
		"fn f(s: Set<String>, who: String) -> Int\n    case s\n        [who, ..] -> 1\n        _ -> 0\n    end\nend": "an element of a set pattern is a constant the set contains, not a place to bind: `who` would bind a name",
		"fn f(s: Set<String>) -> Int\n    case s\n        [_, ..] -> 1\n        _ -> 0\n    end\nend":                "an element of a set pattern is a constant the set contains, not a place to bind: `_` names no value",
		"fn f(s: Set<Int>) -> Int\n    case s\n        [..rest, 1] -> 1\n        _ -> 0\n    end\nend":               "set pattern `..` must be the last element",
		"fn f(v: Vector<Int>) -> Int\n    case v\n        [..Some(x)] -> 1\n        _ -> 0\n    end\nend":            "the rest of a vector pattern is a name or nothing",
		"fn f(n: Int) -> Int\n    case n\n        [x] -> x\n        _ -> 0\n    end\nend":                            "list pattern requires a list, vector or set, got Int",
	} {
		_, errs := checkSourceWithStdlib(src + "\n")
		found := false
		for _, e := range errs {
			if strings.Contains(e.Message, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s:\nerrors %v, want one containing %q", src, errs, want)
		}
	}
}
