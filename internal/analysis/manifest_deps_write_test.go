package analysis

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/modcache"
)

// WriteDependencies keeps every line outside [dependencies], and inside it
// the comments, blank lines and the text after each entry's value; it
// changes only the entries that change, adds new ones at the table's end,
// and drops removed ones with their comments.
func TestWriteDependencies_KeepsTheLayout(t *testing.T) {
	sqlite := modcache.Requirement{Name: "sqlite", Module: "example.com/sqlite", Version: "v0.4.0"}
	local := modcache.Requirement{Name: "local", Dir: "../local"}
	text := "# The shop.\n[module]\nname = \"shop\" # its name\n\n" +
		"[dependencies]\n# The driver.\nsqlite = { module = \"example.com/sqlite\", version = \"v0.3.0\" } # pinned\n\n" +
		"# Kept as it is.\nlocal   =   { dir = \"../local\" }\n# Goes.\nold = { dir = \"../old\" }\n\n[i18n]\nlocales = [\"en\"]\n"
	for _, tc := range []struct {
		name, text, want string
		deps             []modcache.Requirement
	}{
		{"change, keep, remove and add", text,
			"# The shop.\n[module]\nname = \"shop\" # its name\n\n" +
				"[dependencies]\n# The driver.\nsqlite = { module = \"example.com/sqlite\", version = \"v0.4.0\" } # pinned\n\n" +
				"# Kept as it is.\nlocal   =   { dir = \"../local\" }\n\nnew = { module = \"example.com/new\", version = \"v1.0.0\" }\n\n[i18n]\nlocales = [\"en\"]\n",
			[]modcache.Requirement{local, {Name: "new", Module: "example.com/new", Version: "v1.0.0"}, sqlite}},
		{"no table: after [module]", "[module]\nname = \"shop\"\n\n[i18n]\nlocales = [\"en\"]\n",
			"[module]\nname = \"shop\"\n\n[dependencies]\nsqlite = { module = \"example.com/sqlite\", version = \"v0.4.0\" }\n\n[i18n]\nlocales = [\"en\"]\n",
			[]modcache.Requirement{sqlite}},
		{"no table, [module] last", "[module]\nname = \"shop\"\n",
			"[module]\nname = \"shop\"\n\n[dependencies]\nlocal = { dir = \"../local\" }\n",
			[]modcache.Requirement{local}},
		{"the last entry goes with its table", "[module]\nname = \"shop\"\n\n[dependencies]\nlocal = { dir = \"../local\" }\n\n[i18n]\nlocales = [\"en\"]\n",
			"[module]\nname = \"shop\"\n\n[i18n]\nlocales = [\"en\"]\n",
			nil},
		{"CRLF", "[module]\r\nname = \"shop\"\r\n\r\n[dependencies]\r\nsqlite = { module = \"example.com/sqlite\", version = \"v0.3.0\" }\r\n",
			"[module]\r\nname = \"shop\"\r\n\r\n[dependencies]\r\nsqlite = { module = \"example.com/sqlite\", version = \"v0.4.0\" }\r\nlocal = { dir = \"../local\" }\r\n",
			[]modcache.Requirement{sqlite, local}},
	} {
		got, err := WriteDependencies([]byte(tc.text), tc.deps)
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
func TestWriteDependencies_AnEntryOfItsOwnTable(t *testing.T) {
	text := "[module]\nname = \"shop\"\n\n[dependencies.sqlite]\nmodule = \"example.com/sqlite\"\nversion = \"v0.3.0\"\n"
	keep := []modcache.Requirement{{Name: "sqlite", Module: "example.com/sqlite", Version: "v0.3.0"}, {Name: "local", Dir: "../local"}}
	got, err := WriteDependencies([]byte(text), keep)
	if err != nil {
		t.Fatalf("adding beside a dependency of its own table: %v", err)
	}
	if !strings.Contains(string(got), "[dependencies.sqlite]\nmodule = \"example.com/sqlite\"\nversion = \"v0.3.0\"\n") || !strings.Contains(string(got), "local = { dir = \"../local\" }") {
		t.Errorf("wrote\n%s", got)
	}
	_, err = WriteDependencies([]byte(text), []modcache.Requirement{{Name: "sqlite", Module: "example.com/sqlite", Version: "v0.4.0"}})
	if err == nil {
		t.Fatal("WriteDependencies changed a dependency written as a table of its own")
	}
	if want := "nomi.toml writes dependency sqlite as a table of its own or with dotted keys; edit its [dependencies] by hand"; err.Error() != want {
		t.Errorf("error %q, want %q", err, want)
	}
}
