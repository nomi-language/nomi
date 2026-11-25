package tourpackages_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/pkgcache"
	"github.com/nomi-language/nomi/internal/tourpackages"
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

// A block that imports a tour package gets the package's files attached,
// with nothing in the block that names where they come from, and a block
// that imports none is left as it is.
func TestSourceAttachesThePackagesABlockImports(t *testing.T) {
	src := tourpackages.Source(firstQuery)
	if !strings.Contains(src, "// FILE: main.nomi\n") || !strings.Contains(src, "// FILE: "+tourpackages.PackagesDir+"/sqlite/sqlite.nomi\n") ||
		!strings.Contains(src, "sqlite = { dir = \"./"+tourpackages.PackagesDir+"/sqlite\" }\n") ||
		strings.Contains(src, "sqlite_test.nomi") {
		t.Fatalf("the source with its packages:\n%s", src)
	}
	if got := tourpackages.Imported(firstQuery); strings.Join(got, ",") != "sqlite" {
		t.Fatalf("Imported: %q", got)
	}
	plain := "import std::io\n\nfn main()\n    io.print(\"sqlite\")\nend\n"
	if got := tourpackages.Source(plain); got != plain {
		t.Fatalf("a block that imports no tour package changed:\n%s", got)
	}
}

// TestPackagesAreTheirManifests holds each of tourpackages.Packages to the
// nomi.toml in its directory: its name, and the tour packages that file
// names by their directories, which Source attaches with it.
func TestPackagesAreTheirManifests(t *testing.T) {
	for _, m := range tourpackages.Packages {
		dir := filepath.Join(root, m.Dir)
		mf, err := pkgcache.ReadManifest(dir)
		if err != nil || mf == nil {
			t.Fatalf("%s/nomi.toml: %v", m.Dir, err)
		}
		if mf.Name != m.Name {
			t.Errorf("%s/nomi.toml is package %s; tourpackages.Packages says %s", m.Dir, mf.Name, m.Name)
		}
		var requires []string
		for _, r := range mf.Requires {
			for _, other := range tourpackages.Packages {
				if r.Dir != "" && r.LocalDir(dir) == filepath.Join(root, other.Dir) {
					requires = append(requires, other.Name)
				}
			}
		}

		if strings.Join(requires, " ") != strings.Join(m.Requires, " ") {
			t.Errorf("%s/nomi.toml names the tour packages %q; tourpackages.Packages says %q", m.Dir, requires, m.Requires)
		}
	}
}
