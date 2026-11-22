package main

import (
	"path/filepath"
	"testing"
)

// treeProject is an app whose main reaches std::fs by two paths through
// read_settings, uses read_settings as a value, calls a dependency whose
// call nomi.toml's entry for it refuses, and calls two functions that
// reach nothing outside; one of its two tests reaches std::fs.
func treeProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	app := filepath.Join(dir, "app")
	mustWrite(t, filepath.Join(app, "nomi.toml"), effectsModule+"\n[effects.self]\nstd = [\"std::io\", \"std::fs\"]\n\n"+
		"[effects.deps]\nentries_required = true\n\n[effects.deps.modules]\n\"pricing\" = []\n"+"\n[dependencies]\npricing = { dir = \"../pricing\" }\n")
	mustWrite(t, filepath.Join(app, "main.nomi"), `import
    std::fs
    std::io
    pricing::rules
end

fn read_settings(path: String) -> String
    fs.read_text(path) |> Result.with_default("")
end

fn load_config() -> String
    read_settings("settings.txt")
end

fn reload() -> String
    read_settings("backup.txt") |> String.trim()
end

fn twice(n: Int) -> Int
    n * 2
end

fn label(n: Int) -> String
    "#${twice(n)}"
end

fn apply(f: (String) -> String, s: String) -> String
    f(s)
end

fn main()
    io.print(load_config())
    io.print(reload())
    io.print(apply(read_settings, "other.txt"))
    io.print(rules.load("prices.txt"))
    io.print(label(2))
end

test "reads the settings"
    assert load_config() == ""
end

test "doubles"
    assert twice(2) == 4
end
`)
	mustWrite(t, filepath.Join(dir, "pricing", "nomi.toml"), "[module]\nname = \"pricing\"\n")
	mustWrite(t, filepath.Join(dir, "pricing", "rules.nomi"), "import std::fs\n\npub fn load(path: String) -> String\n    fs.read_text(path) |> Result.with_default(\"\")\nend\n")
	return app
}

// treeMain is treeProject's main as --tree prints it: a function reached
// by a second path, or used as a value, is expanded once and referred to
// after; a chain of single steps is one line; each effect call names what
// a list holds it by, and a refused one the list that refuses it; the
// functions main reaches that reach nothing outside are one line.
const treeMain = `main
├─ io.print                                     std::io
├─ load_config → read_settings → fs.read_text   std::fs
├─ reload → read_settings (above)
├─ read_settings (as a value) (above)
├─ load (pricing) → fs.read_text                std::fs  refused: the entry for pricing
└─ 2 functions with no effects
`

// --tree prints main's tree, then each test file's cases on one line,
// and --tests, or a file for the path, prints each case that reaches
// outside, counting the rest.
func TestEffects_Tree(t *testing.T) {
	app := treeProject(t)
	got := runNomiIn(t, app, "off", "effects", "--tree")
	want := treeMain + "main.nomi: 2 tests, 1 of which has the effect std::fs\n--tests prints the tree of each test\n"
	if got.exit != 0 || got.stdout != want {
		t.Errorf("--tree:\n%s\nwant stdout:\n%s", got, want)
	}
	const tests = "main.nomi: 2 tests\ntest \"reads the settings\"\n└─ load_config (above)\nand 1 test with no effects\n"
	for _, args := range [][]string{{"--tree", "--tests"}, {"--tree", "main.nomi"}} {
		got := runNomiIn(t, app, "off", append([]string{"effects"}, args...)...)
		if want := treeMain + tests; got.exit != 0 || got.stdout != want {
			t.Errorf("%v:\n%s\nwant stdout:\n%s", args, got, want)
		}
	}
}

// --tree --format mermaid writes the same graph as a flowchart: each
// function once, an edge labelled when it is not a plain call, the outside
// calls grouped by what a list names them with a refused one marked, the
// functions that reach nothing outside as one node under main, and the
// test file as one node with an edge to what its tests reach.
func TestEffects_TreeMermaid(t *testing.T) {
	app := treeProject(t)
	got := runNomiIn(t, app, "off", "effects", "--tree", "--format", "mermaid")
	const want = `flowchart LR
  f1["main"]
  f2["load_config"]
  f3["read_settings"]
  f4["reload"]
  f5["load (pricing)"]
  p1["2 functions with no effects"]
  t1["main.nomi: 2 tests"]
  subgraph m1["std::io"]
    c1["io.print"]
  end
  subgraph m2["std::fs"]
    c2["fs.read_text"]
    c3["fs.read_text (refused: the entry for pricing)"]
  end
  f1 --> c1
  f3 --> c2
  f2 --> f3
  f1 --> f2
  f4 --> f3
  f1 --> f4
  f1 -- "as a value" --> f3
  f5 --> c3
  f1 --> f5
  f1 -.-> p1
  t1 --> m2
  classDef refused stroke:#d33,stroke-width:2px
  class c3 refused
`
	if got.exit != 0 || got.stdout != want {
		t.Errorf("--tree --format mermaid:\n%s\nwant stdout:\n%s", got, want)
	}
}

// --tree goes with no other mode, --tests and mermaid go with --tree
// alone, and the tree has no JSON form.
func TestEffects_TreeFlags(t *testing.T) {
	app := treeProject(t)
	const usage = "usage: nomi effects [--write | --check | --why <name> | --tree [--tests]] [--format text|json|mermaid] [path]\n"
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--tree", "--why", "std::fs"}, "nomi effects: --tree and --why do not go together\n"},
		{[]string{"--tests"}, "nomi effects: --tests goes with --tree\n"},
		{[]string{"--format", "mermaid"}, "nomi effects: --format mermaid goes with --tree\n"},
		{[]string{"--tree", "--format", "json"}, "nomi effects: --tree prints text or mermaid\n"},
	} {
		got := runNomiIn(t, app, "off", append([]string{"effects"}, tc.args...)...)
		if got.exit != 1 || got.stderr != tc.want+usage {
			t.Errorf("%v:\n%s\nwant stderr:\n%s", tc.args, got, tc.want+usage)
		}
	}
}
