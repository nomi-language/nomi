package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The tour's Macros chapter, from the repository root.
var macroChapter = filepath.Join("tour", "src", "content", "docs", "macros.md")

// chapterConsole is one command of a ```console block and what the
// chapter says it prints.
type chapterConsole struct {
	args []string
	want string
}

// fences answers the contents of the chapter's fenced blocks of the given
// info string, in order.
func fences(t *testing.T, text, info string) []string {
	t.Helper()
	re := regexp.MustCompile("(?ms)^```" + regexp.QuoteMeta(info) + "\n(.*?)^```$")
	var blocks []string
	for _, m := range re.FindAllStringSubmatch(text, -1) {
		blocks = append(blocks, m[1])
	}
	return blocks
}

// consoleCommands splits a ```console block into its `$ nomi ...` commands
// and the output after each.
func consoleCommands(t *testing.T, block string) []chapterConsole {
	t.Helper()
	var cmds []chapterConsole
	for _, line := range strings.SplitAfter(block, "\n") {
		if rest, ok := strings.CutPrefix(line, "$ "); ok {
			fields := strings.Fields(rest)
			if len(fields) == 0 || fields[0] != "nomi" {
				t.Fatalf("a console command is not a nomi command: %q", line)
			}
			cmds = append(cmds, chapterConsole{args: fields[1:]})
			continue
		}
		if len(cmds) == 0 {
			t.Fatalf("a console block does not start with a command:\n%s", block)
		}
		cmds[len(cmds)-1].want += line
	}
	return cmds
}

