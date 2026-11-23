package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// dollarWarnings checks src as a one-file project and answers its errors
// and its `$name` warnings (analysis.DollarWarnings).
func dollarWarnings(t *testing.T, src string) ([]analysis.TypeError, []analysis.TypeError) {
	t.Helper()
	nodes, perrs := parser.ParseWithRecovery(lexer.Lex(src))
	if len(perrs) > 0 {
		t.Fatalf("parse errors: %v", perrs)
	}
	lib := loadStd()
	fa, _, _ := analysis.BuildProjectWithCache(nodes, lib.Primitives, lib.Modules, lib.Files, t.TempDir(), nil)
	errs := append(fa.TypeErrors, analysis.CheckTypes(fa, nodes)...)
	return errs, analysis.DollarWarnings(fa, nodes)
}

// `"Hello, $name"` with a `name` in scope checks, and is warned about at
// the `$name` with the literal rewritten in the help. The fix writes that
// literal, and the fixed program checks with no warning.
func TestDollarWarning_FiresWithTheRewrite(t *testing.T) {
	src := "pub fn greet(name: String) -> String\n    \"Hello, $name\" + name\nend\n"
	errs, warns := dollarWarnings(t, src)
	if len(errs) > 0 {
		t.Fatalf("the front end rejects this, which it must accept: %v", errs)
	}
	if len(warns) != 1 {
		t.Fatalf("want one warning, got %+v", warns)
	}
	w := warns[0]
	if want := "`$name` is text here, not the value of `name`: Nomi interpolates with `${...}`, not `$`"; w.Message != want {
		t.Errorf("message:\n got %q\nwant %q", w.Message, want)
	}
	if want := []string{"to interpolate, write `\"Hello, ${name}\"`"}; len(w.Hints) != 1 || w.Hints[0] != want[0] {
		t.Errorf("help:\n got %q\nwant %q", w.Hints, want)
	}
	if w.Code != analysis.DollarInterpolationCode {
		t.Errorf("code %q, want %q", w.Code, analysis.DollarInterpolationCode)
	}
	if w.Line != 2 || w.Col != 13 || w.EndLine != 2 || w.EndCol != 18 {
		t.Errorf("span %d:%d to %d:%d, want 2:13 to 2:18", w.Line, w.Col, w.EndLine, w.EndCol)
	}
	if w.Fix == nil || w.Fix.Title != "Write `${name}`" {
		t.Fatalf("fix %+v, want one titled \"Write `${name}`\"", w.Fix)
	}
	fixed := applyFix(t, src, w.Fix)
	if want := "pub fn greet(name: String) -> String\n    \"Hello, ${name}\" + name\nend\n"; fixed != want {
		t.Fatalf("fixed:\n%s\nwant:\n%s", fixed, want)
	}
	errs, warns = dollarWarnings(t, fixed)
	if len(errs) > 0 || len(warns) > 0 {
		t.Errorf("the fixed program has errors %v and warnings %v", errs, warns)
	}
}

// Each kind of value a `$name` may name, and each form of literal that is
// warned about, with the help.
func TestDollarWarning_FiresForEachValueAndForm(t *testing.T) {
	cases := []struct {
		name, src, message, help string
		line, col                int
	}{
		{
			"a local binding",
			"pub fn f() -> String\n    total = 3\n    \"cost: $total\" + \"${total}\"\nend\n",
			"`$total` is text here, not the value of `total`: Nomi interpolates with `${...}`, not `$`",
			"to interpolate, write `\"cost: ${total}\"`", 3, 12,
		},
		{
			"a pattern binding",
			"pub fn f(m: Option<Int>) -> String\n    case m\n        Some(n) -> \"got $n\" + \"${n}\"\n        None -> \"none\"\n    end\nend\n",
			"`$n` is text here, not the value of `n`: Nomi interpolates with `${...}`, not `$`",
			"to interpolate, write `\"got ${n}\"`", 3, 25,
		},
		{
			"a once",
			"once greeting: String = \"hi\"\n\npub fn f() -> String\n    \"$greeting!\"\nend\n",
			"`$greeting` is text here, not the value of `greeting`: Nomi interpolates with `${...}`, not `$`",
			"to interpolate, write `\"${greeting}!\"`", 4, 6,
		},
		{
			"the text of an interpolated string, past a slot that holds a string of braces",
			"pub fn f(a: Int, b: Int) -> String\n    \"${a + String.length(\"}{\")} and $b.\" + \"${b}\"\nend\n",
			"`$b` is text here, not the value of `b`: Nomi interpolates with `${...}`, not `$`",
			"to interpolate, write `\"${a + String.length(\"}{\")} and ${b}.\"`", 2, 37,
		},
		{
			"two names in one literal",
			"pub fn f(a: Int, b: Int) -> String\n    \"$a and $b and $a\" + \"${a}${b}\"\nend\n",
			"`$a` is text here, not the value of `a`: Nomi interpolates with `${...}`, not `$`",
			"to interpolate, write `\"${a} and ${b} and ${a}\"`", 2, 6,
		},
		{
			"past a slot of several lines that holds a string of a brace (a comment in a slot is a parse error)",
			"pub fn f(name: String, n: Int) -> String\n    \"\"\"\n    ${\n        String.length(\"}\") +\n            n} for $name\n    \"\"\" + name\nend\n",
			"`$name` is text here, not the value of `name`: Nomi interpolates with `${...}`, not `$`",
			"to interpolate, write `${name}`", 5, 20,
		},
		{
			"a triple-quoted string",
			"pub fn f(name: String) -> String\n    \"\"\"\n    Dear $name,\n    \"\"\" + name\nend\n",
			"`$name` is text here, not the value of `name`: Nomi interpolates with `${...}`, not `$`",
			"to interpolate, write `${name}`", 3, 10,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs, warns := dollarWarnings(t, tc.src)
			if len(errs) > 0 {
				t.Fatalf("the front end rejects this, which it must accept: %v", errs)
			}
			if len(warns) != 1 {
				t.Fatalf("want one warning, got %+v", warns)
			}
			w := warns[0]
			if w.Message != tc.message {
				t.Errorf("message:\n got %q\nwant %q", w.Message, tc.message)
			}
			if len(w.Hints) != 1 || w.Hints[0] != tc.help {
				t.Errorf("help:\n got %q\nwant %q", w.Hints, tc.help)
			}
			if w.Line != tc.line || w.Col != tc.col {
				t.Errorf("at %d:%d, want %d:%d", w.Line, w.Col, tc.line, tc.col)
			}
			fixed := applyFix(t, tc.src, w.Fix)
			errs, warns = dollarWarnings(t, fixed)
			if len(errs) > 0 || len(warns) > 0 {
				t.Errorf("the fixed program has errors %v and warnings %v:\n%s", errs, warns, fixed)
			}
		})
	}
}

