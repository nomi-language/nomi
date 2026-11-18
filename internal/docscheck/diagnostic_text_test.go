package docscheck

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// A diagnostic is recognized by its Code, and what it names is read from
// its Args or its Fix (analysis.TypeError). Code that recognizes one by its
// message stops working, silently, when the message is reworded. This test
// fails when non-test Go code does so, in any of these shapes:
//
//   - a strings test or cut of message text (strings.Contains, HasPrefix,
//     HasSuffix, CutPrefix, TrimPrefix, Index, ...);
//   - a regexp match of it (MatchString, FindStringSubmatch, ...);
//   - `==` or `!=` between message text and anything but other message
//     text, `""` or nil, and a `switch` on it.
//
// Message text is a `.Message`, `.Msg` or `.Hints` field, and, in the
// packages that hold diagnostics (errorTextDirs), the result of `.Error()`;
// and any local a function assigns from an expression that holds one, so
// `head := diagnosticHeadline(d.Message)` is message text too. The scan
// reads syntax only: it has no types, so it also sees a field of those
// names on a type that is no diagnostic, which goes in the list below with
// that as its reason.
//
// Rendering is not recognizing: joining, splitting on lines, replacing and
// printing message text are not reported.

// messageTextAllowed are the functions that may test message text, each
// with the reason it is not a diagnostic recognized by its wording. Keyed
// `path: function`. An entry no function matches fails the test.
var messageTextAllowed = map[string]string{
	"internal/lsp/diagnostics.go: reported": "joins a diagnostic the client sends back, which arrives without its code, " +
		"to the error it was published for; both sides are that error's message, so a reword changes both",
	"internal/lsp/diagnostics.go: diagnosticMatchesExpectedTypeError": "an `// expect type-error: <text>` directive in a fixture " +
		"names the error it expects by text the fixture's author wrote",
	"cmd/nomi/repl_vm.go: (*replSource).explain": "looks for the REPL's own generated binding name, " +
		"`nomi_repl_probe_N`, which a diagnostic quotes from the source the REPL wrote; no wording is matched",
	"internal/analysis/existential_demand.go: (*checker).alreadyReported": "drops an error the checker already holds at the same position; " +
		"both messages come from the one builder that is about to report again, so an error is compared with itself",
	"internal/analysis/equality_domain.go: (*checker).rejectKeyWithoutEquality": "checks that the error it reported is still at the index " +
		"it remembered, after a speculative check may have dropped it; both sides are keyWithoutEqualityMessage",
	"internal/analysis/stategen/stategen.go: load": "reads go/types errors about the generated file it is about to write; " +
		"no diagnostic of Nomi's",
	"vmhost/debugeval.go: (*DebugExpr).failureText": "strips the `line N: ` a run-time trap's text starts with, " +
		"for a debugger expression; a trap is not a diagnostic and carries its line only in its text here",
}

// messageTextOutside are the trees that hold no diagnostics: the runtime,
// whose traps are run-time faults, and the lib modules and their runners,
// whose errors are a database's or a codec's.
var messageTextOutside = []string{"rt/", "lib/", "librunners/"}

// errorTextDirs are the directories whose code holds diagnostics as Go
// errors, where the text of `.Error()` is message text.
var errorTextDirs = []string{
	"internal/analysis/", "internal/frontend/", "internal/lsp/", "internal/mcp/",
	"internal/dap/", "internal/parser/", "vmhost/", "cmd/nomi/",
}

// stringsTests are the functions of package strings that test or cut a
// string by its content.
var stringsTests = map[string]bool{
	"Contains": true, "ContainsAny": true, "HasPrefix": true, "HasSuffix": true,
	"Index": true, "LastIndex": true, "EqualFold": true, "Compare": true, "Count": true,
	"CutPrefix": true, "CutSuffix": true, "TrimPrefix": true, "TrimSuffix": true,
}

// regexpTests are the regexp methods and functions that match a string.
var regexpTests = map[string]bool{
	"MatchString": true, "FindString": true, "FindStringIndex": true,
	"FindStringSubmatch": true, "FindStringSubmatchIndex": true,
	"FindAllString": true, "FindAllStringSubmatch": true,
}

