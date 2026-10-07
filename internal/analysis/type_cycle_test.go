package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// A type defined in terms of itself is an error at its declaration. Before
// this, a distinct type's inner type held a pointer back to the type, and
// the first walk over it (collectTypeParamsByName, for the constructor)
// overflowed the stack: `type e e` killed `nomi check` and the language
// server, which cannot recover a Go stack overflow.
func TestTypeCycle_RejectedAtTheDeclaration(t *testing.T) {
	cases := []struct {
		name string
		src  string
		line int
		want string
	}{
		{"distinct over itself", "type E E\n", 1, "type 'E' is defined in terms of itself"},
		{"two distinct types", "type A B\ntype B A\n", 2, "type 'B' is defined in terms of itself (B → A → B)"},
		{"distinct over a list of itself", "type L List<L>\n", 1, "type 'L' is defined in terms of itself"},
		{"distinct over a maybe of itself", "type M Maybe<M>\n", 1, "type 'M' is defined in terms of itself"},
		{"distinct over a generic struct of itself", "struct Box<T> {\n    v: T\n}\n\ntype B Box<B>\n", 5, "type 'B' is defined in terms of itself"},
		{"distinct over a tuple holding itself", "type P (Int, P)\n", 1, "type 'P' is defined in terms of itself"},
		{"distinct over a function of itself", "type F (F) -> Int\n", 1, "type 'F' is defined in terms of itself"},
		{"distinct over an anonymous struct holding itself", "type R (Int, {next: Maybe<R>})\n", 1, "type 'R' is defined in terms of itself"},
		{"distinct through a typealias", "type A B\n\ntypealias B List<A>\n", 1, "type 'A' is defined in terms of itself"},
		{"typealias of itself", "typealias X X\n", 1, "typealias 'X' refers to itself"},
		{"two typealiases", "typealias X Y\n\ntypealias Y X\n", 1, "typealias 'X' refers to itself (X → Y → X)"},
		{"typealias over a list of itself", "typealias X List<X>\n", 1, "typealias 'X' refers to itself"},
		{"bound typealias of itself", "typealias S S and Display\n", 1, "typealias 'S' refers to itself"},
		{"distinct inside a function body", "fn main() {\n    type E E\n}\n", 2, "type 'E' is defined in terms of itself"},
		{"typealias inside a function body", "fn main() {\n    typealias X List<X>\n}\n", 2, "typealias 'X' refers to itself"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src
			if !strings.Contains(src, "fn main") {
				src += "\nfn main() {\n}\n"
			}
			errs := diagnosticsFor(t, src)
			for _, e := range errs {
				if e.Message == tc.want {
					if e.Line != tc.line {
						t.Fatalf("%q is at line %d, want the declaration at line %d", e.Message, e.Line, tc.line)
					}
					return
				}
			}
			t.Fatalf("no %q among:\n%s", tc.want, cycleMessages(errs))
		})
	}
}

// Recursion through a struct or an enum is how a recursive type is written,
// and a distinct type may wrap one. A typealias may name one declared after
// it.
func TestTypeCycle_LegalRecursionAndOrderStayLegal(t *testing.T) {
	src := `struct Node {
    next: Maybe<Node>
    kids: List<Node>
}

enum Tree {
    Leaf
    Branch(Tree, Tree)
}

struct W {
    b: Maybe<B>
}

type B W

type C Maybe<W>

type Forest List<Tree>

type A Later

typealias Later Earlier

typealias Earlier Int

fn main() {
    _n = Node{next: None, kids: []}
    _t = Tree.Branch(Tree.Leaf, Tree.Leaf)
    w = W{b: None}
    _b = B(w)
    _c = C(Some(w))
    _f = Forest([])
    A(n) = A(3)
    _m: Later = n + 1
}
`
	if errs := diagnosticsFor(t, src); len(errs) > 0 {
		t.Fatalf("legal recursive types are rejected:\n%s", cycleMessages(errs))
	}
}

func cycleMessages(errs []analysis.TypeError) string {
	var b strings.Builder
	for _, e := range errs {
		b.WriteString(e.Error())
		b.WriteString("\n")
	}
	return b.String()
}