// A `$` that names no value in scope, or that the source writes as plain
// text on purpose, checks with no warning.
func TestDollarWarning_DoesNotFire(t *testing.T) {
	cases := []struct{ name, src string }{
		{"a digit", "pub fn f(name: String) -> String\n    \"$5\" + name\nend\n"},
		{"a lone dollar", "pub fn f(name: String) -> String\n    \"$\" + name + \"a $ b\"\nend\n"},
		{"a name not in scope", "pub fn f(name: String) -> String\n    \"cost: $total\" + name\nend\n"},
		{"a longer name than the one in scope", "pub fn f(name: String) -> String\n    \"$names\" + name\nend\n"},
		{"a binding made after the string", "pub fn f() -> String\n    text = \"$later\"\n    later = \"x\"\n    text + later\nend\n"},
		{"a function's name", "pub fn f() -> String\n    \"$f\"\nend\n"},
		{"a type's name", "pub struct Total\n    n: Int\nend\n\npub fn f(t: Total) -> String\n    \"$Total ${t.n}\"\nend\n"},
		{"a raw string", "pub fn f(name: String) -> String\n    `Hello, $name` + name\nend\n"},
		{"a raw typed literal", "import std::regex.Regex\n\npub fn f(name: String) -> Bool\n    String.captures(name, Regex`^(a)$name`) == None\nend\n"},
		{"a quoted typed literal", "import std::regex.Regex\n\npub fn f(name: String) -> Bool\n    String.captures(name, Regex\"^(a)$name\") == None\nend\n"},
		{"the escape of an interpolation", "pub fn f(name: String) -> String\n    \"\\${name}\" + name\nend\n"},
		{"an interpolation", "pub fn f(name: String) -> String\n    \"${name}\"\nend\n"},
		{"the dollar written as an escape", "pub fn f(name: String) -> String\n    \"\\u{24}name\" + name\nend\n"},
		{"two dollars", "pub fn f(name: String) -> String\n    \"$$name\" + name\nend\n"},
		{"a MessageFormat variable", "pub fn f(name: String) -> String\n    \"Hello, {$name}\" + name\nend\n"},
		{"a backslash before it in a triple-quoted string", "pub fn f(name: String) -> String\n    \"\"\"\n    echo \\$name\n    \"\"\" + name\nend\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs, warns := dollarWarnings(t, tc.src)
			if len(errs) > 0 {
				t.Fatalf("the front end rejects this, which it must accept: %v", errs)
			}
			if len(warns) > 0 {
				t.Errorf("want no warning, got %+v", warns)
			}
		})
	}
}

// A binding whose only use is a `$name` in a string is never read, which is
// an error, and the error says why the string does not read it.
func TestDollarWarning_UnreadBindingSaysTheStringIsText(t *testing.T) {
	errs, _ := dollarWarnings(t, "pub fn f() -> String\n    name = \"Ada\"\n    \"Hello, $name\"\nend\n")
	e := expectUnusedBindingContaining(t, errs, "binding 'name' is never read")
	want := "`$name` in a string is text and reads nothing: Nomi interpolates with `${name}`"
	if len(e.Hints) != 2 || e.Hints[0] != want {
		t.Errorf("help:\n got %q\nwant %q first", e.Hints, want)
	}
	// A `$` an escape writes is no `$name`, so the error has its usual help
	// alone.
	errs, _ = dollarWarnings(t, "pub fn f() -> String\n    name = \"Ada\"\n    \"Hello, \\u{24}name\"\nend\n")
	e = expectUnusedBindingContaining(t, errs, "binding 'name' is never read")
	if len(e.Hints) != 1 || strings.Contains(e.Hints[0], "$") {
		t.Errorf("help %q, want the usual one alone", e.Hints)
	}
	// Without the string, the error has its usual help alone.
	errs, _ = dollarWarnings(t, "pub fn f() -> String\n    name = \"Ada\"\n    \"Hello\"\nend\n")
	e = expectUnusedBindingContaining(t, errs, "binding 'name' is never read")
	if len(e.Hints) != 1 || strings.Contains(e.Hints[0], "$") {
		t.Errorf("help %q, want the usual one alone", e.Hints)
	}
}
