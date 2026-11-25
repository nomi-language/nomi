package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The generator fixture, from the repository root, where TestMain runs the
// tests: package csvdata ships tools, and package rates requires it; package
// cafe runs a tool of its own.
var toolFixture = filepath.Join("testdata", "tools")

// `nomi tool <package> <name>` runs the package's tools/<name>.nomi in the
// current directory, with the access to files, the network and output that
// `nomi run` gives a program.
func TestToolCommand_RunsAPackagesTool(t *testing.T) {
	rates, err := filepath.Abs(filepath.Join(toolFixture, "rates"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := runNomiInDir(t, t.TempDir(), rates, "", "tool", "csvdata", "rows", "data/rates.csv", "data/names.csv")
	if err != nil {
		t.Fatalf("nomi tool: %v\n%s", err, out)
	}
	if want := "data/rates.csv: 3 rows\ndata/names.csv: 3 rows\n"; out != want {
		t.Errorf("nomi tool printed:\n%s\nwant:\n%s", out, want)
	}
	out, err = runNomiInDir(t, t.TempDir(), rates, "", "tool", "csvdata", "nope")
	if err == nil || !strings.Contains(out, "package csvdata has no tool nope: there is no tools/nope.nomi in") {
		t.Errorf("an unknown tool: %v\n%s", err, out)
	}
	out, err = runNomiInDir(t, t.TempDir(), rates, "", "tool", "nomodule", "rows")
	if err == nil || !strings.Contains(out, "no package nomodule: it is neither this package nor a dependency its nomi.toml names") {
		t.Errorf("an unknown module: %v\n%s", err, out)
	}
}

// A generator's --check finds the fixture's generated files current, so
// they are what the generator writes.
func TestToolCommand_TheFixturesGeneratedFilesAreCurrent(t *testing.T) {
	rates, err := filepath.Abs(filepath.Join(toolFixture, "rates"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := runNomiInDir(t, t.TempDir(), rates, "", "tool", "csvdata", "generate", "--check")
	if err != nil || out != "up to date\n" {
		t.Errorf("nomi tool csvdata generate --check: %v\n%s", err, out)
	}
}

// A tool whose main returns an Err fails with the error after "error: " and,
// for the cafe's own choices tool, writes nothing: a word that is not
// snake_case stops it before it writes enums.nomi.
func TestToolCommand_AToolsErrStopsIt(t *testing.T) {
	cafe := filepath.Join(t.TempDir(), "cafe")
	if err := os.CopyFS(cafe, os.DirFS(filepath.Join(toolFixture, "cafe"))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cafe, "enums.choices"), []byte("Size: Small medium large\nRoast: light medium dark\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	enums := filepath.Join(cafe, "enums.nomi")
	before, err := os.ReadFile(enums)
	if err != nil {
		t.Fatal(err)
	}
	out, err := runNomiInDir(t, t.TempDir(), cafe, "", "tool", "cafe", "choices")
	if code := exitCode(err); code != 1 {
		t.Errorf("nomi tool cafe choices exited %d (%v), want 1\n%s", code, err, out)
	}
	if want := "error: enums.choices:1: `Small` is not a snake_case word\n"; out != want {
		t.Errorf("nomi tool printed:\n%s\nwant:\n%s", out, want)
	}
	after, err := os.ReadFile(enums)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Errorf("the tool wrote enums.nomi after its error:\n%s", after)
	}
}

// A project that names sqlkit `db` configures it in `[db]` and runs its
// generator as `nomi tool db generate`, which writes code that imports
// sqlkit's files through `db` (`db::sqlkit`, `db::runtime`): the code
// checks and runs, `--check` finds it current, and `[sqlkit]`, the
// library's own name, is no table of this project.
func TestToolCommand_AToolReadsAndWritesTheNameTheProjectGivesIt(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; runs sqlkit's generator; -short")
	}
	lib := filepath.Join(repoRoot(t), "lib", "sql")
	app := t.TempDir()
	entry := func(key, dir string) string {
		return key + ` = { dir = ` + strconv.Quote(filepath.ToSlash(filepath.Join(lib, dir))) + " }\n"
	}
	manifest := "[package]\nname = \"app\"\nentry_points = [\"main\"]\n\n[dependencies]\n" +
		entry("db", "sqlkit") +
		entry("migrate", "migrate") +
		entry("postgres", "postgres") +
		entry("sqlite", "sqlite") +
		"\n[db.database.main]\nschema = \"tables\"\n"
	mustWrite(t, filepath.Join(app, "nomi.toml"), manifest)
	mustWrite(t, filepath.Join(app, "db", "migrations", "1_notes.sql"), "CREATE TABLE notes (id INTEGER PRIMARY KEY, body TEXT NOT NULL);\n")
	mustWrite(t, filepath.Join(app, "db", "queries", "notes.sql"), "-- query: add(body: String) -> Changed\nINSERT INTO notes (body) VALUES (@body);\n\n"+
		"-- query: all() -> List<Note>\nSELECT id, body FROM notes ORDER BY id;\n")
	mustWrite(t, filepath.Join(app, "main.nomi"), `import
    std::io
    std::sql.{self, Error}
    db::sqlkit
    notes
    sqlite
    tables
end

fn main() -> Result<Unit, Error>
    conn = sqlite.memory()?
    defer sql.close_quietly(conn)

    sqlkit.migrate(conn, tables.migrations)?
    added = notes.add(conn, "buy milk")?
    rows = notes.all(conn)?
    Iter.each(rows, fn n -> io.print("${n.id}: ${n.body} (${added.rows} added)"))
    Ok(Unit)
end
`)
	cache := t.TempDir()
	nomi := func(args ...string) string {
		t.Helper()
		out, err := runNomiInDir(t, cache, app, "", args...)
		if err != nil {
			t.Fatalf("nomi %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return out
	}
	if out := nomi("tool", "db", "generate"); out != "main: wrote tables.nomi, tables_test.nomi, notes.nomi\n" {
		t.Errorf("nomi tool db generate printed:\n%s", out)
	}
	for _, file := range []string{"tables.nomi", "tables_test.nomi", "notes.nomi"} {
		text := mustRead(t, filepath.Join(app, file))
		if !strings.HasPrefix(text, "// Generated by `nomi tool db generate`") || strings.Contains(text, "sqlkit::") || strings.Contains(text, "    sqlkit\n") {
			t.Errorf("%s does not import sqlkit by the project's name:\n%s", file, text)
		}
	}
	if notes := mustRead(t, filepath.Join(app, "notes.nomi")); !strings.Contains(notes, "    db::runtime\n    db::sqlkit\n") {
		t.Errorf("notes.nomi does not import db::runtime and db::sqlkit:\n%s", notes)
	}
	nomi("check", ".")
	if out := nomi("run", "main.nomi"); out != "1: buy milk (1 added)\n" {
		t.Errorf("nomi run main.nomi printed %q", out)
	}
	if out := nomi("tool", "db", "generate", "--check"); out != "main: up to date\n" {
		t.Errorf("nomi tool db generate --check printed:\n%s", out)
	}

	// The library's own name is not this project's name for it.
	mustWrite(t, filepath.Join(app, "nomi.toml"), strings.Replace(manifest, "[db.database.main]", "[sqlkit.database.main]", 1))
	out, err := runNomiInDir(t, cache, app, "", "check", ".")
	if err == nil || !strings.Contains(out, "nomi.toml has no [sqlkit] table") {
		t.Errorf("nomi check with a [sqlkit] table in a project that names sqlkit db: %v\n%s", err, out)
	}
}

// A package's tools are entry points without being listed in nomi.toml. The
// cafe lists only main: its tool runs, `nomi check .` passes and checks the
// tool with main, and main runs. Listing the tool as well changes nothing.
func TestToolCommand_AToolIsAnEntryWithoutBeingListed(t *testing.T) {
	for _, entries := range []string{`["main"]`, `["main", "tools/choices"]`} {
		cafe := filepath.Join(t.TempDir(), "cafe")
		if err := os.CopyFS(cafe, os.DirFS(filepath.Join(toolFixture, "cafe"))); err != nil {
			t.Fatal(err)
		}
		manifest := "[package]\nname = \"cafe\"\nentry_points = " + entries + "\n"
		if err := os.WriteFile(filepath.Join(cafe, "nomi.toml"), []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
		cache := t.TempDir()
		if out, err := runNomiInDir(t, cache, cafe, "", "tool", "cafe", "choices"); err != nil || out != "wrote enums.nomi\n" {
			t.Errorf("entry_points = %s: nomi tool cafe choices: %v\n%s", entries, err, out)
		}
		if out, err := runNomiInDir(t, cache, cafe, "", "check", "."); err != nil {
			t.Errorf("entry_points = %s: nomi check .: %v\n%s", entries, err, out)
		}
		if out, err := runNomiInDir(t, cache, cafe, "", "run", "main.nomi"); err != nil || out != "12\nTrue\n" {
			t.Errorf("entry_points = %s: nomi run main.nomi: %v\n%s", entries, err, out)
		}
		// `nomi check .` checks the tool, which nothing imports.
		tool := filepath.Join(cafe, "tools", "choices.nomi")
		src, err := os.ReadFile(tool)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(tool, append(src, "\nfn broken() -> Int\n    \"text\"\nend\n"...), 0o644); err != nil {
			t.Fatal(err)
		}
		if out, err := runNomiInDir(t, cache, cafe, "", "check", "."); err == nil || !strings.Contains(out, "choices.nomi") {
			t.Errorf("entry_points = %s: nomi check . of a broken tool: %v\n%s", entries, err, out)
		}
	}
}
