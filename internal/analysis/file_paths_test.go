package analysis_test

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/std"
)

// The files of module app, for the rules of short names, directory imports
// and paths at a use (spec §14 *Short names*, *Directory imports*).
var directoryImportFiles = map[string]string{
	"nomi.toml": "[module]\nname = \"app\"\nentry_points = [\"main\"]\n",
	"db/schema.nomi": "pub struct Post {\n    id: Int\n}\n\npub enum Kind {\n    Draft\n    Shown(Int)\n}\n\n" +
		"pub fn post(id: Int) -> Post {\n    Post{id: id}\n}\n\nimpl Post {\n    pub fn blank() -> Post {\n        Post{id: 0}\n    }\n}\n",
	"db/notifications.nomi":           "pub fn count() -> Int {\n    1\n}\n",
	"jobs/notifications.nomi":         "pub struct EmailArgs {\n    to: String\n}\n\npub fn count() -> Int {\n    2\n}\n",
	"views/admin/users.nomi":          "pub struct UserDetail {\n    name: String\n}\n\npub fn render(u: UserDetail) -> String {\n    u.name\n}\n",
	"views/admin/reports/daily.nomi":  "pub fn total() -> Int {\n    3\n}\n",
	"views/admin/internal/keys.nomi":  "pub fn key() -> Int {\n    4\n}\n",
	"pages.nomi":                      "pub fn title() -> String {\n    \"pages\"\n}\n",
	"pages/about.nomi":                "pub fn text() -> String {\n    \"about\"\n}\n",
	"plugins/billing/nomi.toml":       "[module]\nname = \"billing\"\n",
	"plugins/billing/pay.nomi":        "pub fn amount() -> Int {\n    5\n}\n",
	"users.nomi":                      "pub fn count() -> Int {\n    6\n}\n",
	"views/admin/reports/weekly.nomi": "pub fn total() -> Int {\n    7\n}\n",
	"jobs/admin.nomi":                 "pub fn count() -> Int {\n    8\n}\n",
}

// directoryImportErrors is the diagnostics of main.nomi, written as main,
// in module app, each `line:col: message` and its hints after `; `, with
// unused bindings left out.
func directoryImportErrors(t *testing.T, main string) []string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "app")
	for path, text := range directoryImportFiles {
		writeTestFile(t, filepath.Join(root, filepath.FromSlash(path)), text)
	}
	entry := filepath.Join(root, "main.nomi")
	writeTestFile(t, entry, main)
	nodes, _ := parser.ParseWithRecovery(lexer.Lex(main))
	lib := std.Load()
	fa, _, _ := analysis.BuildProjectFromEntry(entry, nodes, lib.Primitives, lib.Modules, lib.Files, root, std.MakeLoader())
	var out []string
	for _, e := range append(append([]analysis.TypeError{}, fa.TypeErrors...), analysis.CheckTypes(fa, nodes)...) {
		if e.Code == analysis.UnusedBindingCode {
			continue
		}
		s := fmt.Sprintf("%d:%d: %s", e.Line, e.Col, e.Message)
		if len(e.Hints) > 0 {
			s += "; " + strings.Join(e.Hints, "; ")
		}
		out = append(out, s)
	}
	return out
}

// accepts fails the test when the front end reports anything for main.
func accepts(t *testing.T, main string) {
	t.Helper()
	if got := directoryImportErrors(t, main); len(got) != 0 {
		t.Errorf("the front end rejects it:\n  %s", strings.Join(got, "\n  "))
	}
}

