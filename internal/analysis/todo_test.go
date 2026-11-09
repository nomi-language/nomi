package analysis_test

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// TestTodo_FitsEveryTypedPosition pins that `todo` checks in every position
// that expects a type, generic ones included, with and without a reason.
func TestTodo_FitsEveryTypedPosition(t *testing.T) {
	src := `struct Header {
    name: String
    size: Int
}

enum Shape {
    Circle(Float)
    Square(Float)
}

fn parse(text: String) -> Header {
    todo "parse the header"
}

fn area(s: Shape) -> Float {
    case s {
        .Square(_) -> todo
        .Circle(r) -> r * r
    }
}

fn first<T>(xs: List<T>) -> T {
    todo
}

fn pick<T>(xs: List<T>, d: T) -> T {
    case List.head(xs) {
        Some(x) -> x
        None -> todo ` + "`raw reason`" + `
    }
}

fn make() -> Header {
    Header{name: "x", size: todo """
        a multi-line reason
        """}
}

fn twice(n: Int) -> Int {
    n * 2
}

fn uses(flag: Bool) -> Int {
    n: Int = todo
    _ = todo
    f = fn x: Int -> if x > 1 { todo } else { x }
    g: (Int) -> String = fn _ -> todo
    m: Int = 3 |> todo
    _ = g
    if flag {
        todo
    }
    twice(todo) + f(n) + m
}
`
	_, errs := checkSourceWithStdlib(src)
	if len(errs) > 0 {
		t.Fatalf("todo positions rejected: %v", errs)
	}
}

// TestTodo_AnUnannotatedBindingAsksForAType pins the one position with no
// expected type: a binding with no annotation, for `todo` and for a pipeline
// ending in it. A discard needs no type.
func TestTodo_AnUnannotatedBindingAsksForAType(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		line, col int
	}{
		{"bare", "fn f() -> Int {\n    x = todo\n    x\n}\n", 2, 5},
		{"pipe stage", "fn f() -> Int {\n    x = 3 |> todo\n    x\n}\n", 2, 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(tc.src)
			got := errorsAt(errs, tc.line, tc.col)
			want := "cannot infer a type for 'x' from `todo`: annotate the binding (`x: T = todo`)"
			if len(got) != 1 || got[0] != want {
				t.Fatalf("errors at %d:%d = %q, want exactly [%q]; all: %v", tc.line, tc.col, got, want, errs)
			}
		})
	}
	if _, errs := checkSourceWithStdlib("fn f() -> Int {\n    _ = todo\n    1\n}\n"); len(errs) > 0 {
		t.Fatalf("`_ = todo` rejected: %v", errs)
	}
}

// TestTodo_CaseTypeComesFromTheFirstArmWithAValue pins that a diverging first
// arm does not decide a `case`'s type: the later arms are still checked
// against the arm that produces a value.
func TestTodo_CaseTypeComesFromTheFirstArmWithAValue(t *testing.T) {
	src := "fn f(n: Int) -> Int {\n    case n {\n        0 -> todo\n        1 -> 1\n        _ -> \"two\"\n    }\n}\n"
	_, errs := checkSourceWithStdlib(src)
	found := false
	for _, e := range errs {
		found = found || strings.Contains(e.Message, "case branch type mismatch: expected Int, got String")
	}
	if !found {
		t.Fatalf("a String arm after a todo arm and an Int arm was accepted: %v", errs)
	}
}

// TestTodo_UnfinishedBodiesDoNotReportUnreadParameters pins that a function
// or lambda whose body holds a `todo` is not written yet, so its parameters
// are not reported as never read; a finished body's still are.
func TestTodo_UnfinishedBodiesDoNotReportUnreadParameters(t *testing.T) {
	unread := func(src string) []string {
		nodes, perrs := parser.ParseWithRecovery(lexer.Lex(src))
		if len(perrs) > 0 {
			t.Fatalf("parse: %v", perrs)
		}
		var out []string
		for _, e := range analysis.CheckUnusedBindings(analysis.BuildFile(nodes)) {
			out = append(out, e.Message)
		}
		return out
	}
	if got := unread("fn parse(text: String, n: Int) -> Int {\n    todo\n}\n\nfn g() -> (Int) -> Int {\n    fn x -> todo\n}\n"); len(got) > 0 {
		t.Fatalf("an unfinished body reports unread parameters: %q", got)
	}
	got := unread("fn parse(text: String) -> Int {\n    1\n}\n")
	if len(got) != 1 || !strings.HasPrefix(got[0], "parameter 'text' is never read") {
		t.Fatalf("a finished body's unread parameter = %q", got)
	}
}

