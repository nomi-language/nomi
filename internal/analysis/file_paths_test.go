package analysis_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// The files of a project with two files of one short name and a nested
// directory, for the short-name and full-path rules (spec §14 *Short names
// and full paths*).
var fullPathSiblings = map[string]string{
	"db/schema": "pub struct Post {\n    id: Int\n}\n\npub enum Kind {\n    Draft\n    Shown(Int)\n}\n\n" +
		"pub fn post(id: Int) -> Post {\n    Post{id: id}\n}\n\nimpl Post {\n    pub fn blank() -> Post {\n        Post{id: 0}\n    }\n}\n",
	"db/notifications":    "pub fn count() -> Int {\n    1\n}\n",
	"jobs/notifications":  "pub struct EmailArgs {\n    to: String\n}\n\npub fn count() -> Int {\n    2\n}\n",
	"views/admin/users":   "pub struct UserDetail {\n    name: String\n}\n\npub fn render(u: UserDetail) -> String {\n    u.name\n}\n",
	"views/admin/reports": "pub fn total() -> Int {\n    3\n}\n",
}

// fullPathErrors is the diagnostics of entry over fullPathSiblings, each
// `line:col: message`, then `  hint` lines; unused bindings left out.
func fullPathErrors(t *testing.T, entry string) []string {
	t.Helper()
	var got []string
	for _, e := range buildProjectExpectingErrors(t, entry, fullPathSiblings) {
		if e.Code == analysis.UnusedBindingCode {
			continue
		}
		got = append(got, fmt.Sprintf("%d:%d: %s", e.Line, e.Col, e.Message))
		for _, h := range e.Hints {
			got = append(got, "  "+h)
		}
	}
	return got
}

// A full path stands wherever the short name does: a call, a type, a
// pattern, a struct literal, an owner function, a variant.
func TestFullPath_ResolvesInEveryPosition(t *testing.T) {
	entry := `import {
    db::notifications
    db::schema
    jobs::notifications
    views::admin::users
}

fn kind(k: db::schema.Kind) -> Int {
    case k {
        db::schema.Kind.Draft -> 0
        db::schema.Kind.Shown(n) -> n
    }
}

fn id(p: db::schema.Post) -> Int {
    case p {
        db::schema.Post{id} -> id
    }
}

fn main() {
    a: db::schema.Post = db::schema.post(1)
    b = db::schema.Post{id: 2}
    c = db::schema.Post.blank()
    d = kind(db::schema.Kind.Shown(4))
    e = db::notifications.count() + jobs::notifications.count()
    f = jobs::notifications.EmailArgs{to: "x"}
    u: views::admin::users.UserDetail = users.UserDetail{name: "ada"}
    _ = #(id(a), id(b), id(c), d, e, f.to, views::admin::users.render(u))
}
`
	if got := fullPathErrors(t, entry); len(got) != 0 {
		t.Errorf("the front end rejects full paths:\n  %s", strings.Join(got, "\n  "))
	}
}

// Two imports may bind one short name. A use of the name is the error, at
// the use, naming both files; the import lines report nothing. The test
// fails if the ambiguous use is admitted.
func TestShortName_AmbiguousUseIsAnError(t *testing.T) {
	entry := `import {
    db::notifications
    jobs::notifications
}

fn main() {
    a = notifications.count()
    b: notifications.EmailArgs = jobs::notifications.EmailArgs{to: ""}
    _ = #(a, b, db::notifications.count())
}
`
	got := fullPathErrors(t, entry)
	want := []string{
		"7:9: `notifications` could be db::notifications or jobs::notifications; write the full path",
		"8:8: `notifications` could be db::notifications or jobs::notifications; write the full path",
	}
	if len(got) == 0 {
		t.Fatal("the front end admits a use of an ambiguous short name")
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

// A declaration of the file named like an import's short name keeps the
// name, and the file stays reachable by its full path. A member access on the
// declaration hints the full path.
func TestShortName_DeclarationWins(t *testing.T) {
	ok := `import views::admin::users

fn users() -> Int {
    1
}

fn main() {
    _ = #(users(), views::admin::users.render(views::admin::users.UserDetail{name: "x"}))
}
`
	if got := fullPathErrors(t, ok); len(got) != 0 {
		t.Errorf("the front end rejects a declaration named like an import:\n  %s", strings.Join(got, "\n  "))
	}
	bad := `import views::admin::users

fn users() -> Int {
    1
}

fn main() {
    _ = users.render(views::admin::users.UserDetail{name: "x"})
}
`
	got := fullPathErrors(t, bad)
	want := []string{
		"8:15: () -> Int has no field 'render'",
		"  `users` here is the function on line 3, not the file views::admin::users; write `views::admin::users.render`",
	}
	if len(got) == 0 {
		t.Fatal("the front end reads `users` as the file where the function holds the name")
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

// A local named like an import's short name wins in its scope, and the file
// stays reachable by its full path.
func TestShortName_LocalWins(t *testing.T) {
	ok := `import db::schema

fn main() {
    schema = 3
    _ = schema + db::schema.post(1).id
}
`
	if got := fullPathErrors(t, ok); len(got) != 0 {
		t.Errorf("the front end rejects a local named like an import:\n  %s", strings.Join(got, "\n  "))
	}
	bad := `import db::schema

fn main() {
    schema = 3
    _ = schema.post(1)
}
`
	got := fullPathErrors(t, bad)
	if len(got) == 0 {
		t.Fatal("the front end reads `schema` as the file where a local holds the name")
	}
	wantHint := "  `schema` here is the binding on line 4, not the file db::schema; write `db::schema.post`"
	if len(got) != 2 || got[0] != "5:16: Int has no field 'post'" || got[1] != wantHint {
		t.Errorf("got\n  %s\nwant the no-field error with the hint\n%s", strings.Join(got, "\n  "), wantHint)
	}
}

// An import used only by its full path is used.
func TestFullPath_CountsAsAUseOfTheImport(t *testing.T) {
	entry := `import views::admin::reports

fn main() {
    _ = views::admin::reports.total()
}
`
	if got := fullPathErrors(t, entry); len(got) != 0 {
		t.Errorf("an import used by full path is reported:\n  %s", strings.Join(got, "\n  "))
	}
}

// A full path names a file this file imports as a file. Any other is an
// error that says to import it; the test fails if the path is admitted.
func TestFullPath_NotImportedIsAnError(t *testing.T) {
	entry := `import views::admin::users.UserDetail

fn main() {
    _ = views::admin::reports.total()
    _ = views::admin::users.render(UserDetail{name: "x"})
}
`
	got := fullPathErrors(t, entry)
	want := []string{
		"4:9: `views::admin::reports` is not imported",
		"  a full path names a file this file imports: add `import views::admin::reports`",
		"5:9: `views::admin::users` is not imported",
		"  a full path names a file this file imports: add `import views::admin::users`",
	}
	if len(got) == 0 {
		t.Fatal("the front end admits a full path to a file it does not import")
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}
