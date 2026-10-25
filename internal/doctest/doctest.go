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
	Info     []string // remaining info-string words, e.g. ["error"]
	Code     string   // the block body, newline-joined, no trailing newline
	Line     int      // 1-based source line of the opening fence
	Expected []string // optional hidden `<!-- expect ... -->` output lines
}

// The words a tour ```nomi-run fence may carry after its language.
const (
	// InfoError marks a block that fails on purpose, to compile or while it
	// runs. Its hidden `<!-- expect ... -->` block holds what the tour's
	// runner shows, the diagnostic or the output then the failure (Shown),
	// and the tour marks the block as an expected error.
	InfoError = "error"
	// InfoNoRun marks a block that must compile but whose output differs
	// from run to run (it reads the clock), so a doctest checks it and does
	// not run it.
	InfoNoRun = "no-run"
)

// HasInfo reports whether word appears in the block's info string after the
// language token (e.g. "error").
func (b Block) HasInfo(word string) bool {
	for _, w := range b.Info {
		if w == word {
			return true
		}
	}
	return false
}

// Runs reports whether a doctest runs the block and compares its output:
// it carries neither InfoError nor InfoNoRun.
func (b Block) Runs() bool {
	return !b.HasInfo(InfoError) && !b.HasInfo(InfoNoRun)
}

// UnknownInfo answers the block's info words that are neither InfoError nor
// InfoNoRun, so a misspelt word fails a doctest instead of being dropped.
func (b Block) UnknownInfo() []string {
	var unknown []string
	for _, w := range b.Info {
		if w != InfoError && w != InfoNoRun {
			unknown = append(unknown, w)
		}
	}
	return unknown
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

// Shown is a run's output panel as the tour's runner shows it
// (tour-client.mjs's showOutput): what the program printed, then, on the
// line after it, the failure that stopped it: a `todo` reached, a trap, or
// the `error: ` line of an `Err` from main. A block that stops on purpose
// holds this text in its expect block, so its failure is part of what a
// doctest compares.
func Shown(output, failure string) string {
	if failure == "" {
		return output
	}
	if output != "" && !strings.HasSuffix(output, "\n") {
		output += "\n"
	}
	return output + failure
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
