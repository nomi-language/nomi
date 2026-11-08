package hoverdoc

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/lexer"
)

// Every word the lexer reserves, and every contextual word, has a hover in
// keywordDocs for its main use, and the table holds no other word and no
// use twice.
func TestKeywordDocsCoverTheLexer(t *testing.T) {
	words := map[string]bool{}
	for _, w := range lexer.ReservedWords() {
		words[w] = true
	}
	for _, w := range lexer.ContextualWords() {
		words[w] = true
	}
	main := map[string]bool{}
	seen := map[string]bool{}
	for _, d := range keywordDocs {
		key := d.word + "/" + d.context
		if seen[key] {
			t.Errorf("keywordDocs holds %q twice", key)
		}
		seen[key] = true
		if !words[d.word] {
			t.Errorf("keywordDocs holds %q, which the lexer neither reserves nor lists as a contextual word", d.word)
		}
		if d.context == "" {
			main[d.word] = true
		}
		if d.form == "" || d.text == "" {
			t.Errorf("keywordDocs %q has no form or no text", key)
		}
		if !strings.HasPrefix(d.link, "/") || strings.Contains(d.link, "://") {
			t.Errorf("keywordDocs %q links %q, which is not a path on the site", key, d.link)
		}
		if n := strings.Count(d.text, ". ") + 1; n > 3 {
			t.Errorf("keywordDocs %q says %d sentences; a hover says at most three", key, n)
		}
	}
	for w := range words {
		if !main[w] {
			t.Errorf("the lexer knows %q, and keywordDocs has no hover for its main use (context \"\")", w)
		}
	}
}
