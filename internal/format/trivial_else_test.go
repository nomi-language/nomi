package format

import "testing"

// A two-branch `if` whose `else` is one atom (a name, a qualified name, a
// literal, a variant with no payload, an empty collection) is limited by the
// line, not by the 50 columns of two branches side by side; one whose
// `else` does work still breaks past 50.
func TestFormat_TrivialElseIsLimitedByTheLine(t *testing.T) {
	src := "fn f(name: String, n: Int) -> Maybe<String>\n" +
		"    label = if String.length(name) < 3 then Some(\"too short\") else None\n" +
		"    named = if String.length(name) < 3 then Some(\"too short\") else Maybe.None\n" +
		"    count = if String.length(name) > 3 then n * 10 + String.length(name) else 0\n" +
		"    title = if String.length(name) > 3 then String.to_upper(name) else \"\"\n" +
		"    items = if String.length(name) > 3 then [name, name, name, name] else []\n" +
		"    both = if String.length(name) < 3 then Some(\"too short\") else Some(name)\n" +
		"    label\n" +
		"end\n"
	want := "fn f(name: String, n: Int) -> Maybe<String>\n" +
		"    label = if String.length(name) < 3 then Some(\"too short\") else None\n" +
		"    named = if String.length(name) < 3 then Some(\"too short\") else Maybe.None\n" +
		"    count = if String.length(name) > 3 then n * 10 + String.length(name) else 0\n" +
		"    title = if String.length(name) > 3 then String.to_upper(name) else \"\"\n" +
		"    items = if String.length(name) > 3 then [name, name, name, name] else []\n" +
		"\n" +
		"    both = if String.length(name) < 3 then\n" +
		"        Some(\"too short\")\n" +
		"    else\n" +
		"        Some(name)\n" +
		"    end\n" +
		"\n" +
		"    label\n" +
		"end\n"
	formatsKeepingMeaning(t, src, want)
}
