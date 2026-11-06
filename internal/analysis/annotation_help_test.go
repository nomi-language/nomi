package analysis_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// The declarations the annotation help cases share: an interface, two
// structs that implement it, an alias of a function type that returns the
// interface, as std/http's `Handler` returns `ToResponse`, and functions
// of each shape.
const annotationHelpDecls = `interface Reply {
  fn body(r: self) -> String
}

struct Page {
  text: String
}

struct Note {
  text: String
}

impl Reply for Page {
  fn body(p: Page) -> String { p.text }
}

impl Reply for Note {
  fn body(n: Note) -> String { n.text }
}

typealias Handler (Int) -> Reply

fn page(_n: Int) -> Page { Page{text: "p"} }

fn note(_n: Int) -> Note { Note{text: "n"} }

fn other(_s: String) -> Page { Page{text: "o"} }

fn routes() -> List<#(String, Handler)> { [#("a", page)] }
`

// annotationHelpSource is a program of decls and a function run whose
// parameters are params and whose body is body.
func annotationHelpSource(decls, params, body string) string {
	return decls + "\nfn run(" + params + ") -> Unit {\n  " + body + "\n}\n\nfn main() {}\n"
}

// annotationHelpErrors are the errors of src other than an unused binding.
func annotationHelpErrors(src string) []analysis.TypeError {
	var out []analysis.TypeError
	for _, e := range checkWithStdlib(src) {
		if !strings.Contains(e.Message, "is never read") {
			out = append(out, e)
		}
	}
	return out
}

// expectOneDiagnostic fails unless src has exactly one error, other than
// an unused binding, whose message and help lines are want.
func expectOneDiagnostic(t *testing.T, src, want string) analysis.TypeError {
	t.Helper()
	errs := annotationHelpErrors(src)
	if len(errs) != 1 {
		msgs := make([]string, len(errs))
		for i, e := range errs {
			msgs[i] = diagText(e)
		}
		t.Fatalf("want one error, got %d:\n%s", len(errs), strings.Join(msgs, "\n---\n"))
	}
	if got := diagText(errs[0]); got != want {
		t.Fatalf("diagnostic:\n%s\nwant:\n%s", got, want)
	}
	return errs[0]
}

type annotationHelpCase struct {
	decls, params, body, want string
}

// annotationHelpCases are values that need one type where nothing says
// which way they flow, each refused with the annotation that makes them
// check (spec §5 *Assignability*).
var annotationHelpCases = map[string]annotationHelpCase{
	"two typed operands of +": {annotationHelpDecls, "_c: Bool",
		`mine = [#("b", page)]
  r = mine + routes()`,
		"binary + type mismatch: List<#(String, (Int) -> Page)> vs List<#(String, (Int) -> Reply)>\n" +
			"help: annotate the binding: `mine: List<#(String, Handler)> = ...`"},
	"two typed spreads": {annotationHelpDecls, "_c: Bool",
		`mine = [#("b", page)]
  r = [..mine, ..routes()]`,
		"list spread element type mismatch: #(String, (Int) -> Page) vs #(String, (Int) -> Reply)\n" +
			"help: annotate the binding: `mine: List<#(String, Handler)> = ...`"},
	"a spread into an expected list": {annotationHelpDecls, "_c: Bool",
		`mine = [#("b", page)]
  r: List<#(String, Handler)> = [..mine, ..routes()]`,
		"list spread element type mismatch: #(String, (Int) -> Reply) vs #(String, (Int) -> Page)\n" +
			"help: annotate the binding: `mine: List<#(String, Handler)> = ...`"},
	"elements of an unannotated list": {annotationHelpDecls, "_c: Bool",
		`all = [#("a", page), #("b", note)]`,
		"list element type mismatch: expected #(String, (Int) -> Page), got #(String, (Int) -> Note)\n" +
			"help: annotate the binding: `all: List<#(String, Handler)> = ...`"},
	"if branches": {annotationHelpDecls, "c: Bool",
		`xs = if c { [#("a", page)] } else { routes() }`,
		"if/else branch type mismatch: then is List<#(String, (Int) -> Page)>, else is List<#(String, (Int) -> Reply)>\n" +
			"help: annotate the binding: `xs: List<#(String, Handler)> = ...`"},
	"case branches": {annotationHelpDecls, "c: Bool",
		`xs = case c {
    True -> [#("a", page)]
    False -> [#("b", note)]
  }`,
		"case branch type mismatch: expected List<#(String, (Int) -> Page)>, got List<#(String, (Int) -> Note)>\n" +
			"help: annotate the binding: `xs: List<#(String, Handler)> = ...`"},
	// With no alias that fits, the type is one side's, when the other
	// converts to it.
	"one side's type": {strings.NewReplacer("typealias Handler (Int) -> Reply", "", "Handler", "(Int) -> Reply").Replace(annotationHelpDecls) +
		"\nfn replies() -> List<(Int) -> Reply> { [page] }\n", "_c: Bool",
		`mine = [page]
  r = mine + replies()`,
		"binary + type mismatch: List<(Int) -> Page> vs List<(Int) -> Reply>\n" +
			"help: annotate the binding: `mine: List<(Int) -> Reply> = ...`"},
	// Two aliases fit; the one the function's signature names is the one.
	"the alias the signature names": {annotationHelpDecls + "\ntypealias Responder (Int) -> Reply\n", "_c: Bool, _r: Responder",
		`mine = [#("b", page)]
  r = mine + routes()`,
		"binary + type mismatch: List<#(String, (Int) -> Page)> vs List<#(String, (Int) -> Reply)>\n" +
			"help: annotate the binding: `mine: List<#(String, Responder)> = ...`"},
	// An alias a module the file imports declares is written qualified.
	"a qualified alias": {`import std::http
import std::html.{Html}

fn home(_r: http.Request) -> Html { todo }

fn routes() -> List<#(http.Route, http.Handler)> { todo }
`, "route: http.Route",
		"mine = [#(route, home)]\n  r = mine + routes()",
		"binary + type mismatch: List<#(Route, (Request) -> Html)> vs List<#(Route, (Request) -> ToResponse)>\n" +
			"help: annotate the binding: `mine: List<#(http.Route, http.Handler)> = ...`"},
}

