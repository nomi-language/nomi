package analysis

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ast"
)

// An error a synthesized body raises moves to the declaration the body was
// written for (repointSynthErrors), and there says each name the body binds
// as a value of the impl, and each type without the hidden import's
// qualifier: the declaration's reader wrote neither. An error outside the
// band, which a programmer's own `__x` may be in, keeps its words.
func TestRepointSynthErrors_SayGeneratedNamesInTheProgrammersTerms(t *testing.T) {
	band := synthLineBase + 7
	impl := &ast.ImplBlock{
		Interface:       &ast.SimpleType{Name: "FromJson"},
		Receiver:        &ast.SimpleType{Name: "Changes"},
		Line:            synthLineBase,
		SynthOriginLine: 7,
		SynthOriginCol:  1,
	}
	inherent := &ast.ImplBlock{
		Receiver:        &ast.SimpleType{Name: "Port"},
		Line:            synthLineBase,
		SynthOriginLine: 3,
		SynthOriginCol:  1,
	}
	cases := []struct {
		block *ast.ImplBlock
		in    TypeError
		want  TypeError
	}{
		{
			block: impl,
			in: TypeError{Line: band, Col: 9, Message: "the type of '__value_email' is not determined", Hints: []string{
				"annotate `__value_email` with its type, as in `json$.Json`",
			}},
			want: TypeError{Line: 7, Col: 1, Message: "the type of a value the `FromJson` impl the compiler writes for `Changes` binds is not determined", Hints: []string{
				"annotate a value the `FromJson` impl the compiler writes for `Changes` binds with its type, as in `Json`",
			}},
		},
		{
			block: inherent,
			in:    TypeError{Line: band, Col: 2, Message: "__err is never read"},
			want:  TypeError{Line: 3, Col: 1, Message: "a value the functions the compiler writes for `Port` binds is never read"},
		},
		{
			block: impl,
			in:    TypeError{Line: 4, Col: 5, Message: "'__mine' is never read"},
			want:  TypeError{Line: 4, Col: 5, Message: "'__mine' is never read"},
		},
	}
	for _, tc := range cases {
		errs := []TypeError{tc.in}
		repointSynthErrors(tc.block, errs)
		got := errs[0]
		if got.Line != tc.want.Line || got.Col != tc.want.Col || got.Message != tc.want.Message ||
			strings.Join(got.Hints, "\n") != strings.Join(tc.want.Hints, "\n") {
			t.Errorf("repointSynthErrors(%q) = %d:%d %q %q, want %d:%d %q %q", tc.in.Message,
				got.Line, got.Col, got.Message, got.Hints, tc.want.Line, tc.want.Col, tc.want.Message, tc.want.Hints)
		}
	}
}