// copyTree copies the directory from to to.
func copyTree(t *testing.T, from, to string) {
	t.Helper()
	err := filepath.WalkDir(from, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, path)
		target := filepath.Join(to, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// How chapterProject lays out a project's macro module.
type macroLayout int

const (
	// No macros/ directory, and a go.mod that requires nothing.
	macroBare macroLayout = iota
	// The macro module in macros/, and a go.mod that requires nothing.
	macroCopied
	// The macro module in macros/, required as `nomi add` requires it.
	macroAdded
)

// chapterProject lays out one of the chapter's projects in a fresh
// directory and answers its root: app is shop (with csvdata in
// macros/csvdata) or cafe (with choices in macros/choices).
func chapterProject(t *testing.T, app string, layout macroLayout) string {
	t.Helper()
	macro := map[string]string{"shop": "csvdata", "cafe": "choices"}[app]
	root := filepath.Join(t.TempDir(), app)
	copyTree(t, filepath.Join(macroFixture, app), root)
	if layout != macroBare {
		copyTree(t, filepath.Join(macroFixture, macro), filepath.Join(root, "macros", macro))
	}
	goMod := "module " + app + "\n\ngo 1.27.0\n"
	if layout == macroAdded {
		goMod += "\nrequire " + macro + " v0.0.0\n\nreplace " + macro + " => ./macros/" + macro + "\n"
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// edit replaces old with new in the file at path, which must hold old.
func edit(t *testing.T, path, old, new string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), old) {
		t.Fatalf("%s does not hold %q", path, old)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(data), old, new, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Every command the Macros chapter shows prints what the chapter quotes,
// run on the fixture projects testdata/macros/shop and cafe laid out as
// the chapter lays them out, and the chapter's sources are the fixtures'.
func TestMacroChapter_QuotesWhatTheCommandsPrint(t *testing.T) {
	data, err := os.ReadFile(macroChapter)
	if err != nil {
		t.Fatal(err)
	}
	chapter := string(data)

	// Each console block in chapter order, with the project it runs in and
	// the edit that makes the mistake it shows.
	cases := []struct {
		name   string
		app    string
		layout macroLayout
		setup  func(root string)
	}{
		{"nomi add csvdata", "shop", macroCopied, nil},
		{"nomi run", "shop", macroAdded, nil},
		{"nomi expand tables.nomi", "shop", macroAdded, nil},
		{"a price in dollars", "shop", macroAdded, func(root string) {
			edit(t, filepath.Join(root, "data", "prices.csv"), "tote,1800", "tote,18.00")
		}},
		{"a tag and a missing file", "shop", macroAdded, func(root string) {
			tables := filepath.Join(root, "tables.nomi")
			edit(t, tables, "Maybe<Int> `prices.csv`", "Maybe<Int> Csv`prices.csv`")
			edit(t, tables, "`names.csv`", "`name.csv`")
		}},
		{"a name declared twice", "shop", macroAdded, func(root string) {
			tables := filepath.Join(root, "tables.nomi")
			data, _ := os.ReadFile(tables)
			text := string(data) + "\nfn price(sku: String) -> Int {\n    String.length(sku)\n}\n"
			if err := os.WriteFile(tables, []byte(text), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{"nomi new choices", "cafe", macroBare, nil},
		{"nomi add choices", "cafe", macroCopied, nil},
		{"nomi expand sizes.nomi", "cafe", macroAdded, nil},
		{"a repeated word", "cafe", macroAdded, func(root string) {
			edit(t, filepath.Join(root, "sizes.nomi"), "`small medium large`", "`small medium small`")
		}},
		{"a word that is not a name", "cafe", macroAdded, func(root string) {
			edit(t, filepath.Join(root, "sizes.nomi"), "`small medium large`", "`small 12oz large`")
		}},
		{"nomi test", "cafe", macroAdded, nil},
	}

	blocks := fences(t, chapter, "console")
	if len(blocks) != len(cases) {
		t.Fatalf("the chapter has %d console blocks and the test %d cases; give each new block a case", len(blocks), len(cases))
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := chapterProject(t, c.app, c.layout)
			if c.setup != nil {
				c.setup(root)
			}
			for _, cmd := range consoleCommands(t, blocks[i]) {
				out, _ := runNomiInDir(t, t.TempDir(), root, "", cmd.args...)
				if out != cmd.want {
					t.Errorf("nomi %s printed:\n%s\nthe chapter says:\n%s", strings.Join(cmd.args, " "), out, cmd.want)
				}
			}
			if i == 0 {
				// The go.mod the chapter shows is the one `nomi add` wrote.
				got, err := os.ReadFile(filepath.Join(root, "go.mod"))
				if err != nil {
					t.Fatal(err)
				}
				if mods := fences(t, chapter, "gomod"); len(mods) != 1 || mods[0] != string(got) {
					t.Errorf("nomi add wrote go.mod:\n%s\nthe chapter shows:\n%v", got, mods)
				}
			}
		})
	}

	// Errors the chapter quotes in a sentence: csvdata's refusal of a
	// result that is not a Maybe, the compiler's of an expansion that
	// leaves out the declaration's `pub`, and one_of's of a name that is
	// not a type name.
	for _, c := range []struct {
		app   string
		setup func(root string)
		want  string
	}{
		{"shop", func(root string) {
			edit(t, filepath.Join(root, "tables.nomi"), "-> Maybe<Int>", "-> Int")
		}, "a table answers `None` for a missing key, so write `-> Maybe<Int>`"},
		{"cafe", func(root string) {
			choices := filepath.Join(root, "macros", "choices", "choices.nomi")
			edit(t, choices, `"${visibility}enum `, `"enum `)
			edit(t, choices, "visibility = if", "_visibility = if")
		}, "it should be `pub`, as the declaration is, a defect in the macro"},
		{"cafe", func(root string) {
			edit(t, filepath.Join(root, "sizes.nomi"), "one_of! Size", "one_of! size")
		}, "a `one_of` declares a type, so its name is a type name: `Size`"},
	} {
		root := chapterProject(t, c.app, macroAdded)
		c.setup(root)
		out, _ := runNomiInDir(t, t.TempDir(), root, "", "check", "main.nomi")
		if !strings.Contains(out, c.want) || !strings.Contains(chapter, c.want) {
			t.Errorf("nomi check printed:\n%s\nwant it and the chapter to say %q", out, c.want)
		}
	}

	// The sources the chapter shows are the fixtures': each file whole, and
	// choices.nomi as the macro and then its tests.
	nomiBlocks := fences(t, chapter, "nomi")
	csvBlocks := fences(t, chapter, "csv")
	has := func(blocks []string, text string) bool {
		for _, b := range blocks {
			if b == text {
				return true
			}
		}
		return false
	}
	read := func(parts ...string) string {
		data, err := os.ReadFile(filepath.Join(append([]string{macroFixture}, parts...)...))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	choices := read("choices", "choices.nomi")
	macro, tests, ok := strings.Cut(choices, "\n/// The call `expand` gets")
	if !ok {
		t.Fatal("choices.nomi has no test helper")
	}
	for name, text := range map[string]string{
		"shop/tables.nomi":             read("shop", "tables.nomi"),
		"shop/main.nomi":               read("shop", "main.nomi"),
		"cafe/sizes.nomi":              read("cafe", "sizes.nomi"),
		"choices/choices.nomi (macro)": macro,
		"choices/choices.nomi (tests)": "/// The call `expand` gets" + tests,
	} {
		if !has(nomiBlocks, text) {
			t.Errorf("the chapter shows no ```nomi block that is %s:\n%s", name, text)
		}
	}
	if prices := read("shop", "data", "prices.csv"); !has(csvBlocks, prices) {
		t.Errorf("the chapter shows no ```csv block that is shop/data/prices.csv:\n%s", prices)
	}
	if toml := read("shop", "nomi.toml"); !has(fences(t, chapter, "toml"), toml) {
		t.Errorf("the chapter shows no ```toml block that is shop/nomi.toml:\n%s", toml)
	}
}
