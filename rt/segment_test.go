package rt

import (
	"slices"
	"strings"
	"testing"
)

func listStrings(l *List[string]) []string { return listToSlice(l) }

func TestStringDisplayWidth(t *testing.T) {
	for _, c := range []struct {
		text  string
		width int64
	}{
		{"", 0},
		{"hello", 5},
		{"日本語", 6},       // wide: 2 each
		{"ｈｉ", 4},        // fullwidth: 2 each
		{"é", 1},        // a combining mark adds nothing
		{"👩‍💻", 2},       // a ZWJ sequence is one wide cluster
		{"🇯🇵", 2},        // a flag is one cluster of two regional indicators
		{"a\tb", 2},      // a tab is a control character: 0
		{"¡", 1},         // ambiguous width counts 1
		{"한국어 text", 11}, // Hangul syllables are wide
	} {
		if got := StringDisplayWidth(c.text); got != c.width {
			t.Errorf("StringDisplayWidth(%q) = %d, want %d", c.text, got, c.width)
		}
	}
}

func TestStringWordSegmentsAndSentences(t *testing.T) {
	words := listStrings(StringWordSegments("Hello, wörld! 3.14 isn't 日本語"))
	want := []string{"Hello", ",", " ", "wörld", "!", " ", "3.14", " ", "isn't", " ", "日", "本", "語"}
	if !slices.Equal(words, want) {
		t.Errorf("word segments are %q, want %q", words, want)
	}
	if got := strings.Join(words, ""); got != "Hello, wörld! 3.14 isn't 日本語" {
		t.Errorf("the word segments join to %q", got)
	}
	sentences := listStrings(StringSentences("One. Two?  Three! Mr. Smith"))
	wantS := []string{"One. ", "Two?  ", "Three! ", "Mr. ", "Smith"}
	if !slices.Equal(sentences, wantS) {
		t.Errorf("sentences are %q, want %q", sentences, wantS)
	}
	if StringWordSegments("") != nil || StringSentences("") != nil {
		t.Error("the empty text has segments")
	}
}

func TestStringWrap(t *testing.T) {
	for _, c := range []struct {
		text  string
		width int64
		want  []string
	}{
		{"", 10, nil},
		{"the quick brown fox", 10, []string{"the quick", "brown fox"}},
		{"the quick brown fox", 9, []string{"the quick", "brown fox"}},
		{"the quick brown fox", 5, []string{"the", "quick", "brown", "fox"}},
		{"a\nb\n", 10, []string{"a", "b"}},
		{"a\n\nb", 10, []string{"a", "", "b"}},
		{"abcdefghij", 4, []string{"abcd", "efgh", "ij"}},
		{"日本語のテキスト", 6, []string{"日本語", "のテキ", "スト"}},
		{"ééé", 2, []string{"éé", "é"}},
		{"日", 1, []string{"日"}},
		{"well-known words", 6, []string{"well-", "known", "words"}},
		{"  indented text", 20, []string{"  indented text"}},
		{"🇯🇵🇯🇵🇯🇵", 4, []string{"🇯🇵🇯🇵", "🇯🇵"}},
	} {
		got := listStrings(StringWrap(c.text, SmallInt(c.width)))
		if !slices.Equal(got, c.want) {
			t.Errorf("StringWrap(%q, %d) = %q, want %q", c.text, c.width, got, c.want)
		}
		for _, line := range got {
			if StringDisplayWidth(line) > c.width && StringLength(line) > 1 {
				t.Errorf("StringWrap(%q, %d) has the line %q, %d cells wide", c.text, c.width, line, StringDisplayWidth(line))
			}
		}
	}
}
