package vmhost_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/std"
	"github.com/nomi-language/nomi/vmhost"
)

// Front-end diagnostics as `nomi check` and `nomi run` report them: through
// vmhost.Check and vmhost.Load on a real entry file, which is the path both
// commands enter.

// checkEntry writes src as main.nomi in a fresh directory and checks it the
// way `nomi check` does.
func checkEntry(t *testing.T, src string) error {
	t.Helper()
	path := filepath.Join(t.TempDir(), "main.nomi")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write entry: %v", err)
	}
	return vmhost.Check(path)
}

// loadEntry writes src as main.nomi in a fresh directory and loads it the way
// `nomi run` does.
func loadEntry(t *testing.T, src string) error {
	t.Helper()
	path := filepath.Join(t.TempDir(), "main.nomi")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write entry: %v", err)
	}
	_, err := vmhost.Load(path)
	return err
}

// TestSynthesizedImplDiagnosticNamesRealSource: a synthesized `impl Debug`
// block records the declaration it was synthesized for, and the checker
// reports there rather than at a line from the analyzer's synth band, which
// names a position no programmer can navigate to.
//
// The assertion is two-sided. Moving the position before
// `fa.Definitions[pos]`, which keys the item's resolved signature by
// position, makes the lookup miss, and then every signature mismatch on every
// synthesized impl goes unreported. The error must still fire, and it must
// name real source.
//
// No well-formed program reaches a synthesized-impl mismatch any more (the
// witness this test used, a positional function-typed payload colliding with
// the impl's position, is fixed), so the mismatch is made by editing the
// synthesized impl's return type before the checker sees it.
func TestSynthesizedImplDiagnosticNamesRealSource(t *testing.T) {
	src := "enum Handler {\n  Text Int\n}\n\nfn main() {\n}\n"
	nodes, err := parser.Parse(lexer.Lex(src))
	if err != nil {
		t.Fatal(err)
	}
	nodes = analysis.SynthesizeUniversalDebug(nodes)
	edited := false
	for _, n := range nodes {
		if ib, ok := n.(*ast.ImplBlock); ok && ib.SynthOriginLine != 0 {
			fd := ib.Items[0].(*ast.FuncDef)
			line, col := fd.ReturnTypeExpr.(*ast.SimpleType).Line, fd.ReturnTypeExpr.(*ast.SimpleType).Col
			fd.ReturnTypeExpr = &ast.SimpleType{Name: "Int", Line: line, Col: col}
			edited = true
		}
	}
	if !edited {
		t.Fatal("no synthesized impl to edit; the test exercises nothing")
	}
	msg := checkNodes(t, nodes)
	if msg == "" {
		t.Fatal("the synthesized-impl signature mismatch is no longer reported at all — the check may have " +
			"been skipped rather than the program fixed")
	}
	if !strings.Contains(msg, "impl function 'inspect'") || !strings.Contains(msg, "interface 'Debug'") {
		t.Fatalf("expected the synthesized Debug impl's signature mismatch, got: %s", msg)
	}
	found := false
	for _, m := range diagPos.FindAllStringSubmatch(msg, -1) {
		line, err := strconv.Atoi(m[1])
		if err != nil {
			t.Fatalf("unparseable line in %q", m[0])
		}
		found = true
		if analysis.IsSynthesizedLine(line) {
			t.Errorf("diagnostic names synth-band position %q:\n%s", m[0], msg)
		}
	}
	if !found {
		t.Fatalf("no `line N, col M` position in the diagnostic at all:\n%s", msg)
	}
	if !strings.Contains(msg, "line 1, col 6: impl function 'inspect'") {
		t.Errorf("want the `Handler` declaration at line 1, col 6; got:\n%s", msg)
	}
}

// A positional function-typed payload checks clean: its synthesized Debug
// impl's payload binding does not share the impl function's position.
func TestPositionalFunctionPayloadChecksClean(t *testing.T) {
	if err := checkEntry(t, "enum Handler {\n  Text ((String) -> Bool)\n}\n\nfn main() {\n}\n"); err != nil {
		t.Fatal(err)
	}
}

// checkNodes runs the project front end over already-parsed entry nodes and
// answers the diagnostics as `line N, col M: message` lines.
func checkNodes(t *testing.T, nodes []ast.Node) string {
	t.Helper()
	lib := std.Load()
	dir := t.TempDir()
	loader := func(string, []string) ([]ast.Node, error) { return nil, os.ErrNotExist }
	fa, _, _ := analysis.BuildProjectWithCache(nodes, lib.Primitives, lib.Modules, lib.Files, dir, loader)
	if fa == nil {
		t.Fatal("BuildProjectWithCache returned nil")
	}
	var errs []analysis.TypeError
	errs = append(errs, fa.TypeErrors...)
	errs = append(errs, analysis.CheckTypes(fa, nodes)...)
	var sb strings.Builder
	for _, e := range errs {
		sb.WriteString(e.Error())
		sb.WriteString("\n")
	}
	return sb.String()
}

