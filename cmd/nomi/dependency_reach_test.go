package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// reachModules writes, beside the project app in work, the local modules
// the add tests add: example.com/fetch reads files, example.com/pure
// computes, example.com/pg binds Go, and example.com/pg2 binds Go too.
func reachModules(t *testing.T, work string) {
	t.Helper()
	module := func(name, src string) {
		dir := filepath.Join(work, name)
		mustWrite(t, filepath.Join(dir, "go.mod"), "module example.com/"+name+"\n\ngo 1.27.0\n")
		mustWrite(t, filepath.Join(dir, "nomi.toml"), "[module]\nname = \""+name+"\"\n")
		mustWrite(t, filepath.Join(dir, name+".nomi"), src)
	}
	module("fetch", "import std::fs\n\npub fn load(path: String) -> String\n    fs.read_text(path) |> Result.with_default(\"\")\nend\n")
	module("pure", "pub fn double(n: Int) -> Int\n    n * 2\nend\n")
	module("pg", "gopkg \"strings\" as gostrings\n\npub fn upper(s: String) -> String go gostrings.ToUpper\n")
	module("pg2", "gopkg \"strings\" as gostrings\n\npub fn lower(s: String) -> String go gostrings.ToLower\n")
}

// reachMain is a main.nomi that imports each of modules and calls into it.
func reachMain(modules ...string) string {
	calls := map[string]string{
		"fetch": "    io.print(fetch.load(\"notes.txt\"))\n",
		"pure":  "    io.print(\"${pure.double(2)}\")\n",
		"pg":    "    io.print(pg.upper(\"hi\"))\n",
		"pg2":   "    io.print(pg2.lower(\"HI\"))\n",
	}
	src := "import\n    std::io\n"
	for _, m := range modules {
		src += "    " + m + "\n"
	}
	src += "end\n\nfn main()\n    io.print(\"start\")\n"
	for _, m := range modules {
		src += calls[m]
	}
	return src + "end\n"
}

// nomi add says what the module it added reaches, in the words nomi
// capabilities uses under modules: its std names, that it binds Go, that
// it reaches nothing, or, when nothing imports it yet, that. A project
// with no [capabilities] tables gets the reach and nothing else, and a
// project that does not check gets the add and one line saying why the
// reach is not known.
func TestModules_AddSaysWhatTheModuleReaches(t *testing.T) {
	c := newModuleCLI(t)
	work := t.TempDir()
	c.must(work, "new", "example.com/app")
	app := filepath.Join(work, "app")
	reachModules(t, work)

	cases := []struct {
		imports []string
		add     string
		want    string
	}{
		{nil, "pg2", "added example.com/pg2 from ../pg2 (module pg2; import pg2)\n\nmodules\n  example.com/pg2\n    not imported yet"},
		{[]string{"pure"}, "pure", "added example.com/pure from ../pure (module pure; import pure)\n\nmodules\n  example.com/pure\n    nothing"},
		{[]string{"pure", "fetch"}, "fetch", "added example.com/fetch from ../fetch (module fetch; import fetch)\n\nmodules\n  example.com/fetch\n    std::fs"},
		{[]string{"pure", "fetch", "pg"}, "pg", "added example.com/pg from ../pg (module pg; import pg)\n\nmodules\n  example.com/pg\n    binds Go"},
	}
	for _, tc := range cases {
		mustWrite(t, filepath.Join(app, "main.nomi"), reachMain(tc.imports...))
		if out := c.must(app, "add", "../"+tc.add); out != tc.want {
			t.Errorf("nomi add ../%s printed\n%s\nwant\n%s", tc.add, out, tc.want)
		}
	}
	mustWrite(t, filepath.Join(app, "main.nomi"), reachMain("pure"))
	if out, want := c.must(app, "remove", "fetch"), "removed example.com/fetch (from ../fetch)"; out != want {
		t.Fatalf("nomi remove printed %q", out)
	}
	mustWrite(t, filepath.Join(app, "main.nomi"), reachMain("pure", "fetch")+"\nfn broken() -> Int\n    undefined_name\nend\n")
	out := c.must(app, "add", "../fetch")
	want := "added example.com/fetch from ../fetch (module fetch; import fetch)\ncould not work out what example.com/fetch reaches: main.nomi does not check: "
	if !strings.HasPrefix(out, want) || strings.Count(out, "\n") != 1 || !strings.Contains(out, "undefined_name") {
		t.Errorf("nomi add in a project that does not check printed\n%s\nwant a second line starting\n%s", out, want)
	}
}

