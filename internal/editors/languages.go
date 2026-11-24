package editors

import (
	"errors"
	"strings"
)

// The lines that mark Nomi's block in Helix's languages.toml. An install
// writes its entries between them and touches nothing else in the file.
const (
	blockBegin = "# BEGIN NOMI HELIX SUPPORT"
	blockEnd   = "# END NOMI HELIX SUPPORT"
)

// withHelixBlock is the languages.toml text with Nomi's block, whose
// grammar source is grammar: in place of the block text has, or after
// the rest, a blank line between.
func withHelixBlock(text, grammar string) (string, error) {
	entry, err := helixEntry(grammar)
	if err != nil {
		return "", err
	}
	block := blockBegin + "\n" + entry + blockEnd + "\n"
	start, end, found, err := findBlock(text)
	switch {
	case err != nil:
		return "", err
	case found:
		return text[:start] + block + text[end:], nil
	case text == "":
		return block, nil
	case !strings.HasSuffix(text, "\n"):
		text += "\n"
	}
	return text + "\n" + block, nil
}

// withoutHelixBlock is text with Nomi's block and the blank line before
// it removed, so a file that ended in a newline before the install reads
// as it did; found says whether there was a block.
func withoutHelixBlock(text string) (out string, found bool, err error) {
	start, end, found, err := findBlock(text)
	if err != nil || !found {
		return text, false, err
	}
	before := text[:start]
	if strings.HasSuffix(before, "\n\n") {
		before = before[:len(before)-1]
	}
	return before + text[end:], true, nil
}

// findBlock is the span of text's block: from the start of its BEGIN line
// to the end of its END line, the line break included.
func findBlock(text string) (start, end int, found bool, err error) {
	start = lineAt(text, blockBegin, 0)
	if start < 0 {
		if lineAt(text, blockEnd, 0) >= 0 {
			return 0, 0, false, errors.New("it has a `" + blockEnd + "` line and no `" + blockBegin + "` line before it; fix the block by hand")
		}
		return 0, 0, false, nil
	}
	e := lineAt(text, blockEnd, start)
	if e < 0 {
		return 0, 0, false, errors.New("it has a `" + blockBegin + "` line and no `" + blockEnd + "` line after it; fix the block by hand")
	}
	end = e + len(blockEnd)
	if i := strings.IndexByte(text[end:], '\n'); i >= 0 {
		end += i + 1
	} else {
		end = len(text)
	}
	return start, end, true, nil
}

// lineAt is the offset of the first line at or after from that is marker,
// with nothing after it but spaces and a carriage return, or -1.
func lineAt(text, marker string, from int) int {
	for i := from; i <= len(text); {
		j := strings.Index(text[i:], marker)
		if j < 0 {
			return -1
		}
		at := i + j
		rest := text[at+len(marker):]
		if k := strings.IndexByte(rest, '\n'); k >= 0 {
			rest = rest[:k]
		}
		if (at == 0 || text[at-1] == '\n') && strings.TrimRight(rest, " \t\r") == "" {
			return at
		}
		i = at + 1
	}
	return -1
}
