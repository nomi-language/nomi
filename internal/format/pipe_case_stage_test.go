package format

import (
	"strings"
	"testing"
)

// A piped `case` or `if` stays the stage it was written as: `|> g() |>
// case {` keeps `g()` a stage of its own, and `|> case g() {` keeps it the
// case's subject. Its arms or branches sit one indent in from the line the
// `|>` is on, and its `}` at that line's indent, as a block lambda stage's
// body does.
func TestFormat_PipedCaseStaysAsWritten(t *testing.T) {
	for name, src := range map[string]string{
		"bare case after a stage": "fn f(x: Int): Int {\n" +
			"    x |> g() |> case {\n" +
			"        6 -> 1\n" +
			"        _ -> 2\n" +
			"    }\n" +
			"}\n",
		"bare case": "fn f(x: Int): Int {\n" +
			"    x |> case {\n" +
			"        6 -> 1\n" +
			"        _ -> 2\n" +
			"    }\n" +
			"}\n",
		"case with a subject stage": "fn f(x: Int): Int {\n" +
			"    x |> case g() {\n" +
			"        6 -> 1\n" +
			"        _ -> 2\n" +
			"    }\n" +
			"}\n",
		"parenthesized subject stage": "fn f(x: Int): Int {\n" +
			"    x |> case (x) {\n" +
			"        6 -> 1\n" +
			"        _ -> 2\n" +
			"    }\n" +
			"}\n",
		"bound": "fn f(x: Int): Int {\n" +
			"    y = x |> g() |> case {\n" +
			"        6 -> 1\n" +
			"        _ -> 2\n" +
			"    }\n" +
			"\n" +
			"    y\n" +
			"}\n",
		"bound with a subject stage": "fn f(x: Int): Int {\n" +
			"    y = x |> case g() {\n" +
			"        6 -> 1\n" +
			"        _ -> 2\n" +
			"    }\n" +
			"\n" +
			"    y\n" +
			"}\n",
		"stage after it": "fn f(x: Int): Int {\n" +
			"    x |> case {\n" +
			"        6 -> 1\n" +
			"        _ -> 2\n" +
			"    } |> g()\n" +
			"}\n",
		"stacked": "fn f(x: Int): Int {\n" +
			"    x\n" +
			"    |> g()\n" +
			"    |> case {\n" +
			"        6 -> 1\n" +
			"        _ -> 2\n" +
			"    }\n" +
			"    |> g()\n" +
			"}\n",
		"stacked with a subject stage": "fn f(x: Int): Int {\n" +
			"    x\n" +
			"    |> case g() {\n" +
			"        6 -> 1\n" +
			"        _ -> 2\n" +
			"    }\n" +
			"    |> g()\n" +
			"}\n",
		"stacked and bound": "fn f(x: Int): Int {\n" +
			"    y =\n" +
			"        x\n" +
			"        |> g()\n" +
			"        |> case {\n" +
			"            6 -> 1\n" +
			"            _ -> 2\n" +
			"        }\n" +
			"\n" +
			"    y\n" +
			"}\n",
		"under assert": "fn main() {\n" +
			"    assert 5\n" +
			"        |> case {\n" +
			"            5 -> True\n" +
			"            _ -> False\n" +
			"        }\n" +
			"}\n",
		"case in an arm": "fn f(x: Int): Int {\n" +
			"    x |> case {\n" +
			"        6 -> x |> g() |> case {\n" +
			"            7 -> 1\n" +
			"            _ -> 2\n" +
			"        }\n" +
			"        _ -> 3\n" +
			"    }\n" +
			"}\n",
		"comments": "fn f(x: Int): Int {\n" +
			"    x\n" +
			"    // before the subject stage\n" +
			"    |> g()\n" +
			"    // before the case\n" +
			"    |> case {\n" +
			"        // first arm\n" +
			"        6 -> 1 // one\n" +
			"        _ -> 2\n" +
			"        // after the arms\n" +
			"    }\n" +
			"}\n",
		"comments with a subject stage": "fn f(x: Int): Int {\n" +
			"    x\n" +
			"    // before the case\n" +
			"    |> case g() {\n" +
			"        6 -> 1 // one\n" +
			"        _ -> 2\n" +
			"        // after the arms\n" +
			"    }\n" +
			"}\n",
	} {
		t.Run(name, func(t *testing.T) {
			formatsKeepingMeaning(t, src, src)
		})
	}
}

