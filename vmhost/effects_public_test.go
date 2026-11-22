package vmhost

import (
	"path/filepath"
	"strings"
	"testing"
)

// checkedRootsPass loads entry from a project of files and fails the test
// when what `nomi check` holds it to (checkedRoots) refuses it.
func checkedRootsPass(t *testing.T, entry string, files map[string]string) {
	t.Helper()
	dir := writeProject(t, files)
	p, err := Load(filepath.Join(dir, filepath.FromSlash(entry)))
	if err != nil {
		t.Fatalf("the program does not load: %v", err)
	}
	if err := p.effectError(p.checkedRoots()...); err != nil {
		t.Errorf("the lists refuse the program: %v", strings.ReplaceAll(err.Error(), dir+string(filepath.Separator), ""))
	}
}

// libraryFiles is a library with no main and no tests, whose
// [effects.self] allows std, with its files and more added.
func libraryFiles(std string, more map[string]string) map[string]string {
	files := map[string]string{
		"nomi.toml": "[module]\nname = \"libroots\"\n\n[effects.self]\nstd = " + std + "\n",
		"libroots.nomi": `import std::fs

pub fn load(path: String) -> Result<String, fs.Error>
    fs.read_text(path)
end
`,
	}
	for name, text := range more {
		files[name] = text
	}
	return files
}

// A library's public functions are roots of its own check: one that reads
// a file is refused under an [effects.self] that allows nothing, at the
// call, reached from the function, though the library has no main and no
// tests; and allowed once std names std::fs.
func TestEffectRoots_ALibrarysPublicFunctionsAreRoots(t *testing.T) {
	got, ds := effectDiagnostics(t, "libroots.nomi", libraryFiles("[]", nil))
	want := "libroots.nomi:4:5: this module's own code has the effect std::fs, and [effects.self] allows it no effects\n" +
		"libroots.nomi:4:5: help: reached from `load`\n" +
		"libroots.nomi:4:5: help: add \"std::fs\" to std under [effects.self] in nomi.toml, or remove the call"
	if got != want {
		t.Errorf("diagnostics:\n%s\nwant:\n%s", got, want)
	}
	if len(ds) != 1 || ds[0].Code != EffectNotAllowedCode {
		t.Errorf("diagnostics %v; want one %s", ds, EffectNotAllowedCode)
	}
	checkedRootsPass(t, "libroots.nomi", libraryFiles(`["std::fs"]`, nil))
}

// What no public function reaches is not counted: a private function that
// reads a file and that nothing calls, and a `pub fn` in an internal/
// file, which no other module can call, leave the library pure. A private
// function a public one calls counts, reached through it.
func TestEffectRoots_WhatNoPublicFunctionReachesIsNotCounted(t *testing.T) {
	pure := map[string]string{
		"libroots.nomi": `import
    std::fs
    internal::files
end

pub fn size(text: String) -> Int
    files.count(text)
end

fn unused() -> Result<String, fs.Error>
    fs.read_text("notes.txt")
end
`,
		"internal/files.nomi": `import std::fs

pub fn count(text: String) -> Int
    String.length(text)
end

pub fn read_all(path: String) -> Result<String, fs.Error>
    fs.read_text(path)
end
`,
	}
	checkedRootsPass(t, "libroots.nomi", libraryFiles("[]", pure))

	through := map[string]string{
		"libroots.nomi": `import std::fs

pub fn size(path: String) -> Int
    String.length(read(path))
end

fn read(path: String) -> String
    fs.read_text(path) |> Result.with_default("")
end
`,
	}
	got, _ := effectDiagnostics(t, "libroots.nomi", libraryFiles("[]", through))
	want := "libroots.nomi:8:5: this module's own code has the effect std::fs, and [effects.self] allows it no effects\n" +
		"libroots.nomi:8:5: help: reached from `size`; `size` calls `read`\n" +
		"libroots.nomi:8:5: help: add \"std::fs\" to std under [effects.self] in nomi.toml, or remove the call"
	if got != want {
		t.Errorf("diagnostics:\n%s\nwant:\n%s", got, want)
	}
}

// The bodies a value of a type may run are roots once a public function
// makes one, in an internal/ file and of a private type too, since the
// value can leave through the function: Loud's Display reads a file. So
// are the impls of a type another module can build itself, a `pub` type
// that is not `opaque`, though no function makes one: Note's Display. A
// type nothing public makes and no other module can build adds nothing:
// Quiet's.
func TestEffectRoots_TheBodiesOfAMadeOrBuildableType(t *testing.T) {
	t.Setenv(AllCallsEnv, "1")
	impls := map[string]string{
		"libroots.nomi": `import
    std::fs
    internal::loud
end

pub struct Note
    path: String
end

impl Display for Note
    fn to_string(n: Note) -> String
        fs.read_text(n.path) |> Result.with_default("")
    end
end

struct Quiet
    path: String
end

impl Display for Quiet
    fn to_string(q: Quiet) -> String
        fs.read_text(q.path) |> Result.with_default("")
    end
end

pub fn make() -> Display
    loud.make()
end
`,
		"internal/loud.nomi": `import std::fs

pub struct Loud
    path: String
end

pub fn make() -> Loud
    Loud{path: "loud.txt"}
end

impl Display for Loud
    fn to_string(l: Loud) -> String
        fs.read_text(l.path) |> Result.with_default("")
    end
end
`,
	}
	got, _ := effectDiagnostics(t, "libroots.nomi", libraryFiles("[]", impls))
	want := "internal/loud.nomi:13:9: this module's own code has the effect std::fs, and [effects.self] allows it no effects\n" +
		"internal/loud.nomi:13:9: help: reached from `Display.to_string for Loud`\n" +
		"internal/loud.nomi:13:9: help: add \"std::fs\" to std under [effects.self] in nomi.toml, or remove the call\n" +
		"libroots.nomi:12:9: this module's own code has the effect std::fs, and [effects.self] allows it no effects\n" +
		"libroots.nomi:12:9: help: reached from `Display.to_string for Note`\n" +
		"libroots.nomi:12:9: help: add \"std::fs\" to std under [effects.self] in nomi.toml, or remove the call"
	if got != want {
		t.Errorf("diagnostics:\n%s\nwant:\n%s", got, want)
	}
}

