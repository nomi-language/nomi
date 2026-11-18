package lexer

import "testing"

func TestEveryReservedWordHasAPurpose(t *testing.T) {
	for _, r := range reservedWords {
		if r.purpose == "" || r.instead == "" {
			t.Errorf("reserved word %q has no purpose or no name to write instead", r.word)
		} else if _, reserved := keywords[r.instead]; reserved {
			t.Errorf("reservedWords suggests %q for %q, which is itself reserved", r.instead, r.word)
		}
		if !IsKeyword(r.tok) || r.tok.String() == "" {
			t.Errorf("reserved word %q has no token", r.word)
		}
	}
	if len(keywords) != len(reservedWords) || len(keywordTokens) != len(reservedWords) {
		t.Errorf("reservedWords lists a word or a token twice")
	}
}
