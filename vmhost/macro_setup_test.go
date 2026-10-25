package vmhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A macro module the project has not set up is an error at the import that
// says what is missing: no require line for it in go.mod, or a replaced
// directory with no nomi.toml. The declarations that would use the
// macro add no error of their own.
func TestMacro_ModuleSetupErrorsSayWhatIsMissing(t *testing.T) {
	cases := []struct {
		name, gomod, tables string
		noManifest          bool
		want, hint          string
	}{
		{name: "no require, no replace", gomod: "module rates\n\ngo 1.27.0\n",
			want: "tables.nomi:1:8: no module `csvdata`: no file csvdata.nomi in this file's directory, and go.mod has no `require` line for `csvdata`",
			hint: "run `nomi add ../csvdata` to add the module in that directory"},
		{name: "a replace and no require", gomod: "module rates\n\ngo 1.27.0\n\nreplace csvdata => ../csvdata\n",
			want: "tables.nomi:1:8: no module `csvdata`: no file csvdata.nomi in this file's directory, and go.mod has no `require` line for `csvdata`",
			hint: "go.mod replaces `csvdata` with ../csvdata but does not require it; run `nomi add ../csvdata`"},
		{name: "an item import and no require", gomod: "module rates\n\ngo 1.27.0\n",
			tables: "import csvdata.table\n\npub table! rate(code: String) -> Maybe<Int> `rates.csv`\n\npub table! currency_name(code: String) -> Maybe<String> `names.csv`\n",
			want:   "tables.nomi:1:8: no module `csvdata`: no file csvdata.nomi in this file's directory, and go.mod has no `require` line for `csvdata`"},
		{name: "a replaced directory with no nomi.toml", noManifest: true,
			want: "tables.nomi:1:8: no module `csvdata`: go.mod replaces `csvdata` with ../csvdata, which has no nomi.toml",
			hint: "a Nomi module's root holds a nomi.toml naming it: `[module]`, then `name = \"csvdata\"`"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := copyFixture(t)
			if c.gomod != "" {
				writeFile(t, filepath.Join(dir, "rates", "go.mod"), c.gomod)
			}
			if c.tables != "" {
				writeFile(t, filepath.Join(dir, "rates", "tables.nomi"), c.tables)
			}
			if c.noManifest {
				if err := os.Remove(filepath.Join(dir, "csvdata", "nomi.toml")); err != nil {
					t.Fatal(err)
				}
			}
			text := checkText(t, filepath.Join(dir, "rates", "main.nomi"))
			if !strings.Contains(text, c.want) || !strings.Contains(text, c.hint) {
				t.Errorf("got:\n%s\nwant %q with the hint %q", text, c.want, c.hint)
			}
			for _, followUp := range []string{"names no macro", "unknown declaration keyword"} {
				if strings.Contains(text, followUp) {
					t.Errorf("the failed import is followed by %q:\n%s", followUp, text)
				}
			}
		})
	}
}

// A plain import of a missing file in a project with a go.mod says the
// go.mod part only when the path could name a module: not when it is a
// misspelling of a file, or its first name is a directory of the project.
func TestMissingImport_NamesGoModOnlyForAModule(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "nomi.toml"), "[module]\nname = \"app\"\nentry_points = [\"main\"]\n")
	writeFile(t, filepath.Join(dir, "go.mod"), "module app\n\ngo 1.27.0\n")
	writeFile(t, filepath.Join(dir, "tables.nomi"), "pub fn one() -> Int {\n    1\n}\n")
	writeFile(t, filepath.Join(dir, "sub", "other.nomi"), "pub fn two() -> Int {\n    2\n}\n")
	for _, c := range []struct{ imp, want, not string }{
		{"helpers", "no module `helpers`: no file helpers.nomi in this file's directory, and go.mod has no `require` line for `helpers`", ""},
		{"helpers", "run `nomi add <module path>` to add a published module, or `nomi add <directory>` for one on disk", "nomi add ../"},
		{"tabels", "no module `tabels`: no file tabels.nomi in this file's directory", "go.mod"},
		{"sub/lib", "no module `sub/lib`: no file lib.nomi in sub/", "go.mod"},
	} {
		main := filepath.Join(dir, "main.nomi")
		writeFile(t, main, "import "+c.imp+"\n\nfn main() {\n}\n")
		text := checkText(t, main)
		if !strings.Contains(text, c.want) || (c.not != "" && strings.Contains(text, c.not)) {
			t.Errorf("import %s: got:\n%s\nwant %q and no %q", c.imp, text, c.want, c.not)
		}
	}
}
