package analysis_test

import "testing"

// A typed literal written where another type is expected is an error whose
// help says the literal is what its prefix's handler builds, not that type,
// and, where a String is expected, names the function that gives the
// value's text when the type has one (typedLiteralHint).
func TestTypedLiteralMismatchHint(t *testing.T) {
	const decls = `import
    std::html.Html
    std::calendar.Date
    std::literals.{Fragment, Literal}
end

struct Box
    contents: String
end

impl Literal for Box
    fn from_fragments(fragments: List<Fragment<String>>) -> Box
        Box{contents: ""}
    end
end

fn show(s: String) -> String
    s
end

fn count(n: Int) -> Int
    n
end

`
	for _, tc := range []struct {
		name, body, want string
	}{
		{"an Html literal bound as a String", `    name = "x"
    s: String = Html"<p>${name}</p>"
    _ = s`,
			"type mismatch: expected String, got Html\nhelp: a typed literal is its prefix's type (`Html`), not a String; `Html.to_string` gives its text"},
		{"an Html literal passed as a String", `    _ = show(Html"<b>hi</b>")`,
			"argument 1: expected String, got Html\nhelp: a typed literal is its prefix's type (`Html`), not a String; `Html.to_string` gives its text"},
		{"a raw Date literal, which is a Date", "    s: String = Date`2026-05-04`\n    _ = s",
			"type mismatch: expected String, got Date\nhelp: a typed literal is its prefix's type (`Date`), not a String; `Display.to_string` gives its text"},
		{"a quoted Date literal without slots, which is a Date", "    s: String = Date\"2026-05-04\"\n    _ = s",
			"type mismatch: expected String, got Date\nhelp: a typed literal is its prefix's type (`Date`), not a String; `Display.to_string` gives its text"},
		{"a Date literal with a slot, which is a Result", `    month = "05"
    s: String = Date"2026-${month}-04"
    _ = s`,
			"type mismatch: expected String, got Result<Date, Error>\nhelp: a typed literal is what `Date`'s `Literal` impl builds (`Result<Date, Error>`), not a String"},
		{"a program's literal with no text function", `    _ = show(Box"hi")`,
			"argument 1: expected String, got Box\nhelp: a typed literal is its prefix's type (`Box`), not a String"},
		{"a literal where an Int is expected", `    _ = count(Html"<b>hi</b>")`,
			"argument 1: expected Int, got Html\nhelp: a typed literal is its prefix's type (`Html`), not an `Int`"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			errs := checkTypedLiteralProject(t, decls+"fn main()\n"+tc.body+"\nend\n", nil)
			expectExactDiag(t, errs, tc.want)
		})
	}
}
