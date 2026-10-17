package irbuild

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/expectation"
)

// Types that refer to each other across files build as a type group
// (typecycle.go), and a program over them runs as the same program written in
// one file does.

// The declarations of the two-file cycle, without their imports, so the same
// text can be staged as two files and as one.
const (
	typeCycleParentDecls = `pub struct Parent {
    name: String
    kids: List<Child>
    first: Maybe<Child>
}

derive Equatable for Parent
derive ToJson for Parent

pub enum Shape {
    Empty
    One Child
    Pair {
        left: Child
        right: Child
    }
}

derive Equatable for Shape
derive ToJson for Shape

pub struct Holder<T> {
    value: T
    owner: Maybe<Child>
}

derive Equatable for Holder<T> where T: Equatable
derive ToJson for Holder<T> where T: ToJson

pub struct Head {
    lead: Child
    label: String
}

derive Equatable for Head
derive ToJson for Head
`
	typeCycleChildDecls = `pub struct Child {
    n: Int
    parent: Maybe<Parent>
    shape: Shape
    held: List<Holder<Int>>
    head: Maybe<Head>
}

derive Equatable for Child
derive ToJson for Child
`
	typeCycleMain = `fn describe(s: Shape) -> String {
    case s {
        Shape.Empty -> "empty"
        Shape.One(c) -> "one ${c.n}"
        Shape.Pair{left, right} -> "pair ${left.n} ${right.n}"
    }
}

fn main() {
    leaf = Child{n: 1, parent: None, shape: Shape.Empty, held: [], head: None}
    two = Child{n: 2, parent: None, shape: Shape.One(leaf), held: [Holder{value: 7, owner: None}], head: Some(Head{lead: leaf, label: "h"})}
    p = Parent{name: "p", kids: [leaf, two], first: Some(leaf)}
    c = Child{n: 3, parent: Some(p), shape: Shape.Pair{left: leaf, right: two}, held: [], head: None}
    io.print(describe(c.shape))
    io.print(describe(two.shape))
    io.print(describe(leaf.shape))
    io.inspect(two)
    io.print(Iter.count(p.kids))
    io.print(p == p)
    io.print(c == two)
    io.print(Maybe.map(two.head, fn h -> h.lead.n) |> Maybe.with_default(0))
    io.print("${ToJson.to_json(leaf)}")
}
`
	typeCycleWant = `pair 1 2
one 1
empty
Child{n: 2, parent: None, shape: One(Child{n: 1, parent: None, shape: Empty, held: [], head: None}), held: [Holder{value: 7, owner: None}], head: Some(Head{lead: Child{n: 1, parent: None, shape: Empty, held: [], head: None}, label: "h"})}
2
True
False
1
{"n":1,"parent":null,"shape":"Empty","held":[],"head":null}
`
)

// typeCycleRun stages files, runs the entry main.nomi on the VM and fails
// unless it prints want and exits 0.
func typeCycleRun(t *testing.T, files map[string]string, want string) {
	t.Helper()
	entry := stagedProgram(t, "main.nomi", files)
	if _, err := Analyze(entry); err != nil {
		t.Fatalf("the front end rejects this, so nothing below is tested: %v", err)
	}
	got := vmReference(entry)
	if got.stdout != want || got.stderr != "" || got.exit != 0 {
		t.Fatalf("VM run (exit %d):\n--- stdout ---\n%s--- stderr ---\n%s--- want ---\n%s",
			got.exit, got.stdout, got.stderr, want)
	}
}

// Two files whose types refer to each other through a List, a Maybe, a direct
// field (Head.lead), an enum payload (Shape) and a generic struct (Holder),
// with derived Equatable, ToJson and Debug over the cycle and a `case` over
// the enum, print what the same declarations print from one file.
func TestTypeCycle_TwoFilesRunAsOne(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	typeCycleRun(t, map[string]string{
		"main.nomi":   "import {\n    std/io\n    std/json.ToJson\n    child.Child\n    parent.{Head, Holder, Parent, Shape}\n}\n\n" + typeCycleMain,
		"parent.nomi": "import {\n    std/json.ToJson\n    child.Child\n}\n\n" + typeCycleParentDecls,
		"child.nomi":  "import {\n    std/json.ToJson\n    parent.{Head, Holder, Parent, Shape}\n}\n\n" + typeCycleChildDecls,
	}, typeCycleWant)
	typeCycleRun(t, map[string]string{
		"main.nomi": "import {\n    std/io\n    std/json.ToJson\n}\n\n" + typeCycleParentDecls + "\n" + typeCycleChildDecls + "\n" + typeCycleMain,
	}, typeCycleWant)
}

