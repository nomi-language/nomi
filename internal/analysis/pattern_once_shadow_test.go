package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// checkOnceShadowProject analyses entry against sibling modules (by import path)
// with std, and answers every error the project reports, the unused-binding
// check's included.
func checkOnceShadowProject(t *testing.T, entry string, siblings map[string]string) []analysis.TypeError {
	t.Helper()
	lib := loadStd()
	loader := func(projectRoot string, modulePath []string) ([]ast.Node, error) {
		src, ok := siblings[strings.Join(modulePath, "/")]
		if !ok {
			return nil, nil
		}
		nodes, _ := parser.ParseWithRecovery(lexer.Lex(src))
		return nodes, nil
	}
	nodes, perrs := parser.ParseWithRecovery(lexer.Lex(entry))
	if len(perrs) > 0 {
		t.Fatalf("parse: %v", perrs)
	}
	fa, cache, _ := analysis.BuildProjectWithCache(nodes, lib.Primitives, lib.Modules, lib.Files, "/project", loader)
	if fa == nil {
		t.Fatal("BuildProjectWithCache returned nil")
	}
	all := append([]analysis.TypeError{}, fa.TypeErrors...)
	all = append(all, analysis.CheckTypes(fa, nodes)...)
	all = append(all, analysis.CheckUnusedBindings(fa)...)
	for _, sib := range cache {
		all = append(all, sib.TypeErrors...)
	}
	// What this harness reports about the prelude's synthesized lines is
	// not the program's.
	var out []analysis.TypeError
	for _, e := range all {
		if !analysis.IsSynthesizedLine(e.Line) {
			out = append(out, e)
		}
	}
	return out
}

const onceShadowMessage = "this binds a new `not_found`, which matches every value; `not_found` is a `once`"

// wantOnceShadow fails unless errs hold the once-shadow error at line:col
// with help, and nothing calls that binding never read.
func wantOnceShadow(t *testing.T, errs []analysis.TypeError, line, col int, help string) {
	t.Helper()
	for _, e := range errs {
		if e.Line == line && e.Col == col && strings.Contains(e.Message, "never read") {
			t.Errorf("the binding is also reported as never read: %s", e.Message)
		}
	}
	for _, e := range errs {
		if e.Message != onceShadowMessage || e.Line != line || e.Col != col {
			continue
		}
		if len(e.Hints) != 1 || e.Hints[0] != help {
			t.Errorf("help = %q, want %q", e.Hints, help)
		}
		return
	}
	t.Fatalf("the front end admits a pattern name that names a `once`: want %q at %d:%d, got:\n  %s",
		onceShadowMessage, line, col, errMsgs(errs))
}

// wantNoOnceShadow fails if errs hold a once-shadow error, or any error.
func wantNoOnceShadow(t *testing.T, errs []analysis.TypeError) {
	t.Helper()
	if len(errs) > 0 {
		t.Fatalf("the front end refuses this, which binds a name by intent:\n  %s", errMsgs(errs))
	}
}

