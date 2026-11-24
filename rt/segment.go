package rt

import (
	"strings"
	"unicode"

	"github.com/rivo/uniseg"
)

// The display and segmentation half of std/strings: how many terminal
// cells text takes, where its words and sentences begin (UAX #29), and
// where a line may break (UAX #14). Each reads uniseg's tables, as the
// grapheme functions in grapheme.go do.

// StringDisplayWidth is `String.display_width`: the number of monospace
// cells s takes, summed over its grapheme clusters as uniseg measures
// them. A wide or fullwidth East Asian character takes 2, an emoji
// presented as one (a ZWJ sequence or a flag is one cluster) 2, a
// combining mark or a zero-width joiner 0 within its cluster, an
// ambiguous-width character 1, and a control character, the tab
// included, 0: where a tab ends depends on the column it starts at, which
// the text alone does not say.
func StringDisplayWidth(s string) int64 {
	return int64(uniseg.StringWidth(s))
}

// StringWordSegments is `String.word_segments`: s cut at its UAX #29 word
// boundaries. Each piece is a word, a run of spaces or a punctuation mark,
// and the pieces joined are s.
func StringWordSegments(s string) *List[string] {
	return segments(s, uniseg.FirstWordInString)
}

// StringSentences is `String.sentences`: s cut at its UAX #29 sentence
// boundaries. Each sentence keeps the spaces after it, so the pieces joined
// are s.
func StringSentences(s string) *List[string] {
	return segments(s, uniseg.FirstSentenceInString)
}

// segments is s cut by first, a uniseg segmenter that answers the first
// piece of what it is given and the state to carry to the rest.
func segments(s string, first func(string, int) (string, string, int)) *List[string] {
	var out []string
	state := -1
	for rest := s; rest != ""; {
		var piece string
		piece, rest, state = first(rest, state)
		out = append(out, piece)
	}
	return listOf(out)
}

// StringWrap is `String.wrap`: s as lines of at most width cells, broken
// greedily at UAX #14's line-break opportunities. A line ends where the
// text has a line break (which no line keeps), and before a piece that
// would take it past width; the spaces at a line's end are dropped. A
// piece wider than width on its own is broken between grapheme clusters,
// and a cluster wider than width takes a line of its own. A line break
// that ends the text starts no empty last line, as `String.lines` holds.
// A width past int64 saturates: no text is that wide.
func StringWrap(s string, width Int) *List[string] {
	w := max(int(min(width.Saturated(), int64(^uint(0)>>1))), 1)
	var lines []string
	var line strings.Builder
	used := 0        // the cells of line, its trailing spaces included
	visible := false // whether line holds more than spaces
	flush := func() {
		lines = append(lines, strings.TrimRightFunc(line.String(), unicode.IsSpace))
		line.Reset()
		used, visible = 0, false
	}
	state := -1
	start := 0
	for rest := s; rest != ""; {
		var boundaries int
		_, rest, boundaries, state = uniseg.StepString(rest, state)
		end := len(s) - len(rest)
		if boundaries&uniseg.MaskLine == uniseg.LineDontBreak && rest != "" {
			continue
		}
		piece := s[start:end]
		start = end
		body := strings.TrimRightFunc(piece, unicode.IsSpace)
		bodyWidth := uniseg.StringWidth(body)
		if used+bodyWidth > w {
			if visible {
				flush()
			} else {
				// Spaces that start a line lead nothing that fits.
				line.Reset()
				used = 0
			}
		}
		if bodyWidth > w {
			// Break the piece between clusters.
			cs := -1
			for b := body; b != ""; {
				var c string
				var cw int
				c, b, cw, cs = uniseg.FirstGraphemeClusterInString(b, cs)
				if used > 0 && used+cw > w {
					flush()
				}
				line.WriteString(c)
				used += cw
			}
		} else {
			line.WriteString(body)
			used += bodyWidth
		}
		visible = visible || body != ""
		tail := piece[len(body):]
		line.WriteString(tail)
		used += uniseg.StringWidth(tail)
		if uniseg.HasTrailingLineBreakInString(piece) {
			flush()
		}
	}
	if visible {
		flush()
	}
	return listOf(lines)
}