func TestAnnotationHelp_NamesTheAnnotation(t *testing.T) {
	for name, tc := range annotationHelpCases {
		t.Run(name, func(t *testing.T) {
			e := expectOneDiagnostic(t, annotationHelpSource(tc.decls, tc.params, tc.body), tc.want)
			if e.Fix == nil || len(e.Fix.Edits) != 1 || !strings.HasPrefix(e.Fix.Edits[0].Text, ": ") {
				t.Fatalf("want a fix inserting the annotation, got %+v", e.Fix)
			}
		})
	}
}

var annotationHelpLine = regexp.MustCompile("help: annotate the binding: `([a-z_]+): (.*) = \\.\\.\\.`")

// The annotation the help names, written into the source, makes the
// program check.
func TestAnnotationHelp_TheAnnotationChecks(t *testing.T) {
	for name, tc := range annotationHelpCases {
		t.Run(name, func(t *testing.T) {
			src := annotationHelpSource(tc.decls, tc.params, tc.body)
			m := annotationHelpLine.FindStringSubmatch(tc.want)
			if m == nil {
				t.Fatalf("no help in %q", tc.want)
			}
			binding, typ := m[1], m[2]
			if !strings.Contains(src, "  "+binding+" = ") {
				t.Fatalf("no binding %q in the source", binding)
			}
			fixed := strings.Replace(src, "  "+binding+" = ", "  "+binding+": "+typ+" = ", 1)
			if errs := annotationHelpErrors(fixed); len(errs) != 0 {
				t.Fatalf("the annotated program does not check: %s\n%s", diagText(errs[0]), fixed)
			}
		})
	}
}

// Where no annotation would make the values one type, or where two
// would, the error stands with no help.
func TestAnnotationHelp_NoneWithoutOneCommonType(t *testing.T) {
	for name, tc := range map[string]annotationHelpCase{
		"a narrowing parameter": {annotationHelpDecls, "_c: Bool",
			`mine = [#("b", other)]
  r = mine + routes()`,
			"binary + type mismatch: List<#(String, (String) -> Page)> vs List<#(String, (Int) -> Reply)>"},
		"unrelated elements": {annotationHelpDecls, "_c: Bool",
			`a = [1]
  b = ["x"]
  r = a + b`,
			"binary + type mismatch: List<Int> vs List<String>"},
		"two aliases, neither named": {annotationHelpDecls + "\ntypealias Responder (Int) -> Reply\n", "_c: Bool",
			`mine = [#("b", page)]
  r = mine + routes()`,
			"binary + type mismatch: List<#(String, (Int) -> Page)> vs List<#(String, (Int) -> Reply)>"},
		// Branches join as a type written for them only when it is an
		// interface or a collection, so an annotation of a function type
		// would not make these check.
		"function branches": {annotationHelpDecls, "c: Bool",
			`h = if c { page } else { note }`,
			"if/else branch type mismatch: then is (Int) -> Page, else is (Int) -> Note"},
	} {
		t.Run(name, func(t *testing.T) {
			e := expectOneDiagnostic(t, annotationHelpSource(tc.decls, tc.params, tc.body), tc.want)
			if e.Fix != nil {
				t.Fatalf("want no fix, got %+v", e.Fix)
			}
		})
	}
}
