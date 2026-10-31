package analysis_test

import (
	"strings"
	"testing"
)

// literalTypeOf is the type the checker gives expr, read off the mismatch it
// reports when expr is bound to an Int.
func literalTypeOf(t *testing.T, expr string) string {
	t.Helper()
	src := "import std/regex.Regex\n\nfn f(w: String) -> Unit {\n  a: Int = " + expr + "\n  _ = a\n}\n"
	for _, e := range checkTypedLiteralSource(src) {
		if _, got, found := strings.Cut(e.Message, ", got "); found {
			return got
		}
	}
	t.Fatalf("%s: binding it to an Int reported no mismatch", expr)
	return ""
}

// A Regex literal's type names its capture groups, computed from a fixed
// pattern; a regex compiled while the program runs lists them.
func TestRegexLiteral_TypeNamesItsCaptureGroups(t *testing.T) {
	for _, tc := range []struct{ expr, want string }{
		{"Regex`\\d+`", "Regex<Unit>"},
		{"Regex`v(\\d+)`", "Regex<String>"},
		{"Regex`a(b)?c`", "Regex<Maybe<String>>"},
		{"Regex`(\\d+)-(\\w+)?`", "Regex<#(String, Maybe<String>)>"},
		{"Regex`(a)|(b)`", "Regex<#(Maybe<String>, Maybe<String>)>"},
		{"Regex`(a)+(b)*`", "Regex<#(String, Maybe<String>)>"},
		{"Regex`(?:(a))?(b)`", "Regex<#(Maybe<String>, String)>"},
		{"Regex`((a)?)`", "Regex<#(String, Maybe<String>)>"},
		{"Regex`(?P<year>\\d{4})-(?P<month>\\d\\d)?`", "Regex<#{year: String, month: Maybe<String>}>"},
		{"Regex`(?P<year>\\d{4})`", "Regex<#{year: String}>"},
		{"Regex`(?P<key>\\w+)=(\\w+)`", "Regex<#(String, String)>"},
		// A name a field cannot have, or a keyword, makes a tuple.
		{"Regex`(?P<Key>\\w+)=(?P<value>\\w+)`", "Regex<#(String, String)>"},
		{"Regex`(?P<fn>\\w+)=(?P<value>\\w+)`", "Regex<#(String, String)>"},
		// A double-quoted literal with no interpolation is computed too,
		// inside its Result.
		{"Regex\"(\\\\d+)-(x)?\"", "Result<Regex<#(String, Maybe<String>)>, String>"},
		// An interpolated literal's groups are known only when it runs.
		{"Regex\"(${w})\"", "Result<Regex<List<Maybe<String>>>, String>"},
		{"Regex.compile(\"(a)\")", "Result<Regex<List<Maybe<String>>>, String>"},
		// An invalid pattern keeps the handler's type; a double-quoted one
		// fails when it runs.
		{"Regex\"(\"", "Result<Regex<List<Maybe<String>>>, String>"},
	} {
		if got := literalTypeOf(t, tc.expr); got != tc.want {
			t.Errorf("%s is a %s, want %s", tc.expr, got, tc.want)
		}
	}
}

// The capture type flows through String.captures and captures_all, and a
// function that does not read groups takes a Regex of any capture type.
func TestRegexLiteral_CapturesAnswerTheCaptureType(t *testing.T) {
	src := "import std/regex.Regex\n\n" +
		"fn first<C>(re: Regex<C>, text: String) -> Maybe<C> {\n" +
		"  String.captures(text, re)\n" +
		"}\n\n" +
		"fn f() -> Unit {\n" +
		"  a: Maybe<#(String, Maybe<String>)> = String.captures(\"x\", Regex`(\\w)(\\d)?`)\n" +
		"  b: List<#{key: String, value: String}> = String.captures_all(\"a=1\", Regex`(?P<key>\\w)=(?P<value>\\d)`)\n" +
		"  c: Maybe<String> = first(Regex`(\\d)`, \"1\")\n" +
		"  d: Maybe<List<Maybe<String>>> = String.captures(\"x\", Regex.compile(\"(x)\")!)\n" +
		"  e: Bool = String.contains(\"x\", Regex`(x)`) and String.contains(\"x\", Regex.compile(\"x\")!)\n" +
		"  g: String = Regex.pattern(Regex`(?P<n>x)`)\n" +
		"  _ = #(a, b, c, d, e, g)\n" +
		"}\n"
	if errs := checkTypedLiteralSource(src); len(errs) != 0 {
		t.Fatalf("the capture types are rejected: %v", errs)
	}
	for _, mistyped := range []string{
		"  a: Maybe<#(String, String)> = String.captures(\"x\", Regex`(\\w)(\\d)?`)\n",
		"  a: Maybe<List<Maybe<String>>> = String.captures(\"x\", Regex`(\\w)`)\n",
		"  a: Maybe<String> = String.captures(\"x\", Regex.compile(\"(x)\")!)\n",
	} {
		src := "import std/regex.Regex\n\nfn f() -> Unit {\n" + mistyped + "  _ = a\n}\n"
		if errs := checkTypedLiteralSource(src); len(errs) == 0 {
			t.Errorf("%q is admitted", strings.TrimSpace(mistyped))
		}
	}
}

// A Regex's capture type is phantom, so asking for Display or Debug of a
// Regex<C> does not ask for them of C (a struct or Unit has no Display).
func TestRegexLiteral_DisplayDoesNotDemandTheCaptureType(t *testing.T) {
	src := "import std/regex.Regex\n\nfn f() -> String {\n" +
		"  Regex.to_string(Regex`x`) + Display.to_string(Regex`(?P<a>x)`) + \"${Regex`(?P<b>y)`}\"\n" +
		"}\n"
	if errs := checkTypedLiteralSource(src); len(errs) != 0 {
		t.Fatalf("displaying a regex is rejected: %v", errs)
	}
}
