package tourmodules_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/modcache"
	"github.com/nomi-language/nomi/internal/tourmodules"
)

const root = "../.."

const firstQuery = `import
    std::io
    std::sql.{self, Conn, Error, Row, Sql}
    sqlite
end

fn main() -> Result<Unit, Error>
    db = sqlite.memory()?
    defer sql.close_quietly(db)

    _ = Conn.exec(db, Sql"CREATE TABLE notes (id INTEGER PRIMARY KEY, body TEXT NOT NULL)")?
    _ = Conn.exec(db, Sql"INSERT INTO notes (body) VALUES (${"buy milk"})")?
    rows = Conn.query(db, Sql"SELECT id, body FROM notes ORDER BY id")?
    lines = (rows |> Iter.map(note_line) |> Result.collect())?
    Iter.each(lines, io.print)
    Ok(Unit)
end

fn note_line(row: Row) -> Result<String, Error>
    id = Row.get<Int>(row, "id")?
    body = Row.get<String>(row, "body")?
    Ok("${id}: ${body}")
end
`

// A block that imports a tour module gets the module's files attached,
// with nothing in the block that names where they come from, and a block
// that imports none is left as it is.
func TestSourceAttachesTheModulesABlockImports(t *testing.T) {
	src := tourmodules.Source(firstQuery)
	if !strings.Contains(src, "// FILE: main.nomi\n") || !strings.Contains(src, "// FILE: "+tourmodules.ModulesDir+"/sqlite/sqlite.nomi\n") ||
		!strings.Contains(src, "sqlite = { dir = \"./"+tourmodules.ModulesDir+"/sqlite\" }\n") ||
		strings.Contains(src, "sqlite_test.nomi") {
		t.Fatalf("the source with its modules:\n%s", src)
	}
	if got := tourmodules.Imported(firstQuery); strings.Join(got, ",") != "sqlite" {
		t.Fatalf("Imported: %q", got)
	}
	plain := "import std::io\n\nfn main()\n    io.print(\"sqlite\")\nend\n"
	if got := tourmodules.Source(plain); got != plain {
		t.Fatalf("a block that imports no tour module changed:\n%s", got)
	}
}

// TestModulesAreTheirManifests holds each of tourmodules.Modules to the
// nomi.toml in its directory: its name, and the tour modules that file
// names by their directories, which Source attaches with it.
func TestModulesAreTheirManifests(t *testing.T) {
	for _, m := range tourmodules.Modules {
		dir := filepath.Join(root, m.Dir)
		mf, err := modcache.ReadModuleFile(dir)
		if err != nil || mf == nil {
			t.Fatalf("%s/nomi.toml: %v", m.Dir, err)
		}
		if mf.Name != m.Name {
			t.Errorf("%s/nomi.toml is module %s; tourmodules.Modules says %s", m.Dir, mf.Name, m.Name)
		}
		var requires []string
		for _, r := range mf.Requires {
			for _, other := range tourmodules.Modules {
				if r.Dir != "" && r.LocalDir(dir) == filepath.Join(root, other.Dir) {
					requires = append(requires, other.Name)
				}
			}
		}

		if strings.Join(requires, " ") != strings.Join(m.Requires, " ") {
			t.Errorf("%s/nomi.toml names the tour modules %q; tourmodules.Modules says %q", m.Dir, requires, m.Requires)
		}
	}
}
