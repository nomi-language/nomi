package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// reachModules writes, beside the project app in work, the local packages
// the add tests add: fetch reads files, and pure and pure2 compute.
func reachModules(t *testing.T, work string) {
	t.Helper()
	module := func(name, src string) {
		dir := filepath.Join(work, name)
		mustWrite(t, filepath.Join(dir, "nomi.toml"), "[package]\nname = \""+name+"\"\n")
		mustWrite(t, filepath.Join(dir, name+".nomi"), src)
	}
	module("fetch", "import std::fs\n\npub fn load(path: String) -> String\n    fs.read_text(path) |> Result.with_default(\"\")\nend\n")
	module("pure", "pub fn double(n: Int) -> Int\n    n * 2\nend\n")
	module("pure2", "pub fn triple(n: Int) -> Int\n    n * 3\nend\n")
}

// reachMain is a main.nomi that imports each of modules and calls into it.
func reachMain(modules ...string) string {
	calls := map[string]string{
		"fetch": "    io.print(fetch.load(\"notes.txt\"))\n",
		"pure":  "    io.print(\"${pure.double(2)}\")\n",
		"pure2": "    io.print(\"${pure2.triple(2)}\")\n",
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

// nomi add says what the package it added reaches, in the words nomi
// effects uses under packages: its std names, that it reaches nothing, or, when nothing imports it yet, that. A project
// with no [effects] tables gets the reach and nothing else, and a
// project that does not check gets the add and one line saying why the
// reach is not known.
func TestPackages_AddSaysWhatThePackageReaches(t *testing.T) {
	c := newModuleCLI(t)
	work := t.TempDir()
	c.must(work, "new", "app")
	app := filepath.Join(work, "app")
	reachModules(t, work)

	cases := []struct {
		imports []string
		add     string
		want    string
	}{
		{nil, "pure2", "added pure2 from ../pure2 (import pure2)\n\npackages\n  pure2\n    not imported yet"},
		{[]string{"pure"}, "pure", "added pure from ../pure (import pure)\n\npackages\n  pure\n    no effects"},
		{[]string{"pure", "fetch"}, "fetch", "added fetch from ../fetch (import fetch)\n\npackages\n  fetch\n    std::fs"},
	}
	for _, tc := range cases {
		mustWrite(t, filepath.Join(app, "main.nomi"), reachMain(tc.imports...))
		if out := c.must(app, "add", "../"+tc.add); out != tc.want {
			t.Errorf("nomi add ../%s printed\n%s\nwant\n%s", tc.add, out, tc.want)
		}
	}
	mustWrite(t, filepath.Join(app, "main.nomi"), reachMain("pure"))
	if out, want := c.must(app, "remove", "fetch"), "removed fetch (from ../fetch)"; out != want {
		t.Fatalf("nomi remove printed %q", out)
	}
	mustWrite(t, filepath.Join(app, "main.nomi"), reachMain("pure", "fetch")+"\nfn broken() -> Int\n    undefined_name\nend\n")
	out := c.must(app, "add", "../fetch")
	want := "added fetch from ../fetch (import fetch)\ncould not work out the effects of fetch: main.nomi does not check: "
	if !strings.HasPrefix(out, want) || strings.Count(out, "\n") != 1 || !strings.Contains(out, "undefined_name") {
		t.Errorf("nomi add in a project that does not check printed\n%s\nwant a second line starting\n%s", out, want)
	}
}

// With [effects] tables nomi add also says whether the tables allow
// every call the program reaches. With entries_required it writes the
// added entry's tightest `effects` key and says so, but writes none for a
// package nothing imports yet.
func TestPackages_AddWritesThePackagesEntry(t *testing.T) {
	c := newModuleCLI(t)
	work := t.TempDir()
	c.must(work, "new", "app")
	app := filepath.Join(work, "app")
	reachModules(t, work)
	manifest := "[package]\nname = \"app\"\nentry_points = [\"main\"]\n\n" +
		"[effects.self]\neffects = [\"std::io\"]\n\n" +
		"[effects.dependencies]\nentries_required = true\neffects = [\"std::fs\"]\n"
	mustWrite(t, filepath.Join(app, "nomi.toml"), manifest)

	mustWrite(t, filepath.Join(app, "main.nomi"), reachMain())
	want := "added pure from ../pure (import pure)\n\npackages\n  pure\n    not imported yet\n\n" +
		"once a file imports it, entries_required needs an effects key on its [dependencies] entry; nomi effects --write writes it\n" +
		"nomi.toml's [effects] tables allow every effect the program has"
	if out := c.must(app, "add", "../pure"); out != want {
		t.Errorf("nomi add of a module nothing imports printed\n%s\nwant\n%s", out, want)
	}
	withPure := strings.Replace(manifest, "\n\n[effects.self]", "\n\n[dependencies]\npure = { dir = \"../pure\" }\n\n[effects.self]", 1)
	if got := mustRead(t, filepath.Join(app, "nomi.toml")); got != withPure {
		t.Errorf("nomi add of a module nothing imports wrote more than its entry into nomi.toml:\n%s", got)
	}

	mustWrite(t, filepath.Join(app, "main.nomi"), reachMain("fetch"))
	want = "added fetch from ../fetch (import fetch)\n\npackages\n  fetch\n    std::fs\n\n" +
		"wrote effects = [\"std::fs\"] on fetch under [dependencies] in nomi.toml\n" +
		"nomi.toml's [effects] tables allow every effect the program has"
	if out := c.must(app, "add", "../fetch"); out != want {
		t.Errorf("nomi add of a module that reads files printed\n%s\nwant\n%s", out, want)
	}
	if got := mustRead(t, filepath.Join(app, "nomi.toml")); !strings.Contains(got, "fetch = { dir = \"../fetch\", effects = [\"std::fs\"] }\n") {
		t.Errorf("nomi.toml after nomi add ../fetch:\n%s", got)
	}

}

// nomi update says, for each package it moves, what the new version
// reaches and how that differs from the old one: each std name it now also
// reaches or no longer reaches; a package nothing imports is not imported yet. With tables it says whether nomi
// check still passes them.
func TestPackages_UpdateSaysWhatChangedInTheReach(t *testing.T) {
	c := newModuleCLI(t)
	greet := func(src string) map[string]string {
		return map[string]string{
			"nomi.toml":  "[package]\nname = \"greet\"\n",
			"greet.nomi": src,
		}
	}
	idle := func(v string) map[string]string {
		return map[string]string{
			"nomi.toml": "[package]\nname = \"idle\"\n",
			"idle.nomi": "pub fn version() -> String\n    \"" + v + "\"\nend\n",
		}
	}
	c.host.Tag("example.com/greet", "v1.0.0", greet("import std::fs\n\npub fn hello() -> String\n    fs.read_text(\"greeting.txt\") |> Result.with_default(\"hello\")\nend\n"))
	c.host.Tag("example.com/idle", "v1.0.0", idle("v1.0.0"))
	work := t.TempDir()
	c.must(work, "new", "app")
	app := filepath.Join(work, "app")
	mustWrite(t, filepath.Join(app, "main.nomi"), "import\n    std::io\n    greet\nend\n\nfn main()\n    io.print(greet.hello())\nend\n")
	c.must(app, "add", "example.com/greet")
	c.must(app, "add", "example.com/idle")
	mustWrite(t, filepath.Join(app, "nomi.toml"), mustRead(t, filepath.Join(app, "nomi.toml"))+"\n[effects.self]\neffects = [\"std::io\"]\n\n[effects.dependencies]\neffects = [\"std::fs\"]\n")

	c.host.Tag("example.com/greet", "v1.1.0", greet("import\n    std::http\n    std::url.Url\nend\n\n"+
		"pub fn hello() -> String\n    _ = http.get(Url\"https://example.com/greeting\")\n    \"hello\"\nend\n"))
	c.host.Tag("example.com/idle", "v1.1.0", idle("v1.1.0"))
	want := "example.com/greet v1.0.0 -> v1.1.0\nexample.com/idle v1.0.0 -> v1.1.0\n\npackages\n" +
		"  greet\n    std::http\n    now also has the effect std::http\n    no longer has the effect std::fs\n" +
		"  idle\n    not imported yet\n\n" +
		"nomi check refuses 1 call under nomi.toml's [effects] tables; nomi effects says how they differ"
	if out := c.must(app, "update"); out != want {
		t.Errorf("nomi update printed\n%s\nwant\n%s", out, want)
	}
}

// An entry's effects keys stay when nomi add takes its package again and
// when nomi update moves its version, and go with the entry when nomi
// remove takes it out.
func TestPackages_AddAndUpdateKeepAnEntrysEffectsKeys(t *testing.T) {
	c := newModuleCLI(t)
	greet := map[string]string{
		"nomi.toml":  "[package]\nname = \"greet\"\n",
		"greet.nomi": "import std::fs\n\npub fn hello() -> String\n    fs.read_text(\"greeting.txt\") |> Result.with_default(\"hello\")\nend\n",
	}
	c.host.Tag("example.com/greet", "v1.0.0", greet)
	work := t.TempDir()
	c.must(work, "new", "app")
	app := filepath.Join(work, "app")
	manifest := filepath.Join(app, "nomi.toml")
	mustWrite(t, filepath.Join(app, "main.nomi"), "import\n    std::io\n    greet\nend\n\nfn main()\n    io.print(greet.hello())\nend\n")
	c.must(app, "add", "example.com/greet")
	keys := `effects = ["std::fs"], effects_in_tests = ["std::process"]`
	entry := func(version string) string {
		return `greet = { repo = "example.com/greet", version = "` + version + `", ` + keys + ` } # reads its greeting` + "\n"
	}
	text := mustRead(t, manifest)
	text = strings.Replace(text, `greet = { repo = "example.com/greet", version = "v1.0.0" }`+"\n", entry("v1.0.0"), 1)
	if !strings.Contains(text, entry("v1.0.0")) {
		t.Fatalf("nomi.toml after nomi add has no entry to give keys to:\n%s", text)
	}
	mustWrite(t, manifest, text)

	c.must(app, "add", "example.com/greet@v1.0.0")
	if got := mustRead(t, manifest); !strings.Contains(got, entry("v1.0.0")) {
		t.Errorf("nomi add of the same package dropped the entry's effects keys:\n%s", got)
	}
	c.host.Tag("example.com/greet", "v1.1.0", greet)
	c.must(app, "update")
	if got := mustRead(t, manifest); !strings.Contains(got, entry("v1.1.0")) {
		t.Errorf("nomi update dropped the entry's effects keys:\n%s", got)
	}
	mustWrite(t, filepath.Join(app, "main.nomi"), "import std::io\n\nfn main()\n    io.print(\"hi\")\nend\n")
	c.must(app, "remove", "greet")
	if got := mustRead(t, manifest); strings.Contains(got, "greet") || strings.Contains(got, "effects") {
		t.Errorf("nomi remove left the entry or its keys:\n%s", got)
	}
}