// The arms of a piped `case` written deeper than one indent come back to it.
func TestFormat_PipedCaseArmsIndentOneLevel(t *testing.T) {
	src := "fn f(x: Int): Int {\n" +
		"    y = x |> case g() {\n" +
		"                6 -> 1\n" +
		"                _ -> 2\n" +
		"            }\n" +
		"    x |> case {\n" +
		"            6 -> 1\n" +
		"            _ -> 2\n" +
		"        }\n" +
		"}\n"
	want := "fn f(x: Int): Int {\n" +
		"    y = x |> case g() {\n" +
		"        6 -> 1\n" +
		"        _ -> 2\n" +
		"    }\n" +
		"\n" +
		"    x |> case {\n" +
		"        6 -> 1\n" +
		"        _ -> 2\n" +
		"    }\n" +
		"}\n"
	formatsKeepingMeaning(t, src, want)
}

// An inline pipeline too long for its line stacks one stage per line, and
// each `case` stays the stage it was written as, on its own line.
func TestFormat_PipedCaseInABrokenPipeline(t *testing.T) {
	long := strings.Repeat("a", 30)
	for _, c := range []struct{ name, stage string }{
		{"bare", "case"},
		{"with a subject stage", "case Int.add(" + long + ")"},
	} {
		t.Run(c.name, func(t *testing.T) {
			head := "Int.add(" + long + ") |> Int.add(" + long + ")"
			if c.stage == "case" {
				head += " |> Int.add(" + long + ")"
			}
			src := "fn f(x: Int): Int {\n" +
				"    x |> " + head + " |> " + c.stage + " {\n" +
				"        6 -> 1\n" +
				"        _ -> 2\n" +
				"    }\n" +
				"}\n"
			want := "fn f(x: Int): Int {\n" +
				"    x\n" +
				"    |> Int.add(" + long + ")\n" +
				"    |> Int.add(" + long + ")\n"
			if c.stage == "case" {
				want += "    |> Int.add(" + long + ")\n"
			}
			want += "    |> " + c.stage + " {\n" +
				"        6 -> 1\n" +
				"        _ -> 2\n" +
				"    }\n" +
				"}\n"
			formatsKeepingMeaning(t, src, want)
		})
	}
}

// A piped `if` stays the stage it was written as too, and its broken
// branches sit one indent in from the `|>` line.
func TestFormat_PipedIfStaysAsWritten(t *testing.T) {
	for name, src := range map[string]string{
		"bare if after a stage": "fn f(x: Int): Int {\n" +
			"    x |> g() |> if { 1 } else { 2 }\n" +
			"}\n",
		"if with a condition stage": "fn f(x: Int): Int {\n" +
			"    x |> if g() { 1 } else { 2 }\n" +
			"}\n",
		"parenthesized condition stage": "fn f(x: Int): Int {\n" +
			"    x |> if (g()) { 1 } else { 2 }\n" +
			"}\n",
		"broken branches": "fn main(): String {\n" +
			"    \"Ada\"\n" +
			"    |> if String.contains?(\"A\") {\n" +
			"        \"initialed\"\n" +
			"    } else {\n" +
			"        \"plain\"\n" +
			"    }\n" +
			"    |> dbg\n" +
			"}\n",
		"broken branches after a stage": "fn main(): String {\n" +
			"    \"Ada\"\n" +
			"    |> String.contains?(\"A\")\n" +
			"    |> if {\n" +
			"        \"the name has an initial\"\n" +
			"    } else {\n" +
			"        \"the name is plain\"\n" +
			"    }\n" +
			"}\n",
	} {
		t.Run(name, func(t *testing.T) {
			formatsKeepingMeaning(t, src, src)
		})
	}
}

// SameMeaning tells the two spellings apart, so the format meaning checks
// fail if the formatter rewrites one into the other.
func TestSameMeaning_SeesAPipedCaseRespelled(t *testing.T) {
	arms := " {\n    6 -> 1\n    _ -> 2\n}\n"
	for _, c := range []struct{ a, b string }{
		{"y = x |> g() |> case" + arms, "y = x |> case g()" + arms},
		{"y = x |> g() |> if { 1 } else { 2 }\n", "y = x |> if g() { 1 } else { 2 }\n"},
	} {
		if err := SameMeaning(c.a, c.b); err == nil {
			t.Errorf("SameMeaning takes these for the same program:\n%s\n%s", c.a, c.b)
		}
		if err := SameMeaning(c.b, c.a); err == nil {
			t.Errorf("SameMeaning takes these for the same program:\n%s\n%s", c.b, c.a)
		}
	}
}
