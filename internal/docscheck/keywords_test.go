package docscheck

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/lexer"
)

// keywordList is one place outside Go that writes Nomi's keywords down:
// the file, how to read its words out of it, and the words it
// deliberately treats differently from the lexer, each with the reason.
type keywordList struct {
	file string
	// words reads the file's keywords out of its text.
	words func(t *testing.T, text string) []string
	// reserved, contextual: whether the list holds the reserved words,
	// the contextual words, or both.
	reserved, contextual bool
	// extra are words the file lists that the lexer does not, and lacks
	// are lexer words the file leaves out, each with why. A reason that
	// quotes text in backquotes names what the file writes instead, and
	// the file must hold that text.
	extra, lacks map[string]string
}

// notKeywords are words the highlighters write beside their keywords that
// are literals or conventions, not keywords.
var notKeywords = map[string]string{
	"true":  "a literal",
	"false": "a literal",
}

var keywordLists = []keywordList{
	{
		file:     "docs/spec.md",
		words:    specWords("These words are reserved and cannot name anything:"),
		reserved: true,
	},
	{
		file:       "docs/spec.md",
		words:      specWords("These words have a meaning only where their construct stands"),
		contextual: true,
	},
	{
		// The keywords an import path segment may be: every reserved word
		// (parser isImportPathSegment).
		file:     "tree-sitter-nomi/grammar.js",
		words:    importPathKeywords,
		reserved: true,
		extra: map[string]string{
			"setup": "a contextual word the grammar writes as a literal, so the path rule names it too",
			"todo":  "a contextual word the grammar writes as a literal, so the path rule names it too",
		},
	},
	{
		// Every word the grammar writes as a literal token.
		file:       "tree-sitter-nomi/grammar.js",
		words:      quotedWords(`(field\(|name: )?'([a-z]{2,})'`),
		reserved:   true,
		contextual: true,
		extra:      notKeywords,
		lacks: map[string]string{
			"host":    "the scanner's token `$.host_keyword`, a keyword only before `fn` or `type`",
			"private": "the scanner's token `$.private_keyword`, a keyword only before a declaration",
			"embed":   "a `keyword_declaration:` starts with any identifier",
			"noun":    "a `keyword_declaration:` starts with any identifier",
			"phrase":  "a `keyword_declaration:` starts with any identifier",
		},
	},
	{
		// Every word the query names as a token. Helix's and Neovim's
		// queries and Zed's are generated from this file.
		file:       "tree-sitter-nomi/queries/highlights.scm",
		words:      quotedWords(`()"([a-z]{2,})"`),
		reserved:   true,
		contextual: true,
		extra: map[string]string{
			"true":     "a literal",
			"false":    "a literal",
			"internal": "a path segment the query colors by convention, not a keyword",
		},
		lacks: map[string]string{
			"pub":      "colored by `(pub) @keyword`",
			"export":   "colored by `(export) @keyword`",
			"continue": "colored by `(continue_statement) @keyword`",
			"self":     "colored by `(self_type) @keyword`",
			"host":     "colored by `(host_keyword) @keyword`",
			"private":  "colored by `(private_keyword) @keyword`",
			"on":       "colored by `(on_error) @keyword`",
			"error":    "colored by `(on_error) @keyword`",
			"noun":     "colored by `(keyword_declaration`, whatever word starts it",
			"phrase":   "colored by `(keyword_declaration`, whatever word starts it",
		},
	},
	{
		file:       "editors/bat/nomi.sublime-syntax",
		words:      batKeywords,
		reserved:   true,
		contextual: true,
		extra: map[string]string{
			"true":     "a literal",
			"false":    "a literal",
			"internal": "a path segment the syntax colors by convention, not a keyword",
		},
	},
	{
		file:       "editors/vscode/syntaxes/nomi.tmLanguage.json",
		words:      vscodeKeywords,
		reserved:   true,
		contextual: true,
		extra: map[string]string{
			"true":     "a literal",
			"false":    "a literal",
			"internal": "a path segment the syntax colors by convention, not a keyword",
		},
	},
}

// Every list of Nomi's keywords outside Go holds the lexer's words: the
// spec's two lists, the grammar's literals and its import path rule, the
// highlight query, and the two regex grammars. A word a file treats
// differently on purpose is in that file's extra or lacks, with the
// reason. Go code reads the lexer's list itself (lexer.ReservedWords,
// lexer.ContextualWords, lexer.IsKeyword), and the editors' other query
// files are generated from the ones named here.
func TestKeywordListsMatchTheLexer(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, list := range keywordLists {
		text, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(list.file)))
		if err != nil {
			t.Fatal(err)
		}
		want := map[string]bool{}
		if list.reserved {
			for _, w := range lexer.ReservedWords() {
				want[w] = true
			}
		}
		if list.contextual {
			for _, w := range lexer.ContextualWords() {
				want[w] = true
			}
		}
		got := map[string]bool{}
		for _, w := range list.words(t, string(text)) {
			got[w] = true
		}
		if len(got) == 0 {
			t.Errorf("%s: found no keywords; the list moved or changed shape", list.file)
			continue
		}
		var missing, unknown, stale []string
		for w := range want {
			if _, allowed := list.lacks[w]; !got[w] && !allowed {
				missing = append(missing, w)
			}
		}
		for w := range got {
			if _, allowed := list.extra[w]; !want[w] && !allowed {
				unknown = append(unknown, w)
			}
		}
		for w, reason := range list.lacks {
			if got[w] || !want[w] {
				stale = append(stale, w)
			}
			for _, m := range regexp.MustCompile("`([^`]+)`").FindAllStringSubmatch(reason, -1) {
				if !strings.Contains(string(text), m[1]) {
					t.Errorf("%s leaves out %q for %s, and no longer holds that text", list.file, w, reason)
				}
			}
		}
		for w := range list.extra {
			if want[w] {
				stale = append(stale, w)
			}
		}
		sort.Strings(missing)
		sort.Strings(unknown)
		sort.Strings(stale)
		if len(missing) > 0 {
			t.Errorf("%s lacks %q, which the lexer lists", list.file, missing)
		}
		if len(unknown) > 0 {
			t.Errorf("%s lists %q, which the lexer does not", list.file, unknown)
		}
		if len(stale) > 0 {
			t.Errorf("%s: the allowed differences %q no longer differ; remove them from keywordLists", list.file, stale)
		}
	}
}