func TestPatternNameThatNamesAOnceIsRefused(t *testing.T) {
	cases := []struct {
		name      string
		src       string
		line, col int
		help      string
	}{
		{
			name: "a case arm that is the name",
			src: `once not_found: Int = 404

pub fn describe(code: Int) -> String
  case code
    not_found -> "missing"
    _ -> "other"
  end
end
`,
			line: 5, col: 5,
			help: "to compare with it, write `code when code == not_found`",
		},
		{
			name: "a name inside a case arm's variant",
			src: `once not_found: Int = 404

pub fn describe(r: Result<Int, Int>) -> String
  case r
    Err(not_found) -> "missing"
    _ -> "fine"
  end
end
`,
			line: 5, col: 9,
			help: "to compare with it, bind `value` here and add `when value == not_found`",
		},
		{
			name: "a case on a value that is not a name",
			src: `once not_found: Int = 404

pub fn describe(code: Int) -> String
  case code + 1
    not_found -> "missing"
    _ -> "other"
  end
end
`,
			line: 5, col: 5,
			help: "to compare with it, write `value when value == not_found`",
		},
		{
			name: "a pattern if",
			src: `once not_found: Int = 404

pub fn describe(m: Maybe<Int>) -> Int
  if Some(not_found) = m then
    not_found
  else
    0
  end
end
`,
			line: 4, col: 11,
			help: "to compare with it, bind `value` here and compare `value == not_found`",
		},
		{
			name: "a binding with else",
			src: `once not_found: Int = 404

pub fn describe(m: Maybe<Int>) -> Int
  Some(not_found) = m else
      return 0
  end
  not_found
end
`,
			line: 4, col: 8,
			help: "to compare with it, bind `value` here and compare `value == not_found`",
		},
		{
			name: "an else arm",
			src: `once not_found: Int = 404

pub fn describe(r: Result<Int, Int>) -> Int
  Ok(n) = r else
    Err(not_found) -> return 0
  end
  n
end
`,
			line: 5, col: 9,
			help: "to compare with it, bind `value` here and add `when value == not_found`",
		},
		{
			name: "an assert",
			src: `once not_found: Int = 404

test "assert"
  r: Result<Int, Int> = Err(404)
  assert Err(not_found) = r
  assert not_found == 404
end
`,
			line: 5, col: 14,
			help: "to compare with it, bind `value` here and compare `value == not_found`",
		},
		{
			name: "an or alternative",
			src: `once not_found: Int = 404

pub fn describe(code: Int) -> String
  case code
    400 or not_found -> "client"
    _ -> "other"
  end
end
`,
			line: 5, col: 12,
			help: "to compare with it, bind `value` here and add `when value == not_found`",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wantOnceShadow(t, checkOnceShadowProject(t, tc.src, nil), tc.line, tc.col, tc.help)
		})
	}
}

// An imported `once` is held to the rule as a file's own is.
func TestPatternNameThatNamesAnImportedOnceIsRefused(t *testing.T) {
	entry := `import codes.not_found

pub fn describe(code: Int) -> String
  case code
    not_found -> "missing"
    _ -> "other"
  end
end
`
	siblings := map[string]string{"codes": "pub once not_found: Int = 404\n"}
	wantOnceShadow(t, checkOnceShadowProject(t, entry, siblings), 5, 5, "to compare with it, write `code when code == not_found`")
}

func TestPatternNameWithNoOnceVisibleIsAccepted(t *testing.T) {
	cases := []struct {
		name     string
		src      string
		siblings map[string]string
	}{
		{
			name: "a discard name",
			src: `once _not_found: Int = 404

pub fn describe(code: Int) -> String
  _ = _not_found
  case code
    _not_found -> "any"
  end
end
`,
		},
		{
			name: "no once of the name",
			src: `pub fn describe(code: Int) -> Int
  case code
    not_found -> not_found
  end
end
`,
		},
		{
			name: "a once of another module, not imported",
			src: `import codes

pub fn describe(code: Int) -> Int
  case code
    not_found -> not_found + codes.not_found
  end
end
`,
			siblings: map[string]string{"codes": "pub once not_found: Int = 404\n"},
		},
		{
			name: "a local binding hides the once",
			src: `once not_found: Int = 404

pub fn describe(code: Int) -> Int
  not_found = 3
  case code + not_found
    not_found -> not_found
  end
end
`,
		},
		{
			name: "a plain binding, a parameter and a destructure",
			src: `once not_found: Int = 404

pub fn plain() -> Int
  not_found = 3
  not_found
end

pub fn param(not_found: Int) -> Int
  not_found
end

pub fn destructure(pair: (Int, Int)) -> Int
  (not_found, other) = pair
  not_found + other
end
`,
		},
		{
			name: "a guard that compares with the once",
			src: `once not_found: Int = 404

pub fn describe(code: Int) -> String
  case code
    code when code == not_found -> "missing"
    _ -> "other"
  end
end
`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wantNoOnceShadow(t, checkOnceShadowProject(t, tc.src, tc.siblings))
		})
	}
}
