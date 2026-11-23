package modfetch

import (
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/mod/module"
)

func TestAddLocal_WritesADirEntry(t *testing.T) {
	p := newTestProxy(t)
	publishGreet(t, p, "v0.1.0")
	app, _ := setup(t, p)
	lib := filepath.Join(app, "csvdata")
	writeFile(t, filepath.Join(lib, "nomi.toml"), "[module]\nname = \"csvdata\"\n\n[dependencies]\n"+
		"strutil = { module = \"example.com/strutil\", version = \"v1.0.0\" }\n")

	added, err := AddLocal(app, "", lib, Options{})
	if err != nil {
		t.Fatalf("AddLocal: %v", err)
	}
	if added.Name != "csvdata" || added.Local != "./csvdata" {
		t.Fatalf("AddLocal answered %+v", added)
	}
	if manifest := readFile(t, filepath.Join(app, "nomi.toml")); !strings.HasSuffix(manifest, "[dependencies]\ncsvdata = { dir = \"./csvdata\" }\n") {
		t.Errorf("nomi.toml after AddLocal:\n%s", manifest)
	}
	if lock := readFile(t, filepath.Join(app, "nomi.lock")); !strings.Contains(lock, "example.com/strutil v1.0.0 ") || strings.Contains(lock, "csvdata") {
		t.Errorf("nomi.lock locks what the local module needs, and not the local module:\n%s", lock)
	}

	// A sibling directory is written relative to the project root, under
	// the name given.
	sib := filepath.Join(filepath.Dir(app), "shared")
	writeFile(t, filepath.Join(sib, "nomi.toml"), "[module]\nname = \"shared\"\npath = \"example.com/shared\"\n")
	if _, err := AddLocal(app, "common", sib, Options{}); err != nil {
		t.Fatalf("AddLocal of a sibling: %v", err)
	}
	if manifest := readFile(t, filepath.Join(app, "nomi.toml")); !strings.Contains(manifest, "common = { dir = \"../shared\" }\n") {
		t.Errorf("nomi.toml lacks the sibling's entry:\n%s", manifest)
	}
}

func TestAddLocal_RefusesADirectoryThatIsNotANomiModule(t *testing.T) {
	p := newTestProxy(t)
	app, _ := setup(t, p)
	before := readFile(t, filepath.Join(app, "nomi.toml"))

	noManifest := filepath.Join(app, "gocode")
	writeFile(t, filepath.Join(noManifest, "go.mod"), "module example.com/gocode\n\ngo 1.27.0\n")
	_, err := AddLocal(app, "", noManifest, Options{})
	if err == nil || !strings.Contains(err.Error(), "has no nomi.toml, so it is not a Nomi module") {
		t.Errorf("AddLocal of a directory with no nomi.toml: %v", err)
	}

	_, err = AddLocal(app, "", filepath.Join(app, "missing"), Options{})
	if err == nil || !strings.Contains(err.Error(), "is not a directory") {
		t.Errorf("AddLocal of a missing directory: %v", err)
	}
	if after := readFile(t, filepath.Join(app, "nomi.toml")); after != before {
		t.Errorf("a refused AddLocal changed nomi.toml:\n%s", after)
	}
}

// A path inside a module names the module that holds it, and a module with
// no nomi.toml is refused.
func TestAdd_APackagePathAddsItsModule(t *testing.T) {
	p := newTestProxy(t)
	publishGreet(t, p, "v0.1.0")
	p.publish(t, "example.com/gokit", "v1.0.0", map[string]string{
		"go.mod": "module example.com/gokit\n\ngo 1.22\n",
		"kit.go": "package gokit\n",
	})
	app, _ := setup(t, p)
	added, err := Add(app, "", "example.com/strutil/inner", Options{})
	if err != nil {
		t.Fatalf("Add of a package path: %v", err)
	}
	if added.Module.Path != "example.com/strutil" {
		t.Fatalf("Add of a package path added %s", added.Module.Path)
	}
	_, err = Add(app, "", "example.com/gokit", Options{})
	if err == nil || err.Error() != "example.com/gokit v1.0.0 has no nomi.toml, so it is not a Nomi module" {
		t.Errorf("Add of a module with no nomi.toml: %v", err)
	}
}

