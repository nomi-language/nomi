package parser

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
)

// `subtests rows, pattern { body }` is a statement of its own, with the rows,
// the pattern and the body as its parts.
func TestParseSubtestsStatement(t *testing.T) {
	nodes := parse(t, "test \"t\" {\n  subtests cases, #(line, known) {\n    assert line == known\n  }\n}")
	body := nodes[0].(*ast.TestDecl).Body
	st, ok := body.Stmts[0].(*ast.Subtests)
	if !ok {
		t.Fatalf("expected *ast.Subtests, got %#v", body.Stmts[0])
	}
	if st.Line != 2 || st.Col != 3 {
		t.Errorf("subtests at %d:%d, want 2:3", st.Line, st.Col)
	}
	if rows, ok := st.Rows.(*ast.Ident); !ok || rows.Name != "cases" {
		t.Errorf("rows: %#v", st.Rows)
	}
	if tp, ok := st.Pattern.(*ast.TuplePattern); !ok || len(tp.Patterns) != 2 {
		t.Errorf("pattern: %#v", st.Pattern)
	}
	if st.Body == nil || len(st.Body.Stmts) != 1 {
		t.Fatalf("body: %#v", st.Body)
	}
	if _, ok := st.Body.Stmts[0].(*ast.Assertion); !ok {
		t.Errorf("body statement: %#v", st.Body.Stmts[0])
	}
}

// The rows may be any expression, broken across lines inside brackets, and
// the pattern any irrefutable one.
func TestParseSubtestsRowsAndPatterns(t *testing.T) {
	src := "fn f() {\n  subtests [\n    #Point{x: 1, y: 2},\n  ], #Point{x, y} {\n    assert x < y\n  }\n  subtests 1..=3, n {\n    assert n > 0\n  }\n  subtests xs |> Iter.map(g), _ {\n    assert True\n  }\n}"
	stmts := parse(t, src)[0].(*ast.FuncDef).Body.Stmts
	if len(stmts) != 3 {
		t.Fatalf("%d statements, want 3", len(stmts))
	}
	first := stmts[0].(*ast.Subtests)
	if _, ok := first.Rows.(*ast.ListLit); !ok {
		t.Errorf("rows: %#v", first.Rows)
	}
	if _, ok := first.Pattern.(*ast.StructPattern); !ok {
		t.Errorf("pattern: %#v", first.Pattern)
	}
	if _, ok := stmts[1].(*ast.Subtests).Rows.(*ast.RangeLit); !ok {
		t.Errorf("range rows: %#v", stmts[1].(*ast.Subtests).Rows)
	}
	if _, ok := stmts[2].(*ast.Subtests).Pattern.(*ast.WildcardPattern); !ok {
		t.Errorf("wildcard pattern: %#v", stmts[2].(*ast.Subtests).Pattern)
	}
}

// `subtests` is a keyword only where a statement starts with it and an
// expression and a comma follow. Everywhere else it is an identifier, so a
// binding, a call, a read and a function named `subtests` keep working.
func TestParseSubtestsIsAContextualKeyword(t *testing.T) {
	src := "fn subtests(n: Int) -> Int {\n  n\n}\n\nfn f() {\n  subtests = 3\n  subtests(2)\n  g(subtests, 1)\n  subtests\n  x = subtests + 1\n  subtests.field\n}"
	nodes := parse(t, src)
	if fn := nodes[0].(*ast.FuncDef); fn.Name != "subtests" {
		t.Errorf("function name %q", fn.Name)
	}
	stmts := nodes[1].(*ast.FuncDef).Body.Stmts
	if len(stmts) != 6 {
		t.Fatalf("%d statements, want 6", len(stmts))
	}
	for i, s := range stmts {
		if _, isSubtests := s.(*ast.Subtests); isSubtests {
			t.Errorf("statement %d parsed as a subtests statement", i)
		}
	}
	if b, ok := stmts[0].(*ast.Binding); !ok || b.Name != "subtests" {
		t.Errorf("binding: %#v", stmts[0])
	}
	if call, ok := stmts[1].(*ast.ExprStmt).Expr.(*ast.Call); !ok || call.Func.(*ast.Ident).Name != "subtests" {
		t.Errorf("call: %#v", stmts[1])
	}
}

// Once the rows and their comma are read the statement is committed: a
// missing pattern or body is an error at the keyword, not a fallback to an
// ordinary statement.
func TestParseSubtestsErrors(t *testing.T) {
	cases := []struct{ src, want string }{
		{"test \"t\" {\n  subtests cases,\n  assert True\n}", "line 2, col 3: expected a pattern after the rows in `subtests`, such as `subtests cases, #(input, expected) { ... }`"},
		{"test \"t\" {\n  subtests cases, row\n  assert True\n}", "line 2, col 3: expected '{' for the `subtests` body after its pattern"},
	}
	for _, tc := range cases {
		err := parseError(t, tc.src)
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("Parse(%q) = %v, want %q", tc.src, err, tc.want)
		}
	}
}