// An impl for a `pub` interface type runs for every value of the
// interface another module makes, so its functions are roots, built with
// the receiver held as the interface (irbuild's abstract.go): one that
// reads a file is refused under an [effects.self] that allows nothing.
func TestEffectRoots_AnImplForAPublicInterfaceType(t *testing.T) {
	impls := map[string]string{
		"libroots.nomi": `import std::fs

pub interface Store
    fn path(store: self) -> String
end

impl Display for Store
    fn to_string(store: Store) -> String
        fs.read_text(Store.path(store)) |> Result.with_default("")
    end
end
`,
	}
	got, _ := effectDiagnostics(t, "libroots.nomi", libraryFiles("[]", impls))
	want := "libroots.nomi:9:9: this module's own code has the effect std::fs, and [effects.self] allows it no effects\n" +
		"libroots.nomi:9:9: help: reached from `Display.to_string for Store`\n" +
		"libroots.nomi:9:9: help: add \"std::fs\" to std under [effects.self] in nomi.toml, or remove the call"
	if got != want {
		t.Errorf("diagnostics:\n%s\nwant:\n%s", got, want)
	}
}

// An app's public function nothing calls is a root too: one whose effect
// the app's own [effects.self] lists passes, and one whose effect it does
// not list is refused at the call, reached from the function.
func TestEffectRoots_AnAppsUnreachedPublicFunction(t *testing.T) {
	app := func(std string) map[string]string {
		return map[string]string{
			"nomi.toml": "[module]\nname = \"app\"\nentry_points = [\"main\"]\n\n[effects.self]\nstd = " + std + "\n",
			"main.nomi": `import
    std::io
    notes
end

fn main()
    io.print(notes.title())
end
`,
			"notes.nomi": `import std::fs

pub fn title() -> String
    "notes"
end

pub fn load() -> String
    fs.read_text("notes.txt") |> Result.with_default("")
end
`,
		}
	}
	checkedRootsPass(t, "main.nomi", app(`["std::io", "std::fs"]`))
	got, _ := effectDiagnostics(t, "main.nomi", app(`["std::io"]`))
	want := "notes.nomi:8:5: this module's own code has the effect std::fs, and [effects.self] allows it only std::io\n" +
		"notes.nomi:8:5: help: reached from `load`\n" +
		"notes.nomi:8:5: help: add \"std::fs\" to std under [effects.self] in nomi.toml, or remove the call"
	if got != want {
		t.Errorf("diagnostics:\n%s\nwant:\n%s", got, want)
	}
}

// A dependency's public functions are not roots of the project that needs
// it: [effects.deps] holds the dependency where the program reaches it.
// An app whose entry for pricing allows nothing passes while it calls only
// pricing's pure function, though pricing's other public function reads a
// file, and is refused at that call once main calls it.
func TestEffectRoots_ADependencysPublicFunctionsAreNotRoots(t *testing.T) {
	files := func(call string) map[string]string {
		return map[string]string{
			"app/nomi.toml": "[module]\nname = \"app\"\nentry_points = [\"main\"]\n\n[dependencies]\npricing = { dir = \"../pricing\" }\n\n" +
				"[effects.self]\nstd = [\"std::io\"]\n\n[effects.deps.modules]\n\"pricing\" = []\n",
			"app/main.nomi": `import
    std::io
    pricing::rules
end

fn main()
    io.print(rules.` + call + `)
end
`,
			"pricing/nomi.toml": "[module]\nname = \"pricing\"\n",
			"pricing/rules.nomi": `import std::fs

pub fn rate() -> String
    "0.2"
end

pub fn load(path: String) -> String
    fs.read_text(path) |> Result.with_default("")
end
`,
		}
	}
	checkedRootsPass(t, "app/main.nomi", files("rate()"))
	got, _ := effectDiagnostics(t, "app/main.nomi", files(`load("prices.txt")`))
	want := "pricing/rules.nomi:8:5: pricing has the effect std::fs, and its entry under [effects.deps.modules] allows it no effects\n" +
		"pricing/rules.nomi:8:5: help: reached from `main`; `main` calls `load`\n" +
		"pricing/rules.nomi:8:5: help: add \"std::fs\" to \"pricing\" under [effects.deps.modules] in nomi.toml, or remove the call\n" +
		"app/main.nomi:7:14: note: your code has the effect std::fs in pricing here: `main` calls `load`"
	if got != want {
		t.Errorf("diagnostics:\n%s\nwant:\n%s", got, want)
	}
}
