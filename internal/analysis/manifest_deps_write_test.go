package analysis

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/pkgcache"
)

// WriteRequirements keeps every line outside [dependencies], and inside it
// the comments, blank lines and the text after each entry's value; it
// changes only the entries that change, adds new ones at the table's end,
// and drops removed ones with their comments.
func TestWriteRequirements_KeepsTheLayout(t *testing.T) {
	sqlite := pkgcache.Requirement{Name: "sqlite", ID: pkgcache.ID{Repo: "example.com/sqlite"}, Version: "v0.4.0"}
	local := pkgcache.Requirement{Name: "local", Dir: "../local"}
	text := "# The shop.\n[package]\nname = \"shop\" # its name\n\n" +
		"[dependencies]\n# The driver.\nsqlite = { repo = \"example.com/sqlite\", version = \"v0.3.0\" } # pinned\n\n" +
		"# Kept as it is.\nlocal   =   { dir = \"../local\" }\n# Goes.\nold = { dir = \"../old\" }\n\n[i18n]\nlocales = [\"en\"]\n"
	for _, tc := range []struct {
		name, text, want string
		deps             []pkgcache.Requirement
	}{
		{"change, keep, remove and add", text,
			"# The shop.\n[package]\nname = \"shop\" # its name\n\n" +
				"[dependencies]\n# The driver.\nsqlite = { repo = \"example.com/sqlite\", version = \"v0.4.0\" } # pinned\n\n" +
				"# Kept as it is.\nlocal   =   { dir = \"../local\" }\n\nnew = { repo = \"example.com/new\", version = \"v1.0.0\" }\n\n[i18n]\nlocales = [\"en\"]\n",
			[]pkgcache.Requirement{local, {Name: "new", ID: pkgcache.ID{Repo: "example.com/new"}, Version: "v1.0.0"}, sqlite}},
		{"no table: after [package]", "[package]\nname = \"shop\"\n\n[i18n]\nlocales = [\"en\"]\n",
			"[package]\nname = \"shop\"\n\n[dependencies]\nsqlite = { repo = \"example.com/sqlite\", version = \"v0.4.0\" }\n\n[i18n]\nlocales = [\"en\"]\n",
			[]pkgcache.Requirement{sqlite}},
		{"no table, [package] last", "[package]\nname = \"shop\"\n",
			"[package]\nname = \"shop\"\n\n[dependencies]\nlocal = { dir = \"../local\" }\n",
			[]pkgcache.Requirement{local}},
		{"the last entry goes with its table", "[package]\nname = \"shop\"\n\n[dependencies]\nlocal = { dir = \"../local\" }\n\n[i18n]\nlocales = [\"en\"]\n",
			"[package]\nname = \"shop\"\n\n[i18n]\nlocales = [\"en\"]\n",
			nil},
		{"CRLF", "[package]\r\nname = \"shop\"\r\n\r\n[dependencies]\r\nsqlite = { repo = \"example.com/sqlite\", version = \"v0.3.0\" }\r\n",
			"[package]\r\nname = \"shop\"\r\n\r\n[dependencies]\r\nsqlite = { repo = \"example.com/sqlite\", version = \"v0.4.0\" }\r\nlocal = { dir = \"../local\" }\r\n",
			[]pkgcache.Requirement{sqlite, local}},
	} {
		got, err := WriteRequirements([]byte(tc.text), tc.deps)
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if string(got) != tc.want {
			t.Errorf("%s: wrote\n%s\nwant\n%s", tc.name, strings.ReplaceAll(string(got), "\r", "\\r"), strings.ReplaceAll(tc.want, "\r", "\\r"))
		}
	}
}

// An entry written as a table of its own is kept, and changing it is an
// error that says to edit the table by hand.
func TestWriteRequirements_AnEntryOfItsOwnTable(t *testing.T) {
	text := "[package]\nname = \"shop\"\n\n[dependencies.sqlite]\nrepo = \"example.com/sqlite\"\nversion = \"v0.3.0\"\n"
	keep := []pkgcache.Requirement{{Name: "sqlite", ID: pkgcache.ID{Repo: "example.com/sqlite"}, Version: "v0.3.0"}, {Name: "local", Dir: "../local"}}
	got, err := WriteRequirements([]byte(text), keep)
	if err != nil {
		t.Fatalf("adding beside a dependency of its own table: %v", err)
	}
	if !strings.Contains(string(got), "[dependencies.sqlite]\nrepo = \"example.com/sqlite\"\nversion = \"v0.3.0\"\n") || !strings.Contains(string(got), "local = { dir = \"../local\" }") {
		t.Errorf("wrote\n%s", got)
	}
	_, err = WriteRequirements([]byte(text), []pkgcache.Requirement{{Name: "sqlite", ID: pkgcache.ID{Repo: "example.com/sqlite"}, Version: "v0.4.0"}})
	if err == nil {
		t.Fatal("WriteRequirements changed a dependency written as a table of its own")
	}
	if want := "nomi.toml writes dependency sqlite as a table of its own or with dotted keys; edit its [dependencies] by hand"; err.Error() != want {
		t.Errorf("error %q, want %q", err, want)
	}
}
