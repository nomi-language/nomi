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

// std/testing's check takes the subjects `assert` takes, by any name for
// the module or the function, called, piped or named as a value: under an
// alias, `t.check(5)` is refused as `testing.check(5)` is, and so are
// `check(5)` and `must(5)` after a selective import, the stage `5 |>
// check()`, and `check` passed where a function of an Int is expected.
// The checker typed `check(5)` as a call of the generic `host fn`, and the
// builder declined it.
func TestTestingCheck_AnAliasOfStdTestingTakesAssertsSubjects(t *testing.T) {
	const subject = "testing.check expects Bool, Result, Option, or Assertable, got Int"
	for _, c := range []struct{ main, want string }{
		{"import std::testing\n\nfn main()\n    _ = testing.check(5)\nend\n", subject},
		{"import std::testing as t\n\nfn main()\n    _ = t.check(5)\nend\n", subject},
		{"import std::testing.{check}\n\nfn main()\n    _ = check(5)\nend\n", subject},
		{"import std::testing.check\n\nfn main()\n    _ = check(5)\nend\n", subject},
		{"import std::testing.{check as must}\n\nfn main()\n    _ = must(5)\nend\n", subject},
		{"import std::testing.{check}\n\nfn main()\n    _ = 5 |> check()\nend\n", subject},
		{"import\n    std::assertions.AssertionFailure\n    std::testing.{check}\nend\n\nfn main()\n    f: (Int) -> Result<Int, AssertionFailure> = check\n    _ = f\nend\n", subject},
		{"import std::testing\n\nfn main()\n    _ = [1, 2] |> Iter.map(testing.check) |> Iter.to_list()\nend\n", subject},
		{"import std::testing.{check}\n\nfn main()\n    _ = [1, 2] |> Iter.map(check) |> Iter.to_list()\nend\n", subject},
		{"import std::testing.{check}\n\nfn main()\n    _ = check<Bool>(True)\nend\n",
			"`check` takes no type arguments: write `check(subject)`"},
		{"import std::testing\n\nfn main()\n    _ = testing.check<Bool>(True)\nend\n",
			"`testing.check` takes no type arguments: write `testing.check(subject)`"},
	} {
		_, errs := opaqueProject(t, map[string]string{"main.nomi": c.main})
		var msgs []string
		for _, e := range errs {
			msgs = append(msgs, e.Message)
		}
		if !strings.Contains(strings.Join(msgs, "\n"), c.want) {
			t.Errorf("%q: want %q, got %q", c.main, c.want, msgs)
		}
	}
}

// std/testing's check imported by name, under an alias and in a pipe stage
// is the keyword form, as `testing.check` is: the file checks, and its
// import is used.
func TestTestingCheck_AnImportedCheckIsTheKeywordForm(t *testing.T) {
	main := `import
    std::assertions.AssertionFailure
    std::testing.{check, check as must}
end

fn main()
    a: Result<Bool, AssertionFailure> = check(1 == 1)
    b: Result<Bool, AssertionFailure> = (1 == 2) |> must()
    f: (Option<Int>) -> Result<Option<Int>, AssertionFailure> = check
    _ = (a, b, f)
end
`
	_, errs := opaqueProject(t, map[string]string{"main.nomi": main})
	for _, e := range errs {
		t.Errorf("%d:%d: %s", e.Line, e.Col, e.Message)
	}
}
