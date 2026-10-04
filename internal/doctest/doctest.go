// Package doctest extracts fenced code blocks from Markdown and hidden
// language-tour output expectations.
package doctest

import (
	"errors"
	"fmt"
	"strings"
)

// Block is a fenced code block whose info string's first word matched the
// requested language.
type Block struct {
	Lang     string   // the language token, e.g. "nomi"
	Info     []string // remaining info-string words, e.g. ["ignore"]
	Code     string   // the block body, newline-joined, no trailing newline
	Line     int      // 1-based source line of the opening fence
	Expected []string // optional hidden `<!-- expect ... -->` output lines
}

// HasInfo reports whether word appears in the block's info string after the
// language token (e.g. "ignore").
func (b Block) HasInfo(word string) bool {
	for _, w := range b.Info {
		if w == word {
			return true
		}
	}
	return false
}

// ExtractBlocks returns every fenced ``` code block in markdown whose info
// string starts with lang. Non-matching fences (other languages, plain fences)
// are skipped.
func ExtractBlocks(markdown, lang string) []Block {
	var blocks []Block
	lines := strings.Split(markdown, "\n")
	for i := 0; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(trimmed, "```") {
			continue
		}
		info := strings.Fields(strings.TrimPrefix(trimmed, "```"))
		open := i
		i++
		var body []string
		for i < len(lines) && strings.TrimSpace(lines[i]) != "```" {
			body = append(body, lines[i])
			i++
		}
		// i now sits on the closing fence (or at EOF); the outer loop's i++
		// steps past it.
		if len(info) > 0 && info[0] == lang {
			expected, _ := parseHiddenExpect(lines, i+1)
			blocks = append(blocks, Block{
				Lang:     info[0],
				Info:     info[1:],
				Code:     strings.Join(body, "\n"),
				Line:     open + 1,
				Expected: expected,
			})
		}
	}
	return blocks
}

func parseHiddenExpect(lines []string, start int) ([]string, bool) {
	if start >= len(lines) {
		return nil, false
	}
	if strings.TrimSpace(lines[start]) != "<!-- expect" {
		return nil, false
	}
	var expected []string
	for i := start + 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "-->" {
			return expected, true
		}
		expected = append(expected, lines[i])
	}
	return expected, true
}

// CheckOutput compares captured program output against expected lines. It is a
// no-op (nil) when expected is empty. Otherwise it requires an exact
// line-by-line match and returns a descriptive error listing every mismatch.
func CheckOutput(expected []string, output string) error {
	if len(expected) == 0 {
		return nil
	}
	actual := strings.Split(strings.TrimRight(output, "\n"), "\n")
	if len(actual) != len(expected) {
		return fmt.Errorf("expected %d output lines, got %d\nexpected: %v\nactual:   %v",
			len(expected), len(actual), expected, actual)
	}
	var mismatches []string
	for i := range expected {
		if actual[i] != expected[i] {
			mismatches = append(mismatches,
				fmt.Sprintf("line %d: expected %q, got %q", i+1, expected[i], actual[i]))
		}
	}
	if len(mismatches) > 0 {
		return errors.New(strings.Join(mismatches, "\n"))
	}
	return nil
}