// A `subtests` statement counts as its test's assertion, whatever its body
// holds: a row may fail by ending with a helper's Result<_, AssertionFailure>,
// which only the checker can see (it rejects a body that can never fail). It
// counts the same in a test and in a test in a group.
func TestParseSubtestsCountsAsATestsAssertion(t *testing.T) {
	for _, src := range []string{
		"test \"t\" {\n  subtests [1], n {\n    assert n > 0\n  }\n}",
		"test \"t\" {\n  subtests [1], n {\n    check(n)\n  }\n}",
		"group \"g\" {\n  test \"t\" {\n    subtests [1], n {\n      check(n)\n    }\n  }\n}",
	} {
		if _, err := Parse(lexer.Lex(src)); err != nil {
			t.Errorf("the parser rejects a test whose assertion is a `subtests`:\n%s\n%v", src, err)
		}
	}
}

// A third part after the pattern, `subtests rows, pattern, name { body }`,
// is the row's name: any expression up to the body's `{`, read as an `if`
// condition is, so a `Type {` there opens the body. A tuple pattern may
// break across lines, as the formatter breaks a long header.
func TestParseSubtestsName(t *testing.T) {
	src := "test \"t\" {\n" +
		"  subtests cases, #(description, year, expected), description {\n    assert year > 0\n  }\n" +
		"  subtests cases, #(a, b, sum), \"${a} + ${b}\" {\n    assert a + b == sum\n  }\n" +
		"  subtests cases, Ok(n) or Err(n), label(n) |> String.trim {\n    assert n > 0\n  }\n" +
		"  subtests cases, n, (#Label{text: \"x\"}).text {\n    assert n > 0\n  }\n" +
		"  subtests cases, #(_, year), if year > 0 { \"after\" } else { \"before\" } {\n    assert year != 0\n  }\n" +
		"  subtests cases, #(\n    _description,\n    year\n  ), year {\n    assert year != 0\n  }\n" +
		"  subtests cases, #(_description, year) {\n    assert year != 0\n  }\n" +
		"}"
	stmts := parse(t, src)[0].(*ast.TestDecl).Body.Stmts
	if len(stmts) != 7 {
		t.Fatalf("%d statements, want 7", len(stmts))
	}
	names := []string{"Ident", "StringInterp", "Binary", "FieldAccess", "If", "Ident", ""}
	for i, want := range names {
		st, ok := stmts[i].(*ast.Subtests)
		if !ok {
			t.Fatalf("statement %d: %#v", i, stmts[i])
		}
		if st.Body == nil || len(st.Body.Stmts) != 1 {
			t.Errorf("statement %d's body: %#v", i, st.Body)
		}
		if want == "" {
			if st.Name != nil {
				t.Errorf("statement %d has a name: %#v", i, st.Name)
			}
			continue
		}
		if st.Name == nil || st.Name.NodeType() != want {
			t.Errorf("statement %d's name: %#v, want a %s", i, st.Name, want)
		}
	}
	if _, ok := stmts[2].(*ast.Subtests).Pattern.(*ast.OrPattern); !ok {
		t.Errorf("or pattern: %#v", stmts[2].(*ast.Subtests).Pattern)
	}
}

// A comma after the pattern commits to a name: a missing name, or a missing
// body after it, is an error at the keyword.
func TestParseSubtestsNameErrors(t *testing.T) {
	cases := []struct{ src, want string }{
		{"test \"t\" {\n  subtests cases, row, {\n    assert True\n  }\n}", "line 2, col 3: expected the row's name after the pattern in `subtests`"},
		{"test \"t\" {\n  subtests cases, row,\n  assert True\n}", "line 2, col 3: expected the row's name after the pattern in `subtests`"},
		{"test \"t\" {\n  subtests cases, row, name\n  assert True\n}", "line 2, col 3: expected '{' for the `subtests` body after its name"},
	}
	for _, tc := range cases {
		err := parseError(t, tc.src)
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("Parse(%q) = %v, want %q", tc.src, err, tc.want)
		}
	}
}