// TestTodos_ListsEveryTodoInSourceOrder pins the walk `nomi build` and the
// language server share.
func TestTodos_ListsEveryTodoInSourceOrder(t *testing.T) {
	src := "fn a() -> Int {\n    todo \"one\"\n}\n\nfn b(f: Bool) -> Int {\n    if f { todo } else { [1] |> Iter.map(fn x -> todo \"two\") |> Iter.count() }\n}\n"
	nodes, perrs := parser.ParseWithRecovery(lexer.Lex(src))
	if len(perrs) > 0 {
		t.Fatalf("parse: %v", perrs)
	}
	var got []string
	for _, td := range analysis.Todos(nodes, analysis.BuildFile(nodes)) {
		got = append(got, todoSite(td))
	}
	want := []string{`2:5 "one"`, `6:12 ""`, `6:50 "two"`}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("Todos = %q, want %q", got, want)
	}
}

func todoSite(td *ast.Todo) string {
	return fmt.Sprintf("%d:%d %q", td.Line, td.Col, td.ReasonText())
}

// A bare `todo` in the scope of a binding named `todo` reads the binding,
// a reference to it; one outside every such scope, and one with a reason,
// are placeholders. The parser reads a bare `todo` as an identifier and
// the analysis decides, so the tree is the same after the check: with no
// analysis, all four are placeholders.
func TestTodo_InABindingsScopeReadsTheBinding(t *testing.T) {
	nodes := parseTodoBindings(t)
	fa := analysis.BuildFile(nodes)
	if got, want := todoSites(nodes, fa), `8:17 ""|13:5 "reasoned"`; got != want {
		t.Fatalf("Todos %q, want %q", got, want)
	}
	if got, want := todoSites(nodes, nil), `2:5 ""|7:23 ""|8:17 ""|13:5 "reasoned"`; got != want {
		t.Fatalf("Todos with no analysis %q, want %q", got, want)
	}
	for _, at := range []analysis.Pos{{Line: 2, Col: 5}, {Line: 7, Col: 23}} {
		if sym := fa.References[at]; sym == nil || sym.Name != "todo" {
			t.Errorf("%v refers to %+v, want the binding `todo`", at, sym)
		}
	}
}

const todoBindingsSrc = "fn apply(todo: Int) -> Int {\n    todo + 1\n}\n\nfn pick(m: Maybe<Int>) -> Int {\n    case m {\n        Some(todo) -> todo\n        None -> todo\n    }\n}\n\nfn later(todo: Int) -> Int {\n    todo \"reasoned\"\n}\n"

func parseTodoBindings(t *testing.T) []ast.Node {
	t.Helper()
	nodes, perrs := parser.ParseWithRecovery(lexer.Lex(todoBindingsSrc))
	if len(perrs) > 0 {
		t.Fatalf("parse: %v", perrs)
	}
	return nodes
}

func todoSites(nodes []ast.Node, fa *analysis.FileAnalysis) string {
	var got []string
	for _, td := range analysis.Todos(nodes, fa) {
		got = append(got, todoSite(td))
	}
	return strings.Join(got, "|")
}

// One parsed tree, checked by many checks at once, as the language server's
// checks share a file nothing edited: each check's analysis gives each bare
// `todo` its meaning, and none writes the tree (run under -race). Half the
// checks see a `todo` from outside the file, as a name an import binds
// would be, so there every bare `todo` reads it; the others see none, so
// the one in `pick`'s `None` arm is the placeholder. A check after them
// with no outside `todo`, the tree unparsed again, finds the placeholder
// back, as a recheck after an edit removes the binding does.
func TestTodo_ChecksSharingATreeEachResolveIt(t *testing.T) {
	nodes := parseTodoBindings(t)
	outside := func() *analysis.Scope {
		s := analysis.NewScope(nil)
		s.Define(&analysis.Symbol{Name: "todo", Kind: analysis.SymbolFunction})
		return s
	}
	check := func(bound bool) (string, []analysis.TypeError) {
		var prims *analysis.Scope
		if bound {
			prims = outside()
		}
		fa := analysis.BuildFileWithStdlib(nodes, prims, nil, "", nil)
		errs := append(analysis.BuildTypes(fa, nodes), analysis.CheckTypes(fa, nodes)...)
		return todoSites(nodes, fa), errs
	}
	want := map[bool]string{true: `13:5 "reasoned"`, false: `8:17 ""|13:5 "reasoned"`}
	var wg sync.WaitGroup
	got := make([]string, 16)
	for i := range got {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got[i], _ = check(i%2 == 0)
		}()
	}
	wg.Wait()
	for i, g := range got {
		if w := want[i%2 == 0]; g != w {
			t.Errorf("check %d (outside todo %v): Todos %q, want %q", i, i%2 == 0, g, w)
		}
	}
	if g, errs := check(false); g != want[false] {
		t.Fatalf("a later check with no outside todo: Todos %q, want %q (errors %v)", g, want[false], errs)
	}
	if got := todoSites(nodes, nil); got != `2:5 ""|7:23 ""|8:17 ""|13:5 "reasoned"` {
		t.Fatalf("the checks changed the tree: Todos with no analysis %q", got)
	}
}
