package modfetch

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/modcache"
	"github.com/nomi-language/nomi/internal/modfetch/modfetchtest"
)

func TestAddLocal_WritesADirEntry(t *testing.T) {
	s := serve(t)
	publishGreet(s, "v0.1.0")
	app, _ := setup(t)
	lib := filepath.Join(app, "csvdata")
	writeFile(t, filepath.Join(lib, "nomi.toml"), "[module]\nname = \"csvdata\"\n\n[dependencies]\n"+
		"strutil = { repo = \"example.com/strutil\", version = \"v1.0.0\" }\n")

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
	if lock := readFile(t, filepath.Join(app, "nomi.lock")); !strings.Contains(lock, "example.com/strutil . v1.0.0 ") || strings.Contains(lock, "csvdata") {
		t.Errorf("nomi.lock locks what the local module needs, and not the local module:\n%s", lock)
	}

	// A sibling directory is written relative to the project root, under
	// the name given.
	sib := filepath.Join(filepath.Dir(app), "shared")
	writeFile(t, filepath.Join(sib, "nomi.toml"), "[module]\nname = \"shared\"\n")
	if _, err := AddLocal(app, "common", sib, Options{}); err != nil {
		t.Fatalf("AddLocal of a sibling: %v", err)
	}
	if manifest := readFile(t, filepath.Join(app, "nomi.toml")); !strings.Contains(manifest, "common = { dir = \"../shared\" }\n") {
		t.Errorf("nomi.toml lacks the sibling's entry:\n%s", manifest)
	}

	// An entry of that name that names a repository keeps it and gains the
	// dir, which then stands for that module.
	if _, err := Add(app, "", "example.com/greet@v0.1.0", Options{}); err != nil {
		t.Fatal(err)
	}
	fork := filepath.Join(filepath.Dir(app), "greet")
	writeFile(t, filepath.Join(fork, "nomi.toml"), "[module]\nname = \"greet\"\n")
	if _, err := AddLocal(app, "", fork, Options{}); err != nil {
		t.Fatalf("AddLocal of a fork: %v", err)
	}
	if manifest := readFile(t, filepath.Join(app, "nomi.toml")); !strings.Contains(manifest, "greet = { repo = \"example.com/greet\", version = \"v0.1.0\", dir = \"../greet\" }\n") {
		t.Errorf("nomi.toml after adding a fork of greet:\n%s", manifest)
	}
}

