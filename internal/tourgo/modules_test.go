package tourgo_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/tourgo"
	"golang.org/x/mod/modfile"
)

const firstQuery = `import {
    std/io
    std/sql.{self, Conn, Error, Row, Sql}
    sqlite
}

fn main() -> Result<Unit, Error> {
    sql.with_connection(sqlite.memory, fn db -> {
        _ = try Conn.exec(db, Sql"CREATE TABLE notes (id INTEGER PRIMARY KEY, body TEXT NOT NULL)")
        _ = try Conn.exec(db, Sql"INSERT INTO notes (body) VALUES (${"buy milk"})")
        rows = try Conn.query(db, Sql"SELECT id, body FROM notes ORDER BY id")
        lines = try (rows |> Iter.map(note_line) |> Result.collect())
        Iter.each(lines, io.print)
        Ok(Unit)
    })
}

fn note_line(row: Row) -> Result<String, Error> {
    id = try Row.get<Int>(row, "id")
    body = try Row.get<String>(row, "body")
    Ok("${id}: ${body}")
}
`

// A block that imports a tour module gets the module's files attached,
// with nothing in the block that names where they come from, and a block
// that imports none is left as it is. Outside the browser the module's Go
// side is not linked, so Unlinked names it there: the doctests compile such
// a block, and the staged wasm runs it (vmhost's TestTourWasm).
func TestSourceAttachesTheModulesABlockImports(t *testing.T) {
	src := tourgo.Source(firstQuery)
	if !strings.Contains(src, "// FILE: main.nomi\n") || !strings.Contains(src, "// FILE: "+tourgo.ModulesDir+"/sqlite/sqlite.nomi\n") ||
		!strings.Contains(src, "replace github.com/nomi-language/nomi/lib/sql/sqlite => ./"+tourgo.ModulesDir+"/sqlite\n") ||
		strings.Contains(src, "sqlite_test.nomi") {
		t.Fatalf("the source with its modules:\n%s", src)
	}
	if got := tourgo.Imported(firstQuery); strings.Join(got, ",") != "sqlite" {
		t.Fatalf("Imported: %q", got)
	}
	if got := tourgo.Unlinked(firstQuery); strings.Join(got, ",") != "sqlite" {
		t.Fatalf("Unlinked outside the browser: %q", got)
	}
	plain := "import std/io\n\nfn main() {\n    io.print(\"sqlite\")\n}\n"
	if got := tourgo.Source(plain); got != plain || len(tourgo.Unlinked(plain)) != 0 {
		t.Fatalf("a block that imports no tour module changed:\n%s", got)
	}
}

// TestTourWorkspaceUsesEveryModule holds tour.work, the workspace the tour's
// js/wasm build runs in, to the compiler's module and each tour module's
// directory, each module's go.mod to the path a block's go.mod requires it
// by, and the workspace's go line to the compiler's.
func TestTourWorkspaceUsesEveryModule(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(root, tourgo.GoWork))
	if err != nil {
		t.Fatal(err)
	}
	work, err := modfile.ParseWork(tourgo.GoWork, data, nil)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, u := range work.Use {
		got = append(got, filepath.ToSlash(filepath.Clean(filepath.Join(filepath.Dir(tourgo.GoWork), u.Path))))
	}
	want := []string{"."}
	for _, m := range tourgo.Modules {
		want = append(want, m.Dir)
		goMod, err := os.ReadFile(filepath.Join(root, m.Dir, "go.mod"))
		if err != nil {
			t.Fatal(err)
		}
		if path := modfile.ModulePath(goMod); path != m.Path {
			t.Errorf("%s/go.mod is module %s; tourgo.Modules says %s", m.Dir, path, m.Path)
		}
		// The tour modules its go.mod requires are its Requires, which
		// Source attaches with it.
		f, err := modfile.Parse(m.Dir+"/go.mod", goMod, nil)
		if err != nil {
			t.Fatal(err)
		}
		var requires []string
		for _, r := range f.Require {
			for _, other := range tourgo.Modules {
				if r.Mod.Path == other.Path {
					requires = append(requires, other.Name)
				}
			}
		}
		if strings.Join(requires, " ") != strings.Join(m.Requires, " ") {
			t.Errorf("%s/go.mod requires the tour modules %q; tourgo.Modules says %q", m.Dir, requires, m.Requires)
		}
	}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("%s uses %q; want %q", tourgo.GoWork, got, want)
	}
	rootMod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	f, err := modfile.Parse("go.mod", rootMod, nil)
	if err != nil {
		t.Fatal(err)
	}
	if work.Go == nil || f.Go == nil || work.Go.Version != f.Go.Version {
		t.Errorf("%s's go line differs from go.mod's %s", tourgo.GoWork, f.Go.Version)
	}
}
