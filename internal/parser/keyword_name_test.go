package parser

import (
	"testing"

	"github.com/nomi-language/nomi/internal/lexer"
)

// A top-level statement refused at a declaration keyword it never consumed
// (`fn=`, a keyword written as a binding's name) used to leave recovery
// parsing that keyword forever, since synchronize stops on it. Recovery
// reports it and goes on; a regression hangs until -timeout.
func TestRecoveryStepsOverAKeywordRefusedAsABindingName(t *testing.T) {
	for _, src := range []string{
		"fn=",
		"fn=main() {}",
		"type = 1\nfn main() {}",
		"test = 1",
		"import = 1",
		"pub = 1",
		"fn=main() {\n  zerd   /.00d\n  _ = 1o / zero\n}",
	} {
		if _, errs := ParseAll(lexer.Lex(src)); len(errs) == 0 {
			t.Errorf("ParseAll(%q) reported no error", src)
		}
		if _, errs, _ := ParseResilient(lexer.Lex(src)); len(errs) == 0 {
			t.Errorf("ParseResilient(%q) reported no error", src)
		}
	}
}

// A reserved word written as a name gets an error that names the word,
// what it is for, and a name to write instead.
func TestKeywordAsNameSaysItIsAKeyword(t *testing.T) {
	for src, want := range map[string]string{
		"fn apply(with: Int, patch: Int) -> Int { patch }":       "line 1, col 10: `with` is a keyword (replaces an application field for a block), so it can't name a parameter; name it something else, such as `options`",
		"fn main() {\n    x = fn test -> 1\n}":                   "line 2, col 12: `test` is a keyword (declares a test), so it can't name a parameter; name it something else, such as `check`",
		"fn main() {\n    when = 1\n}":                           "line 2, col 5: `when` is a keyword (an arm's guard), so it can't name a binding; name it something else, such as `condition`",
		"fn main() {\n    for = 2\n}":                            "line 2, col 5: `for` is a keyword (names the type an `impl` is for), so it can't name a binding; name it something else, such as `target`",
		"fn main() {\n    case 1 {\n        type -> 1\n    }\n}": "line 3, col 9: `type` is a keyword (declares a distinct type), so it can't name a binding; name it something else, such as `kind`",
		"struct S {\n    when: Int\n}":                           "line 2, col 5: `when` is a keyword (an arm's guard), so it can't name a field; name it something else, such as `condition`",
		"enum E {\n    A{in: Int}\n}":                            "line 2, col 7: `in` is a keyword (tests membership, `x in collection`), so it can't name a field; name it something else, such as `input`",
		"fn main() {\n    s = S{test: 1}\n}":                     "line 2, col 11: `test` is a keyword (declares a test), so it can't name a field; name it something else, such as `check`",
		"fn unreachable() {}":                                    "line 1, col 4: `unreachable` is a keyword (marks code that cannot run), so it can't name a function; name it something else, such as `never`",
	} {
		if err := parseError(t, src); err.Error() != want {
			t.Errorf("%q:\n got  %s\n want %s", src, err, want)
		}
	}
}

// `todo` is a contextual keyword: where a name stands it is an ordinary
// name, and read as a value it is the placeholder, which resolution turns
// into the name when a binding `todo` is in scope.
func TestTodoIsANameWhereANameStands(t *testing.T) {
	for _, src := range []string{
		"fn apply(todo: Todo) -> Todo { todo }",
		"fn todo() -> Int { 1 }",
		"fn main() {\n    todo = 1\n}",
		"struct S {\n    todo: Int\n}",
		"enum E {\n    A{todo: Int}\n}",
		"fn main() {\n    s = S{todo: 1}\n    t = .{todo}\n    S{todo} = s\n}",
		"fn main() {\n    case x {\n        Some(todo) -> todo\n        None -> 0\n    }\n}",
		"fn main() {\n    f = fn todo: Int -> todo + 1\n    _ = x.todo\n}",
		"import std::list.{todo}",
	} {
		if _, err := Parse(lexer.Lex(src)); err != nil {
			t.Errorf("%q: the parser rejects this: %v", src, err)
		}
	}
}

// A name that a keyword only prefixes (`todos`, `tests`) is an ordinary
// name.
func TestKeywordAsNameAcceptsNamesThatAreNotKeywords(t *testing.T) {
	for _, src := range []string{
		"fn apply(item: Int, todos: Int) -> Int { todos }",
		"struct S {\n    kind: Int\n    tests: Int\n}",
	} {
		if _, err := Parse(lexer.Lex(src)); err != nil {
			t.Errorf("%q: the parser rejects this: %v", src, err)
		}
	}
}
