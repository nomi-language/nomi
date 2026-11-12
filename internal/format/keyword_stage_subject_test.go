package format

import "testing"

// A keyword stage is no `case` subject or `if` condition. The parser makes
// a condition-less `if` stage of `|> if ((is_big())) then`, which stands
// before a subject-less `case` stage, and the formatter once wrote it as the
// case's subject, which does not parse. The `if` keeps the condition stage
// it was written with, parentheses included, and is written in block form,
// since an inline `else` branch would take the next stage in.
func TestFormat_KeywordStageIsNoCaseSubject(t *testing.T) {
	src := "fn mixed(n: Int) -> String\n" +
		"    n\n" +
		"    |> if ((is_big())) then\n" +
		"        Some(\"large\")\n" +
		"    else\n" +
		"        None\n" +
		"    end\n" +
		"    |> case\n" +
		"        Some(word) -> word\n" +
		"        None -> \"no\"\n" +
		"    end\n" +
		"end\n"
	want := "fn mixed(n: Int) -> String\n" +
		"    n\n" +
		"    |> if (is_big()) then\n" +
		"        Some(\"large\")\n" +
		"    else\n" +
		"        None\n" +
		"    end\n" +
		"    |> case\n" +
		"        Some(word) -> word\n" +
		"        None -> \"no\"\n" +
		"    end\n" +
		"end\n"
	formatsKeepingMeaning(t, src, want)

	// A `?` stage is no `if` condition either.
	src = "fn f(n: Result<Bool, String>) -> Result<Int, String>\n" +
		"    n\n" +
		"    |> Result.map_err(fn e -> e)?\n" +
		"    |> if then Ok(1) else Ok(2)\n" +
		"end\n"
	formatsKeepingMeaning(t, src, src)
}