// diagPos scrapes `line N, col M` out of a rendered diagnostic.
var diagPos = regexp.MustCompile(`line (\d+), col (\d+)`)

// diagLoc scrapes the line and column out of a `path:line:col: message`
// diagnostic.
var diagLoc = regexp.MustCompile(`\.nomi:(\d+):(\d+): `)

// TestSyntaxErrorStillRefusedByRunAndCheck is the hard edge of resilient
// parsing. Recovery exists for the editor; `nomi run` and `nomi check` must
// still refuse a file with a syntax error, with the strict parser's
// diagnostic, and neither may see a partially-read tree.
func TestSyntaxErrorStillRefusedByRunAndCheck(t *testing.T) {
	const halfTyped = "fn compute(n: Int): Int {\n" +
		"  total = n * 2\n" +
		"  f = |x| n + \n" +
		"}\n" +
		"\n" +
		"fn main() {\n" +
		"  io.print(\"hi\")\n" +
		"}\n"
	_, parseErr := parser.Parse(lexer.Lex(halfTyped))
	if parseErr == nil {
		t.Fatal("the fixture is supposed to have a syntax error; it parsed cleanly")
	}
	var pe parser.ParseError
	if !errors.As(parseErr, &pe) || !strings.Contains(pe.Message, "unexpected token RBRACE") {
		t.Fatalf("unexpected diagnostic from the strict parser: %q", parseErr)
	}
	// The command line names the file, then the parser's line, column and
	// message.
	want := fmt.Sprintf("main.nomi:%d:%d: %s", pe.Line, pe.Col, pe.Message)
	if err := loadEntry(t, halfTyped); err == nil {
		t.Fatal("nomi run's load path accepted a file with a syntax error")
	} else if !strings.Contains(err.Error(), want) {
		t.Fatalf("nomi run's diagnostic changed\nwant: %q\n got: %q", want, err.Error())
	}
	if err := checkEntry(t, halfTyped); err == nil {
		t.Fatal("nomi check accepted a file with a syntax error")
	} else if !strings.Contains(err.Error(), want) {
		t.Fatalf("nomi check's diagnostic changed\nwant: %q\n got: %q", want, err.Error())
	}

	// Positive control: the same shape with the operand supplied loads.
	const control = "import std/io\n" +
		"\n" +
		"fn compute(n: Int): Int {\n" +
		"  f = |x: Int| n + x\n" +
		"  f(2)\n" +
		"}\n" +
		"\n" +
		"fn main() {\n" +
		"  io.print(\"hi\")\n" +
		"}\n"
	if err := loadEntry(t, control); err != nil {
		t.Fatalf("the control fixture must load: %v", err)
	}
}

// A user `impl Equatable for Int` must be rejected as a duplicate of
// std/int's: `==` on an Int cannot dispatch to it, so accepting it leaves
// `Equatable.equal?(1, 1)` answering the user's impl while `1 == 1` answers
// structurally.
//
// `Int`, `String`, `Float`, `Byte` and `Bytes` resolve to a shared
// *PrimitiveType singleton that carries no Origin, so without
// `analysis.primitiveOriginIndex` the user's receiver qualified to bare `Int`
// while std/int's own impl qualified to `int.Int` — two grouping keys, and
// `detectImplCollisions` saw one impl in each. `Bool` is an *EnumType that
// always carried an Origin, and is the control that identified the mechanism.
//
// The rows quantify over the primitive set rather than pinning `Int`, because
// a fix for one name would pass a one-row test. The harness is a real entry
// file: `analysis.buildProjectExpectingErrors` reaches no stdlib module, so
// there is no std impl to collide with and it reports nothing for any row.
func TestUserImplForAPrimitiveIsADuplicateOfStdlibs(t *testing.T) {
	for _, c := range []struct {
		iface   string
		method  string
		recv    string
		ret     string
		body    string
		qualRcv string
		home    string
	}{
		{"Equatable", "equal?", "Int", "Bool", "False", "int.Int", "std/int"},
		{"Equatable", "equal?", "String", "Bool", "False", "strings.String", "std/strings"},
		{"Equatable", "equal?", "Float", "Bool", "False", "float.Float", "std/float"},
		{"Equatable", "equal?", "Byte", "Bool", "False", "bytes.Byte", "std/bytes"},
		{"Equatable", "equal?", "Bytes", "Bool", "False", "bytes.Bytes", "std/bytes"},
		{"Equatable", "equal?", "Bool", "Bool", "False", "bool.Bool", "std/bool"},
		// `String` is absent from Hashable: std/strings spells its `hash` as
		// a `host fn`, which this check cannot see; see
		// TestExternStdImplsAreInvisibleToTheDuplicateRule.
		{"Hashable", "hash", "Int", "Int", "0", "int.Int", "std/int"},
		{"Hashable", "hash", "Float", "Int", "0", "float.Float", "std/float"},
	} {
		t.Run(c.iface+"/"+c.recv, func(t *testing.T) {
			params := "a: " + c.recv + ", b: " + c.recv
			if c.iface == "Hashable" {
				params = "a: " + c.recv
			}
			src := "impl " + c.iface + " for " + c.recv + " {\n" +
				"  fn " + c.method + "(" + params + "): " + c.ret + " { " + c.body + " }\n" +
				"}\n\nfn main() {\n  Unit\n}\n"
			err := checkEntry(t, src)
			if err == nil {
				t.Fatalf("impl %s for %s was ACCEPTED; it silently takes %s's dispatch slot, so "+
					"`%s.%s` and the operator answer differently", c.iface, c.recv, c.home, c.iface, c.method)
			}
			msg := err.Error()
			if !strings.Contains(msg, "duplicate impl") {
				t.Fatalf("rejected for the wrong reason (want `duplicate impl`): %s", msg)
			}
			for _, want := range []string{"`" + c.qualRcv + "`", c.home, "<project entry>"} {
				if !strings.Contains(msg, want) {
					t.Errorf("diagnostic does not name %q, so the user cannot find "+
						"both halves of the duplicate: %s", want, msg)
				}
			}
		})
	}
}

