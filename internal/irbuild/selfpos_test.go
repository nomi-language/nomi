package irbuild

import (
	goast "go/ast"
	goparser "go/parser"
	gotoken "go/token"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// The self-position model, and the three shapes nothing else in this repository
// exercises.
//
// Population, unit = INTERFACE METHOD DECLARATIONS, over `std/` and
// `tests/`: `self@N>0` is EMPTY and `self@multi` is three stdlib declarations reached
// only through the stdlib function index with both operands statically typed.
// The corpus reaches neither, so a clean corpus run says nothing about
// either. A testdata fixture is the only guard available.

// --- shape 1: a self position that is not argument zero ----------------------

// --- shape 2: two self positions ---------------------------------------------

// --- shape 3: self only in the return, nested --------------------------------

// --- the model itself --------------------------------------------------------

func soleInterfaceMethod(t *testing.T, src string) *ast.InterfaceMethod {
	t.Helper()
	nodes, err := parser.Parse(lexer.Lex(src))
	if err != nil {
		t.Fatalf("parsing %q: %v", src, err)
	}
	for _, n := range nodes {
		id, ok := n.(*ast.InterfaceDef)
		if !ok || len(id.Methods) != 1 {
			continue
		}
		return &id.Methods[0]
	}
	t.Fatalf("%q declares no single-method interface", src)
	return nil
}

// `self` in type position is its own AST node, and every derivation in this
// repository tests for that node rather than for the NAME.
//
// Asserted rather than assumed because the analyzer's rule is WIDER: its
// ResolveTypeExpr maps a `*ast.SimpleType` named "self" to the same
// `typeParams["self"]` entry a `*ast.SelfType` resolves to, so
// isSelfPositionParam would answer true for a spelling selfShapeOf answers
// false for. If the parser could ever produce that node the two would disagree,
// and this is the measurement that says it cannot.
func TestSelfShape_SelfIsAlwaysItsOwnNode(t *testing.T) {
	for _, src := range []string{
		"interface I { fn m(x: self): Int }",
		"interface I { fn m(x: Maybe<self>): Int }",
		"interface I { fn m(x: Int): self }",
		"interface I { fn m(x: (self) -> Bool): Int }",
	} {
		im := soleInterfaceMethod(t, src)
		found := false
		var walk func(te ast.TypeExpr)
		walk = func(te ast.TypeExpr) {
			switch n := te.(type) {
			case nil:
			case *ast.SelfType:
				found = true
			case *ast.SimpleType:
				if n.Name == "self" {
					t.Fatalf("%q parsed `self` as a SimpleType; the analyzer would call it a self position and selfShapeOf would not", src)
				}
			case *ast.GenericType:
				for _, p := range n.Params {
					walk(p)
				}
			case *ast.FuncType:
				for _, p := range n.Params {
					walk(p)
				}
				walk(n.Return)
			}
		}
		for j := range im.Params {
			walk(im.Params[j].TypeAnnotation)
		}
		walk(im.ReturnTypeExpr)
		if !found {
			t.Fatalf("%q mentions self and no *ast.SelfType was parsed", src)
		}
	}
}

// typeExprHasSelf walks ast.TypeExpr EXHAUSTIVELY, and this is what keeps it
// exhaustive.
//
// A walk that skips a node kind looks finished from every angle except the one
// it skips, and the result can be a crash. So the guard is structural rather than a comment: it reads the set of types
// implementing `typeExpr()` out of internal/ast/ast.go and the set of case types out of
// typeExprHasSelf's own switch, and requires them to be equal. Adding an eighth
// TypeExpr fails HERE, at the walk that would silently stop covering it.
func TestSelfShape_TypeExprKindsAreCovered(t *testing.T) {
	implementors := typeExprImplementors(t)
	covered := typeExprHasSelfCases(t)
	if len(implementors) == 0 || len(covered) == 0 {
		t.Fatalf("the guard measured nothing: %d implementors, %d cases", len(implementors), len(covered))
	}
	if strings.Join(implementors, ",") != strings.Join(covered, ",") {
		t.Fatalf("typeExprHasSelf does not cover ast.TypeExpr exhaustively\n"+
			"  implements typeExpr(): %v\n"+
			"  handled by the walk : %v", implementors, covered)
	}
}

// typeExprImplementors is every ast type declaring the `typeExpr()` marker.
func typeExprImplementors(t *testing.T) []string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "ast", "ast.go"))
	if err != nil {
		t.Fatal(err)
	}
	f, err := goparser.ParseFile(gotoken.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, decl := range f.Decls {
		fn, isFn := decl.(*goast.FuncDecl)
		if !isFn || fn.Name.Name != "typeExpr" || fn.Recv == nil || len(fn.Recv.List) != 1 {
			continue
		}
		star, isStar := fn.Recv.List[0].Type.(*goast.StarExpr)
		if !isStar {
			continue
		}
		ident, isIdent := star.X.(*goast.Ident)
		if !isIdent {
			continue
		}
		out = append(out, ident.Name)
	}
	sort.Strings(out)
	return out
}

// typeExprHasSelfCases is every `*ast.X` case type in typeExprHasSelf's switch.
func typeExprHasSelfCases(t *testing.T) []string {
	t.Helper()
	path, err := filepath.Abs("selfpos.go")
	if err != nil {
		t.Fatal(err)
	}
	f, err := goparser.ParseFile(gotoken.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, decl := range f.Decls {
		fn, isFn := decl.(*goast.FuncDecl)
		if !isFn || fn.Name.Name != "typeExprHasSelf" {
			continue
		}
		goast.Inspect(fn, func(n goast.Node) bool {
			cc, isCase := n.(*goast.CaseClause)
			if !isCase {
				return true
			}
			for _, e := range cc.List {
				star, isStar := e.(*goast.StarExpr)
				if !isStar {
					continue
				}
				sel, isSel := star.X.(*goast.SelectorExpr)
				if !isSel {
					continue
				}
				pkg, isIdent := sel.X.(*goast.Ident)
				if isIdent && pkg.Name == "ast" {
					out = append(out, sel.Sel.Name)
				}
			}
			return true
		})
	}
	sort.Strings(out)
	return out
}