func TestNoCodeRecognizesADiagnosticByItsMessage(t *testing.T) {
	root, files := trackedFilesMatching(t, "*.go")
	found := map[string][]string{}
	for _, rel := range files {
		if strings.HasSuffix(rel, "_test.go") || strings.Contains(rel, "/testdata/") ||
			slices.ContainsFunc(messageTextOutside, func(dir string) bool { return strings.HasPrefix(rel, dir) }) {
			continue
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, filepath.Join(root, rel), nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		for key, lines := range messageTextTests(fset, file, rel) {
			found[key] = append(found[key], lines...)
		}
	}
	var keys []string
	for key := range found {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if _, ok := messageTextAllowed[key]; !ok {
			t.Errorf("%s tests a diagnostic's message text (line %s); give the diagnostic a Code where it is built and read that, "+
				"with what it names in Args or Fix", key, strings.Join(found[key], ", "))
		}
	}
	for key, reason := range messageTextAllowed {
		if strings.TrimSpace(reason) == "" {
			t.Errorf("messageTextAllowed[%q] has no reason", key)
		}
		if _, ok := found[key]; !ok {
			t.Errorf("messageTextAllowed[%q] matches no function that tests message text; remove it", key)
		}
	}
}

// The scanner reports each shape, follows a local assigned from message
// text, and leaves rendering and a test against "" alone.
func TestMessageTextScan_FindsEachShape(t *testing.T) {
	const src = `package p

func contains(e E) bool   { return strings.Contains(e.Message, "x") }
func viaLocal(e E) bool   { head := first(e.Message); return strings.HasPrefix(head, "x") }
func regex(e E) bool      { return re.MatchString(e.Message) }
func equal(e E) bool      { return e.Message == "x" }
func switched(e E) int    { switch e.Msg { case "x": return 1 }; return 0 }
func hint(e E) bool       { for _, h := range e.Hints { if strings.HasSuffix(h, "x") { return true } }; return false }
func asError(err error) bool { return strings.Contains(err.Error(), "x") }

func empty(e E) bool      { return e.Message == "" || len(e.Hints) == 0 }
func same(a, b E) bool    { return a.Message == b.Message }
func render(e E) string   { return strings.ReplaceAll(e.Message, "a", "b") + strings.Join(e.Hints, "\n") }
func other(s string) bool { return strings.Contains(s, "x") }
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "p.go", src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for key := range messageTextTests(fset, file, "vmhost/p.go") {
		got = append(got, strings.TrimPrefix(key, "vmhost/p.go: "))
	}
	sort.Strings(got)
	want := "asError contains equal hint regex switched viaLocal"
	if strings.Join(got, " ") != want {
		t.Errorf("reported %q, want %q", strings.Join(got, " "), want)
	}
	if got := messageTextTests(fset, file, "internal/vm/p.go"); len(got["internal/vm/p.go: asError"]) != 0 {
		t.Errorf("an `.Error()` outside the diagnostic packages was reported")
	}
}

// messageTextTests are the functions of file that test message text, as
// `rel: function`, each with the lines it does so on.
func messageTextTests(fset *token.FileSet, file *ast.File, rel string) map[string][]string {
	errorText := false
	for _, dir := range errorTextDirs {
		errorText = errorText || strings.HasPrefix(rel, dir)
	}
	out := map[string][]string{}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		s := &messageScan{errorText: errorText, tainted: map[string]bool{}}
		// Assignments first, to a fixed point, so a local is message text
		// wherever it is read.
		for changed := true; changed; {
			changed = false
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				changed = s.taint(n) || changed
				return true
			})
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if s.tests(n) {
				key := rel + ": " + funcName(fn)
				out[key] = append(out[key], itoa(fset.Position(n.Pos()).Line))
			}
			return true
		})
	}
	return out
}

type messageScan struct {
	errorText bool
	tainted   map[string]bool
}

// taint marks the locals n assigns from message text, and reports whether
// it marked a new one.
func (s *messageScan) taint(n ast.Node) bool {
	mark := func(lhs ast.Expr) bool {
		id, ok := lhs.(*ast.Ident)
		if !ok || id.Name == "_" || s.tainted[id.Name] {
			return false
		}
		s.tainted[id.Name] = true
		return true
	}
	changed := false
	switch n := n.(type) {
	case *ast.AssignStmt:
		for i, rhs := range n.Rhs {
			if !s.isText(rhs) {
				continue
			}
			if len(n.Rhs) == len(n.Lhs) {
				changed = mark(n.Lhs[i]) || changed
			} else {
				// `a, b, ok := f(text)`: every result may hold it.
				for _, lhs := range n.Lhs {
					changed = mark(lhs) || changed
				}
			}
		}
	case *ast.ValueSpec:
		for i, rhs := range n.Values {
			if s.isText(rhs) && i < len(n.Names) {
				changed = mark(n.Names[i]) || changed
			}
		}
	case *ast.RangeStmt:
		if s.isText(n.X) && n.Value != nil {
			changed = mark(n.Value) || changed
		}
	}
	return changed
}

// isText reports whether e holds message text: a `.Message`, `.Msg` or
// `.Hints` field, an `.Error()` where errors are diagnostics, or a local
// assigned from one. A `len(...)` holds none.
func (s *messageScan) isText(e ast.Expr) bool {
	found := false
	ast.Inspect(e, func(n ast.Node) bool {
		if found {
			return false
		}
		switch n := n.(type) {
		case *ast.CallExpr:
			if id, ok := n.Fun.(*ast.Ident); ok && id.Name == "len" {
				return false
			}
			if sel, ok := n.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Error" && len(n.Args) == 0 && s.errorText {
				found = true
			}
		case *ast.SelectorExpr:
			if n.Sel.Name == "Message" || n.Sel.Name == "Msg" || n.Sel.Name == "Hints" {
				found = true
			}
		case *ast.Ident:
			if s.tainted[n.Name] {
				found = true
			}
		case *ast.FuncLit:
			return false
		}
		return !found
	})
	return found
}

// tests reports whether n tests message text.
func (s *messageScan) tests(n ast.Node) bool {
	switch n := n.(type) {
	case *ast.CallExpr:
		sel, ok := n.Fun.(*ast.SelectorExpr)
		if !ok {
			return false
		}
		pkg, _ := sel.X.(*ast.Ident)
		if !(pkg != nil && pkg.Name == "strings" && stringsTests[sel.Sel.Name]) && !regexpTests[sel.Sel.Name] {
			return false
		}
		for _, arg := range n.Args {
			if s.isText(arg) {
				return true
			}
		}
	case *ast.BinaryExpr:
		if n.Op != token.EQL && n.Op != token.NEQ {
			return false
		}
		x, y := s.isText(n.X), s.isText(n.Y)
		return x != y && !isEmptyOrNil(n.X) && !isEmptyOrNil(n.Y)
	case *ast.SwitchStmt:
		return n.Tag != nil && s.isText(n.Tag)
	}
	return false
}

// isEmptyOrNil reports the literal `""` and nil.
func isEmptyOrNil(e ast.Expr) bool {
	switch e := e.(type) {
	case *ast.BasicLit:
		return e.Value == `""`
	case *ast.Ident:
		return e.Name == "nil"
	}
	return false
}

// funcName is fn's name, with its receiver's type: `(*T).name`.
func funcName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return fn.Name.Name
	}
	recv := fn.Recv.List[0].Type
	star := ""
	if p, ok := recv.(*ast.StarExpr); ok {
		recv, star = p.X, "*"
	}
	if ix, ok := recv.(*ast.IndexExpr); ok {
		recv = ix.X
	}
	name := "?"
	if id, ok := recv.(*ast.Ident); ok {
		name = id.Name
	}
	return "(" + star + name + ")." + fn.Name.Name
}

func itoa(n int) string { return strconv.Itoa(n) }
