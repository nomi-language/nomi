package format

import (
	"strings"
	"testing"
)

// A literal `${` is written `\${`; `$`, `$$`, `#`, `##` and `#{` are
// ordinary text and stay as written.
func TestFormatEscapesOnlyTheInterpolationOpener(t *testing.T) {
	source := `value = "$ \${old} $$ # ## #{new}"` + "\n"
	got := formatOnce(t, source)
	if got != source {
		t.Fatalf("got %q, want %q", got, source)
	}
	if again := formatOnce(t, got); again != got {
		t.Fatalf("not idempotent: %q", again)
	}
	if firstStringValue(t, source) != "$ ${old} $$ # ## #{new}" {
		t.Fatalf("decoded %q", firstStringValue(t, source))
	}
}

// A carriage return is written `\r`, as the formatter writes every other
// escape from the string's value: a `\u{d}` or `\u{D}` becomes `\r`.
func TestFormatWritesACarriageReturnAsR(t *testing.T) {
	for source, want := range map[string]string{
		"value = \"a\\rb\"\n":          "value = \"a\\rb\"\n",
		"value = \"a\\u{d}b\"\n":       "value = \"a\\rb\"\n",
		"value = \"a\\u{D}\\u{a}\"\n":  "value = \"a\\r\\n\"\n",
		"value = \"${n}\\u{000d}\"\n":  "value = \"${n}\\r\"\n",
		"value = Sql\"a\\u{d}${n}\"\n": "value = Sql\"a\\r${n}\"\n",
	} {
		got := formatOnce(t, source)
		if got != want {
			t.Errorf("Format(%q) = %q, want %q", source, got, want)
		}
	}
}

// An emoji typed into a string stays as typed, its zero-width joiners,
// variation selectors and regional indicators included, in a plain literal
// and beside an interpolation; a joiner that joins no emoji is written as
// its escape, so it stays visible.
func TestFormatKeepsAnEmojiAsTyped(t *testing.T) {
	for _, source := range []string{
		"value = \"dev 👩‍💻 ❤️ 🇵🇱 é\"\n",
		"value = \"${n} 👨‍👩‍👧\"\n",
		"value = \"a\\u{200d}b\"\n",
	} {
		got := formatOnce(t, source)
		if got != source {
			t.Fatalf("got %q, want %q", got, source)
		}
	}
	if got := formatOnce(t, "value = \"a‍b\"\n"); got != "value = \"a\\u{200d}b\"\n" {
		t.Fatalf("a lone joiner: got %q", got)
	}
}

// A joiner or non-joiner typed inside a word of a script that uses them
// stays as typed: a Devanagari conjunct and a Persian word.
func TestFormatKeepsAScriptJoinerAsTyped(t *testing.T) {
	for _, source := range []string{
		"value = \"क्‍ष\"\n",
		"value = \"می‌خواهم\"\n",
		"value = \"${n} می‌خواهم\"\n",
	} {
		if got := formatOnce(t, source); got != source {
			t.Fatalf("got %q, want %q", got, source)
		}
	}
	if got := formatOnce(t, "value = \"a‌b\"\n"); got != "value = \"a\\u{200c}b\"\n" {
		t.Fatalf("a non-joiner between ASCII letters: got %q", got)
	}
}

// A `$` written before an interpolation stays text.
func TestFormatDollarBeforeInterpolation(t *testing.T) {
	source := `value = "$${n} and $"` + "\n"
	if got := formatOnce(t, source); got != source {
		t.Fatalf("got %q, want %q", got, source)
	}
}

func TestFormatRawInterpolationTextStaysVerbatim(t *testing.T) {
	for _, source := range []string{
		"value = `#{x} ## ${x} \\${x} $$`\n",
		"value = Tag`#{x} ## ${x} \\${x} $$`\n",
		"value = `\n  #{x} ## ${x} \\${x} $$\n`\n",
		"value = Tag`\n  #{x} ## ${x} \\${x} $$\n`\n",
	} {
		got := formatOnce(t, source)
		if again := formatOnce(t, got); again != got {
			t.Fatalf("not idempotent: %q => %q", got, again)
		}
		if !strings.Contains(got, "#{x} ## ${x} \\${x} $$") {
			t.Fatalf("changed raw body: %q", got)
		}
	}
}

func TestFormatTypedInterpolationPreservesAST(t *testing.T) {
	for _, source := range []string{
		`value = Tag"# ${f(port)} \${old} $$"` + "\n",
		"value = Tag\"\"\"\n  # ${f(port)} \\${old} $$\n\"\"\"\n",
	} {
		formatted := formatOnce(t, source)
		if err := SameMeaning(source, formatted); err != nil {
			t.Fatalf("format changed typed literal: %q => %q: %v", source, formatted, err)
		}
		if again := formatOnce(t, formatted); again != formatted {
			t.Fatalf("not idempotent: %q", again)
		}
	}
}
