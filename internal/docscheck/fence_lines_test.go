package docscheck

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoLineStartsWithAnInlineBacktickSpan fails for a line of a tracked
// Markdown file under docs/ or tour/ that begins with three backticks and
// is not a code fence: an inline span quoted in triple backticks, such as
// an error message that itself holds backticks, put at the start of a line.
//
// Markdown renders such a line as inline code, but every reader in this
// repository that tracks fences line by line (the doc-example format check,
// the spec example parser, the llms files' HTML check) takes it for a fence
// and reads every later block inverted. An even number of such lines hides
// the inversion past the last of them; the spec once held four, and its
// integer division example went unformatted and unchecked between two of
// them. Put the span on the line before it.
func TestNoLineStartsWithAnInlineBacktickSpan(t *testing.T) {
	root, files := trackedFilesMatching(t, "docs/*.md", "tour/src/content/docs/*.md", "tour/src/content/docs/**/*.md", "*.md")
	for _, rel := range files {
		src, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(bytes.NewReader(src))
		sc.Buffer(make([]byte, 0, 1<<20), 1<<24)
		for n := 1; sc.Scan(); n++ {
			if line := strings.TrimSpace(sc.Text()); inlineSpanAtLineStart(line) {
				t.Errorf("%s:%d: the line begins with an inline span in triple backticks, which fence readers take for a code fence; put it on the line before", rel, n)
			}
		}
	}
}

// inlineSpanAtLineStart reports whether line, already trimmed, begins with
// three backticks and closes them again on the same line: an inline span,
// where a fence's line holds only the fence and an info string.
func inlineSpanAtLineStart(line string) bool {
	if !strings.HasPrefix(line, "```") {
		return false
	}
	rest := strings.TrimLeft(line, "`")
	return strings.Contains(rest, "```")
}

func TestInlineSpanAtLineStart(t *testing.T) {
	for line, want := range map[string]bool{
		"```":                            false,
		"```nomi":                        false,
		"```nomi-run error":              false,
		"````":                           false,
		"``` `a` is b ```.":              true,
		"``` `a` is b ``` with the help": true,
		"is ``` `a` is b ```.":           false,
		"`` a ``":                        false,
	} {
		if got := inlineSpanAtLineStart(line); got != want {
			t.Errorf("inlineSpanAtLineStart(%q) = %v, want %v", line, got, want)
		}
	}
}