// With [capabilities] tables nomi add also says whether the tables allow
// every call the program reaches. With entries_required it writes the
// added module's tightest entry under [capabilities.deps.modules] and says
// so, but writes none for a module nothing imports yet. A module that
// binds Go is added to [capabilities.deps]' go only with --allow-go;
// without it the add says what to add, and the check refuses the binding.
func TestModules_AddWritesTheModulesEntry(t *testing.T) {
	c := newModuleCLI(t)
	work := t.TempDir()
	c.must(work, "new", "example.com/app")
	app := filepath.Join(work, "app")
	reachModules(t, work)
	manifest := "[module]\nname = \"app\"\nentry_points = [\"main\"]\n\n" +
		"[capabilities.self]\nstd = [\"std::io\"]\n\n" +
		"[capabilities.deps]\nentries_required = true\nstd = [\"std::fs\"]\n"
	mustWrite(t, filepath.Join(app, "nomi.toml"), manifest)

	mustWrite(t, filepath.Join(app, "main.nomi"), reachMain())
	want := "added example.com/pure from ../pure (module pure; import pure)\n\nmodules\n  example.com/pure\n    not imported yet\n\n" +
		"once a file imports it, entries_required needs its entry under [capabilities.deps.modules]; nomi capabilities --write writes it\n" +
		"nomi.toml's [capabilities] tables allow every call the program reaches"
	if out := c.must(app, "add", "../pure"); out != want {
		t.Errorf("nomi add of a module nothing imports printed\n%s\nwant\n%s", out, want)
	}
	if got := mustRead(t, filepath.Join(app, "nomi.toml")); got != manifest {
		t.Errorf("nomi add of a module nothing imports changed nomi.toml:\n%s", got)
	}

	mustWrite(t, filepath.Join(app, "main.nomi"), reachMain("fetch"))
	want = "added example.com/fetch from ../fetch (module fetch; import fetch)\n\nmodules\n  example.com/fetch\n    std::fs\n\n" +
		"wrote \"example.com/fetch\" = [\"std::fs\"] under [capabilities.deps.modules] in nomi.toml\n" +
		"nomi.toml's [capabilities] tables allow every call the program reaches"
	if out := c.must(app, "add", "../fetch"); out != want {
		t.Errorf("nomi add of a module that reads files printed\n%s\nwant\n%s", out, want)
	}
	if got := mustRead(t, filepath.Join(app, "nomi.toml")); !strings.HasSuffix(got, "[capabilities.deps.modules]\n\"example.com/fetch\" = [\"std::fs\"]\n") {
		t.Errorf("nomi.toml after nomi add ../fetch:\n%s", got)
	}

	mustWrite(t, filepath.Join(app, "main.nomi"), reachMain("fetch", "pg2"))
	want = "added example.com/pg2 from ../pg2 (module pg2; import pg2)\n\nmodules\n  example.com/pg2\n    binds Go\n\n" +
		"wrote \"example.com/pg2\" = [] under [capabilities.deps.modules] in nomi.toml\n" +
		"added \"example.com/pg2\" to go under [capabilities.deps] in nomi.toml, so its Go may run\n" +
		"nomi.toml's [capabilities] tables allow every call the program reaches"
	if out := c.must(app, "add", "--allow-go", "../pg2"); out != want {
		t.Errorf("nomi add --allow-go printed\n%s\nwant\n%s", out, want)
	}
	if got := mustRead(t, filepath.Join(app, "nomi.toml")); !strings.Contains(got, "go = [\"example.com/pg2\"]\n") {
		t.Errorf("nomi add --allow-go did not list the module in go:\n%s", got)
	}
	// The tables are the tightest ones now; nomi check would build pg2's Go.
	if out, stderr, err := c.run(app, "capabilities", "--check"); err != nil {
		t.Errorf("nomi capabilities --check after nomi add --allow-go: %v\n%s\n%s", err, out, stderr)
	}

	mustWrite(t, filepath.Join(app, "main.nomi"), reachMain("fetch", "pg2", "pg"))
	want = "added example.com/pg from ../pg (module pg; import pg)\n\nmodules\n  example.com/pg\n    binds Go\n\n" +
		"wrote \"example.com/pg\" = [] under [capabilities.deps.modules] in nomi.toml\n" +
		"example.com/pg binds Go, which nomi.toml's [capabilities.deps] does not allow; add \"example.com/pg\" to go under [capabilities.deps] in nomi.toml, or pass --allow-go to nomi add\n" +
		"nomi check refuses 1 call under nomi.toml's [capabilities] tables; nomi capabilities says how they differ"
	if out := c.must(app, "add", "../pg"); out != want {
		t.Errorf("nomi add of a module that binds Go printed\n%s\nwant\n%s", out, want)
	}
	if got := mustRead(t, filepath.Join(app, "nomi.toml")); !strings.Contains(got, "go = [\"example.com/pg2\"]\n") {
		t.Errorf("nomi add without --allow-go changed go:\n%s", got)
	}
}

