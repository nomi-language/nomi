package lsp

import (
	"strings"
	"testing"
)

// classifyAt classifies the position marked in src.
func classifyAt(t *testing.T, src string) completionContext {
	t.Helper()
	off := strings.Index(src, cursorMark)
	content := src[:off] + src[off+len(cursorMark):]
	return classifyCompletion(content, off)
}

// TestClassifyCompletion pins what the reparsed tree says each position is,
// including half-typed and broken input.
func TestClassifyCompletion(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want ctxKind
	}{
		{"empty file", "‸\n", ctxTopLevel},
		{"top-level word", "pu‸\n", ctxTopLevel},
		{"statement start", "fn f() {\n  ret‸\n}\n", ctxStatement},
		{"statement in an unclosed body", "fn f() {\n  x = 1\n  ‸\n", ctxStatement},
		{"binding value", "fn f() {\n  x = ‸\n}\n", ctxExpr},
		{"call argument", "fn f() {\n  foo(1, ‸)\n}\n", ctxExpr},
		{"call argument, unclosed", "fn f() {\n  foo(1, ‸\n}\n", ctxExpr},
		{"owner member", "fn f() {\n  String.‸\n}\n", ctxMember},
		{"owner member, typed", "fn f() {\n  String.tr‸\n}\n", ctxMember},
		{"value member", "fn f() {\n  p.‸\n}\n", ctxMember},
		{"chained member", "fn f() {\n  a.b.‸\n}\n", ctxMember},
		{"pipe stage", "fn f() {\n  xs |> ‸\n}\n", ctxPipe},
		{"pipe stage, typed", "fn f() {\n  xs |> ma‸\n}\n", ctxPipe},
		{"pipe owner member", "fn f() {\n  xs |> Iter.‸\n}\n", ctxMember},
		{"dot variant alone", "fn f(): Color {\n  .‸\n}\n", ctxDotVariant},
		{"dot variant argument", "fn f() {\n  paint(.‸)\n}\n", ctxDotVariant},
		{"dot variant after ==", "fn f() {\n  if c == .‸\n}\n", ctxDotVariant},
		{"dot variant typed", "fn f() {\n  x: Color = .Re‸\n}\n", ctxDotVariant},
		{"type annotation", "fn f(x: ‸) {}\n", ctxType},
		{"type annotation typed", "fn f(x: Str‸) {}\n", ctxType},
		{"return type", "fn f(): ‸ {}\n", ctxType},
		{"struct literal field", "fn f() {\n  p = Point{x: 1, ‸}\n}\n", ctxStructField},
		{"struct literal field, unclosed", "fn f() {\n  p = Point{x: 1, ‸\n}\n", ctxStructField},
		{"struct update field", "fn f() {\n  p = {..base, ‸}\n}\n", ctxStructField},
		{"case arm", "fn f(c: Color) {\n  case c {\n    ‸\n  }\n}\n", ctxCaseArm},
		{"second case arm", "fn f(c: Color) {\n  case c {\n    .Red -> 1\n    ‸\n  }\n}\n", ctxCaseArm},
		{"import path", "import ‸\n", ctxImportPath},
		{"import path segment", "import std/‸\n", ctxImportPath},
		{"import selector", "import std/io.{‸}\n", ctxImportName},
		{"tests group line", "tests \"g\" {\n  ‸\n}\n", ctxTestGroup},
		{"tests group after boot", "tests \"g\" {\n  boot server.boot(s)\n  se‸\n}\n", ctxTestGroup},
		{"tests group boot call", "tests \"g\" {\n  boot ‸\n}\n", ctxTestBoot},
		{"with target", "fn f() {\n  with App.‸\n}\n", ctxMember},
		{"string literal", "fn f() {\n  x = \"a‸b\"\n}\n", ctxNone},
		{"comment", "fn f() {\n  // com‸\n}\n", ctxNone},
		{"parameter name", "fn f(na‸: Int) {}\n", ctxNone},
		{"parameter name without a type", "fn f(na‸) {}\n", ctxParamName},
		{"parameter name, unclosed", "fn f(a: Int, na‸\n", ctxParamName},
		{"parameter type, unclosed", "fn f(a: ‸\n", ctxType},
		{"lambda parameter name", "fn f() {\n  g = |na‸| 1\n}\n", ctxNone},
		{"predicate name", "fn f() {\n  empty?‸\n}\n", ctxStatement},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyAt(t, tt.src)
			if got.kind != tt.want {
				t.Fatalf("kind = %d, want %d", got.kind, tt.want)
			}
		})
	}
}

