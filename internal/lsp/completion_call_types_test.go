package lsp

import (
	"path/filepath"
	"testing"
)

// genericDecls declares generic functions and types whose results member
// completion reads while the statement being typed does not parse.
const genericDecls = `struct Point {
    x: Int
    y: Int
}

struct Box<T> {
    value: T
}

fn first<T>(xs: List<T>): T {
    case List.head(xs) {
        Some(x) -> x
        None -> first(xs)
    }
}

fn pick<T>(a: T, b: T): T {
    a
}

fn to_point(n: Int): Point {
    Point{x: n, y: n}
}
`

// A generic call's result type is solved from its arguments while the
// statement does not parse, so `value.` after it offers the instantiated
// type's fields.
func TestCompletion_MemberOfGenericCall(t *testing.T) {
	tests := []struct {
		name, body string
		want, not  []string
	}{
		{
			"file function",
			"fn f(points: List<Point>): Int {\n    first(points)." + cursorMark + "\n}\n",
			[]string{"x", "y"}, nil,
		},
		{
			"binding mid-block, with a later broken line",
			"fn f(points: List<Point>): Int {\n    n = first(points)." + cursorMark + "\n    if\n    1\n}\n",
			[]string{"x", "y"}, nil,
		},
		{
			"nested generic calls",
			"fn f(nested: List<List<Point>>): Int {\n    first(first(nested))." + cursorMark + "\n}\n",
			[]string{"x", "y"}, nil,
		},
		{
			"generic struct field through the call's instantiation",
			"fn f(boxes: List<Box<Point>>): Int {\n    first(boxes).value." + cursorMark + "\n}\n",
			[]string{"x", "y"}, []string{"value"},
		},
		{
			"generic struct field through a binding's type",
			"fn f(box: Box<Point>): Int {\n    box.value." + cursorMark + "\n}\n",
			[]string{"x", "y"}, []string{"value"},
		},
		{
			"owner generic function nested in one",
			"fn f(points: List<Point>, p: Point): Int {\n    Maybe.with_default(List.head(points), p)." + cursorMark + "\n}\n",
			[]string{"x", "y"}, nil,
		},
		{
			"map value through Map.get",
			"fn f(m: Map<String, Point>, p: Point): Int {\n    Maybe.with_default(Map.get(m, \"a\"), p)." + cursorMark + "\n}\n",
			[]string{"x", "y"}, nil,
		},
		{
			"Result.map with a named function",
			"fn f(r: Result<Int, String>, p: Point): Int {\n    Result.with_default(Result.map(r, to_point), p)." + cursorMark + "\n}\n",
			[]string{"x", "y"}, nil,
		},
		{
			"pipe solved stage by stage",
			"fn f(ns: List<Int>): Int {\n    (Iter.map(ns, to_point) |> Iter.to_list() |> first())." + cursorMark + "\n}\n",
			[]string{"x", "y"}, nil,
		},
		{
			"pipe with an argument after the piped value",
			"fn f(r: Result<Int, String>, p: Point): Int {\n    (Result.map(r, to_point) |> Result.with_default(p))." + cursorMark + "\n}\n",
			[]string{"x", "y"}, nil,
		},
		{
			"a Maybe value has no fields",
			"fn f(points: List<Point>): Int {\n    List.head(points)." + cursorMark + "\n}\n",
			nil, []string{"x", "y", "map", "with_default"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			items := complete(t, genericDecls+tt.body)
			labelsInclude(t, items, tt.want...)
			labelsExclude(t, items, tt.not...)
		})
	}
}

// When the arguments do not solve the result's type parameters, member
// completion offers nothing rather than a guess.
func TestCompletion_MemberOfGenericCallInferenceFails(t *testing.T) {
	tests := []struct{ name, body string }{
		{
			// The lambda's parameter type needs inference, which the
			// unparsed statement does not get, so Iter.map's U stays open.
			"lambda argument",
			"fn f(points: List<Point>): Int {\n    first(Iter.map(points, |p| p) |> Iter.to_list())." + cursorMark + "\n}\n",
		},
		{
			"argument that does not unify",
			"fn f(): Int {\n    first(5)." + cursorMark + "\n}\n",
		},
		{
			"arguments that disagree",
			"fn f(p: Point): Int {\n    pick(p, 5)." + cursorMark + "\n}\n",
		},
		{
			"missing argument",
			"fn f(): Int {\n    first()." + cursorMark + "\n}\n",
		},
		{
			// `xs |> first().x` calls first with no arguments: `.` binds
			// tighter than `|>`.
			"member of a pipe stage's call",
			"fn f(points: List<Point>): Int {\n    points |> first()." + cursorMark + "\n}\n",
		},
		{
			"the caller's own type parameter",
			"fn f<T>(xs: List<T>): Int {\n    first(xs)." + cursorMark + "\n}\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			items := complete(t, genericDecls+tt.body)
			if len(items) != 0 {
				t.Errorf("want nothing, got %v", itemLabels(items))
			}
		})
	}
}

// A generic call at a pipe's head gives the pipe its instantiated type, so
// the next stage is offered the functions that take it.
func TestCompletion_PipeAfterGenericCall(t *testing.T) {
	items := complete(t, genericDecls+"fn f(ns: List<Int>): Int {\n    Iter.map(ns, to_point) |> Iter.to_list() |> "+cursorMark+"\n}\n")
	labelsInclude(t, items, "first", "List.head", "Iter.map")
	labelsExclude(t, items, "to_point")

	items = complete(t, genericDecls+"fn f(m: Map<String, Point>): Int {\n    Map.get(m, \"a\") |> "+cursorMark+"\n}\n")
	labelsInclude(t, items, "Maybe.with_default", "Maybe.map")
	labelsExclude(t, items, "first", "List.head")
}

// A generic function from another file, file-qualified or imported by name.
func TestCompletion_MemberOfImportedGenericCall(t *testing.T) {
	dir := writeProject(t, map[string]string{
		"geo.nomi":  "pub struct Point {\n    x: Int\n    y: Int\n}\n",
		"util.nomi": "pub fn first<T>(xs: List<T>, fallback: T): T {\n    List.head(xs) |> Maybe.with_default(fallback)\n}\n",
		"main.nomi": "fn main() {\n}\n",
	})
	s := NewServer()
	uri := "file://" + filepath.Join(dir, "main.nomi")

	items := completeIn(t, s, uri, "import geo.Point\nimport util\n\nfn f(points: List<Point>, p: Point): Int {\n    util.first(points, p)."+cursorMark+"\n}\n").Items
	labelsInclude(t, items, "x", "y")

	items = completeIn(t, s, uri, "import geo.Point\nimport util.first\n\nfn f(points: List<Point>, p: Point): Int {\n    first(points, fallback: p)."+cursorMark+"\n}\n").Items
	labelsInclude(t, items, "x", "y")
}
