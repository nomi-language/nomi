package format

import "testing"

// A keyword stage is no `case` subject or `if` condition. The parser makes
// a condition-less `if` stage of `|> if ((is_big())) {`, which stands before a
// subject-less `case` stage, and the formatter once wrote it as the case's
// subject, `|> case if { ... } {`, which does not parse. The `if` keeps the
// condition stage it was written with, parentheses included.
func TestFormat_KeywordStageIsNoCaseSubject(t *testing.T) {
	src := "fn mixed(n: Int) -> String {\n" +
		"    n\n" +
		"    |> if ((is_big())) { Some(\"large\") } else { None }\n" +
		"    |> case {\n" +
		"        Some(word) -> word\n" +
		"        None -> \"no\"\n" +
		"    }\n" +
		"}\n"
	want := "fn mixed(n: Int) -> String {\n" +
		"    n\n" +
		"    |> if (is_big()) { Some(\"large\") } else { None }\n" +
		"    |> case {\n" +
		"        Some(word) -> word\n" +
		"        None -> \"no\"\n" +
		"    }\n" +
		"}\n"
	formatsKeepingMeaning(t, src, want)

	// A `?` stage is no `if` condition either.
	src = "fn f(n: Result<Bool, String>) -> Result<Int, String> {\n" +
		"    n\n" +
		"    |> Result.map_err(fn e -> e)?\n" +
		"    |> if { Ok(1) } else { Ok(2) }\n" +
		"}\n"
	formatsKeepingMeaning(t, src, src)
}
