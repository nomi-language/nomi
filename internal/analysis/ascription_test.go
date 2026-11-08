package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// `value as Type` checks value as `x: Type = value` would (ascription.go).

func TestAscription_TypesWhatAnAnnotationTypes(t *testing.T) {
	src := `import {
    std::http.Route
    std::uuid.Uuid
}

struct Post {
    title: String
}

interface Named {
    fn name(value: self) -> String
}

impl Named for Post {
    fn name(post: Post) -> String {
        post.title
    }
}

once admin = #{
    home: Route` + "`GET /admin/{$}`" + `,
    user: ` + "`GET /admin/users/{id}`" + ` as Route<#{id: Uuid}>,
    tags: [] as List<String>,
}

fn user_route() -> Route<#{id: Uuid}> {
    admin.user
}

fn main() {
    later = None as Maybe<Post>
    named = Post{title: "a"} as Named
    _ = (user_route(), later, Named.name(named), admin.tags + ["x"])
}
`
	got := checkOnceProject(t, map[string]string{"main.nomi": src}, []string{""})
	if errs := got["main.nomi"]; len(errs) > 0 {
		t.Fatalf("unexpected errors:\n%s", strings.Join(messages(errs), "\n"))
	}
}

// A mismatch is the binding's error, and `as` written to convert a scalar
// gets help naming the function that converts.
func TestAscription_MismatchesAreRefused(t *testing.T) {
	cases := []struct{ body, msg, hint string }{
		{"x = 3\n    _ = x as Float", "type mismatch: expected Float, got Int", "`as` doesn't convert a value; write `Int.to_float(x)`"},
		{"x = 1.5\n    _ = x as Int", "type mismatch: expected Int, got Float", "`as` doesn't convert a value; write `Float.to_int(x)`"},
		{"s = \"7\"\n    _ = s as Int", "type mismatch: expected Int, got String", "`as` doesn't convert a value; write `String.to_int(s)`"},
		{"_ = \"a\" as Bool", "type mismatch: expected Bool, got String", ""},
	}
	for _, c := range cases {
		src := "fn main() {\n    " + c.body + "\n}\n"
		got := checkOnceProject(t, map[string]string{"main.nomi": src}, []string{""})
		errs := got["main.nomi"]
		if len(errs) != 1 || errs[0].Message != c.msg {
			t.Errorf("%q: want %q, got:\n%s", c.body, c.msg, strings.Join(messages(errs), "\n"))
			continue
		}
		if c.hint != "" && (len(errs[0].Hints) == 0 || errs[0].Hints[0] != c.hint) {
			t.Errorf("%q: want hint %q, got %q", c.body, c.hint, errs[0].Hints)
		}
	}
}

// An ascription whose value already has the type is recorded for the
// editor's hint, and is no error.
func TestAscription_RedundantIsRecordedNotRefused(t *testing.T) {
	src := "fn main() {\n    x = 3\n    _ = x as Int\n    _ = [] as List<Int>\n}\n"
	fa, errs := checkEntry(t, src)
	if len(errs) > 0 {
		t.Fatalf("unexpected errors:\n%s", strings.Join(messages(errs), "\n"))
	}
	if len(fa.RedundantAscriptions) != 1 {
		t.Fatalf("want one redundant ascription (`x as Int`), got %d", len(fa.RedundantAscriptions))
	}
	for a := range fa.RedundantAscriptions {
		if a.Type.TypeString() != "Int" {
			t.Errorf("the redundant ascription is `as %s`, want `as Int`", a.Type.TypeString())
		}
	}
}

// checkEntry builds src as a one-file project and checks it, answering its
// analysis and errors.
func checkEntry(t *testing.T, src string) (*analysis.FileAnalysis, []analysis.TypeError) {
	t.Helper()
	nodes, _ := parser.ParseWithRecovery(lexer.Lex(src))
	lib := loadStd()
	fa, _, _ := analysis.BuildProjectWithCache(nodes, lib.Primitives, lib.Modules, lib.Files, t.TempDir(), nil)
	errs := withoutUnreadParams(append(fa.TypeErrors, analysis.CheckTypes(fa, nodes)...))
	return fa, errs
}
