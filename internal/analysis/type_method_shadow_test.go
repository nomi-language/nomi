package analysis_test

import (
	"strings"
	"testing"
)

// A program's own interface or struct named like a std type (`Url`, `Regex`,
// `Html`) answers `Name.method(...)` with its own function, never std's type
// method of the same name: the receiver's declaring module is known, and the
// bare-name tables must not answer for a different module's type.
func TestTypeMethod_AProgramTypeNamedLikeAStdTypeKeepsItsOwnMethod(t *testing.T) {
	for _, tc := range []struct{ name, method string }{
		{"Url", "to_string"},
		{"Regex", "to_string"},
		{"Html", "to_html"},
		{"Html", "to_string"},
	} {
		t.Run(tc.name+"."+tc.method, func(t *testing.T) {
			src := strings.NewReplacer("NAME", tc.name, "METHOD", tc.method).Replace(`
interface NAME
    fn METHOD(value: self) -> String
end

struct User
    id: Int
end

impl NAME for User
    fn METHOD(u: User) -> String
        "${u.id}"
    end
end

fn shown() -> String
    NAME.METHOD(User{id: 1})
end
`)
			_, errs := checkSourceWithStdlib(src)
			if len(errs) != 0 {
				t.Fatalf("the program's own %s.%s resolved elsewhere: %v", tc.name, tc.method, errs)
			}
		})
	}
}

// A program's own struct named like a std type (`Html`, which std's
// `impl ToHtml for Html` gives a `to_html`) implements only what the
// program writes for it: with its own `impl Render for Html`, `Html.to_html`
// is Render's, not ambiguous between Render and std's ToHtml. The file is
// checked on its own against the stdlib's impl index, which holds none of
// its impls; the index still says std's impl is for std's Html.
func TestTypeMethod_AProgramStructNamedLikeAStdTypeHasOnlyItsOwnImpls(t *testing.T) {
	_, errs := checkSourceWithStdlib(`
interface Render
    fn to_html(value: self) -> String
end

struct Html
    id: Int
end

impl Render for Html
    fn to_html(h: Html) -> String
        "${h.id}"
    end
end

fn shown() -> String
    Html.to_html(Html{id: 1})
end
`)
	if len(errs) != 0 {
		t.Fatalf("the program's own Html took std's impl: %v", errs)
	}
}