// specWords reads the backquoted lowercase words of the spec paragraphs
// that start at lead, up to the next paragraph that holds none.
func specWords(lead string) func(*testing.T, string) []string {
	word := regexp.MustCompile("`([a-z]+)`")
	return func(t *testing.T, text string) []string {
		_, rest, found := strings.Cut(text, lead)
		if !found {
			t.Errorf("docs/spec.md has no paragraph starting %q", lead)
			return nil
		}
		var words []string
		for i, para := range strings.Split(rest, "\n\n") {
			ms := word.FindAllStringSubmatch(para, -1)
			if len(ms) == 0 && i > 0 {
				break
			}
			for _, m := range ms {
				words = append(words, m[1])
			}
			if i > 0 || len(ms) > 0 {
				break
			}
		}
		return words
	}
}

// importPathKeywords reads the alternatives of grammar.js's
// _import_path_keyword pattern, a `word(suffix)?` as both words.
func importPathKeywords(t *testing.T, text string) []string {
	m := regexp.MustCompile(`_import_path_keyword: _ => token\(prec\(-1, /([^/]+)/\)\)`).FindStringSubmatch(text)
	if m == nil {
		return nil
	}
	optional := regexp.MustCompile(`^([a-z]+)\(([a-z]+)\)\?$`)
	var words []string
	for _, alt := range strings.Split(m[1], "|") {
		if o := optional.FindStringSubmatch(alt); o != nil {
			words = append(words, o[1], o[1]+o[2])
			continue
		}
		words = append(words, alt)
	}
	return words
}

// quotedWords reads every lowercase word a file writes as a quoted
// literal, outside its comments. pattern's first group is what marks a
// quoted word as something else (a field's name) and its second the word.
func quotedWords(pattern string) func(*testing.T, string) []string {
	quoted := regexp.MustCompile(pattern)
	return func(t *testing.T, text string) []string {
		var words []string
		for _, line := range strings.Split(text, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, ";") {
				continue
			}
			for _, m := range quoted.FindAllStringSubmatch(line, -1) {
				if m[1] == "" {
					words = append(words, m[2])
				}
			}
		}
		return words
	}
}

// patternWords are the lowercase words of a regular expression: runs of
// two or more lowercase letters outside an escape (`\b`), a character
// range (`A-Za-z`) and a variable (`{{ident}}`).
func patternWords(pattern string) []string {
	var words []string
	for i := 0; i < len(pattern); {
		c := pattern[i]
		if c == '\\' {
			i += 2
			continue
		}
		if c < 'a' || c > 'z' {
			i++
			continue
		}
		j := i
		for j < len(pattern) && pattern[j] >= 'a' && pattern[j] <= 'z' {
			j++
		}
		before := byte(' ')
		if i > 0 {
			before = pattern[i-1]
		}
		after := byte(' ')
		if j < len(pattern) {
			after = pattern[j]
		}
		upper := func(b byte) bool { return b >= 'A' && b <= 'Z' }
		if j-i >= 2 && before != '{' && !upper(before) && !upper(after) {
			words = append(words, pattern[i:j])
		}
		i = j
	}
	return words
}

// batKeywords reads the words of the `match:` patterns in the bat
// syntax's `keywords` context.
func batKeywords(t *testing.T, text string) []string {
	_, rest, found := strings.Cut(text, "\n  keywords:\n")
	if !found {
		return nil
	}
	var words []string
	for _, line := range strings.Split(rest, "\n") {
		if strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "   ") && strings.TrimSpace(line) != "" {
			break // the next context
		}
		if pattern, ok := strings.CutPrefix(strings.TrimSpace(line), "- match: "); ok {
			words = append(words, patternWords(pattern)...)
		}
	}
	return words
}

// vscodeKeywords reads the words of every pattern under the TextMate
// grammar's `keywords` repository entry.
func vscodeKeywords(t *testing.T, text string) []string {
	var grammar struct {
		Repository map[string]json.RawMessage `json:"repository"`
	}
	if err := json.Unmarshal([]byte(text), &grammar); err != nil {
		t.Errorf("editors/vscode/syntaxes/nomi.tmLanguage.json: %v", err)
		return nil
	}
	var entry any
	if err := json.Unmarshal(grammar.Repository["keywords"], &entry); err != nil {
		return nil
	}
	var words []string
	var walk func(v any)
	walk = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			for key, child := range v {
				if s, ok := child.(string); ok && key == "match" {
					words = append(words, patternWords(s)...)
					continue
				}
				walk(child)
			}
		case []any:
			for _, child := range v {
				walk(child)
			}
		}
	}
	walk(entry)
	return words
}