func TestClassifyCompletion_Details(t *testing.T) {
	c := classifyAt(t, "fn f() {\n  xs |> Iter.ma‸p(g)\n}\n")
	if c.kind != ctxMember || c.pipeLHS == nil || c.prefix != "ma" {
		t.Fatalf("pipe member: kind %d, pipe %v, prefix %q", c.kind, c.pipeLHS, c.prefix)
	}
	if !c.nextIsParen {
		// The word runs to `map`; the `(` after it is already written.
		t.Fatalf("nextIsParen = false for `Iter.ma‸p(g)`")
	}
	c = classifyAt(t, "fn f() {\n  empty?‸\n}\n")
	if c.prefix != "empty?" {
		t.Fatalf("prefix = %q, want empty?", c.prefix)
	}
	c = classifyAt(t, "import std/io.{‸}\n")
	if c.importStmt == nil {
		t.Fatal("import selector: no import statement")
	}
	c = classifyAt(t, "import a/b/‸\n")
	if c.importSeg != 2 {
		t.Fatalf("import segment = %d, want 2", c.importSeg)
	}
	c = classifyAt(t, "tests \"g\" {\n  clock .Virtual\n  boot server.boot(s)\n  ‸\n}\n")
	if !c.groupHas["clock"] || !c.groupHas["boot"] || c.groupHas["setup"] {
		t.Fatalf("group lines above the cursor = %v, want clock and boot", c.groupHas)
	}
}

func TestCompletion_KeywordsByPosition(t *testing.T) {
	top := complete(t, "‸\n")
	labelsInclude(t, top, "fn", "struct", "enum", "impl", "import", "tests", "pub")
	labelsExclude(t, top, "return", "defer", "if")

	stmt := complete(t, "fn f(n: Int): Int {\n  total = n * 2\n  ‸\n}\n")
	labelsInclude(t, stmt, "return", "defer", "with", "assert", "if", "case", "try", "n", "total")
	labelsExclude(t, stmt, "struct", "impl", "tests")

	operand := complete(t, "fn f(n: Int): Int {\n  total = ‸\n}\n")
	labelsInclude(t, operand, "if", "case", "try", "n")
	labelsExclude(t, operand, "return", "defer", "struct")
}

func TestCompletion_TypePositionOffersTypes(t *testing.T) {
	src := "struct Point {\n  x: Int\n}\n\nfn helper(): Int { 1 }\n\nfn f(p: ‸) {}\n"
	items := complete(t, src)
	labelsInclude(t, items, "Int", "String", "Point", "Maybe", "List")
	labelsExclude(t, items, "helper", "if", "Some")
}

func TestCompletion_NothingInStringsCommentsAndNewNames(t *testing.T) {
	for _, src := range []string{
		"fn f() {\n  x = \"pre‸\"\n  x\n}\n",
		"fn f() {\n  // pre‸\n}\n",
		"fn f(na‸: Int) {}\n",
	} {
		if items := complete(t, src); len(items) != 0 {
			t.Errorf("%q: completion offers %v, want nothing", src, itemLabels(items))
		}
	}
}

func TestCompletion_TestGroupLinesNotYetWritten(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"empty group", "tests \"g\" {\n  ‸\n}\n", "clock,boot,setup,test"},
		{"after boot", "tests \"g\" {\n  boot server.boot(s)\n  ‸\n}\n", "clock,setup,test"},
		{"after a test", "tests \"g\" {\n  test \"t\" {\n    assert true\n  }\n  ‸\n}\n", "clock,boot,setup,test"},
		{"setup below the cursor", "tests \"g\" {\n  ‸\n  setup {\n    1\n  }\n}\n", "clock,boot,test"},
	}
	for _, c := range cases {
		items := complete(t, c.src)
		if got := strings.Join(itemLabels(items), ","); got != c.want {
			t.Errorf("%s: group lines = %s, want %s", c.name, got, c.want)
		}
	}
}

func TestCompletion_PatternPositionOffersVariants(t *testing.T) {
	items := complete(t, "fn f(m: Maybe<Int>): Int {\n  case m {\n    So‸\n  }\n}\n")
	labelsInclude(t, items, "Some")
	labelsExclude(t, items, "f", "if")
}

func TestCompletion_PredicateNamePrefix(t *testing.T) {
	src := "fn empty?(s: String): Bool { s == \"\" }\n\nfn f() {\n  empty?‸\n}\n"
	labelsInclude(t, complete(t, src), "empty?")
}
