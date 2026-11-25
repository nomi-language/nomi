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
		{"distinct over an option of itself", "type M Option<M>\n", 1, "type 'M' is defined in terms of itself"},
		{"distinct over a generic struct of itself", "struct Box<T>\n    v: T\nend\n\ntype B Box<B>\n", 5, "type 'B' is defined in terms of itself"},
		{"distinct over a tuple holding itself", "type P (Int, P)\n", 1, "type 'P' is defined in terms of itself"},
		{"distinct over a function of itself", "type F (F) -> Int\n", 1, "type 'F' is defined in terms of itself"},
		{"distinct over an anonymous struct holding itself", "type R (Int, {next: Option<R>})\n", 1, "type 'R' is defined in terms of itself"},
		{"distinct over an interface of itself", "interface Source<T>\n    fn pull(s: self) -> Option<T>\nend\n\ntype S Source<S>\n", 5, "type 'S' is defined in terms of itself"},
		{"distinct over a conjunction holding itself", "interface Source<T>\n    fn pull(s: self) -> Option<T>\nend\n\ninterface Named\n    fn name(s: self) -> String\nend\n\ntype S Source<S> and Named\n", 9, "type 'S' is defined in terms of itself"},
		{"distinct through a typealias", "type A B\n\ntypealias B List<A>\n", 1, "type 'A' is defined in terms of itself"},
		// A use with type arguments is a copy of the declaration's type, so
		// the cycle is found by the declaration, not the pointer.
		{"generic distinct over another instance of itself", "type A<T> A<Int>\n", 1, "type 'A' is defined in terms of itself"},
		{"generic distinct over itself", "type A<T> A<T>\n", 1, "type 'A' is defined in terms of itself"},
		{"generic distinct over an instance of an instance of itself", "type A<T> A<A<T>>\n", 1, "type 'A' is defined in terms of itself"},
		{"generic distinct over an option of another instance of itself", "type A<T> Option<A<Int>>\n", 1, "type 'A' is defined in terms of itself"},
		{"generic distinct over a generic struct of itself", "struct Box<T>\n    v: T\nend\n\ntype B<T> Box<B<T>>\n", 5, "type 'B' is defined in terms of itself"},
		{"two distinct types through type arguments", "type A B<Int>\ntype B<T> A\n", 2, "type 'B' is defined in terms of itself (B → A → B)"},
		{"two distinct types through type arguments, the other order", "type B<T> A\ntype A B<Int>\n", 2, "type 'A' is defined in terms of itself (A → B → A)"},
		{"two generic distinct types", "type A<T> B<T>\ntype B<T> A<T>\n", 2, "type 'B' is defined in terms of itself (B → A → B)"},
		{"generic validated type over another instance of itself", "type V<T> V<Int> when fn _n -> True\n", 1, "type 'V' is defined in terms of itself"},
		{"generic subtype over itself", "subtype P<T> P<T> when fn _n -> True\n", 1, "type 'P' is defined in terms of itself"},
		{"generic subtype whose further parent is itself", "subtype NonNeg Int when fn n -> n >= 0\nsubtype P<T> NonNeg and P<T> when fn n -> n > 0\n", 2, "type 'P' is defined in terms of itself"},
		{"distinct inside a function body with type arguments", "fn main()\n    type A<T> A<Int>\nend\n", 2, "type 'A' is defined in terms of itself"},
		{"two distinct types inside a function body through type arguments", "fn main()\n    type A B<Int>\n    type B<T> A\nend\n", 3, "type 'B' is defined in terms of itself (B → A → B)"},
		{"typealias over itself with type arguments", "typealias A A<Int>\n", 1, "typealias 'A' refers to itself"},
		{"generic typealias over itself", "typealias A<T> A<T>\n", 1, "typealias 'A' refers to itself"},
		{"typealias of itself", "typealias X X\n", 1, "typealias 'X' refers to itself"},
		{"two typealiases", "typealias X Y\n\ntypealias Y X\n", 1, "typealias 'X' refers to itself (X → Y → X)"},
		{"typealias over a list of itself", "typealias X List<X>\n", 1, "typealias 'X' refers to itself"},
		{"bound typealias of itself", "typealias S S and Display\n", 1, "typealias 'S' refers to itself"},
		{"distinct inside a function body", "fn main()\n    type E E\nend\n", 2, "type 'E' is defined in terms of itself"},
		{"typealias inside a function body", "fn main()\n    typealias X List<X>\nend\n", 2, "typealias 'X' refers to itself"},
		// No value of these can be built without one already (valueCycle).
		{"struct holding itself", "struct S\n    s: S\nend\n", 1, "struct 'S' is defined in terms of itself (S.s → S)"},
		{"struct holding a distinct type over it", "struct W\n    b: B\nend\n\ntype B W\n", 5, "type 'B' is defined in terms of itself (B → W.b → B)"},
		{"distinct type over a struct holding it", "type B W\n\nstruct W\n    b: B\nend\n", 3, "struct 'W' is defined in terms of itself (W.b → B → W)"},
		{"two structs", "struct A\n    b: B\nend\n\nstruct B\n    a: A\nend\n", 5, "struct 'B' is defined in terms of itself (B.a → A.b → B)"},
		{"struct holding itself in a tuple", "struct T\n    t: (Int, T)\nend\n", 1, "struct 'T' is defined in terms of itself (T.t → T)"},
		{"struct holding itself in an anonymous struct", "struct T\n    t: {x: T}\nend\n", 1, "struct 'T' is defined in terms of itself (T.t → T)"},
		{"generic struct holding itself", "struct S<T>\n    s: S<T>\nend\n", 1, "struct 'S' is defined in terms of itself (S.s → S)"},
		{"generic struct holding another instance of itself", "struct S<T>\n    v: T\n    s: S<Int>\nend\n", 1, "struct 'S' is defined in terms of itself (S.s → S)"},
		{"struct holding itself through a type parameter", "struct Box<T>\n    v: T\nend\n\nstruct S\n    b: Box<S>\nend\n", 5, "struct 'S' is defined in terms of itself (S.b → Box.v → S)"},
		{"struct holding itself in both variants of a result", "struct S\n    r: Result<S, S>\nend\n", 1, "struct 'S' is defined in terms of itself (S.r → Result.Ok → S)"},
		{"enum every variant of which holds itself", "enum E\n    A(E)\n    B(E, Int)\nend\n", 1, "enum 'E' is defined in terms of itself (E.A → E)"},
		{"enum whose struct variant holds itself", "enum E\n    A {e: E}\nend\n", 1, "enum 'E' is defined in terms of itself (E.A → E)"},
		{"struct embedded in an enum it holds", "enum E\n    embeds S\nend\n\nstruct S\n    e: E\nend\n", 5, "struct 'S' is defined in terms of itself (S.e → E.S → S)"},
		{"struct inside a function body", "fn main()\n    struct L\n        l: L\n    end\nend\n", 2, "struct 'L' is defined in terms of itself (L.l → L)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src
			if !strings.Contains(src, "fn main") {
				src += "\nfn main() end\n"
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
// generic or not, and a distinct type may wrap one, or an instance of
// another generic distinct type. A typealias may name one declared after
// it.
func TestTypeCycle_LegalRecursionAndOrderStayLegal(t *testing.T) {
	src := `struct Node
    next: Option<Node>
    kids: List<Node>
end

struct Chain<T>
    v: T
    next: Option<Chain<T>>
end

enum Tree
    Leaf
    Branch(Tree, Tree)
end

enum Expr
    Lit
    Neg(Wrapped)
end

type Wrapped Expr

struct W
    b: Option<B>
end

type B W

type C Option<W>

type Forest List<Tree>

type Ids<T> List<T>

type Nested<T> Option<Ids<T>>

struct Holds
    vec: Vector<Holds>
    set: Set<Int>
    map: Map<String, Holds>
    make: (Int) -> Holds
    result: Result<Holds, String>
    tree: Tree
    pair: (Int, Option<Holds>)
end

enum LeafLast
    Branch(LeafLast, LeafLast)
    Leaf
end

struct Defaulted
    again: Option<Defaulted> = None
end

struct Param<T>
    v: T
end

struct Uses
    p: Param<Int>
end

enum Rec
    Cons {head: Int, tail: Rec}
    Nil {head: Int = 0}
end

type A Later

typealias Later Earlier

typealias Earlier Int

fn main()
    _n = Node{next: None, kids: []}
    _ch = Chain{v: 1, next: Some(Chain{v: 2, next: None})}
    _t = Tree.Branch(Tree.Leaf, Tree.Leaf)
    _e = Expr.Neg(Wrapped(Expr.Lit))
    w = W{b: None}
    _b = B(w)
    _c = C(Some(w))
    _f = Forest([])
    _i = Nested(Some(Ids([1, 2])))
    A(n) = A(3)
    _m: Later = n + 1
    _h = Holds{vec: [], set: [], map: {}, make: fn _ -> todo, result: Err("none"), tree: Tree.Leaf, pair: (1, None)}
    _l = LeafLast.Branch(LeafLast.Leaf, LeafLast.Leaf)
    _d = Defaulted{}
    _u = Uses{p: Param{v: 1}}
    _r = Rec.Cons{head: 1, tail: Rec.Nil{}}
    struct Local
        next: Option<Local>
    end
    _lo = Local{next: None}
end
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
