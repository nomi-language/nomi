package analysis_test

import (
	"strings"
	"testing"
)

// A return type naming no type is the only error: the function's result is
// unknown, not Unit, so its body is not a mismatch against Unit and a call
// of it is not one either. Before, `fn f() -> Itn { 1 }` also said "return
// type mismatch: expected Unit, got Int", and each call of it a mismatch of
// Unit with what the caller wanted.
func TestUnknownReturnType_IsTheOnlyError(t *testing.T) {
	rows := []struct{ name, src string }{
		{"a function", "fn f() -> Itn\n    1\nend\n\nfn main()\n    _ = f() + 1\nend\n"},
		{"inside a type argument", "fn g(x: Int) -> Maybe<Itn>\n    Some(x)\nend\n\nfn main()\n    _ = Maybe.with_default(g(1), 2)\nend\n"},
		{"an impl function", "struct P\n    x: Int\nend\n\nimpl P\n    fn x_of(p: P) -> Itn\n        p.x\n    end\nend\n\nfn main()\n    _ = P.x_of(P{x: 1}) + 1\nend\n"},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(r.src)
			var got []string
			for _, e := range errs {
				got = append(got, e.Message)
			}
			if len(got) != 1 || got[0] != `unknown type "Itn"` {
				t.Fatalf("want only `unknown type \"Itn\"`, got:\n%s", strings.Join(got, "\n"))
			}
		})
	}
}
