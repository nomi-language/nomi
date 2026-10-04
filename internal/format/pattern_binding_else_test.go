package format

import (
	"strings"
	"testing"
)

// formatTwice formats src and requires the result to be a fixed point.
func formatTwice(t *testing.T, src string) string {
	t.Helper()
	got, err := Format(src)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	again, err := Format(got)
	if err != nil {
		t.Fatalf("Format (second pass): %v", err)
	}
	if again != got {
		t.Errorf("not idempotent:\nfirst:\n%s\nsecond:\n%s", got, again)
	}
	return got
}

func TestFormat_BindingElse_ShortBlockStaysOnOneLine(t *testing.T) {
	for _, src := range []string{
		"fn f(m: Maybe<String>): String {\n    Some(e) = m else { \"none\" }\n    e\n}\n",
		"fn f(xs: List<Int>): Int {\n    [first, .._rest] = xs else { return -1 }\n    first\n}\n",
		"fn f() {\n    Iter.each(lines, |line| {\n        Ok(entry) = parse(line) else { continue }\n        record(entry)\n    })\n}\n",
	} {
		if got := formatTwice(t, src); got != src {
			t.Errorf("got:\n%s\nwant:\n%s", got, src)
		}
	}
}

func TestFormat_BindingElse_ShortBlockWrittenBrokenCollapses(t *testing.T) {
	src := "fn f(m: Maybe<String>): String {\n    Some(e) = m\n    else {\n        \"none\"\n    }\n    e\n}\n"
	want := "fn f(m: Maybe<String>): String {\n    Some(e) = m else { \"none\" }\n    e\n}\n"
	if got := formatTwice(t, src); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// The whole statement counts toward the one-line width, as rustfmt's
// single_line_let_else_max_width does: 50 columns stays, 51 breaks.
func TestFormat_BindingElse_WidthBoundary(t *testing.T) {
	// `Some(e) = m else { ` is 19 columns and ` }` is 2.
	at50 := "Some(e) = m else { " + strings.Repeat("a", 29) + " }\n"
	if got := formatTwice(t, at50); got != at50 {
		t.Errorf("want one line at 50 columns, got:\n%s", got)
	}
	at51 := "Some(e) = m else { " + strings.Repeat("a", 30) + " }\n"
	want := "Some(e) = m else {\n    " + strings.Repeat("a", 30) + "\n}\n"
	if got := formatTwice(t, at51); got != want {
		t.Errorf("want broken at 51 columns, got:\n%s", got)
	}
}

func TestFormat_BindingElse_LongBlockBreaks(t *testing.T) {
	src := "fn f(id: Int, m: Maybe<String>): Result<String, String> {\n    Some(email) = m else { return Err(\"user ${id} has no email address on file\") }\n    Ok(email)\n}\n"
	want := "fn f(id: Int, m: Maybe<String>): Result<String, String> {\n    Some(email) = m else {\n        return Err(\"user ${id} has no email address on file\")\n    }\n\n    Ok(email)\n}\n"
	if got := formatTwice(t, src); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestFormat_BindingElse_MultiStatementBlockBreaks(t *testing.T) {
	src := "fn f(m: Maybe<Int>): Int {\n    Some(n) = m else { io.print(\"none\")\n return 0 }\n    n\n}\n"
	want := "fn f(m: Maybe<Int>): Int {\n    Some(n) = m else {\n        io.print(\"none\")\n        return 0\n    }\n\n    n\n}\n"
	if got := formatTwice(t, src); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// Arms are laid out as a case's: on consecutive lines when every arm fits,
// each broken with a blank line between them otherwise.
func TestFormat_BindingElse_Arms(t *testing.T) {
	src := "fn f(r: Result<Int, E>): Result<Int, String> {\n    Ok(port) = r else { Err(.Empty) -> 8080\n Err(e) -> return Err(\"bad port\") }\n    Ok(port)\n}\n"
	want := "fn f(r: Result<Int, E>): Result<Int, String> {\n    Ok(port) = r else {\n        Err(.Empty) -> 8080\n        Err(e) -> return Err(\"bad port\")\n    }\n\n    Ok(port)\n}\n"
	if got := formatTwice(t, src); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}

	broken := "fn f(r: Result<Int, E>): Result<Int, String> {\n    Ok(port) = r else {\n        Err(.Empty) -> {\n            io.print(\"empty\")\n            8080\n        }\n        Err(e) -> return Err(\"bad port\")\n    }\n    Ok(port)\n}\n"
	wantBroken := "fn f(r: Result<Int, E>): Result<Int, String> {\n    Ok(port) = r else {\n        Err(.Empty) -> {\n            io.print(\"empty\")\n            8080\n        }\n\n        Err(e) ->\n            return Err(\"bad port\")\n    }\n\n    Ok(port)\n}\n"
	if got := formatTwice(t, broken); got != wantBroken {
		t.Errorf("got:\n%s\nwant:\n%s", got, wantBroken)
	}
}

func TestFormat_BindingElse_ArmComments(t *testing.T) {
	src := "fn f(r: Result<Int, E>): Int {\n    Ok(n) = r else {\n        // nothing there\n        Err(.Empty) -> 0 // default\n        Err(_) -> return -1\n        // done\n    }\n\n    n\n}\n"
	if got := formatTwice(t, src); got != src {
		t.Errorf("got:\n%s\nwant:\n%s", got, src)
	}
}

// A binding without else whose pattern no destructure statement spells keeps
// its pattern and value.
func TestFormat_BindingElse_GeneralPatternWithoutElse(t *testing.T) {
	src := "fn f(r: Result<(Int, Int), String>): Int {\n    Ok((w, h)) = r\n    w * h\n}\n"
	if got := formatTwice(t, src); got != src {
		t.Errorf("got:\n%s\nwant:\n%s", got, src)
	}
}

// A pipe value that fits stays on the binding's line. One that breaks after
// `=` puts the else on its own line at the statement's indent.
func TestFormat_BindingElse_PipeValue(t *testing.T) {
	src := "fn f(text: String): Int {\n    Ok(n) = text |> String.trim() |> Int.parse() else {\n        return 0\n    }\n\n    n\n}\n"
	if got := formatTwice(t, src); got != src {
		t.Errorf("got:\n%s\nwant:\n%s", got, src)
	}
	long := "fn f(text: String): Int {\n    Ok(n) = text |> String.trim() |> String.replace(\"aaaaaaaaaaaaaaa\", \"bbbbbbbbbbbbbbbbbbbbbbbbbbb\") |> Int.parse() else { return 0 }\n    n\n}\n"
	want := "fn f(text: String): Int {\n    Ok(n) =\n        text\n        |> String.trim()\n        |> String.replace(\"aaaaaaaaaaaaaaa\", \"bbbbbbbbbbbbbbbbbbbbbbbbbbb\")\n        |> Int.parse()\n    else {\n        return 0\n    }\n\n    n\n}\n"
	if got := formatTwice(t, long); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}
