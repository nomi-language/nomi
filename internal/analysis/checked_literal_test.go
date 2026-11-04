package analysis_test

import (
	"strings"
	"testing"
)

const checkedTag = `import std::literals.{CheckedLiteral, Fragment, LiteralError}

type Shout

impl CheckedLiteral<Int, String> for Shout {
    fn check(statics: List<String>) -> Result<Unit, LiteralError> {
        if Iter.count(statics) > 9 { Err(LiteralError{fragment: 0, offset: 0, message: "too many"}) } else { Ok(Unit) }
    }

    fn build(fragments: List<Fragment<Int>>) -> String {
        "${Iter.count(fragments)}"
    }
}
`

// A checked literal is its build's result in every quote style: a String
// here, never a Result.
func TestCheckedLiteral_IsBuildsResult(t *testing.T) {
	for _, lit := range []string{"Shout\"a ${n} b\"", "Shout`plain`", "Shout\"plain\""} {
		ok := checkedTag + "\nfn f(n: Int) -> String {\n    s: String = " + lit + "\n    s\n}\n"
		// The project harness also reports std's own import noise, so only
		// a literal or a type error counts, as in checker_typed_literal_test.
		for _, e := range checkTypedLiteralProject(t, ok, nil) {
			if strings.Contains(e.Message, "typed-literal") || strings.Contains(e.Message, "mismatch") || strings.Contains(e.Message, "expected") {
				t.Fatalf("%s: the front end rejects a checked literal as a String: %s", lit, e.Error())
			}
		}
		bad := checkedTag + "\nfn f(n: Int) -> Int {\n    i: Int = " + lit + "\n    i\n}\n"
		errs := checkTypedLiteralProject(t, bad, nil)
		found := false
		for _, e := range errs {
			if strings.Contains(e.Message, "String") {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: no error naming its type String where an Int is wanted: %v", lit, errs)
		}
	}
}

// A type implements Literal or CheckedLiteral, not both.
func TestCheckedLiteral_BothHandlersIsRefused(t *testing.T) {
	src := checkedTag + `
impl Literal<Int, String> for Shout {
    fn from_fragments(fragments: List<Fragment<Int>>) -> String {
        "${Iter.count(fragments)}"
    }
}
`
	errs := checkTypedLiteralProject(t, strings.Replace(src, "import std::literals.{CheckedLiteral, Fragment, LiteralError}", "import std::literals.{CheckedLiteral, Fragment, Literal, LiteralError}", 1), nil)
	want := "type `Shout` implements both `Literal` and `CheckedLiteral`; a typed literal has one handler"
	for _, e := range errs {
		if e.Message == want {
			return
		}
	}
	t.Errorf("the front end admits a type with both handlers; want %q in %v", want, errs)
}