// Three files on one cycle (A holds a B, a B holds Cs, a C holds an A and a
// Step whose payload is an A), with a `case` over the enum and derived
// equality across all three.
func TestTypeCycle_ThreeFiles(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	typeCycleRun(t, map[string]string{
		"main.nomi": `import {
    std/io
    a.A
    b.B
    c.{C, Step}
}

fn walk(s: Step) -> Int {
    case s {
        Step.Stop -> 0
        Step.Next(a) -> 1 + (Maybe.map(a.b, fn b -> Iter.count(b.cs)) |> Maybe.with_default(0))
    }
}

fn main() {
    inner = A{name: "inner", b: None}
    c = C{a: inner, step: Step.Next(inner)}
    outer = A{name: "outer", b: Some(B{cs: [c, c]})}
    io.inspect(outer)
    io.print(walk(Step.Next(outer)))
    io.print(walk(c.step))
    io.print(outer == outer)
    io.print(inner == outer)
}
`,
		"a.nomi": "import b.B\n\npub struct A {\n    name: String\n    b: Maybe<B>\n}\n\nderive Equatable for A\n",
		"b.nomi": "import c.C\n\npub struct B {\n    cs: List<C>\n}\n\nderive Equatable for B\n",
		"c.nomi": "import a.A\n\npub struct C {\n    a: A\n    step: Step\n}\n\nderive Equatable for C\n\n" +
			"pub enum Step {\n    Stop\n    Next A\n}\n\nderive Equatable for Step\n",
	}, `A{name: "outer", b: Some(B{cs: [C{a: A{name: "inner", b: None}, step: Next(A{name: "inner", b: None})}, C{a: A{name: "inner", b: None}, step: Next(A{name: "inner", b: None})}]})}
3
1
True
False
`)
}

// An interface in the cycle: a struct field of an interface another member
// declares, whose signature names the struct back.
func TestTypeCycle_InterfaceField(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	typeCycleRun(t, map[string]string{
		"main.nomi": "import {\n    std/io\n    a.A\n    b.{B, Scorer}\n}\n\n" +
			"fn main() {\n    b = B{owner: None, bonus: 10}\n    x = A{n: 5, s: b, b: Some(b)}\n    io.print(Scorer.score(x.s, x))\n}\n",
		"a.nomi": "import b.{B, Scorer}\n\npub struct A {\n    n: Int\n    s: Scorer\n    b: Maybe<B>\n}\n",
		"b.nomi": "import a.A\n\npub interface Scorer {\n    fn score(value: self, a: A) -> Int\n}\n\n" +
			"pub struct B {\n    owner: Maybe<A>\n    bonus: Int\n}\n\n" +
			"impl Scorer for B {\n    fn score(value: B, a: A) -> Int {\n        value.bonus + a.n\n    }\n}\n",
	}, "15\n")
}

// A declaration the builder refuses (an enum past the tag's 255 variants)
// refuses every type on the cycle that holds it, in both files, and leaves a
// type off the cycle alone: the run stops at the literal of A, not at Fine's.
func TestTypeCycle_ARefusalReachesTheWholeCycle(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	var variants strings.Builder
	for i := range 256 {
		fmt.Fprintf(&variants, "    V%d\n", i)
	}
	entry := stagedProgram(t, "main.nomi", map[string]string{
		"main.nomi": "import {\n    std/io\n    a.{A, Fine}\n}\n\n" +
			"fn main() {\n    io.print(Fine{n: 2}.n)\n    x = A{n: 1, bs: []}\n    io.print(x.n)\n}\n",
		"a.nomi": "import b.B\n\npub struct A {\n    n: Int\n    bs: List<B>\n}\n\npub struct Fine {\n    n: Int\n}\n",
		"b.nomi": "import a.A\n\npub enum Big {\n" + variants.String() + "}\n\n" +
			"pub struct B {\n    big: Maybe<Big>\n    owner: Maybe<A>\n}\n",
	})
	if _, err := Analyze(entry); err != nil {
		t.Fatalf("the front end rejects this, so nothing below is tested: %v", err)
	}
	got := vmReference(entry)
	if _, gap := expectation.VMGap(got.stderr); !gap || !strings.Contains(got.stderr, "main.nomi:8:") {
		t.Fatalf("want the run refused at A's literal (main.nomi:8), got exit %d:\n%s%s",
			got.exit, got.stdout, got.stderr)
	}
}
