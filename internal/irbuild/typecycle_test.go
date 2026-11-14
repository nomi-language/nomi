package irbuild

import (
	"fmt"
	"maps"
	"slices"
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
	typeCycleParentDecls = `pub struct Parent
    name: String
    kids: List<Child>
    first: Maybe<Child>
end

derive Equatable for Parent
derive ToJson for Parent

pub enum Shape
    Empty
    One Child
    Pair {
        left: Child
        right: Child
    }
end

derive Equatable for Shape
derive ToJson for Shape

pub struct Holder<T>
    value: T
    owner: Maybe<Child>
end

derive Equatable for Holder<T> where T: Equatable
derive ToJson for Holder<T> where T: ToJson

pub struct Head
    lead: Child
    label: String
end

derive Equatable for Head
derive ToJson for Head
`
	typeCycleChildDecls = `pub struct Child
    n: Int
    parent: Maybe<Parent>
    shape: Shape
    held: List<Holder<Int>>
    head: Maybe<Head>
end

derive Equatable for Child
derive ToJson for Child
`
	typeCycleMain = `fn describe(s: Shape) -> String
    case s
        Shape.Empty -> "empty"
        Shape.One(c) -> "one ${c.n}"
        Shape.Pair{left:, right:} -> "pair ${left.n} ${right.n}"
    end
end

fn main()
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
end
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
		"main.nomi":   "import\n    std::io\n    std::json.ToJson\n    child.Child\n    parent.{Head, Holder, Parent, Shape}\nend\n\n" + typeCycleMain,
		"parent.nomi": "import\n    std::json.ToJson\n    child.Child\nend\n\n" + typeCycleParentDecls,
		"child.nomi":  "import\n    std::json.ToJson\n    parent.{Head, Holder, Parent, Shape}\nend\n\n" + typeCycleChildDecls,
	}, typeCycleWant)
	typeCycleRun(t, map[string]string{
		"main.nomi": "import\n    std::io\n    std::json.ToJson\nend\n\n" + typeCycleParentDecls + "\n" + typeCycleChildDecls + "\n" + typeCycleMain,
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
		"main.nomi": `import
    std::io
    a.A
    b.B
    c.{C, Step}
end

fn walk(s: Step) -> Int
    case s
        Step.Stop -> 0
        Step.Next(a) -> 1 + (Maybe.map(a.b, fn b -> Iter.count(b.cs)) |> Maybe.with_default(0))
    end
end

fn main()
    inner = A{name: "inner", b: None}
    c = C{a: inner, step: Step.Next(inner)}
    outer = A{name: "outer", b: Some(B{cs: [c, c]})}
    io.inspect(outer)
    io.print(walk(Step.Next(outer)))
    io.print(walk(c.step))
    io.print(outer == outer)
    io.print(inner == outer)
end
`,
		"a.nomi": "import b.B\n\npub struct A\n    name: String\n    b: Maybe<B>\nend\n\nderive Equatable for A\n",
		"b.nomi": "import c.C\n\npub struct B\n    cs: List<C>\nend\n\nderive Equatable for B\n",
		"c.nomi": "import a.A\n\npub struct C\n    a: A\n    step: Step\nend\n\nderive Equatable for C\n\n" +
			"pub enum Step\n    Stop\n    Next A\nend\n\nderive Equatable for Step\n",
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
		"main.nomi": "import\n    std::io\n    a.A\n    b.{B, Scorer}\nend\n\n" +
			"fn main()\n    b = B{owner: None, bonus: 10}\n    x = A{n: 5, s: b, b: Some(b)}\n    io.print(Scorer.score(x.s, x))\nend\n",
		"a.nomi": "import b.{B, Scorer}\n\npub struct A\n    n: Int\n    s: Scorer\n    b: Maybe<B>\nend\n",
		"b.nomi": "import a.A\n\npub interface Scorer\n    fn score(value: self, a: A) -> Int\nend\n\n" +
			"pub struct B\n    owner: Maybe<A>\n    bonus: Int\nend\n\n" +
			"impl Scorer for B\n    fn score(value: B, a: A) -> Int\n        value.bonus + a.n\n    end\nend\n",
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
		"main.nomi": "import\n    std::io\n    a.{A, Fine}\nend\n\n" +
			"fn main()\n    io.print(Fine{n: 2}.n)\n    x = A{n: 1, bs: []}\n    io.print(x.n)\nend\n",
		"a.nomi": "import b.B\n\npub struct A\n    n: Int\n    bs: List<B>\nend\n\npub struct Fine\n    n: Int\nend\n",
		"b.nomi": "import a.A\n\npub enum Big\n" + variants.String() + "end\n\n" +
			"pub struct B\n    big: Maybe<Big>\n    owner: Maybe<A>\nend\n",
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

// typeCycleFile is one file of a cross-file cycle: the names it imports from
// each other file, and its declarations.
type typeCycleFile struct {
	imports map[string]string // file -> "A" or "{A, B}"
	decls   string
}

// typeCycleShapes are cycles of enums, structs and a generic enum across
// files, each with a main that builds, prints and matches over them.
var typeCycleShapes = []struct {
	name  string
	files map[string]typeCycleFile
	main  typeCycleFile
	want  string
}{
	{
		// The shape a Lox interpreter has: a value enum whose payload is a
		// declaration struct holding a list of expressions whose payload is
		// a value.
		name: "enum through struct through enum",
		files: map[string]typeCycleFile{
			"ast": {imports: map[string]string{"value": "Value"},
				decls: "pub enum Expr\n    Literal Value\nend\n\npub struct Decl\n    body: List<Expr>\nend\n"},
			"value": {imports: map[string]string{"ast": "Decl"},
				decls: "pub enum Value\n    Nil\n    Function Decl\nend\n"},
		},
		main: typeCycleFile{imports: map[string]string{"ast": "{Decl, Expr}", "value": "Value"},
			decls: "fn describe(v: Value) -> String\n    case v\n        Value.Nil -> \"nil\"\n        Value.Function(d) -> \"fn of ${Iter.count(d.body)}\"\n    end\nend\n\n" +
				"fn main()\n    io.inspect(Value.Nil)\n    f = Value.Function(Decl{body: [Expr.Literal(Value.Nil)]})\n    io.inspect(f)\n    io.print(describe(Value.Nil))\n    io.print(describe(f))\nend\n"},
		want: "Nil\nFunction(Decl{body: [Literal(Nil)]})\nnil\nfn of 1\n",
	},
	{
		name: "enum and enum",
		files: map[string]typeCycleFile{
			"tree": {imports: map[string]string{"forest": "Forest"},
				decls: "pub enum Tree\n    Leaf\n    Node Forest\nend\n"},
			"forest": {imports: map[string]string{"tree": "Tree"},
				decls: "pub enum Forest\n    Bare\n    Grove Tree\nend\n"},
		},
		main: typeCycleFile{imports: map[string]string{"tree": "Tree", "forest": "Forest"},
			decls: "fn depth(t: Tree) -> Int\n    case t\n        Tree.Leaf -> 0\n        Tree.Node(Forest.Bare) -> 1\n        Tree.Node(Forest.Grove(inner)) -> 1 + depth(inner)\n    end\nend\n\n" +
				"fn main()\n    t = Tree.Node(Forest.Grove(Tree.Node(Forest.Bare)))\n    io.inspect(t)\n    io.print(depth(t))\n    io.print(depth(Tree.Leaf))\nend\n"},
		want: "Node(Grove(Node(Bare)))\n2\n0\n",
	},
	{
		name: "enum to struct to enum over three files",
		files: map[string]typeCycleFile{
			"a": {imports: map[string]string{"b": "Holder"},
				decls: "pub enum Item\n    Empty\n    Held Holder\nend\n"},
			"b": {imports: map[string]string{"c": "Tag"},
				decls: "pub struct Holder\n    tags: List<Tag>\nend\n"},
			"c": {imports: map[string]string{"a": "Item"},
				decls: "pub enum Tag\n    Plain\n    Wraps Item\nend\n"},
		},
		main: typeCycleFile{imports: map[string]string{"a": "Item", "b": "Holder", "c": "Tag"},
			decls: "fn count(i: Item) -> Int\n    case i\n        Item.Empty -> 0\n        Item.Held(h) -> Iter.count(h.tags)\n    end\nend\n\n" +
				"fn main()\n    i = Item.Held(Holder{tags: [Tag.Plain, Tag.Wraps(Item.Empty)]})\n    io.inspect(i)\n    io.print(count(i))\n    io.print(count(Item.Empty))\nend\n"},
		want: "Held(Holder{tags: [Plain, Wraps(Empty)]})\n2\n0\n",
	},
	{
		name: "generic enum through struct",
		files: map[string]typeCycleFile{
			"tagged": {imports: map[string]string{"wrap": "Wrap"},
				decls: "pub enum Tagged<T>\n    Plain T\n    Nested Wrap\nend\n"},
			"wrap": {imports: map[string]string{"tagged": "Tagged"},
				decls: "pub struct Wrap\n    inner: List<Tagged<Int>>\nend\n"},
		},
		main: typeCycleFile{imports: map[string]string{"tagged": "Tagged", "wrap": "Wrap"},
			decls: "fn total(t: Tagged<Int>) -> Int\n    case t\n        Tagged.Plain(n) -> n\n        Tagged.Nested(w) -> w.inner |> Iter.map(total) |> Iter.sum()\n    end\nend\n\n" +
				"fn main()\n    t: Tagged<Int> = Tagged.Nested(Wrap{inner: [Tagged.Plain(2), Tagged.Nested(Wrap{inner: [Tagged.Plain(3)]})]})\n    io.inspect(t)\n    io.print(total(t))\n    s: Tagged<String> = Tagged.Plain(\"x\")\n    io.inspect(s)\nend\n"},
		want: "Nested(Wrap{inner: [Plain(2), Nested(Wrap{inner: [Plain(3)]})]})\n5\nPlain(\"x\")\n",
	},
}

// typeCycleSource is f's text with its imports spelled through prefix
// (`cyc::` for the module's own name, "" for the bare file name), and std/io
// imported when io is set.
func typeCycleSource(f typeCycleFile, prefix string, io bool) string {
	var lines []string
	if io {
		lines = append(lines, "std::io")
	}
	for file, names := range f.imports {
		lines = append(lines, prefix+file+"."+names)
	}
	slices.Sort(lines)
	if len(lines) == 0 {
		return f.decls
	}
	return "import\n    " + strings.Join(lines, "\n    ") + "\nend\n\n" + f.decls
}

// Every cycle shape runs as one file, as files importing each other by file
// name, and as files importing each other through their module's own name
// (`import cyc::value.Value` in module `cyc`), and prints the same.
func TestTypeCycle_ShapesRunAsOneFile(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	for _, shape := range typeCycleShapes {
		t.Run(shape.name, func(t *testing.T) {
			names := slices.Sorted(maps.Keys(shape.files))
			var one strings.Builder
			for _, name := range names {
				one.WriteString(shape.files[name].decls + "\n")
			}
			one.WriteString(shape.main.decls)
			t.Run("one file", func(t *testing.T) {
				typeCycleRun(t, map[string]string{
					"main.nomi": typeCycleSource(typeCycleFile{decls: one.String()}, "", true),
				}, shape.want)
			})
			for _, spelling := range []struct{ name, prefix, toml string }{
				{"file names", "", ""},
				{"the module's own name", "cyc::", "[module]\nname = \"cyc\"\nentry_points = [\"main\"]\n"},
			} {
				t.Run(spelling.name, func(t *testing.T) {
					files := map[string]string{"main.nomi": typeCycleSource(shape.main, spelling.prefix, true)}
					for _, name := range names {
						files[name+".nomi"] = typeCycleSource(shape.files[name], spelling.prefix, false)
					}
					if spelling.toml != "" {
						files["nomi.toml"] = spelling.toml
					}
					typeCycleRun(t, files, shape.want)
				})
			}
		})
	}
}
