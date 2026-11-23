package analysis_test

import (
	"strings"
	"testing"
)

// projectTesting is a project's own module named `testing`, whose `check`
// is an ordinary function.
const projectTesting = `pub fn check(b: Bool) -> Bool
    not b
end
`

// `q.check(e)` is std/testing's check when q is the module std/testing,
// however the file names it, and an ordinary call otherwise: a project
// module named `testing` declares its own `check`, which returns its own
// type.
func TestTestingCheck_TheQualifierIsStdTestingByIdentity(t *testing.T) {
	for _, c := range []struct{ name, main string }{
		{"a project module named testing", `import testing

fn main()
    b: Bool = testing.check(False)
    _ = b
end
`},
		{"a project module named testing beside std's under an alias", `import
    std::testing as std_testing
    testing
end

fn main()
    b: Bool = testing.check(False)
    r: Result<Bool, std_testing.AssertionFailure> = std_testing.check(b)
    _ = r
end
`},
		{"std's testing beside the project module under an alias", `import
    std::testing
    testing as mine
end

fn main()
    b: Bool = mine.check(False)
    r: Result<Bool, testing.AssertionFailure> = testing.check(b)
    _ = r
end
`},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, errs := opaqueProject(t, map[string]string{"main.nomi": c.main, "testing.nomi": projectTesting})
			for _, e := range errs {
				t.Errorf("%d:%d: %s", e.Line, e.Col, e.Message)
			}
		})
	}
}

// std/testing's check takes the subjects `assert` takes, through any name
// for the module: under an alias, `t.check(5)` is refused as
// `testing.check(5)` is.
func TestTestingCheck_AnAliasOfStdTestingTakesAssertsSubjects(t *testing.T) {
	for _, main := range []string{
		"import std::testing\n\nfn main()\n    _ = testing.check(5)\nend\n",
		"import std::testing as t\n\nfn main()\n    _ = t.check(5)\nend\n",
	} {
		_, errs := opaqueProject(t, map[string]string{"main.nomi": main})
		var msgs []string
		for _, e := range errs {
			msgs = append(msgs, e.Message)
		}
		const want = "testing.check expects Bool, Result, Option, or Assertable, got Int"
		if !strings.Contains(strings.Join(msgs, "\n"), want) {
			t.Errorf("%q: want %q, got %q", main, want, msgs)
		}
	}
}
