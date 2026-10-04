package analysis_test

import (
	"fmt"
	"strings"
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

fn parse(text: String): Header {
    todo "parse the header"
}

fn area(s: Shape): Float {
    case s {
        .Square(_) -> todo
        .Circle(r) -> r * r
    }
}

fn first<T>(xs: List<T>): T {
    todo
}

fn pick<T>(xs: List<T>, d: T): T {
    case List.head(xs) {
        Some(x) -> x
        None -> todo ` + "`raw reason`" + `
    }
}

fn make(): Header {
    Header{name: "x", size: todo """
        a multi-line reason
        """}
}

fn twice(n: Int): Int {
    n * 2
}

fn uses(flag: Bool): Int {
    n: Int = todo
    _ = todo
    f = |x: Int| if x > 1 { todo } else { x }
    g: (Int) -> String = |_| todo
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
		{"bare", "fn f(): Int {\n    x = todo\n    x\n}\n", 2, 5},
		{"pipe stage", "fn f(): Int {\n    x = 3 |> todo\n    x\n}\n", 2, 5},
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
	if _, errs := checkSourceWithStdlib("fn f(): Int {\n    _ = todo\n    1\n}\n"); len(errs) > 0 {
		t.Fatalf("`_ = todo` rejected: %v", errs)
	}
}

// TestTodo_CaseTypeComesFromTheFirstArmWithAValue pins that a diverging first
// arm does not decide a `case`'s type: the later arms are still checked
// against the arm that produces a value.
func TestTodo_CaseTypeComesFromTheFirstArmWithAValue(t *testing.T) {
	src := "fn f(n: Int): Int {\n    case n {\n        0 -> todo\n        1 -> 1\n        _ -> \"two\"\n    }\n}\n"
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
	if got := unread("fn parse(text: String, n: Int): Int {\n    todo\n}\n\nfn g(): (Int) -> Int {\n    |x| todo\n}\n"); len(got) > 0 {
		t.Fatalf("an unfinished body reports unread parameters: %q", got)
	}
	got := unread("fn parse(text: String): Int {\n    1\n}\n")
	if len(got) != 1 || !strings.HasPrefix(got[0], "parameter 'text' is never read") {
		t.Fatalf("a finished body's unread parameter = %q", got)
	}
}

// TestTodos_ListsEveryTodoInSourceOrder pins the walk `nomi build` and the
// language server share.
func TestTodos_ListsEveryTodoInSourceOrder(t *testing.T) {
	src := "fn a(): Int {\n    todo \"one\"\n}\n\nfn b(f: Bool): Int {\n    if f { todo } else { [1] |> Iter.map(|x| todo \"two\") |> Iter.count() }\n}\n"
	nodes, perrs := parser.ParseWithRecovery(lexer.Lex(src))
	if len(perrs) > 0 {
		t.Fatalf("parse: %v", perrs)
	}
	var got []string
	for _, td := range analysis.Todos(nodes) {
		got = append(got, todoSite(td))
	}
	want := []string{`2:5 "one"`, `6:12 ""`, `6:46 "two"`}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("Todos = %q, want %q", got, want)
	}
}

func todoSite(td *ast.Todo) string {
	return fmt.Sprintf("%d:%d %q", td.Line, td.Col, td.ReasonText())
}
