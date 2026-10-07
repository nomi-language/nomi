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
