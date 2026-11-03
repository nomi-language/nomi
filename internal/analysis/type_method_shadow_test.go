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
interface NAME {
    fn METHOD(value: self) -> String
}

struct User {
    id: Int
}

impl NAME for User {
    fn METHOD(u: User) -> String {
        "${u.id}"
    }
}

fn shown() -> String {
    NAME.METHOD(User{id: 1})
}
`)
			_, errs := checkSourceWithStdlib(src)
			if len(errs) != 0 {
				t.Fatalf("the program's own %s.%s resolved elsewhere: %v", tc.name, tc.method, errs)
			}
		})
	}
}
