package analysis_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// macrosProject is the macro fixture (testdata/macros): module
// rates, whose tables.nomi declares two `table`s, and module
// csvdata, which declares the macro, copied to a directory a check
// reads them from. Its inputs are in texts too, by their path from rates'
// root: the two tables and the macro's own source.
func macrosProject(tb testing.TB) incrementalProject {
	dir := tb.TempDir()
	src := filepath.Join("..", "..", "testdata", "macros")
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dir, rel), 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dir, rel), data, 0o644)
	})
	if err != nil {
		tb.Fatal(err)
	}
	root := filepath.Join(dir, "rates")
	p := incrementalProject{name: "macros", entry: "main", dir: root, texts: map[string]string{}}
	for _, key := range []string{"main", "tables", "data/rates.csv", "data/names.csv", "../csvdata/csvdata.nomi"} {
		name := key
		if !isInputKey(key) {
			name += ".nomi"
		}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			tb.Fatal(err)
		}
		p.texts[key] = string(data)
	}
	return p
}

// macrosEdits edit a table, a declaration and the macro's code. An
// edit to a table or to a declaration's body that keeps what the
// expansion declares rebuilds the declaring file's bodies alone.
var macrosEdits = []editStep{
	replace("input body only: a value", "data/rates.csv", "usd,9200", "usd,9300"),
	replace("input body only: a row more", "data/rates.csv", "jpy,61\n", "jpy,61\nchf,10400\n"),
	restore("input body only: back", "data/rates.csv"),
	replace("input: a bad row", "data/rates.csv", "gbp,11700", "gbp,11700,1"),
	restore("input: good rows again", "data/rates.csv"),
	replace("input body only: a value of the wrong type", "data/names.csv", "Yen ${not interpolated}", "Yen"),
	replace("declaration body only: another table", "tables", "`names.csv`", "`rates.csv`"),
	restore("declaration body only: back", "tables"),
	replace("declaration signature", "tables", "-> Maybe<Int> `rates.csv`", "-> Maybe<Float> `rates.csv`"),
	restore("declaration signature back", "tables"),
	replace("macro source: what it writes", "../csvdata/csvdata.nomi", `"Some(${value})"`, `"Maybe.Some(${value})"`),
	restore("macro source back", "../csvdata/csvdata.nomi"),
	replace("macro source: a private function for a pub declaration", "../csvdata/csvdata.nomi", `visibility = if decl.public { "pub " } else { "" }`, `visibility = ""`),
	restore("macro source: pub again", "../csvdata/csvdata.nomi"),
	replace("macro source: a result other than the declared one", "../csvdata/csvdata.nomi", "|> Source.mapped(result.text, result.span)", `|> Source.add("Maybe<String>")`),
	restore("macro source: the declared result again", "../csvdata/csvdata.nomi"),
	replace("declaration signature: a parameter the expansion renames", "tables", "rate(code: String)", "rate(key: String)"),
	restore("declaration signature: the parameter back", "tables"),
	{"macro source: an effect, which is refused", "../csvdata/csvdata.nomi", withEffect},
	restore("macro source without the effect", "../csvdata/csvdata.nomi"),
	{"macro source: a type error, so it does not build", "../csvdata/csvdata.nomi", withTypeError},
	restore("macro source that checks again", "../csvdata/csvdata.nomi"),
}

// withTypeError makes csvdata's `expand` bind a String to an Int, so the
// macro's module does not check: every declaration declares nothing, and
// the program reports their failures alone.
func withTypeError(cur, _ string) (string, bool) {
	if !strings.Contains(cur, "        decl = call.decl\n") {
		return "", false
	}
	return strings.Replace(cur, "        decl = call.decl\n", "        decl = call.decl\n        _: Int = \"x\"\n", 1), true
}

