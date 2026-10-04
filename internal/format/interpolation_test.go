package format

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
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
		original, err := parser.Parse(lexer.Lex(source))
		if err != nil {
			t.Fatal(err)
		}
		formatted := formatOnce(t, source)
		result, err := parser.Parse(lexer.Lex(formatted))
		if err != nil {
			t.Fatal(err)
		}
		if !astEquivalent(original, result) {
			t.Fatalf("format changed typed literal: %q => %q", source, formatted)
		}
		if again := formatOnce(t, formatted); again != formatted {
			t.Fatalf("not idempotent: %q", again)
		}
	}
}