// The union must not report std against itself: with pointer dedupe instead
// of CollisionImplIndex's by-declaration dedupe, this program reports over
// 100 diagnostics. The std/io import is load-bearing: it is what makes std
// modules reachable twice, and an import-free program passes under the
// mutant.
func TestACleanProgramReportsNoDuplicateImpl(t *testing.T) {
	err := checkEntry(t, "import {\n  std/io\n}\n\nfn main() {\n  io.print(\"x\")\n}\n")
	if err == nil {
		return
	}
	if strings.Contains(err.Error(), "duplicate impl") {
		t.Fatalf("a program declaring no impls was told it has duplicates:\n%v", err)
	}
	t.Fatalf("unexpected diagnostic on a trivial program: %v", err)
}

// An extern std impl (`impl Hashable for String { host fn hash }`) is
// invisible to the duplicate rule, so a user can declare a second one and
// silently replace std's. This pins an open gap: it asserts that the wrong
// behaviour is still present, that the duplicate rule is blind to extern std
// impls. Closing it means `detectImplCollisions` grouping FuncDefs and
// ExternFuncs together. The test fails when the gap closes; the person
// closing it deletes this and says so.
func TestExternStdImplsAreInvisibleToTheDuplicateRule(t *testing.T) {
	for _, c := range []struct {
		name string
		src  string
	}{
		{
			name: "Hashable for String, whose std impl is a host fn",
			src: "impl Hashable for String {\n" +
				"  fn hash(_s: String): Int { 0 }\n" +
				"}\n\nfn main() {\n  Unit\n}\n",
		},
		{
			name: "Display for Int, same shape and a silent override",
			src: "impl Display for Int {\n" +
				"  fn to_string(_n: Int): String { \"SHADOWED\" }\n" +
				"}\n\nfn main() {\n  Unit\n}\n",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			if err := checkEntry(t, c.src); err != nil {
				t.Fatalf("this is now REJECTED: %v\n\nThe extern half of the "+
					"duplicate rule has been closed. Delete this test and move "+
					"these rows into TestUserImplForAPrimitiveIsADuplicateOfStdlibs.", err)
			}
		})
	}
}

// The duplicate diagnostic must point at a line the reader can open: not
// std/int.nomi's line and not the derive-synthesis line band. Every row
// imports std/io because that is what puts std's own impl first in the
// group; without it the anchor is right through group ordering alone. The
// source is ten lines, so any anchor outside 1..10 is wrong by construction.
func TestImplCollisionAnchorsAtAPositionTheUserCanOpen(t *testing.T) {
	for _, recv := range []string{"Int", "Bool", "Float"} {
		t.Run(recv, func(t *testing.T) {
			src := "import {\n  std/io\n}\n\n" +
				"impl Equatable for " + recv + " {\n" +
				"  fn equal?(a: " + recv + ", b: " + recv + "): Bool { False }\n" +
				"}\n" +
				"fn main() {\n  io.print(\"x\")\n}\n"
			err := checkEntry(t, src)
			if err == nil {
				t.Fatalf("no diagnostic to anchor; the duplicate rule regressed")
			}
			msg := err.Error()
			if !strings.Contains(msg, "duplicate impl") {
				t.Fatalf("rejected for the wrong reason: %s", msg)
			}
			m := diagLoc.FindStringSubmatch(msg)
			if m == nil {
				t.Fatalf("diagnostic carries no position at all: %s", msg)
			}
			line, _ := strconv.Atoi(m[1])
			if line < 1 || line > 10 {
				t.Errorf("anchored at line %d; the source has 10 lines, so this points "+
					"into std or into the synthesized line band: %s", line, msg)
			}
		})
	}
}