// `rows |> subtests pattern, name { body }` is the `subtests` statement with
// the pipe's source as its rows: a literal table, a name, or a pipeline
// that builds the rows, whose stages stay the rows' own.
func TestParseSubtestsStage(t *testing.T) {
	src := "test \"t\" {\n" +
		"  [\n    #(\"a\", 1),\n  ]\n  |> subtests #(description, n), description {\n    assert n > 0\n  }\n" +
		"  cases |> subtests n {\n    assert n > 0\n  }\n" +
		"  xs\n  |> Iter.map(g)\n  // the rows\n  |> subtests _ {\n    assert True\n  }\n" +
		"}"
	stmts := parse(t, src)[0].(*ast.TestDecl).Body.Stmts
	if len(stmts) != 3 {
		t.Fatalf("%d statements, want 3", len(stmts))
	}
	first, ok := stmts[0].(*ast.Subtests)
	if !ok || !first.Piped {
		t.Fatalf("statement 0: %#v, want a piped *ast.Subtests", stmts[0])
	}
	if first.Line != 5 || first.Col != 6 {
		t.Errorf("subtests at %d:%d, want 5:6, the keyword", first.Line, first.Col)
	}
	if _, ok := first.Rows.(*ast.ListLit); !ok {
		t.Errorf("rows: %#v", first.Rows)
	}
	if name, ok := first.Name.(*ast.Ident); !ok || name.Name != "description" {
		t.Errorf("name: %#v", first.Name)
	}
	if span := first.GetSpan(); span.StartLine != 2 || span.StartCol != 3 || span.EndLine != 7 {
		t.Errorf("span %+v, want it from the rows (2:3) to the body's end (line 7)", span)
	}
	if rows, ok := stmts[1].(*ast.Subtests).Rows.(*ast.Ident); !ok || rows.Name != "cases" {
		t.Errorf("rows: %#v", stmts[1].(*ast.Subtests).Rows)
	}
	third := stmts[2].(*ast.Subtests)
	if pipe, ok := third.Rows.(*ast.Binary); !ok || pipe.Op != "|>" {
		t.Errorf("rows: %#v, want the pipeline before the stage", third.Rows)
	}
	if len(third.StageLeading) != 1 || third.StageLeading[0].Text != "// the rows" {
		t.Errorf("stage leading trivia: %#v", third.StageLeading)
	}
}

// After a `|>`, `subtests` is the stage's keyword only when a pattern
// follows it after a space; a call, a bare name or a remainder (`%` with no
// map or set pattern against it) stays an ordinary stage.
func TestParseSubtestsStageIsAContextualKeyword(t *testing.T) {
	stmts := parse(t, "fn f() {\n  x |> subtests(y)\n  x |> subtests\n  x |> subtests % 2\n  x |> subtests%2\n}")[0].(*ast.FuncDef).Body.Stmts
	if len(stmts) != 4 {
		t.Fatalf("%d statements, want 4", len(stmts))
	}
	for i, s := range stmts {
		es, ok := s.(*ast.ExprStmt)
		if !ok {
			t.Fatalf("statement %d: %#v", i, s)
		}
		if pipe, ok := es.Expr.(*ast.Binary); !ok || pipe.Op != "|>" {
			t.Errorf("statement %d: %#v, want a pipeline", i, es.Expr)
		}
	}
}

// A map or set pattern after the word makes it the stage's keyword.
func TestParseSubtestsStageTakesAMapOrSetPattern(t *testing.T) {
	src := "test \"t\" {\n  xs |> subtests %{\"a\" => n} {\n    assert n > 0\n  }\n  xs |> subtests %[n] {\n    assert n > 0\n  }\n}"
	for i, s := range parse(t, src)[0].(*ast.TestDecl).Body.Stmts {
		if st, ok := s.(*ast.Subtests); !ok || !st.Piped {
			t.Errorf("statement %d: %#v, want a piped *ast.Subtests", i, s)
		}
	}
}

// The stage has no value: its pipeline must stand as a statement, and the
// stage must be its last.
func TestParseSubtestsStageErrors(t *testing.T) {
	cases := []struct{ src, want string }{
		{"test \"t\" {\n  x = [1] |> subtests n {\n    assert n > 0\n  }\n}", "line 2, col 14: a `|> subtests` stage has no value, so its pipeline must be a statement of its own"},
		{"test \"t\" {\n  f([1] |> subtests n {\n    assert n > 0\n  })\n}", "line 2, col 12: a `|> subtests` stage has no value, so its pipeline must be a statement of its own"},
		{"test \"t\" {\n  assert [1] |> subtests n {\n    assert n > 0\n  }\n}", "line 2, col 17: a `|> subtests` stage has no value"},
		{"test \"t\" {\n  [1]\n  |> subtests n {\n    assert n > 0\n  }\n  |> dbg\n}", "line 6, col 3: `|> subtests` must be the last stage of its pipeline: it has no value to pass on"},
		{"test \"t\" {\n  [1] |> subtests n {\n    assert n > 0\n  } |> dbg\n}", "line 4, col 5: `|> subtests` must be the last stage of its pipeline"},
		{"once x = [1] |> subtests n {\n  assert n > 0\n}", "line 1, col 17: a `|> subtests` stage has no value"},
		{"test \"t\" {\n  [1] |> subtests n\n  assert True\n}", "line 2, col 10: expected '{' for the `subtests` body after its pattern"},
	}
	for _, tc := range cases {
		err := parseError(t, tc.src)
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("Parse(%q) = %v, want %q", tc.src, err, tc.want)
		}
	}
}
