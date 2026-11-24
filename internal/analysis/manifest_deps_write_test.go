package analysis

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/modcache"
)

// WriteRequirements keeps every line outside [dependencies], and inside it
// the comments, blank lines and the text after each entry's value; it
// changes only the entries that change, adds new ones at the table's end,
// and drops removed ones with their comments.
func TestWriteRequirements_KeepsTheLayout(t *testing.T) {
	sqlite := modcache.Requirement{Name: "sqlite", ID: modcache.ID{Repo: "example.com/sqlite"}, Version: "v0.4.0"}
	local := modcache.Requirement{Name: "local", Dir: "../local"}
	text := "# The shop.\n[module]\nname = \"shop\" # its name\n\n" +
		"[dependencies]\n# The driver.\nsqlite = { repo = \"example.com/sqlite\", version = \"v0.3.0\" } # pinned\n\n" +
		"# Kept as it is.\nlocal   =   { dir = \"../local\" }\n# Goes.\nold = { dir = \"../old\" }\n\n[i18n]\nlocales = [\"en\"]\n"
	for _, tc := range []struct {
		name, text, want string
		deps             []modcache.Requirement
	}{
		{"change, keep, remove and add", text,
			"# The shop.\n[module]\nname = \"shop\" # its name\n\n" +
				"[dependencies]\n# The driver.\nsqlite = { repo = \"example.com/sqlite\", version = \"v0.4.0\" } # pinned\n\n" +
				"# Kept as it is.\nlocal   =   { dir = \"../local\" }\n\nnew = { repo = \"example.com/new\", version = \"v1.0.0\" }\n\n[i18n]\nlocales = [\"en\"]\n",
			[]modcache.Requirement{local, {Name: "new", ID: modcache.ID{Repo: "example.com/new"}, Version: "v1.0.0"}, sqlite}},
		{"no table: after [module]", "[module]\nname = \"shop\"\n\n[i18n]\nlocales = [\"en\"]\n",
			"[module]\nname = \"shop\"\n\n[dependencies]\nsqlite = { repo = \"example.com/sqlite\", version = \"v0.4.0\" }\n\n[i18n]\nlocales = [\"en\"]\n",
			[]modcache.Requirement{sqlite}},
		{"no table, [module] last", "[module]\nname = \"shop\"\n",
			"[module]\nname = \"shop\"\n\n[dependencies]\nlocal = { dir = \"../local\" }\n",
			[]modcache.Requirement{local}},
		{"the last entry goes with its table", "[module]\nname = \"shop\"\n\n[dependencies]\nlocal = { dir = \"../local\" }\n\n[i18n]\nlocales = [\"en\"]\n",
			"[module]\nname = \"shop\"\n\n[i18n]\nlocales = [\"en\"]\n",
			nil},
		{"CRLF", "[module]\r\nname = \"shop\"\r\n\r\n[dependencies]\r\nsqlite = { repo = \"example.com/sqlite\", version = \"v0.3.0\" }\r\n",
			"[module]\r\nname = \"shop\"\r\n\r\n[dependencies]\r\nsqlite = { repo = \"example.com/sqlite\", version = \"v0.4.0\" }\r\nlocal = { dir = \"../local\" }\r\n",
			[]modcache.Requirement{sqlite, local}},
	} {
		got, err := WriteRequirements([]byte(tc.text), tc.deps, nil)
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if string(got) != tc.want {
			t.Errorf("%s: wrote\n%s\nwant\n%s", tc.name, strings.ReplaceAll(string(got), "\r", "\\r"), strings.ReplaceAll(tc.want, "\r", "\\r"))
		}
	}
}

// [indirect] is written by the same rules as [dependencies]: a new table
// goes after [dependencies], an entry that changes keeps its comment, one
// that goes takes its comments, and a table left empty goes.
func TestWriteRequirements_Indirect(t *testing.T) {
	kit := modcache.Requirement{Name: "kit", ID: modcache.ID{Repo: "example.com/kit"}, Version: "v1.0.0"}
	base := "[module]\nname = \"shop\"\n\n[dependencies]\nkit = { repo = \"example.com/kit\", version = \"v1.0.0\" }\n\n[i18n]\nlocales = [\"en\"]\n"
	strutil := modcache.Floor{Repo: "example.com/strutil", Version: "v1.1.0"}
	words := modcache.Floor{Repo: "example.com/words", Version: "v0.3.0"}
	added, err := WriteRequirements([]byte(base), []modcache.Requirement{kit}, []modcache.Floor{strutil})
	if err != nil {
		t.Fatal(err)
	}
	if want := "[module]\nname = \"shop\"\n\n[dependencies]\nkit = { repo = \"example.com/kit\", version = \"v1.0.0\" }\n\n" +
		"[indirect]\n\"example.com/strutil\" = \"v1.1.0\"\n\n[i18n]\nlocales = [\"en\"]\n"; string(added) != want {
		t.Errorf("a new [indirect]:\n%s\nwant\n%s", added, want)
	}
	commented := strings.Replace(string(added), "\"example.com/strutil\" = \"v1.1.0\"\n", "# raised by nomi update\n\"example.com/strutil\" = \"v1.1.0\" # was v1.0.0\n", 1)
	changed, err := WriteRequirements([]byte(commented), []modcache.Requirement{kit}, []modcache.Floor{{Repo: strutil.Repo, Version: "v1.2.0"}, words})
	if err != nil {
		t.Fatal(err)
	}
	if want := "[indirect]\n# raised by nomi update\n\"example.com/strutil\" = \"v1.2.0\" # was v1.0.0\n\"example.com/words\" = \"v0.3.0\"\n\n[i18n]"; !strings.Contains(string(changed), want) {
		t.Errorf("a changed [indirect]:\n%s\nwant it to hold\n%s", changed, want)
	}
	gone, err := WriteRequirements(changed, []modcache.Requirement{kit}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(gone) != base {
		t.Errorf("an [indirect] with no entry left:\n%s\nwant\n%s", gone, base)
	}
}

// An entry written as a table of its own is kept, and changing it is an
// error that says to edit the table by hand.
func TestWriteRequirements_AnEntryOfItsOwnTable(t *testing.T) {
	text := "[module]\nname = \"shop\"\n\n[dependencies.sqlite]\nrepo = \"example.com/sqlite\"\nversion = \"v0.3.0\"\n"
	keep := []modcache.Requirement{{Name: "sqlite", ID: modcache.ID{Repo: "example.com/sqlite"}, Version: "v0.3.0"}, {Name: "local", Dir: "../local"}}
	got, err := WriteRequirements([]byte(text), keep, nil)
	if err != nil {
		t.Fatalf("adding beside a dependency of its own table: %v", err)
	}
	if !strings.Contains(string(got), "[dependencies.sqlite]\nrepo = \"example.com/sqlite\"\nversion = \"v0.3.0\"\n") || !strings.Contains(string(got), "local = { dir = \"../local\" }") {
		t.Errorf("wrote\n%s", got)
	}
	_, err = WriteRequirements([]byte(text), []modcache.Requirement{{Name: "sqlite", ID: modcache.ID{Repo: "example.com/sqlite"}, Version: "v0.4.0"}}, nil)
	if err == nil {
		t.Fatal("WriteRequirements changed a dependency written as a table of its own")
	}
	if want := "nomi.toml writes dependency sqlite as a table of its own or with dotted keys; edit its [dependencies] by hand"; err.Error() != want {
		t.Errorf("error %q, want %q", err, want)
	}
}
