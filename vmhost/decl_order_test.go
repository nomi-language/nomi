package vmhost

import (
	"regexp"
	"slices"
	"strings"
	"testing"
)

// Names resolve whole-program, so the order of a file's top-level
// declarations never changes what it means. TestDeclarationOrderDoesNotChangeLowering
// holds the IR builder to that: a seed whose files declare types, with every
// file's declarations reversed, lowers exactly when the seed does.
// internal/irbuild's TestDeclarationOrderDoesNotChangeAProgram runs one
// program in many orders and compares output.

var typeDeclLine = regexp.MustCompile(`(?m)^(?:pub )?(?:opaque )?(?:struct|enum|type) [A-Z]`)

// reverseDeclarations is src with each .nomi file's top-level items after its
// imports in reverse order, a comment directly above an item moving with it.
// An item starts at a line that begins in column 0 with a letter or `@`; the
// formatter puts everything inside a declaration deeper.
func reverseDeclarations(src string) (string, bool) {
	names, files, ok := splitInput(src)
	if !ok {
		return "", false
	}
	var b strings.Builder
	for _, name := range names {
		text := files[name]
		if strings.HasSuffix(name, ".nomi") {
			text = reverseFileItems(text)
		}
		if len(names) > 1 || name != "main.nomi" {
			b.WriteString("// FILE: " + name + "\n")
		}
		b.WriteString(text)
	}
	return b.String(), true
}

func reverseFileItems(text string) string {
	lines := strings.SplitAfter(text, "\n")
	var starts []int
	for i, l := range lines {
		if l == "" {
			continue
		}
		c := l[0]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '@') {
			continue
		}
		start := i
		for start > 0 && strings.HasPrefix(lines[start-1], "//") && !strings.HasPrefix(lines[start-1], "//#") {
			start--
		}
		if strings.HasPrefix(l, "import") {
			continue
		}
		starts = append(starts, start)
	}
	if len(starts) < 2 {
		return text
	}
	head := strings.Join(lines[:starts[0]], "")
	var items []string
	for k, s := range starts {
		end := len(lines)
		if k+1 < len(starts) {
			end = starts[k+1]
		}
		items = append(items, strings.TrimRight(strings.Join(lines[s:end], ""), " \t\n")+"\n")
	}
	slices.Reverse(items)
	return head + strings.Join(items, "\n")
}

func TestDeclarationOrderDoesNotChangeLowering(t *testing.T) {
	t.Setenv("NOMI_DEBUG_LOWERING", "1")
	checked, skipped := 0, 0
	for _, s := range loweringSeeds(t) {
		if len(typeDeclLine.FindAllString(s.src, 3)) < 2 {
			continue
		}
		rev, ok := reverseDeclarations(s.src)
		if !ok || rev == s.src {
			continue
		}
		orig := checkLowers(t.TempDir(), s.src)
		if !orig.accepted || orig.internal != "" {
			continue
		}
		reversed := checkLowers(t.TempDir(), rev)
		if !reversed.accepted {
			// The text split something the formatter left in column 0, such
			// as a multi-line string's line.
			skipped++
			continue
		}
		checked++
		if lowers, revLowers := len(orig.declines) == 0, len(reversed.declines) == 0 && reversed.internal == ""; lowers != revLowers {
			t.Errorf("%s: lowers=%v as written and %v with its declarations reversed\nas written: %v\nreversed: %v %s\n--- reversed ---\n%s",
				s.name, lowers, revLowers, orig.reasons, reversed.reasons, reversed.internal, rev)
		}
	}
	t.Logf("%d seeds checked in both orders; %d reversed seeds the front end rejects", checked, skipped)
	if checked < 50 {
		t.Errorf("only %d seeds checked; the item split should keep most seeds valid", checked)
	}
}
