package lexer

import "testing"

func TestEveryReservedWordHasAPurpose(t *testing.T) {
	for w := range keywords {
		if p, instead, ok := KeywordPurpose(w); !ok || p == "" || instead == "" {
			t.Errorf("reserved word %q has no entry in keywordPurposes", w)
		} else if _, reserved := keywords[instead]; reserved {
			t.Errorf("keywordPurposes suggests %q for %q, which is itself reserved", instead, w)
		}
	}
	for w := range keywordPurposes {
		if _, ok := keywords[w]; !ok {
			t.Errorf("keywordPurposes lists %q, which is not a reserved word", w)
		}
	}
}
