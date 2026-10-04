package parser

import (
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The half-typed body from the roadmap's Track 3 "Resilient parsing"
// entry, with the dangling operator at the end of the body so the parse
// genuinely fails. (The entry's literal snippet — `|x| n + ` with another
// statement below — parses: `+` continues onto the next line and takes
// that statement as its right operand.)
const halfTypedBody = "fn compute(n: Int): Int {\n" +
	"  total = n * 2\n" +
	"  f = |x| n + \n" +
	"}\n"

// findErrorNodes walks every exported field of every node reachable from
// the given roots and returns the *ast.ErrorNode it finds. Reflection
// rather than a hand-written switch: the claim being tested is that an
// error node appears NOWHERE, and a switch over the node kinds recovery
// happens to use today would pass by not looking.
func findErrorNodes(roots []ast.Node) []*ast.ErrorNode {
	var found []*ast.ErrorNode
	seen := map[uintptr]bool{}
	var walk func(v reflect.Value)
	walk = func(v reflect.Value) {
		switch v.Kind() {
		case reflect.Ptr, reflect.Interface:
			if v.IsNil() {
				return
			}
			if v.Kind() == reflect.Ptr {
				if seen[v.Pointer()] {
					return
				}
				seen[v.Pointer()] = true
				if en, ok := v.Interface().(*ast.ErrorNode); ok {
					found = append(found, en)
					return
				}
			}
			walk(v.Elem())
		case reflect.Struct:
			for i := range v.NumField() {
				if v.Type().Field(i).IsExported() {
					walk(v.Field(i))
				}
			}
		case reflect.Slice, reflect.Array:
			for i := range v.Len() {
				walk(v.Index(i))
			}
		case reflect.Map:
			for _, k := range v.MapKeys() {
				walk(v.MapIndex(k))
			}
		}
	}
	for _, n := range roots {
		walk(reflect.ValueOf(n))
	}
	return found
}

// TestParseResilient_KeepsFunctionWithHalfTypedBody is the roadmap
// entry's reproduction, inverted. Before this change ParseWithRecovery
// returned zero top-level nodes for this source, so the builder never saw
// `compute` at all.
func TestParseResilient_KeepsFunctionWithHalfTypedBody(t *testing.T) {
	// Control: the strict recovery parse still discards the function.
	// This is the "before" half of the reproduction, kept as an
	// assertion so a future change to top-level recovery cannot make the
	// test below pass for a reason unrelated to resilient mode.
	strictNodes, strictErrs := ParseWithRecovery(lexer.Lex(halfTypedBody))
	if len(strictNodes) != 0 {
		t.Fatalf("ParseWithRecovery: want 0 top-level nodes (whole function discarded), got %d", len(strictNodes))
	}
	if len(strictErrs) != 1 {
		t.Fatalf("ParseWithRecovery: want 1 parse error, got %d: %+v", len(strictErrs), strictErrs)
	}

	nodes, errs, damaged := ParseResilient(lexer.Lex(halfTypedBody))
	if len(nodes) != 1 {
		t.Fatalf("ParseResilient: want 1 top-level node, got %d", len(nodes))
	}
	// Same diagnostics as the strict parse, at the same positions.
	if len(errs) != len(strictErrs) {
		t.Fatalf("parse errors: want %d (same as strict), got %d: %+v", len(strictErrs), len(errs), errs)
	}
	for i := range errs {
		if errs[i] != strictErrs[i] {
			t.Fatalf("parse error %d differs from the strict parse\nstrict: %+v\n  ours: %+v", i, strictErrs[i], errs[i])
		}
	}
	if len(damaged) != 1 {
		t.Fatalf("damaged spans: want 1, got %d: %+v", len(damaged), damaged)
	}
	if !damaged[0].Contains(2, 3) {
		t.Fatalf("damaged span %+v should cover the whole declaration, including line 2", damaged[0])
	}

	fn, ok := nodes[0].(*ast.FuncDef)
	if !ok {
		t.Fatalf("top-level node: want *ast.FuncDef, got %T", nodes[0])
	}
	if fn.Name != "compute" || len(fn.Params) != 1 || fn.Params[0].Name != "n" {
		t.Fatalf("recovered function lost its signature: name=%q params=%+v", fn.Name, fn.Params)
	}

	// The valid statement before the damage survives, and so does the
	// lambda — with its parameter, which is the whole reason the lambda
	// body gets its own recovery site.
	var sawTotal bool
	var lambda *ast.Lambda
	for _, stmt := range fn.Body.Stmts {
		b, ok := stmt.(*ast.Binding)
		if !ok {
			continue
		}
		if b.Name == "total" {
			sawTotal = true
		}
		if l, ok := b.Value.(*ast.Lambda); ok {
			lambda = l
		}
	}
	if !sawTotal {
		t.Error("the valid binding before the damage was discarded")
	}
	if lambda == nil {
		t.Fatal("the lambda with the half-typed body was discarded; its parameter cannot reach a scope")
	}
	if len(lambda.Params) != 1 || lambda.Params[0].Name != "x" {
		t.Fatalf("recovered lambda lost its parameter: %+v", lambda.Params)
	}
	// The caret sits after the trailing space of `  f = |x| n + `, at
	// 1-based column 15. ScopeAt only reaches the lambda's parameter if
	// the lambda's span contains that point, so the span has to stretch
	// past the last token the parser actually read (the `+`, ending at
	// column 14).
	inSpan := lambda.EndLine > 3 || (lambda.EndLine == 3 && lambda.EndCol >= 15)
	if lambda.Line != 3 || lambda.Col != 7 || !inSpan {
		t.Fatalf("lambda span %d:%d-%d:%d does not contain the caret at 3:15",
			lambda.Line, lambda.Col, lambda.EndLine, lambda.EndCol)
	}

	found := findErrorNodes(nodes)
	if len(found) != 1 {
		t.Fatalf("want exactly 1 *ast.ErrorNode in the recovered tree, got %d", len(found))
	}
	if !strings.Contains(found[0].Message, "unexpected token RBRACE") {
		t.Errorf("error node should carry the failing production's message, got %q", found[0].Message)
	}
}

// TestParseWithRecovery_ProducesNoErrorNodes is the refusal boundary at
// the parser. `nomi run` and `nomi build` go through Parse; nothing but
// ParseResilient may hand out a partially-read tree, so neither of the
// non-resilient entry points may produce an error node for ANY input.
func TestParseWithRecovery_ProducesNoErrorNodes(t *testing.T) {
	broken := []string{
		halfTypedBody,
		"fn f(): Int {\n  a = 1\n  ] junk\n  a\n}\n",
		"fn f(): Int {\n  a = 1\n  g(a\n  a\n}\n",
		"fn f(): Int {\n  a = 1\n\nfn g(): Int { 1 }\n",
		"fn f(): Int {\n  if True {\n    b = 1\n    ] junk\n    b\n  } else { 0 }\n}\n",
		"test \"t\" {\n  assert 1 ==\n}\n",
	}
	errorNodesUnderResilient := 0
	for _, src := range broken {
		if _, err := Parse(lexer.Lex(src)); err == nil {
			t.Fatalf("Parse accepted a file with a syntax error:\n%s", src)
		}
		nodes, errs := ParseWithRecovery(lexer.Lex(src))
		if len(errs) == 0 {
			t.Fatalf("ParseWithRecovery reported no error for:\n%s", src)
		}
		if found := findErrorNodes(nodes); len(found) != 0 {
			t.Fatalf("ParseWithRecovery produced %d *ast.ErrorNode for:\n%s", len(found), src)
		}
		// Positive control: resilient mode DID repair this same source,
		// so the zero above is a property of the entry point rather than
		// of the input. Not every repair is an error node — an unclosed
		// body is repaired by closing the block — so the per-source
		// assertion is on the damage span, with the error-node count
		// checked across the set below.
		rNodes, _, damaged := ParseResilient(lexer.Lex(src))
		if len(damaged) == 0 {
			t.Fatalf("ParseResilient repaired nothing for:\n%s", src)
		}
		errorNodesUnderResilient += len(findErrorNodes(rNodes))
	}
	if errorNodesUnderResilient == 0 {
		t.Fatal("no source in the set produced an *ast.ErrorNode under resilient mode; " +
			"the ParseWithRecovery zeroes above would then be vacuous")
	}
}

// TestParseResilient_ClosesUnclosedBodyAndKeepsFollowingDecl covers the
// commonest mid-edit state: the `{` is open and the `}` has not been
// typed yet. The declarations below must not be swallowed as nested ones.
func TestParseResilient_ClosesUnclosedBodyAndKeepsFollowingDecl(t *testing.T) {
	src := "fn compute(n: Int): Int {\n  total = n * 2\n\nfn other(): Int { 1 }\n"
	nodes, _, damaged := ParseResilient(lexer.Lex(src))
	if len(nodes) != 2 {
		t.Fatalf("want 2 top-level nodes (the unclosed function and the one after it), got %d", len(nodes))
	}
	for i, want := range []string{"compute", "other"} {
		fn, ok := nodes[i].(*ast.FuncDef)
		if !ok || fn.Name != want {
			t.Fatalf("node %d: want FuncDef %q, got %T", i, want, nodes[i])
		}
	}
	if len(damaged) != 1 || damaged[0].StartLine != 1 {
		t.Fatalf("want the unclosed declaration marked damaged and nothing else, got %+v", damaged)
	}
	if damaged[0].Contains(4, 4) {
		t.Fatalf("damaged span %+v must not cover the clean declaration on line 4", damaged[0])
	}
}

// TestParseResilient_KeepsAttachedTestWithHalfTypedLine covers a `//!`
// group mid-edit: the line being typed becomes an error node, and the
// group's earlier binding and the declaration it is attached to survive,
// so completion has the binding to offer. The declaration is damaged.
func TestParseResilient_KeepsAttachedTestWithHalfTypedLine(t *testing.T) {
	src := "//! u = 1\n//! assert u.\npub fn double(n: Int): Int {\n    n * 2\n}\n"
	if _, errs := ParseWithRecovery(lexer.Lex(src)); len(errs) == 0 {
		t.Fatal("the source parses; it must fail to parse for this test")
	}
	nodes, _, damaged := ParseResilient(lexer.Lex(src))
	if len(nodes) != 1 {
		t.Fatalf("want the function kept, got %d top-level nodes", len(nodes))
	}
	fn, ok := nodes[0].(*ast.FuncDef)
	if !ok || fn.Name != "double" {
		t.Fatalf("node: want FuncDef double, got %T", nodes[0])
	}
	if len(fn.AttachedTests) != 1 || fn.AttachedTests[0].Body == nil {
		t.Fatalf("want the attached test kept, got %+v", fn.AttachedTests)
	}
	stmts := fn.AttachedTests[0].Body.Stmts
	if len(stmts) != 2 {
		t.Fatalf("want the binding and the damaged line, got %d statements", len(stmts))
	}
	if b, ok := stmts[0].(*ast.Binding); !ok || b.Name != "u" {
		t.Errorf("statement 0: want the binding u, got %T", stmts[0])
	}
	if _, ok := stmts[1].(*ast.ErrorNode); !ok {
		t.Errorf("statement 1: want an error node, got %T", stmts[1])
	}
	if len(damaged) != 1 {
		t.Errorf("want the declaration marked damaged, got %+v", damaged)
	}
}

// TestParseResilient_MatchesStrictRecoveryOnValidSource is the
// no-divergence invariant, over every .nomi file in the repo that parses
// cleanly. Resilient mode is only reached after a parse has already
// failed, so it must be unobservable on a valid program.
func TestParseResilient_MatchesStrictRecoveryOnValidSource(t *testing.T) {
	var files []string
	for _, root := range []string{"../../tests", "../../std", "../../tour"} {
		_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(path, ".nomi") {
				return nil
			}
			files = append(files, path)
			return nil
		})
	}
	if len(files) < 100 {
		t.Fatalf("expected a real corpus to compare against, found %d .nomi files", len(files))
	}
	compared := 0
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		tokens := lexer.Lex(string(data))
		want, wantErrs := ParseWithRecovery(tokens)
		if len(wantErrs) != 0 {
			continue // not a valid file; the invariant says nothing about it
		}
		got, gotErrs, damaged := ParseResilient(tokens)
		if len(gotErrs) != 0 || len(damaged) != 0 {
			t.Fatalf("%s parses cleanly but ParseResilient reported %d errors and %d damaged spans",
				path, len(gotErrs), len(damaged))
		}
		if len(got) != len(want) {
			t.Fatalf("%s: top-level node count differs — strict %d, resilient %d", path, len(want), len(got))
		}
		for i := range want {
			if got[i].NodeType() != want[i].NodeType() || got[i].LineNum() != want[i].LineNum() {
				t.Fatalf("%s node %d differs — strict %s@%d, resilient %s@%d",
					path, i, want[i].NodeType(), want[i].LineNum(), got[i].NodeType(), got[i].LineNum())
			}
		}
		if found := findErrorNodes(got); len(found) != 0 {
			t.Fatalf("%s parses cleanly but ParseResilient produced %d error nodes", path, len(found))
		}
		compared++
	}
	if compared < 100 {
		t.Fatalf("compared only %d valid files; the corpus did not load", compared)
	}
	t.Logf("compared %d valid .nomi files", compared)
}