func TestRemove_DropsTheEntryAndWhatOnlyItNeeded(t *testing.T) {
	p := newTestProxy(t)
	publishGreet(t, p, "v0.1.0")
	p.publish(t, "example.com/other", "v1.0.0", map[string]string{
		"nomi.toml":  "[module]\nname = \"other\"\n",
		"other.nomi": "pub fn one() -> Int\n    1\nend\n",
	})
	app, _ := setup(t, p)
	for _, m := range []string{"example.com/greet@v0.1.0", "example.com/other@v1.0.0"} {
		if _, err := Add(app, "", m, Options{}); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := Remove(app, "greet", Options{})
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if removed.Entry.Version != "v0.1.0" || len(removed.Unneeded) != 2 {
		t.Fatalf("Remove answered %+v", removed)
	}
	manifest := readFile(t, filepath.Join(app, "nomi.toml"))
	if strings.Contains(manifest, "greet") || !strings.Contains(manifest, "other = { module = \"example.com/other\", version = \"v1.0.0\" }") {
		t.Errorf("nomi.toml after Remove:\n%s", manifest)
	}
	lock := readFile(t, filepath.Join(app, "nomi.lock"))
	if strings.Contains(lock, "greet") || strings.Contains(lock, "strutil") || !strings.Contains(lock, "example.com/other v1.0.0 ") {
		t.Errorf("nomi.lock after Remove:\n%s", lock)
	}

	if _, err := Remove(app, "greet", Options{}); err == nil || err.Error() != "nomi.toml has no dependency greet" {
		t.Errorf("Remove of a dependency nomi.toml does not name: %v", err)
	}
}

func TestRemove_KeepsAModuleAnotherModuleNeeds(t *testing.T) {
	p := newTestProxy(t)
	publishGreet(t, p, "v0.1.0")
	p.publish(t, "example.com/loud", "v1.0.0", map[string]string{
		"nomi.toml": "[module]\nname = \"loud\"\n\n[dependencies]\nstrutil = { module = \"example.com/strutil\", version = \"v1.0.0\" }\n",
		"loud.nomi": "pub fn one() -> Int\n    1\nend\n",
	})
	app, _ := setup(t, p)
	for _, m := range []string{"example.com/greet@v0.1.0", "example.com/loud@v1.0.0"} {
		if _, err := Add(app, "", m, Options{}); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := Remove(app, "greet", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(removed.Unneeded) != 1 || removed.Unneeded[0].Path != "example.com/greet" {
		t.Fatalf("Remove dropped %v; example.com/loud still needs strutil", removed.Unneeded)
	}
	if lock := readFile(t, filepath.Join(app, "nomi.lock")); !strings.Contains(lock, "example.com/strutil v1.0.0 ") {
		t.Errorf("nomi.lock lost a module another module needs:\n%s", lock)
	}
}

func TestRemove_TakesOutALocalModule(t *testing.T) {
	p := newTestProxy(t)
	app, _ := setup(t, p)
	lib := filepath.Join(app, "lib")
	writeFile(t, filepath.Join(lib, "nomi.toml"), "[module]\nname = \"lib\"\n")
	if _, err := AddLocal(app, "", lib, Options{}); err != nil {
		t.Fatal(err)
	}
	removed, err := Remove(app, "lib", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if removed.Entry.Dir != "./lib" {
		t.Errorf("Remove answered %+v", removed)
	}
	if manifest := readFile(t, filepath.Join(app, "nomi.toml")); strings.Contains(manifest, "lib") {
		t.Errorf("nomi.toml after removing a local module:\n%s", manifest)
	}
}

func TestUpdate_StaysWithinTheModulePath(t *testing.T) {
	p := newTestProxy(t)
	publishGreet(t, p, "v0.1.0", "v0.2.0", "v0.3.0-rc.1")
	// A new major version is another module path, which update never takes.
	p.publish(t, "example.com/greet/v2", "v2.0.0", map[string]string{
		"go.mod":     "module example.com/greet/v2\n",
		"nomi.toml":  "[module]\nname = \"greet\"\n",
		"greet.nomi": "pub fn hello() -> Int\n    2\nend\n",
	})
	p.publish(t, "example.com/strutil", "v1.1.0", map[string]string{
		"nomi.toml":    "[module]\nname = \"strutil\"\n",
		"strutil.nomi": "pub fn twice(s: String) -> String\n    s\nend\n",
	})
	app, _ := setup(t, p)
	if _, err := Add(app, "", "example.com/greet@v0.1.0", Options{}); err != nil {
		t.Fatal(err)
	}

	changes, err := Update(app, "greet", Options{})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(changes) != 1 || changes[0] != (Change{Path: "example.com/greet", From: "v0.1.0", To: "v0.2.0"}) {
		t.Fatalf("Update of one dependency answered %+v", changes)
	}
	lock := readFile(t, filepath.Join(app, "nomi.lock"))
	if strings.Contains(lock, "example.com/greet v0.1.0 ") || !strings.Contains(lock, "example.com/greet v0.2.0 ") {
		t.Errorf("nomi.lock after Update:\n%s", lock)
	}

	// strutil, which greet needs at v1.0.0, is not one of the project's
	// entries, so a newer release of it does not move it.
	if changes, err := Update(app, "", Options{}); err != nil || len(changes) != 0 {
		t.Errorf("a second Update answered %+v, %v", changes, err)
	}
	if manifest := readFile(t, filepath.Join(app, "nomi.toml")); !strings.Contains(manifest, "greet = { module = \"example.com/greet\", version = \"v0.2.0\" }") {
		t.Errorf("nomi.toml after Update:\n%s", manifest)
	}
	if _, err := Update(app, "none", Options{}); err == nil || err.Error() != "nomi.toml has no dependency none" {
		t.Errorf("Update of a dependency nomi.toml does not name: %v", err)
	}
}

// The modules under nomi's lib/ that a project names come from one commit:
// adding sqlkit at a commit moves the project's sqlite entry to that commit
// too, and updating either moves both to the latest commit, which the
// proxy's @latest names since lib/ has no tags. A lib/ module the project
// does not name is at the commit the modules that need it ask for.
func TestAddAndUpdate_KeepTheLibModulesOnOneCommit(t *testing.T) {
	const (
		sqlite = "github.com/nomi-language/nomi/lib/sql/sqlite"
		sqlkit = "github.com/nomi-language/nomi/lib/sql/sqlkit"
		older  = "v0.0.0-20261001000000-aaaaaaaaaaaa"
		newer  = "v0.0.0-20261020000000-bbbbbbbbbbbb"
		newest = "v0.0.0-20261026000000-cccccccccccc"
	)
	p := newTestProxy(t)
	for _, v := range []string{older, newer, newest} {
		p.publish(t, sqlite, v, map[string]string{
			"nomi.toml":   "[module]\nname = \"sqlite\"\n",
			"sqlite.nomi": "pub fn version() -> String\n    \"" + v + "\"\nend\n",
		})
		p.publish(t, sqlkit, v, map[string]string{
			"nomi.toml":   "[module]\nname = \"sqlkit\"\n\n[dependencies]\nsqlite = { module = \"" + sqlite + "\", version = \"" + older + "\" }\n",
			"sqlkit.nomi": "pub fn version() -> String\n    \"" + v + "\"\nend\n",
		})
	}
	app, _ := setup(t, p)

	if _, err := Add(app, "", sqlkit+"@"+newer, Options{}); err != nil {
		t.Fatalf("Add sqlkit: %v", err)
	}
	if lock := readFile(t, filepath.Join(app, "nomi.lock")); !strings.Contains(lock, sqlite+" "+older+" ") {
		t.Errorf("sqlite, which the project does not name, is not at the commit sqlkit asks for:\n%s", lock)
	}
	if _, err := Add(app, "", sqlite+"@"+older, Options{}); err != nil {
		t.Fatalf("Add sqlite: %v", err)
	}
	manifest := readFile(t, filepath.Join(app, "nomi.toml"))
	for _, want := range []string{"sqlkit = { module = \"" + sqlkit + "\", version = \"" + older + "\" }", "sqlite = { module = \"" + sqlite + "\", version = \"" + older + "\" }"} {
		if !strings.Contains(manifest, want) {
			t.Errorf("after adding sqlite, nomi.toml lacks %q:\n%s", want, manifest)
		}
	}

	// @latest names the newest commit, as the proxy answers it for a module
	// with no tags.
	for _, path := range []string{sqlite, sqlkit} {
		enc, _ := module.EscapePath(path)
		writeFile(t, filepath.Join(p.dir, filepath.FromSlash(enc), "@latest"), `{"Version":"`+newest+`"}`)
	}
	if _, err := Update(app, "sqlkit", Options{}); err != nil {
		t.Fatalf("Update sqlkit: %v", err)
	}
	manifest = readFile(t, filepath.Join(app, "nomi.toml"))
	for _, want := range []string{"sqlkit = { module = \"" + sqlkit + "\", version = \"" + newest + "\" }", "sqlite = { module = \"" + sqlite + "\", version = \"" + newest + "\" }"} {
		if !strings.Contains(manifest, want) {
			t.Errorf("after updating sqlkit, nomi.toml lacks %q:\n%s", want, manifest)
		}
	}
}

// publishLib publishes the lib/ module at path, named name, at each of
// versions, and makes the last the proxy's @latest, as it is for lib/'s
// newest commit.
func publishLib(t *testing.T, p *testProxy, path, name string, versions ...string) {
	t.Helper()
	for _, v := range versions {
		p.publish(t, path, v, map[string]string{
			"nomi.toml":    "[module]\nname = \"" + name + "\"\npath = \"" + path + "\"\n",
			name + ".nomi": "pub fn version() -> String\n    \"" + v + "\"\nend\n",
		})
	}
	enc, _ := module.EscapePath(path)
	writeFile(t, filepath.Join(p.dir, filepath.FromSlash(enc), "@latest"), `{"Version":"`+versions[len(versions)-1]+`"}`)
}

const (
	libMarkdown = "github.com/nomi-language/nomi/lib/markdown"
	libSqlite   = "github.com/nomi-language/nomi/lib/sql/sqlite"
	libRelease  = "v0.5.0"
	libCommit   = "v0.5.1-0.20261120000000-cccccccccccc"
)

// A release `nomi` adds a lib/ module at its own version, not at the
// latest commit; a version on the command line wins; and a release whose
// tag the proxy lacks says so, naming the tag, and adds nothing.
func TestAdd_AReleaseTakesALibModuleAtItsOwnVersion(t *testing.T) {
	p := newTestProxy(t)
	publishLib(t, p, libMarkdown, "markdown", libRelease, libCommit)
	publishLib(t, p, libSqlite, "sqlite", libCommit)
	app, _ := setup(t, p)
	release := Options{Release: libRelease}

	added, err := Add(app, "", libMarkdown, release)
	if err != nil {
		t.Fatalf("Add markdown: %v", err)
	}
	if added.Module.Version != libRelease {
		t.Errorf("a release added markdown at %s, want its own %s", added.Module.Version, libRelease)
	}
	if lock := readFile(t, filepath.Join(app, "nomi.lock")); !strings.Contains(lock, libMarkdown+" "+libRelease+" ") {
		t.Errorf("nomi.lock after adding markdown:\n%s", lock)
	}

	if added, err := Add(app, "", libMarkdown+"@"+libCommit, release); err != nil || added.Module.Version != libCommit {
		t.Fatalf("Add markdown@%s answered %+v, %v; want the version named", libCommit, added.Module, err)
	}

	// The proxy has no sqlite at the release, as after a release cut
	// without its lib/ tags. Every proxy before `direct` lacks it, so it is
	// the missing tag, not a download through the go command.
	t.Setenv("GOPROXY", p.url()+",direct")
	before := readFile(t, filepath.Join(app, "nomi.toml"))
	_, err = Add(app, "", libSqlite, release)
	want := "this nomi is release v0.5.0, which takes github.com/nomi-language/nomi/lib/sql/sqlite at v0.5.0, " +
		"and the module proxy does not have that version: the release was published without the tag lib/sql/sqlite/v0.5.0; " +
		"`nomi add github.com/nomi-language/nomi/lib/sql/sqlite@<version>` takes another version"
	if err == nil || err.Error() != want {
		t.Fatalf("Add of a lib/ module the release did not tag: %v\nwant %s", err, want)
	}
	if after := readFile(t, filepath.Join(app, "nomi.toml")); after != before {
		t.Errorf("a failed Add changed nomi.toml:\n%s", after)
	}
}

// A release `nomi`'s Update moves lib/ entries on another version to its
// own, down from a later commit too, answers each move, and then leaves
// them there. A release whose tag the proxy lacks says so.
func TestUpdate_AReleaseKeepsTheLibModulesAtItsVersion(t *testing.T) {
	p := newTestProxy(t)
	publishLib(t, p, libMarkdown, "markdown", libRelease, libCommit)
	publishLib(t, p, libSqlite, "sqlite", libRelease, libCommit)
	app, _ := setup(t, p)
	for _, path := range []string{libMarkdown, libSqlite} {
		if _, err := Add(app, "", path, Options{}); err != nil {
			t.Fatalf("Add %s from source: %v", path, err)
		}
	}
	if lock := readFile(t, filepath.Join(app, "nomi.lock")); !strings.Contains(lock, libMarkdown+" "+libCommit+" ") {
		t.Fatalf("a nomi built from source did not add markdown at the latest commit:\n%s", lock)
	}

	release := Options{Release: libRelease}
	changes, err := Update(app, "", release)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	want := []Change{
		{Path: libMarkdown, From: libCommit, To: libRelease},
		{Path: libSqlite, From: libCommit, To: libRelease},
	}
	if len(changes) != len(want) || changes[0] != want[0] || changes[1] != want[1] {
		t.Fatalf("Update answered %+v, want %+v", changes, want)
	}
	manifest := readFile(t, filepath.Join(app, "nomi.toml"))
	for _, m := range []string{libMarkdown, libSqlite} {
		if !strings.Contains(manifest, `module = "`+m+`", version = "`+libRelease+`" }`) {
			t.Errorf("nomi.toml after Update does not name %s at %s:\n%s", m, libRelease, manifest)
		}
	}
	if changes, err := Update(app, "", release); err != nil || len(changes) != 0 {
		t.Errorf("a second Update answered %+v, %v; want no change", changes, err)
	}
	if off, err := LibOffRelease(app, libRelease); err != nil || len(off) != 0 {
		t.Errorf("LibOffRelease after Update answered %v, %v", off, err)
	}
	if off, err := LibOffRelease(app, "v0.6.0"); err != nil || len(off) != 2 {
		t.Errorf("LibOffRelease for another release answered %v, %v; want both modules", off, err)
	}

	_, err = Update(app, "markdown", Options{Release: "v0.6.0"})
	if err == nil || !strings.Contains(err.Error(), "the release was published without the tag lib/markdown/v0.6.0") {
		t.Errorf("Update to a release the proxy lacks: %v", err)
	}
}
