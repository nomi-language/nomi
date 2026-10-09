package format

import "testing"

// A comment after the last arm of a `case` stage that has a subject stays
// before its `}`.
func TestFormat_CaseStageKeepsClosingComment(t *testing.T) {
	src := "fn f(input: String): Int {\n" +
		"    input\n" +
		"    |> case parse_id() {\n" +
		"        Ok(id) -> id\n" +
		"        Err(_) -> 0\n" +
		"        // trailing\n" +
		"    }\n" +
		"}\n"
	want := "fn f(input: String): Int {\n" +
		"    input\n" +
		"    |> case parse_id() {\n" +
		"            Ok(id) -> id\n" +
		"            Err(_) -> 0\n" +
		"            // trailing\n" +
		"        }\n" +
		"}\n"
	formatsKeepingMeaning(t, src, want)
}
