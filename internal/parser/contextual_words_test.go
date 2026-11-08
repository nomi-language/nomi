package parser

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/lexer"
)

// rejectedWords are the identifiers the parser compares a lexeme against
// only to explain an error, never to start a construct: `implements`,
// `derives` and `mut` from other languages, `field` in an interface or
// type body, and `from` after a Go-backed signature.
var rejectedWords = map[string]bool{
	"derives": true, "field": true, "from": true, "implements": true, "mut": true,
}

// Every word the parser recognizes by lexeme to start a construct is in
// lexer.ContextualWords, which the language server's keyword hover and
// the spec's keyword list are held to, and every listed word is one the
// parser recognizes.
func TestContextualWordsAreListed(t *testing.T) {
	listed := map[string]bool{}
	for _, w := range lexer.ContextualWords() {
		listed[w] = true
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	compared := regexp.MustCompile(`Lexeme [!=]= "([a-z_]+)"|peekIsContextual\("([a-z_]+)"\)|case ("phrase", "noun", "embed"):`)
	quoted := regexp.MustCompile(`"([a-z_]+)"`)
	seen := map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range compared.FindAllStringSubmatch(string(src), -1) {
			words := []string{m[1], m[2]}
			for _, q := range quoted.FindAllStringSubmatch(m[3], -1) {
				words = append(words, q[1])
			}
			for _, w := range words {
				if w != "" {
					seen[w] = true
				}
			}
		}
	}
	var missing, stale []string
	for w := range seen {
		if !listed[w] && !rejectedWords[w] {
			missing = append(missing, w)
		}
	}
	for w := range listed {
		if !seen[w] {
			stale = append(stale, w)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	if len(missing) > 0 {
		t.Errorf("the parser recognizes %q by lexeme, and lexer.ContextualWords lacks them; add each there, or to rejectedWords if the parser only explains an error with it", missing)
	}
	if len(stale) > 0 {
		t.Errorf("lexer.ContextualWords lists %q, which the parser never compares a lexeme against", stale)
	}
}
