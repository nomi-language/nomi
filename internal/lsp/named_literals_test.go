package lsp

import "testing"

// Inside a collection literal, the cursor is where a value goes: an element
// of `[` or `Vector[`, a key or a value of `{`, named or not. The names in scope are
// offered there, and no struct field is.
func TestCompletion_InsideNamedCollectionLiteralsOffersValues(t *testing.T) {
	const decls = "struct Point\n    x: Int\n    y: Int\nend\n\n"
	for _, src := range []string{
		"fn f(count: Int) -> Vector<Int>\n    [1, co‸]\nend\n",
		"fn f(count: Int) -> Set<Int>\n    [co‸]\nend\n",
		"fn f(count: Int) -> Vector<Int>\n    Vector[1, co‸]\nend\n",
		"fn f(count: Int) -> Map<Int, Int>\n    {co‸}\nend\n",
		"fn f(count: Int) -> Map<Int, Int>\n    {1 => co‸}\nend\n",
		"fn f(count: Int, m: Map<Int, Int>) -> Map<Int, Int>\n    {..m, co‸}\nend\n",
	} {
		t.Run(src, func(t *testing.T) {
			items := complete(t, decls+src)
			labelsInclude(t, items, "count")
			labelsExclude(t, items, "x", "y")
		})
	}
}

// The name written in front of a literal is a reference to the type it
// names, so hover, go-to-definition and semantic tokens read it as a type.
func TestHover_NamedShapeLiteralHeads(t *testing.T) {
	const decls = "type Path Vector<Int>\ntype Tags Set<String>\ntype Kvs Map<String, Int>\ntype Pair (Int, Int)\nstruct Point\n    x: Int\nend\n\n"
	for _, tc := range []struct{ src, want string }{
		{decls + "fn f() -> Path\n    ▮Path[1]\nend\n", "Path"},
		{decls + "fn f() -> Tags\n    ▮Tags[\"a\"]\nend\n", "Tags"},
		{decls + "fn f() -> Kvs\n    ▮Kvs{\"a\" => 1}\nend\n", "Kvs"},
		{decls + "fn f() -> Pair\n    ▮Pair(1, 2)\nend\n", "Pair"},
		{decls + "fn f() -> Point\n    ▮Point{x: 1}\nend\n", "Point"},
	} {

		input, pos := hoverMarkerPosition(t, tc.src)
		s := NewServer()
		s.docs.Open("file:///heads.nomi", input)
		sym := s.docs.Get("file:///heads.nomi").Analysis.SymbolAt(pos)
		if sym == nil || sym.Name != tc.want {
			t.Errorf("%q: symbol at the head = %v, want %s", tc.src, sym, tc.want)
		}
	}
}

// A `{` where a struct is expected offers its fields, as `Point{` does.
func TestCompletion_HashBraceOffersTheExpectedStructsFields(t *testing.T) {
	const decls = "struct Point\n    x: Int\n    y: Int\nend\n\n"
	items := complete(t, decls+"fn f() -> Point\n    {x: 1, ‸}\nend\n")
	labelsInclude(t, items, "y")
	labelsExclude(t, items, "x")
}