// A macro whose module does not check declares nothing, and the program
// reports each declaration's failure alone: through the program kept for
// the root and in a document's own build alike, tables.nomi shows the two
// failures and main.nomi, which calls what they would have declared,
// shows nothing.
func TestMacroWhoseModuleDoesNotCheckIsReportedAlone(t *testing.T) {
	p := macrosProject(t)
	src := filepath.Join(p.dir, "..", "csvdata", "csvdata.nomi")
	text, ok := withTypeError(p.texts["../csvdata/csvdata.nomi"], "")
	if !ok {
		t.Fatal("csvdata.nomi has no `decl = call.decl` to add the error after")
	}
	if err := os.WriteFile(src, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	uri := func(key string) string { return "file://" + filepath.Join(p.dir, key+".nomi") }
	for _, kept := range []bool{true, false} {
		dm := newManager()
		for _, key := range []string{"main", "tables"} {
			dm.Open(uri(key), p.texts[key])
		}
		for _, key := range []string{"main", "tables"} {
			dm.UpdateImportEdges(uri(key))
		}
		if got := analysis.BuildForTest(dm, uri("main"), p.texts["main"], kept).TypeErrors; len(got) != 0 {
			t.Errorf("kept %v: main.nomi shows %+v, want nothing", kept, got)
		}
		got := analysis.BuildForTest(dm, uri("tables"), p.texts["tables"], kept).TypeErrors
		if len(got) != 2 {
			t.Fatalf("kept %v: tables.nomi shows %+v, want the two failures", kept, got)
		}
		for _, e := range got {
			if !strings.Contains(e.Message, "(module csvdata) could not expand") || !strings.Contains(e.Message, "its module does not check: "+src+":") {
				t.Errorf("kept %v: tables.nomi shows %q, want the failure", kept, e.Message)
			}
		}
	}
}

// withEffect makes csvdata's `expand` print, which the compiler refuses
// before it runs: every declaration declares nothing, and the program
// reports the refusals alone.
func withEffect(cur, _ string) (string, bool) {
	if !strings.Contains(cur, "    std/json.Json\n") || !strings.Contains(cur, "        decl = call.decl\n") {
		return "", false
	}
	cur = strings.Replace(cur, "    std/json.Json\n", "    std/io\n    std/json.Json\n", 1)
	return strings.Replace(cur, "        decl = call.decl\n", "        decl = call.decl\n        io.print(\"expanding\")\n", 1), true
}

// TestOpenDocumentMacroEditsMatchFreshBuilds: with the declaring file
// and its importer open, an edit to a table or to the macro's source on
// disk (the server's watched-file event, InputsChanged) and an edit to the
// declaring document each leave every open document with what a fresh
// analysis of it gives.
func TestOpenDocumentMacroEditsMatchFreshBuilds(t *testing.T) {
	p := macrosProject(t)
	uri := func(key string) string { return "file://" + filepath.Join(p.dir, filepath.FromSlash(key)+".nomi") }
	dm := newManager()
	cur := map[string]string{}
	for key, text := range p.texts {
		cur[key] = text
		if !isInputKey(key) {
			dm.Open(uri(key), text)
		}
	}
	for _, key := range []string{"main", "tables"} {
		dm.UpdateImportEdges(uri(key))
	}
	for i, s := range macrosEdits {
		text, ok := s.change(cur[s.key], p.texts[s.key])
		if !ok {
			t.Fatalf("step %d (%s) does not apply to %s", i+1, s.kind, s.key)
		}
		cur[s.key] = text
		if isInputKey(s.key) {
			path := filepath.Join(p.dir, filepath.FromSlash(s.key))
			if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
				t.Fatal(err)
			}
			if !dm.IsInput(path) {
				t.Fatalf("step %d (%s): %s is not an input the manager knows", i+1, s.kind, s.key)
			}
			dm.InputsChanged(path)
		} else {
			if err := os.WriteFile(strings.TrimPrefix(uri(s.key), "file://"), []byte(text), 0o644); err != nil {
				t.Fatal(err)
			}
			dm.Update(uri(s.key), text)
			dm.UpdateImportEdges(uri(s.key))
			dm.PropagateChange(uri(s.key))
		}
		for _, key := range []string{"main", "tables"} {
			got := renderBuild(analysis.InstalledForTest(dm, uri(key)))
			want := renderBuild(analysis.BuildForTest(newManager(), uri(key), cur[key], false))
			if got != want {
				t.Fatalf("step %d (%s, editing %s): %s gets\n%s\nand a fresh analysis of it gives\n%s", i+1, s.kind, s.key, key, got, want)
			}
		}
	}
}
