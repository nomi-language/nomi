package lsp

import (
	"slices"
	"testing"

	"github.com/nomi-language/nomi/internal/lexer"
)

// Every keyword completion offers is a word the lexer reserves or lists
// as contextual.
func TestCompletionKeywordsAreTheLexersWords(t *testing.T) {
	contextual := lexer.ContextualWords()
	for _, list := range [][]string{topLevelKeywords, statementKeywords, expressionKeywords} {
		for _, w := range list {
			if !lexer.IsReservedWord(w) && !slices.Contains(contextual, w) {
				t.Errorf("completion offers %q as a keyword, which the lexer neither reserves nor lists as contextual", w)
			}
		}
	}
}
