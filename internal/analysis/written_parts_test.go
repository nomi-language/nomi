package analysis

import (
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

const writtenPartsSrc = `typealias Pair (Int, Int)

enum Shape<T>
    Seg (Int, Int)
    Al Pair
    Gen T
    Mixed (T, String)
    Bare
end

type Coord (Int, Int)
type Aliased Pair
type Id Int

fn local() -> Int
    enum Tree
        Leaf
        Node (Tree, Int, Tree)
    end
    type Span (Int, Int)
    case Tree.Node(Tree.Leaf, 1, Tree.Leaf)
        Tree.Node(_, n, _) -> n
        Tree.Leaf -> Span(0, 0).first
    end
end
`

// writtenPartsDecls is every enum and distinct declaration in nodes, at any
// depth, by name.
func writtenPartsDecls(nodes []ast.Node) map[string]ast.Node {
	out := map[string]ast.Node{}
	for _, top := range nodes {
		ast.Inspect(top, func(n ast.Node) bool {
			switch d := n.(type) {
			case *ast.EnumDef:
				out[d.Name] = d
			case *ast.TypeDef:
				out[d.Name] = d
			}
			return true
		})
	}
	return out
}

// TestWrittenAsParts_TheCheckerRecordsTheDeclaration: a payload or an inner
// type written as a tuple of its parts is recorded as such on the type the
// checker built for the declaration, in a body too. One written as one type
// is not, a typealias of a tuple and a type parameter among them.
func TestWrittenAsParts_TheCheckerRecordsTheDeclaration(t *testing.T) {
	fa, nodes := checkNodes(t, writtenPartsSrc)
	decls := writtenPartsDecls(nodes)
	variants := map[string]map[string]bool{
		"Shape": {"Seg": true, "Al": false, "Gen": false, "Mixed": true, "Bare": false},
		"Tree":  {"Leaf": false, "Node": true},
	}
	for enum, want := range variants {
		et, ok := fa.DeclaredType(decls[enum]).(*EnumType)
		if !ok {
			t.Fatalf("enum %s has no type the checker built: %v", enum, fa.DeclaredType(decls[enum]))
		}
		for name, parts := range want {
			vd := findVariant(et, name)
			if vd == nil {
				t.Fatalf("%s has no variant %s", enum, name)
			}
			if vd.WrittenAsParts != parts {
				t.Errorf("%s.%s: WrittenAsParts = %v, want %v", enum, name, vd.WrittenAsParts, parts)
			}
		}
	}
	for name, parts := range map[string]bool{"Coord": true, "Aliased": false, "Id": false, "Span": true} {
		dt, ok := fa.DeclaredType(decls[name]).(*DistinctType)
		if !ok {
			t.Fatalf("type %s has no type the checker built: %v", name, fa.DeclaredType(decls[name]))
		}
		if dt.WrittenAsParts != parts {
			t.Errorf("type %s: WrittenAsParts = %v, want %v", name, dt.WrittenAsParts, parts)
		}
	}
}

// TestWrittenAsParts_TheGateHoldsTheDeclaredType: a declaration whose type
// the checker did not build is reported, so the builder never reads a
// payload's form from a type that is not there.
func TestWrittenAsParts_TheGateHoldsTheDeclaredType(t *testing.T) {
	fa, nodes := checkNodes(t, writtenPartsSrc)
	if got := VerifyElaboration(fa, nodes); len(got) > 0 {
		t.Fatalf("want nothing reported, got %v", got)
	}
	decls := writtenPartsDecls(nodes)
	for _, name := range []string{"Shape", "Coord", "Tree", "Span"} {
		decl := decls[name]
		line, col := declPos(decl)
		sym := fa.Definitions[Pos{Line: line, Col: col}]
		if sym == nil {
			t.Fatalf("%s has no definition", name)
		}
		saved := sym.Type
		sym.Type = nil
		got := VerifyElaboration(fa, nodes)
		sym.Type = saved
		if !hasElabCategory(got, decl, ElabDeclaredTypeUnrecorded) {
			t.Errorf("%s with no declared type is not reported: %v", name, got)
		}
	}
}
