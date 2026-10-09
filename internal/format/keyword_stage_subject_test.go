package format

import "testing"

// A keyword stage is no `case` subject or `if` condition. A parenthesized
// `if` condition stays a stage of its own, which left a condition-less `if`
// stage before a subject-less `case` stage, and the formatter wrote it as
// the case's subject, `|> case if { ... } {`, which does not parse.
func TestFormat_KeywordStageIsNoCaseSubject(t *testing.T) {
	src := "fn mixed(n: Int): String {\n" +
		"    n\n" +
		"    |> if ((big?())) { Some(\"large\") } else { None }\n" +
		"    |> case {\n" +
		"        Some(word) -> word\n" +
		"        None -> \"no\"\n" +
		"    }\n" +
		"}\n"
	want := "fn mixed(n: Int): String {\n" +
		"    n\n" +
		"    |> (big?())\n" +
		"    |> if { Some(\"large\") } else { None }\n" +
		"    |> case {\n" +
		"            Some(word) -> word\n" +
		"            None -> \"no\"\n" +
		"        }\n" +
		"}\n"
	formatsKeepingMeaning(t, src, want)

	// A bare `try` stage is no `if` condition either.
	src = "fn f(n: Result<Bool, String>): Result<Int, String> {\n" +
		"    n\n" +
		"    |> try\n" +
		"    |> if { Ok(1) } else { Ok(2) }\n" +
		"}\n"
	formatsKeepingMeaning(t, src, src)
}
