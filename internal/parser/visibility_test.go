package parser

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
)

// visibilityOf is the Visibility a parsed declaration carries.
func visibilityOf(t *testing.T, n ast.Node) ast.Visibility {
	t.Helper()
	switch d := n.(type) {
	case *ast.FuncDef:
		return d.Visibility
	case *ast.StructDef:
		return d.Visibility
	case *ast.EnumDef:
		return d.Visibility
	case *ast.TypeDef:
		return d.Visibility
	case *ast.InterfaceDef:
		return d.Visibility
	case *ast.TypeAlias:
		return d.Visibility
	case *ast.OnceBinding:
		return d.Visibility
	case *ast.ExternFunc:
		return d.Visibility
	case *ast.ExternType:
		return d.Visibility
	case *ast.KeywordDecl:
		return d.Visibility
	case *ast.EmbedDecl:
		return d.Visibility
	}
	t.Fatalf("%T carries no visibility", n)
	return ast.Internal
}

// Each declaration that takes `pub` takes `private`, and with neither is
// internal: one test per placement, each read back from the node.
func TestVisibility_EveryPlacementTakesEachLevel(t *testing.T) {
	decls := []struct{ name, src string }{
		{"fn", "fn f() -> Int\n    1\nend\n"},
		{"struct", "struct P\n    x: Int\nend\n"},
		{"opaque struct", "opaque struct P\n    x: Int\nend\n"},
		{"enum", "enum E\n    A\nend\n"},
		{"type", "type Meters Int\n"},
		{"opaque type", "opaque type Meters Int\n"},
		{"refine", "refine Small Int when fn n -> n < 10\n"},
		{"interface", "interface Shape\n    fn area(s: self) -> Int\nend\n"},
		{"typealias", "typealias Count Int\n"},
		{"once", "once limit: Int = 3\n"},
		{"host fn", "host fn now() -> Int\n"},
		{"host type", "host type Handle\n"},
		{"phrase", "phrase greeting `Hello`\n"},
		{"embed", "embed assets `x.txt`\n"},
	}
	for _, level := range ast.Visibilities {
		for _, d := range decls {
			src := d.src
			if kw := level.Keyword(); kw != "" {
				src = kw + " " + src
			}
			t.Run(level.String()+" "+d.name, func(t *testing.T) {
				nodes := parse(t, src)
				if len(nodes) != 1 {
					t.Fatalf("Parse(%q): %d nodes, want 1", src, len(nodes))
				}
				if got := visibilityOf(t, nodes[0]); got != level {
					t.Errorf("Parse(%q): visibility %v, want %v", src, got, level)
				}
			})
		}
	}
}

// A function or `once` of an inherent impl block takes each level too.
func TestVisibility_InherentImplItemsTakeEachLevel(t *testing.T) {
	src := "pub struct P\n    x: Int\nend\n\nimpl P\n    pub fn a() -> Int\n        1\n    end\n\n" +
		"    fn b() -> Int\n        2\n    end\n\n    private fn c() -> Int\n        3\n    end\n\n" +
		"    private once d: Int = 4\n\n    private host fn e() -> Int\nend\n"
	nodes := parse(t, src)
	block, ok := nodes[1].(*ast.ImplBlock)
	if !ok {
		t.Fatalf("got %T, want the impl block", nodes[1])
	}
	want := []ast.Visibility{ast.Public, ast.Internal, ast.Private, ast.Private, ast.Private}
	if len(block.Items) != len(want) {
		t.Fatalf("%d items, want %d", len(block.Items), len(want))
	}
	for i, item := range block.Items {
		if got := visibilityOf(t, item); got != want[i] {
			t.Errorf("item %d: visibility %v, want %v", i, got, want[i])
		}
	}
}

// `private` is a modifier only where a declaration follows it: elsewhere it
// is an ordinary name, a binding, a field, a parameter or a call.
func TestVisibility_PrivateIsANameOutsideTheModifierPosition(t *testing.T) {
	src := "struct Flags\n    private: Bool\nend\n\nfn private_of(private: Int) -> Int\n    private\nend\n\n" +
		"fn main()\n    private = 2\n    _ = Flags{private: True}\n    _ = private_of(private)\nend\n"
	nodes := parse(t, src)
	if len(nodes) != 3 {
		t.Fatalf("%d nodes, want 3", len(nodes))
	}
	for _, n := range nodes {
		if v := visibilityOf(t, n); v != ast.Internal {
			t.Errorf("%T has visibility %v, want internal", n, v)
		}
	}
}

// Each misplaced modifier is refused at the modifier, in the wording the
// `pub` errors use, and the parser never accepts it.
func TestVisibility_MisplacedModifiersAreRefused(t *testing.T) {
	const both = "a declaration has one visibility: write `pub` or `private`, not both"
	cases := []struct {
		name, src, want string
		line, col       int
	}{
		{"pub private", "pub private fn f() end\n", both, 1, 1},
		{"private pub", "private pub fn f() end\n", both, 1, 1},
		{"private private", "private private fn f() end\n", both, 1, 1},
		{"pub private in an impl", "struct P\n    x: Int\nend\nimpl P\n    pub private fn f() end\nend\n", both, 5, 9},
		{"private import", "private import std::io\n", "an import takes no `private`: it binds its names in this file only; write `import`", 1, 1},
		{"private export", "private export fn f() end\n", "an exported function takes no `private`: write `export fn`", 1, 1},
		{"private impl", "struct P\n    x: Int\nend\nprivate impl P\nend\n", "'private' is not valid before `impl`", 4, 1},
		{"private test", "private test \"t\"\nend\n", "'private' is not valid before `test`", 1, 1},
		{"private in a body", "fn main()\n    private fn g() end\nend\n", "`private` applies to top-level declarations only", 2, 5},
		{"private opaque fn", "private opaque fn f() end\n", "'private opaque' must be followed by 'struct', 'enum', or 'type'", 1, 1},
		{"interface method", "interface S\n    private fn f(s: self) -> Int\nend\n",
			"'private' is not allowed on interface methods — a method's visibility is the interface's; drop 'private' (it becomes a default), or use 'host fn' for a host-backed default", 2, 5},
		{"interface impl function", "interface S\n    fn f(s: self) -> Int\nend\nstruct P\n    x: Int\nend\nimpl S for P\n    private fn f(p: P) -> Int\n        p.x\n    end\nend\n",
			"methods inside an `impl Iface for Type` block do not take `private` — visibility is defined by the interface", 8, 5},
		{"struct body", "struct P\n    x: Int\n    private fn f() end\nend\n", "functions are declarations outside type bodies", 3, 5},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, errs := ParseWithRecovery(lexer.Lex(c.src))
			if len(errs) == 0 {
				t.Fatalf("the parser accepts %q", c.src)
			}
			e := errs[0]
			if e.Message != c.want || e.Line != c.line || e.Col != c.col {
				t.Errorf("got %d:%d %q, want %d:%d %q", e.Line, e.Col, e.Message, c.line, c.col, c.want)
			}
		})
	}
}

// A doc comment above a `private` declaration is its doc, as above a `pub`
// one: the modifier starts the declaration the comment documents.
func TestVisibility_DocCommentAttachesThroughPrivate(t *testing.T) {
	nodes := parse(t, "/// Adds one.\nprivate fn inc(n: Int) -> Int\n    n + 1\nend\n")
	fd, ok := nodes[0].(*ast.FuncDef)
	if !ok || fd.Doc != "Adds one." {
		t.Fatalf("got %#v, want the function with its doc", nodes[0])
	}
}