// nomi update says, for each Nomi module it moves, what the new version
// reaches and how that differs from the old one: each std name it now also
// reaches or no longer reaches, and whether it now binds Go; a module
// nothing imports is not imported yet. With tables it says whether nomi
// check still passes them.
func TestModules_UpdateSaysWhatChangedInTheReach(t *testing.T) {
	c := newModuleCLI(t)
	greet := func(src string) map[string]string {
		return map[string]string{
			"go.mod":     "module example.com/greet\n\ngo 1.22\n",
			"nomi.toml":  "[module]\nname = \"greet\"\n",
			"greet.nomi": src,
		}
	}
	idle := func(v string) map[string]string {
		return map[string]string{
			"go.mod":    "module example.com/idle\n\ngo 1.22\n",
			"nomi.toml": "[module]\nname = \"idle\"\n",
			"idle.nomi": "pub fn version() -> String\n    \"" + v + "\"\nend\n",
		}
	}
	publishFiles(t, c.proxy, "example.com/greet", "v1.0.0", greet("import std::fs\n\npub fn hello() -> String\n    fs.read_text(\"greeting.txt\") |> Result.with_default(\"hello\")\nend\n"))
	publishFiles(t, c.proxy, "example.com/idle", "v1.0.0", idle("v1.0.0"))
	work := t.TempDir()
	c.must(work, "new", "example.com/app")
	app := filepath.Join(work, "app")
	mustWrite(t, filepath.Join(app, "main.nomi"), "import\n    std::io\n    greet\nend\n\nfn main()\n    io.print(greet.hello())\nend\n")
	c.must(app, "add", "example.com/greet")
	c.must(app, "add", "example.com/idle")
	mustWrite(t, filepath.Join(app, "nomi.toml"), "[module]\nname = \"app\"\nentry_points = [\"main\"]\n\n[capabilities.self]\nstd = [\"std::io\"]\n\n[capabilities.deps]\nstd = [\"std::fs\"]\n")

	publishFiles(t, c.proxy, "example.com/greet", "v1.1.0", greet("import\n    std::http\n    std::url.Url\nend\n\ngopkg \"strings\" as gostrings\n\n"+
		"pub fn upper(s: String) -> String go gostrings.ToUpper\n\npub fn hello() -> String\n    _ = http.get(Url\"https://example.com/greeting\")\n    upper(\"hello\")\nend\n"))
	publishFiles(t, c.proxy, "example.com/idle", "v1.1.0", idle("v1.1.0"))
	want := "example.com/greet v1.0.0 -> v1.1.0\nexample.com/idle v1.0.0 -> v1.1.0\n\nmodules\n" +
		"  example.com/greet\n    std::http\n    binds Go\n    now also reaches std::http\n    no longer reaches std::fs\n    now binds Go\n" +
		"  example.com/idle\n    not imported yet\n\n" +
		"nomi check refuses 2 calls under nomi.toml's [capabilities] tables; nomi capabilities says how they differ"
	if out := c.must(app, "update"); out != want {
		t.Errorf("nomi update printed\n%s\nwant\n%s", out, want)
	}
}