func TestAddLocal_RefusesADirectoryThatIsNotANomiModule(t *testing.T) {
	app, _ := setup(t)
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

func TestRemove_DropsTheEntryAndWhatOnlyItNeeded(t *testing.T) {
	s := serve(t)
	publishGreet(s, "v0.1.0")
	s.Tag("example.com/other", "v1.0.0", map[string]string{
		"nomi.toml":  "[module]\nname = \"other\"\n",
		"other.nomi": "pub fn one() -> Int\n    1\nend\n",
	})
	app, _ := setup(t)
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
	if strings.Contains(manifest, "greet") || !strings.Contains(manifest, "other = { repo = \"example.com/other\", version = \"v1.0.0\" }") {
		t.Errorf("nomi.toml after Remove:\n%s", manifest)
	}
	lock := readFile(t, filepath.Join(app, "nomi.lock"))
	if strings.Contains(lock, "greet") || strings.Contains(lock, "strutil") || !strings.Contains(lock, "example.com/other . v1.0.0 ") {
		t.Errorf("nomi.lock after Remove:\n%s", lock)
	}

	if _, err := Remove(app, "greet", Options{}); err == nil || err.Error() != "nomi.toml has no dependency greet" {
		t.Errorf("Remove of a dependency nomi.toml does not name: %v", err)
	}
}

func TestRemove_KeepsAModuleAnotherModuleNeeds(t *testing.T) {
	s := serve(t)
	publishGreet(s, "v0.1.0")
	s.Tag("example.com/loud", "v1.0.0", map[string]string{
		"nomi.toml": "[module]\nname = \"loud\"\n\n[dependencies]\nstrutil = { repo = \"example.com/strutil\", version = \"v1.0.0\" }\n",
		"loud.nomi": "pub fn one() -> Int\n    1\nend\n",
	})
	app, _ := setup(t)
	for _, m := range []string{"example.com/greet@v0.1.0", "example.com/loud@v1.0.0"} {
		if _, err := Add(app, "", m, Options{}); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := Remove(app, "greet", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(removed.Unneeded) != 1 || removed.Unneeded[0].ID != (modcache.ID{Repo: "example.com/greet"}) {
		t.Fatalf("Remove dropped %v; example.com/loud still needs strutil", removed.Unneeded)
	}
	if lock := readFile(t, filepath.Join(app, "nomi.lock")); !strings.Contains(lock, "example.com/strutil . v1.0.0 ") {
		t.Errorf("nomi.lock lost a module another module needs:\n%s", lock)
	}
}

func TestRemove_TakesOutALocalModule(t *testing.T) {
	app, _ := setup(t)
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

// Update takes the highest release in the entry's line: not a
// prerelease, below v1 not the next minor version, and from v1 on not the
// next major version, which `nomi add <repo>@<version>` crosses to.
func TestUpdate_StaysWithinTheLine(t *testing.T) {
	s := serve(t)
	publishGreet(s, "v0.1.0", "v0.1.1", "v0.2.0", "v0.3.0-rc.1", "v1.0.0", "v1.1.0", "v1.2.0-rc.1", "v2.0.0")
	s.Tag("example.com/strutil", "v1.1.0", map[string]string{
		"nomi.toml":    "[module]\nname = \"strutil\"\n",
		"strutil.nomi": "pub fn twice(s: String) -> String\n    s\nend\n",
	})
	app, _ := setup(t)
	if _, err := Add(app, "", "example.com/greet@v0.1.0", Options{}); err != nil {
		t.Fatal(err)
	}

	updated, err := Update(app, "greet", Options{})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if changes := updated.Changes; len(changes) != 1 || changes[0] != (Change{ID: modcache.ID{Repo: "example.com/greet"}, From: "v0.1.0", To: "v0.1.1"}) {
		t.Fatalf("Update of one dependency answered %+v", updated)
	}
	lock := readFile(t, filepath.Join(app, "nomi.lock"))
	if strings.Contains(lock, "example.com/greet . v0.1.0 ") || !strings.Contains(lock, "example.com/greet . v0.1.1 ") {
		t.Errorf("nomi.lock after Update:\n%s", lock)
	}

	// strutil, which greet needs at v1.0.0, is not one of the project's
	// entries, so an update of greet alone does not move it.
	if updated, err := Update(app, "greet", Options{}); err != nil || len(updated.Changes) != 0 {
		t.Errorf("a second Update of greet answered %+v, %v", updated, err)
	}
	if manifest := readFile(t, filepath.Join(app, "nomi.toml")); !strings.Contains(manifest, "greet = { repo = \"example.com/greet\", version = \"v0.1.1\" }") {
		t.Errorf("nomi.toml after Update:\n%s", manifest)
	}
	if _, err := Update(app, "none", Options{}); err == nil || err.Error() != "nomi.toml has no dependency none" {
		t.Errorf("Update of a dependency nomi.toml does not name: %v", err)
	}
	for _, step := range []struct{ add, to string }{{"v0.2.0", "v0.2.0"}, {"v1.0.0", "v1.1.0"}} {
		if added, err := Add(app, "", "example.com/greet@"+step.add, Options{}); err != nil || added.Module.Version != step.add {
			t.Errorf("Add across a line to %s answered %+v, %v", step.add, added, err)
		}
		if _, err := Update(app, "greet", Options{}); err != nil {
			t.Fatal(err)
		}
		if manifest := readFile(t, filepath.Join(app, "nomi.toml")); !strings.Contains(manifest, "version = \""+step.to+"\"") {
			t.Errorf("Update from %s did not move greet to %s:\n%s", step.add, step.to, manifest)
		}
	}
}

// One tag versions a whole repository, so updating one of its modules
// moves every entry of the repository with it.
func TestUpdate_MovesEveryEntryOfARepository(t *testing.T) {
	s := serve(t)
	s.Tag("example.com/mono", "v1.0.0", monoFiles("v1.0.0"))
	app, _ := setup(t)
	for _, arg := range []string{"example.com/mono/lib/a", "example.com/mono/lib/b"} {
		if _, err := Add(app, "", arg, Options{}); err != nil {
			t.Fatal(err)
		}
	}
	s.Tag("example.com/mono", "v1.1.0", monoFiles("v1.1.0"))
	if _, err := Update(app, "a", Options{}); err != nil {
		t.Fatal(err)
	}
	manifest := readFile(t, filepath.Join(app, "nomi.toml"))
	for _, want := range []string{
		"a = { repo = \"example.com/mono\", subdir = \"lib/a\", version = \"v1.1.0\" }",
		"b = { repo = \"example.com/mono\", subdir = \"lib/b\", version = \"v1.1.0\" }",
	} {
		if !strings.Contains(manifest, want) {
			t.Errorf("nomi.toml after updating a lacks %q:\n%s", want, manifest)
		}
	}
}

// One tag versions a repository, so another module of a repository the
// project takes at a version is added at that version, not at the latest
// release, which is in another major version here and would conflict.
func TestAdd_AnotherModuleOfARepositoryTakesTheProjectsVersion(t *testing.T) {
	s := serve(t)
	for _, v := range []string{"v1.0.0", "v1.1.0", "v2.0.0"} {
		s.Tag("example.com/mono", v, monoFiles(v))
	}
	app, _ := setup(t)
	if added, err := Add(app, "", "example.com/mono/lib/a@v1.0.0", Options{}); err != nil || added.Held {
		t.Fatalf("Add of a version named answered %+v, %v", added, err)
	}
	added, err := Add(app, "", "example.com/mono/lib/b", Options{})
	if err != nil {
		t.Fatalf("Add of another module of the repository: %v", err)
	}
	if added.Module.Version != "v1.0.0" || !added.Held || len(added.Moved) != 0 {
		t.Errorf("Add answered %+v, want b held at the project's v1.0.0", added)
	}
	manifest := readFile(t, filepath.Join(app, "nomi.toml"))
	for _, want := range []string{
		"a = { repo = \"example.com/mono\", subdir = \"lib/a\", version = \"v1.0.0\" }",
		"b = { repo = \"example.com/mono\", subdir = \"lib/b\", version = \"v1.0.0\" }",
	} {
		if !strings.Contains(manifest, want) {
			t.Errorf("nomi.toml lacks %q:\n%s", want, manifest)
		}
	}
	if lock := readFile(t, filepath.Join(app, "nomi.lock")); strings.Contains(lock, "v1.1.0") || strings.Contains(lock, "v2.0.0") {
		t.Errorf("nomi.lock holds a version the project does not take:\n%s", lock)
	}
}

// A version named moves every entry of the repository to it, as Update
// moves them together: up across a major version, and down too.
func TestAdd_AVersionNamedMovesEveryEntryOfTheRepository(t *testing.T) {
	s := serve(t)
	for _, v := range []string{"v1.0.0", "v2.0.0"} {
		s.Tag("example.com/mono", v, monoFiles(v))
	}
	app, _ := setup(t)
	for _, arg := range []string{"example.com/mono/lib/a@v1.0.0", "example.com/mono/lib/b"} {
		if _, err := Add(app, "", arg, Options{}); err != nil {
			t.Fatal(err)
		}
	}
	for _, step := range []struct{ arg, version, moved string }{
		{"example.com/mono/lib/a@v2.0.0", "v2.0.0", "b"},
		{"example.com/mono/lib/b@v1.0.0", "v1.0.0", "a"},
	} {
		added, err := Add(app, "", step.arg, Options{})
		if err != nil {
			t.Fatalf("Add %s: %v", step.arg, err)
		}
		if added.Module.Version != step.version || added.Held || len(added.Moved) != 1 || added.Moved[0] != step.moved {
			t.Errorf("Add %s answered %+v, want %s moved to %s with it", step.arg, added, step.moved, step.version)
		}
		manifest := readFile(t, filepath.Join(app, "nomi.toml"))
		for _, want := range []string{
			"a = { repo = \"example.com/mono\", subdir = \"lib/a\", version = \"" + step.version + "\" }",
			"b = { repo = \"example.com/mono\", subdir = \"lib/b\", version = \"" + step.version + "\" }",
		} {
			if !strings.Contains(manifest, want) {
				t.Errorf("nomi.toml after Add %s lacks %q:\n%s", step.arg, want, manifest)
			}
		}
		lock := readFile(t, filepath.Join(app, "nomi.lock"))
		if n := strings.Count(lock, " "+step.version+" "); n != 3 {
			t.Errorf("nomi.lock after Add %s holds %d modules at %s, want the root, a and b:\n%s", step.arg, n, step.version, lock)
		}
	}
}

// asRelease makes the test's process a release `nomi` at version v
// (modcache.SetRelease) until the test ends.
func asRelease(t *testing.T, v string) {
	t.Helper()
	modcache.SetRelease(v)
	t.Cleanup(func() { modcache.SetRelease("") })
}

// libFiles is the first-party libraries' repository at version v.
func libFiles(v string) map[string]string {
	return map[string]string{
		"lib/markdown/nomi.toml":     "[module]\nname = \"markdown\"\n",
		"lib/markdown/markdown.nomi": "pub fn version() -> String\n    \"" + v + "\"\nend\n",
		"lib/sql/sqlite/nomi.toml":   "[module]\nname = \"sqlite\"\n",
		"lib/sql/sqlite/sqlite.nomi": "pub fn version() -> String\n    \"" + v + "\"\nend\n",
		"lib/sql/sqlkit/nomi.toml":   "[module]\nname = \"sqlkit\"\n\n[dependencies]\nsqlite = { dir = \"../sqlite\" }\n",
		"lib/sql/sqlkit/sqlkit.nomi": "pub fn version() -> String\n    \"" + v + "\"\nend\n",
	}
}

// serveLib serves the first-party libraries' repository at each of
// versions, on a github.com that is the test's server.
func serveLib(t *testing.T, versions ...string) *modfetchtest.Server {
	t.Helper()
	s := modfetchtest.New(t)
	useHosts(t, map[string]host{"github.com": {base: s.URL, archive: knownHosts["github.com"].archive, depth: 2}})
	for _, v := range versions {
		s.Tag(nomiRepo, v, libFiles(v))
	}
	return s
}

const (
	libMarkdown = nomiRepo + "/lib/markdown"
	libSqlkit   = nomiRepo + "/lib/sql/sqlkit"
)

// A release `nomi` adds a first-party library at its own version, not at
// the latest release; a version on the command line wins; a project that
// takes the libraries at a version already adds another there; and a
// release whose tag the repository lacks says so and adds nothing.
func TestAdd_AReleaseTakesALibraryAtItsOwnVersion(t *testing.T) {
	serveLib(t, "v0.5.0", "v0.5.1")
	app, _ := setup(t)
	asRelease(t, "v0.5.0")

	added, err := Add(app, "", libMarkdown, Options{})
	if err != nil {
		t.Fatalf("Add markdown: %v", err)
	}
	if added.Module != (modcache.Module{ID: modcache.ID{Repo: nomiRepo, Subdir: "lib/markdown"}, Version: "v0.5.0"}) {
		t.Errorf("a release added %+v, want markdown at its own v0.5.0", added.Module)
	}
	if added, err := Add(app, "", libMarkdown+"@v0.5.1", Options{}); err != nil || added.Module.Version != "v0.5.1" {
		t.Fatalf("Add markdown@v0.5.1 answered %+v, %v; want the version named", added.Module, err)
	}

	// The project takes the repository at v0.5.1, so another library of it
	// is added there, whatever the release.
	asRelease(t, "v0.6.0")
	if added, err := Add(app, "", libSqlkit, Options{}); err != nil || added.Module.Version != "v0.5.1" || !added.Held {
		t.Fatalf("Add sqlkit to a project that takes the libraries at v0.5.1 answered %+v, %v", added, err)
	}

	app, _ = setup(t)
	before := readFile(t, filepath.Join(app, "nomi.toml"))
	_, err = Add(app, "", libSqlkit, Options{})
	want := "this nomi is release v0.6.0, which takes the first-party libraries at v0.6.0, and github.com/nomi-language/nomi has no tag v0.6.0: " +
		"the release was published without it; `nomi add github.com/nomi-language/nomi/lib/sql/sqlkit@<version>` takes another version"
	if err == nil || err.Error() != want {
		t.Fatalf("Add of a library at a release the repository has no tag for: %v\nwant %s", err, want)
	}
	if after := readFile(t, filepath.Join(app, "nomi.toml")); after != before {
		t.Errorf("a failed Add changed nomi.toml:\n%s", after)
	}
}

// A `nomi` built from source adds a library at the latest release, with
// its neighbors by directory at the same tag, and says so when the
// repository has no release.
func TestAdd_FromSourceTakesALibraryAtTheLatestRelease(t *testing.T) {
	serveLib(t, "v0.5.0", "v0.5.1")
	app, _ := setup(t)
	if _, err := Add(app, "", libSqlkit, Options{}); err != nil {
		t.Fatal(err)
	}
	lock := readFile(t, filepath.Join(app, "nomi.lock"))
	for _, want := range []string{nomiRepo + " lib/sql/sqlkit v0.5.1 ", nomiRepo + " lib/sql/sqlite v0.5.1 "} {
		if !strings.Contains(lock, want) {
			t.Errorf("nomi.lock lacks %q:\n%s", want, lock)
		}
	}

	// The repository's only tag names no version.
	s := serveLib(t)
	s.Tag(nomiRepo, "nightly", libFiles("nightly"))
	app, _ = setup(t)
	_, err := Add(app, "", libMarkdown, Options{})
	want := "github.com/nomi-language/nomi has no release yet, so its first-party libraries have no version to take; add one from a checkout with `nomi add <directory>`"
	if err == nil || err.Error() != want {
		t.Errorf("Add of a library from a repository with no release: %v\nwant %s", err, want)
	}
}

// A release `nomi`'s Update moves the libraries on another version to its
// own, down from a later tag too, answers each move, and then leaves them
// there. A release whose tag the repository lacks says so.
func TestUpdate_AReleaseKeepsTheLibrariesAtItsVersion(t *testing.T) {
	serveLib(t, "v0.5.0", "v0.5.1")
	app, _ := setup(t)
	for _, path := range []string{libMarkdown, libSqlkit} {
		if _, err := Add(app, "", path, Options{}); err != nil {
			t.Fatalf("Add %s from source: %v", path, err)
		}
	}
	asRelease(t, "v0.5.0")
	updated, err := Update(app, "", Options{})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	changes := updated.Changes
	want := []Change{
		{ID: modcache.ID{Repo: nomiRepo, Subdir: "lib/markdown"}, From: "v0.5.1", To: "v0.5.0"},
		{ID: modcache.ID{Repo: nomiRepo, Subdir: "lib/sql/sqlite"}, From: "v0.5.1", To: "v0.5.0"},
		{ID: modcache.ID{Repo: nomiRepo, Subdir: "lib/sql/sqlkit"}, From: "v0.5.1", To: "v0.5.0"},
	}
	if len(changes) != len(want) {
		t.Fatalf("Update answered %+v, want %+v", changes, want)
	}
	for i := range want {
		if changes[i] != want[i] {
			t.Fatalf("Update answered %+v, want %+v", changes, want)
		}
	}
	if updated, err := Update(app, "", Options{}); err != nil || len(updated.Changes) != 0 {
		t.Errorf("a second Update answered %+v, %v; want no change", updated, err)
	}
	if off, err := LibOffRelease(app, "v0.5.0"); err != nil || len(off) != 0 {
		t.Errorf("LibOffRelease after Update answered %v, %v", off, err)
	}
	if off, err := LibOffRelease(app, "v0.6.0"); err != nil || len(off) != 3 {
		t.Errorf("LibOffRelease for another release answered %v, %v; want the three modules", off, err)
	}
	asRelease(t, "v0.6.0")
	if _, err := Update(app, "markdown", Options{}); err == nil || !strings.Contains(err.Error(), "has no tag v0.6.0: the release was published without it") {
		t.Errorf("Update to a release the repository lacks: %v", err)
	}
}