// rejects fails the test when the front end admits main, or reports
// anything but want.
func rejects(t *testing.T, main string, want ...string) {
	t.Helper()
	got := directoryImportErrors(t, main)
	if len(got) == 0 {
		t.Fatal("the front end admits it")
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

// A path below a directory import's name stands wherever a file's short
// name does: a call, a type, a pattern, a struct literal, an owner function,
// a variant. Two files of one short name are told apart through their
// directories, and a deeper path reaches a file below a subdirectory.
func TestDirectoryImport_ReachesFilesInEveryPosition(t *testing.T) {
	accepts(t, `import {
    db
    jobs
    views::admin
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
    u: admin::users.UserDetail = admin::users.UserDetail{name: "ada"}
    _ = #(id(a), id(b), id(c), d, e, f.to, admin::users.render(u), admin::reports::daily.total())
}
`)
}

// An alias names the directory, and a directory import inside a block binds
// in that block only.
func TestDirectoryImport_AliasAndBlock(t *testing.T) {
	accepts(t, `import views::admin as a

fn main() {
    import db

    _ = #(a::reports::daily.total(), db::notifications.count())
}
`)
	rejects(t, `fn other() -> Int {
    db::notifications.count()
}

fn main() {
    import db

    _ = #(db::notifications.count(), other())
}
`,
		"2:5: `db::notifications` is not imported; import the file (`import db::notifications`) and write `notifications.count`, or import its directory (`import db`) and keep `db::notifications.count`")
}

// A module's own name alone is its root directory when no file at the root
// has that name, and reaches a root file whose short name a declaration
// takes. The standard library's root is `std`.
func TestDirectoryImport_ModuleAndStdRoots(t *testing.T) {
	accepts(t, `import {
    app
    std
}

fn users() -> Int {
    app::users.count()
}

fn main() {
    std::io.print(users())
}
`)
	rejects(t, `import std

fn main() {
    std::message_format.x()
    std::nosuch.f()
}
`,
		"4:5: `std::message_format` is internal to std::message",
		"5:5: `std::nosuch` names no file: std has no nosuch.nomi; std holds `std::actors`, `std::add`, `std::assertions`, `std::base64`, `std::bits`, `std::bitwise` and more")
}

// Where a file and a directory share a name, the import names the file; the
// directory is reached from its parent.
func TestDirectoryImport_AFileWinsOverADirectoryOfItsName(t *testing.T) {
	accepts(t, `import {
    app
    pages
}

fn main() {
    _ = #(pages.title(), app::pages::about.text())
}
`)
	rejects(t, `import pages

fn main() {
    _ = pages::about.text()
}
`,
		"4:9: `pages::about` is not imported: `pages` here is the file pages, not a directory; import the file (`import pages::about`) and write `about.text`, or import the directory above it (`import app`) and write `app::pages::about.text`")
}

// A path below the directory's name that names no file, or names a
// directory, is an error at the use that says what the directory holds.
func TestDirectoryImport_PathsThatReachNoFile(t *testing.T) {
	rejects(t, `import views::admin

fn main() {
    _ = admin::user.render(1)
    _ = admin::nosuch.f()
    _ = admin::reports.total()
    _ = admin::reports::monthly.total()
    _ = admin::missing::deep.f()
}
`,
		"4:9: `admin::user` names no file: views::admin has no user.nomi; did you mean `admin::users`?",
		"5:9: `admin::nosuch` names no file: views::admin has no nosuch.nomi; views::admin holds `admin::users`, the directory `admin::internal` and the directory `admin::reports`",
		"6:9: `admin::reports` is a directory, not a file; name a file in it, as in `admin::reports::daily.name`",
		"7:9: `admin::reports::monthly` names no file: views::admin::reports has no monthly.nomi; views::admin::reports holds `admin::reports::daily` and `admin::reports::weekly`",
		"8:9: `admin::missing::deep` names no file: there is no directory views/admin/missing/")
}

// A directory's name is not a value and has no members: a file in it is
// reached with `::`.
func TestDirectoryImport_TheNameIsNotAFile(t *testing.T) {
	rejects(t, `import views::admin

fn main() {
    _ = admin.users
    x = admin
    _ = #(x, admin::users.render(admin::users.UserDetail{name: ""}))
}
`,
		"4:9: `admin` is a directory, not a file; reach a file in it with `::`, as in `admin::users.name`",
		"5:9: `admin` is a directory, not a value; name a member of a file in it, as in `admin::users.name`")
}

// A directory import through which nothing is reached is unused.
func TestDirectoryImport_UnusedIsAnError(t *testing.T) {
	rejects(t, `import {
    db
    views::admin
}

fn main() {
    _ = db::notifications.count()
}
`,
		"3:12: imported directory 'admin' is unused — remove the import")
}

// A directory holding a nomi.toml is another module, which no path of this
// module reaches; nor does a path reach into another subtree's internal/.
func TestDirectoryImport_AnotherModuleAndInternal(t *testing.T) {
	rejects(t, `import plugins::billing

fn main() {
    _ = billing::pay.amount()
}
`,
		"1:8: `plugins::billing` is another module's directory: it holds a nomi.toml; a module is reached by its own name: run `nomi add ./plugins/billing` and import it by that name")
	rejects(t, `import plugins

fn main() {
    _ = plugins::billing::pay.amount()
}
`,
		"4:9: `plugins::billing::pay` is in another module's directory: plugins/billing/ holds a nomi.toml")
	rejects(t, `import views::admin

fn main() {
    _ = admin::internal::keys.key()
}
`,
		"4:9: cannot import \"views::admin::internal::keys\" from \"main\": outside the parent subtree of views/admin/internal/")
}

// A local or a parameter named like a directory import's name wins in its
// scope, and a path written there is an error that says so.
func TestDirectoryImport_ALocalShadowsTheName(t *testing.T) {
	rejects(t, `import views::admin

fn show(admin: Int) -> Int {
    admin + admin::reports::daily.total()
}

fn main() {
    _ = #(show(1), admin::reports::daily.total())
}
`,
		"4:13: `admin` here is the parameter on line 3, not the directory views::admin; rename the parameter, or import the directory under another name with `as`")
}

// A file import binds the file's short name and nothing else: the import's
// full path at a use is an error whose hint says what to write.
func TestShortName_AFileImportBindsOnlyItsShortName(t *testing.T) {
	rejects(t, `import {
    db::schema
    views
}

fn main() {
    _ = #(schema.post(1), db::schema.post(2), views::admin::users.render(views::admin::users.UserDetail{name: ""}))
    _ = views::nosuch.f()
}
`,
		"7:27: `db::schema` is not a name in this file: `import db::schema` binds the file as `schema`; write `schema.post`, or import its directory (`import db`) and keep `db::schema.post`",
		"8:9: `views::nosuch` names no file: views has no nosuch.nomi; views holds the directory `views::admin`")
	rejects(t, `import views::admin

fn main() {
    _ = views::admin::reports::daily.total()
}
`,
		"4:9: `views::admin::reports::daily` is not a name in this file: `import views::admin` binds the directory as `admin`; write `admin::reports::daily.total`",
		"1:15: imported directory 'admin' is unused — remove the import")
}

// Two imports may not bind one name: the second is an error, whose hint
// says to import the directories or to alias one.
func TestShortName_TwoImportsOfOneNameAreAnError(t *testing.T) {
	rejects(t, `import {
    db::notifications
    jobs::notifications
}

fn main() {
    _ = notifications.count()
}
`,
		"3:11: 'notifications' is already imported on line 2 from db::notifications; two imports cannot bind one name; import the directories (`import db` and `import jobs`) and write `db::notifications` and `jobs::notifications`, or import one under another name with `as`")
	rejects(t, `import {
    views::admin
    jobs::admin
}

fn main() {
    _ = admin::users.render(admin::users.UserDetail{name: ""})
}
`,
		"3:11: 'admin' is already imported on line 2 from views::admin; two imports cannot bind one name; import the directories (`import views` and `import jobs`) and write `views::admin` and `jobs::admin`, or import one under another name with `as`")
	accepts(t, `import {
    db::notifications
    jobs::notifications as job_notifications
}

fn main() {
    _ = notifications.count() + job_notifications.count()
}
`)
}

// A declaration of the file named like a file import's short name keeps the
// name, and the import is an error whose hint says to import the
// directory or to alias the file.
func TestShortName_ADeclarationTakesTheName(t *testing.T) {
	rejects(t, `import views::admin::users

fn users() -> Int {
    1
}

fn main() {
    _ = users()
}
`,
		"1:22: 'users' is already defined in this scope as a function on line 3, so `import views::admin::users` cannot bind it; import its directory (`import views::admin`) and write `admin::users`, or import it under another name with `as`")
	rejects(t, `import users

fn users() -> Int {
    1
}

fn main() {
    _ = users()
}
`,
		"1:8: 'users' is already defined in this scope as a function on line 3, so `import users` cannot bind it; import its directory (`import app`) and write `app::users`, or import it under another name with `as`")
}

// A local named like a file import's short name wins in its scope; a member
// access on it that fails hints how to reach the file.
func TestShortName_ALocalShadowsTheName(t *testing.T) {
	rejects(t, `import db::schema

fn main() {
    schema = 3
    _ = schema.post(1)
}
`,
		"5:16: Int has no field 'post'; `schema` here is the binding on line 4, not the file db::schema; import its directory (`import db`) and write `db::schema.post`, or import the file under another name with `as`")
}
