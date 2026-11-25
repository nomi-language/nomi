package pkgfetch

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/buildversion"
	"github.com/nomi-language/nomi/internal/pkgcache"
	"github.com/nomi-language/nomi/internal/pkgfetch/pkgfetchtest"
)

func TestAddLocal_WritesADirEntry(t *testing.T) {
	s := serve(t)
	publishGreet(s, "v0.1.0")
	app, _ := setup(t)
	lib := filepath.Join(app, "csvdata")
	writeFile(t, filepath.Join(lib, "nomi.toml"), "[package]\nname = \"csvdata\"\n\n[dependencies]\n"+
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
		t.Errorf("nomi.lock locks what the local package needs, and not the local package:\n%s", lock)
	}

	// A sibling directory is written relative to the project root, under
	// the name given.
	sib := filepath.Join(filepath.Dir(app), "shared")
	writeFile(t, filepath.Join(sib, "nomi.toml"), "[package]\nname = \"shared\"\n")
	if _, err := AddLocal(app, "common", sib, Options{}); err != nil {
		t.Fatalf("AddLocal of a sibling: %v", err)
	}
	if manifest := readFile(t, filepath.Join(app, "nomi.toml")); !strings.Contains(manifest, "common = { dir = \"../shared\" }\n") {
		t.Errorf("nomi.toml lacks the sibling's entry:\n%s", manifest)
	}

	// An entry of that name that names a repository keeps it and gains the
	// dir, which then stands for that package.
	if _, err := Add(app, "", "example.com/greet@v0.1.0", Options{}); err != nil {
		t.Fatal(err)
	}
	fork := filepath.Join(filepath.Dir(app), "greet")
	writeFile(t, filepath.Join(fork, "nomi.toml"), "[package]\nname = \"greet\"\n")
	if _, err := AddLocal(app, "", fork, Options{}); err != nil {
		t.Fatalf("AddLocal of a fork: %v", err)
	}
	if manifest := readFile(t, filepath.Join(app, "nomi.toml")); !strings.Contains(manifest, "greet = { repo = \"example.com/greet\", version = \"v0.1.0\", dir = \"../greet\" }\n") {
		t.Errorf("nomi.toml after adding a fork of greet:\n%s", manifest)
	}
}

func TestAddLocal_RefusesADirectoryThatIsNotANomiPackage(t *testing.T) {
	app, _ := setup(t)
	before := readFile(t, filepath.Join(app, "nomi.toml"))

	noManifest := filepath.Join(app, "gocode")
	writeFile(t, filepath.Join(noManifest, "go.mod"), "module example.com/gocode\n\ngo 1.27.0\n")
	_, err := AddLocal(app, "", noManifest, Options{})
	if err == nil || !strings.Contains(err.Error(), "has no nomi.toml, so it is not a Nomi package") {
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
		"nomi.toml":  "[package]\nname = \"other\"\n",
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

func TestRemove_KeepsAPackageAnotherPackageNeeds(t *testing.T) {
	s := serve(t)
	publishGreet(s, "v0.1.0")
	s.Tag("example.com/loud", "v1.0.0", map[string]string{
		"nomi.toml": "[package]\nname = \"loud\"\n\n[dependencies]\nstrutil = { repo = \"example.com/strutil\", version = \"v1.0.0\" }\n",
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
	if len(removed.Unneeded) != 1 || removed.Unneeded[0].ID != (pkgcache.ID{Repo: "example.com/greet"}) {
		t.Fatalf("Remove dropped %v; example.com/loud still needs strutil", removed.Unneeded)
	}
	if lock := readFile(t, filepath.Join(app, "nomi.lock")); !strings.Contains(lock, "example.com/strutil . v1.0.0 ") {
		t.Errorf("nomi.lock lost a package another package needs:\n%s", lock)
	}
}

func TestRemove_TakesOutALocalPackage(t *testing.T) {
	app, _ := setup(t)
	lib := filepath.Join(app, "lib")
	writeFile(t, filepath.Join(lib, "nomi.toml"), "[package]\nname = \"lib\"\n")
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
		t.Errorf("nomi.toml after removing a local package:\n%s", manifest)
	}
}

// Update takes the highest release in the entry's line: not a
// prerelease, below v1 not the next minor version, and from v1 on not the
// next major version, which `nomi add <repo>@<version>` crosses to. In
// each tier it answers the latest release past the line.
func TestUpdate_StaysWithinTheLine(t *testing.T) {
	s := serve(t)
	publishGreet(s, "v0.1.0", "v0.1.1", "v0.2.0", "v0.3.0-rc.1", "v1.0.0", "v1.1.0", "v1.2.0-rc.1", "v2.0.0")
	s.Tag("example.com/strutil", "v1.1.0", map[string]string{
		"nomi.toml":    "[package]\nname = \"strutil\"\n",
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
	if changes := updated.Changes; len(changes) != 1 || changes[0] != (Change{ID: pkgcache.ID{Repo: "example.com/greet"}, From: "v0.1.0", To: "v0.1.1"}) {
		t.Fatalf("Update of one dependency answered %+v", updated)
	}
	// later is the release past its line each Update answers: v2.0.0, the
	// prereleases left out.
	later := func(updated Updated, at string) {
		t.Helper()
		if len(updated.Later) != 1 || updated.Later[0].Entry.Version != at || updated.Later[0].Newest != "v2.0.0" {
			t.Errorf("Update to %s answered the later releases %+v, want v2.0.0", at, updated.Later)
		}
	}
	later(updated, "v0.1.1")
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
		if added, err := Add(app, "", "example.com/greet@"+step.add, Options{}); err != nil || added.Package.Version != step.add {
			t.Errorf("Add across a line to %s answered %+v, %v", step.add, added, err)
		}
		updated, err := Update(app, "greet", Options{})
		if err != nil {
			t.Fatal(err)
		}
		if manifest := readFile(t, filepath.Join(app, "nomi.toml")); !strings.Contains(manifest, "version = \""+step.to+"\"") {
			t.Errorf("Update from %s did not move greet to %s:\n%s", step.add, step.to, manifest)
		}
		later(updated, step.to)
	}
	if _, err := Add(app, "", "example.com/greet@v2.0.0", Options{}); err != nil {
		t.Fatal(err)
	}
	if updated, err := Update(app, "greet", Options{}); err != nil || len(updated.Later) != 0 {
		t.Errorf("Update at the latest release answered %+v, %v; want no later release", updated, err)
	}
}

// Below v0.1 each release is a line of its own: Update does not move
// v0.0.3 to v0.0.5, and answers v0.0.5 with the entry `nomi add` names to
// take it. A prerelease moves to its release, and then answers the later
// one. A repository with nothing past its release answers nothing.
func TestUpdate_A000ReleaseIsALineOfItsOwn(t *testing.T) {
	s := serve(t)
	for _, v := range []string{"v0.0.3", "v0.0.4-rc.1", "v0.0.4", "v0.0.5"} {
		s.Tag("example.com/mono", v, monoFiles(v))
	}
	app, _ := setup(t)
	for _, arg := range []string{"example.com/mono/lib/a@v0.0.3", "example.com/mono/lib/b"} {
		if _, err := Add(app, "", arg, Options{}); err != nil {
			t.Fatal(err)
		}
	}
	a := pkgcache.ID{Repo: "example.com/mono", Subdir: "lib/a"}
	updated, err := Update(app, "", Options{})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(updated.Changes) != 0 || len(updated.Later) != 1 || updated.Later[0].Entry.ID != a ||
		updated.Later[0].Entry.Version != "v0.0.3" || updated.Later[0].Newest != "v0.0.5" {
		t.Fatalf("Update at v0.0.3 answered %+v; want no change and v0.0.5 named for lib/a", updated)
	}
	if manifest := readFile(t, filepath.Join(app, "nomi.toml")); strings.Count(manifest, `version = "v0.0.3"`) != 2 {
		t.Errorf("Update moved a 0.0.x entry:\n%s", manifest)
	}

	if _, err := Add(app, "", "example.com/mono/lib/a@v0.0.4-rc.1", Options{}); err != nil {
		t.Fatal(err)
	}
	updated, err = Update(app, "a", Options{})
	if err != nil {
		t.Fatalf("Update from a prerelease: %v", err)
	}
	// lib/a names its repository's root and lib/b by directory, so the
	// build holds three packages of it.
	if len(updated.Changes) != 3 || len(updated.Later) != 1 || updated.Later[0].Entry.Version != "v0.0.4" || updated.Later[0].Newest != "v0.0.5" {
		t.Fatalf("Update from v0.0.4-rc.1 answered %+v; want three packages at v0.0.4 and v0.0.5 named", updated)
	}
	for _, c := range updated.Changes {
		if c.From != "v0.0.4-rc.1" || c.To != "v0.0.4" {
			t.Errorf("Update from v0.0.4-rc.1 answered the change %+v, want v0.0.4-rc.1 -> v0.0.4", c)
		}
	}

	if _, err := Add(app, "", "example.com/mono/lib/a@v0.0.5", Options{}); err != nil {
		t.Fatal(err)
	}
	if updated, err := Update(app, "", Options{}); err != nil || len(updated.Changes) != 0 || len(updated.Later) != 0 {
		t.Errorf("Update at the latest release answered %+v, %v; want nothing", updated, err)
	}
}

// One tag versions a whole repository, so updating one of its packages
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

// One tag versions a repository, so another package of a repository the
// project takes at a version is added at that version, not at the latest
// release, which is in another major version here and would conflict.
func TestAdd_AnotherPackageOfARepositoryTakesTheProjectsVersion(t *testing.T) {
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
		t.Fatalf("Add of another package of the repository: %v", err)
	}
	if added.Package.Version != "v1.0.0" || !added.Held || len(added.Moved) != 0 {
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
		if added.Package.Version != step.version || added.Held || len(added.Moved) != 1 || added.Moved[0] != step.moved {
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
			t.Errorf("nomi.lock after Add %s holds %d packages at %s, want the root, a and b:\n%s", step.arg, n, step.version, lock)
		}
	}
}

// asRelease makes the test's process a release `nomi` at version v
// (buildversion.SetStampForTest) until the test ends.
func asRelease(t *testing.T, v string) {
	t.Helper()
	t.Cleanup(buildversion.SetStampForTest(v))
}

// libFiles is the first-party libraries' repository at version v.
func libFiles(v string) map[string]string {
	return map[string]string{
		"lib/markdown/nomi.toml":     "[package]\nname = \"markdown\"\n",
		"lib/markdown/markdown.nomi": "pub fn version() -> String\n    \"" + v + "\"\nend\n",
		"lib/sql/sqlite/nomi.toml":   "[package]\nname = \"sqlite\"\n",
		"lib/sql/sqlite/sqlite.nomi": "pub fn version() -> String\n    \"" + v + "\"\nend\n",
		"lib/sql/sqlkit/nomi.toml":   "[package]\nname = \"sqlkit\"\n\n[dependencies]\nsqlite = { dir = \"../sqlite\" }\n",
		"lib/sql/sqlkit/sqlkit.nomi": "pub fn version() -> String\n    \"" + v + "\"\nend\n",
	}
}

// serveLib serves the first-party libraries' repository at each of
// versions, on a github.com that is the test's server.
func serveLib(t *testing.T, versions ...string) *pkgfetchtest.Server {
	t.Helper()
	s := pkgfetchtest.New(t)
	useHosts(t, map[string]host{"github.com": {base: s.URL, archive: knownHosts["github.com"].archive, depth: 2}})
	for _, v := range versions {
		s.Tag(pkgcache.CompilerRepo, v, libFiles(v))
	}
	return s
}

const (
	libMarkdown = pkgcache.CompilerRepo + "/lib/markdown"
	libSqlkit   = pkgcache.CompilerRepo + "/lib/sql/sqlkit"
)

// A release `nomi` adds a first-party library at its own version, not at
// the latest release, and writes that version for a version named on the
// command line and for `@latest` too, saying so (Pinned); another library
// is added at it as the version the project holds; a commit named is
// taken at that commit, and moves the repository's entries with it; and a
// release whose tag the repository lacks says so and adds nothing.
func TestAdd_AReleaseTakesALibraryAtItsOwnVersion(t *testing.T) {
	s := serveLib(t, "v0.5.0", "v0.5.1")
	app, _ := setup(t)
	asRelease(t, "v0.5.0")

	added, err := Add(app, "", libMarkdown, Options{})
	if err != nil {
		t.Fatalf("Add markdown: %v", err)
	}
	if added.Package != (pkgcache.Package{ID: pkgcache.ID{Repo: pkgcache.CompilerRepo, Subdir: "lib/markdown"}, Version: "v0.5.0"}) || added.Pinned || added.Held {
		t.Errorf("a release added %+v, want markdown at its own v0.5.0", added)
	}
	for _, query := range []string{"v0.5.1", "latest"} {
		added, err := Add(app, "", libMarkdown+"@"+query, Options{})
		if err != nil || added.Package.Version != "v0.5.0" || !added.Pinned {
			t.Fatalf("Add markdown@%s answered %+v, %v; want the release's v0.5.0, pinned", query, added, err)
		}
		if manifest := readFile(t, filepath.Join(app, "nomi.toml")); !strings.Contains(manifest, `version = "v0.5.0"`) || strings.Contains(manifest, "v0.5.1") {
			t.Fatalf("Add markdown@%s wrote\n%s\nwant the release's v0.5.0", query, manifest)
		}
	}
	if added, err := Add(app, "", libSqlkit, Options{}); err != nil || added.Package.Version != "v0.5.0" || !added.Held {
		t.Fatalf("Add sqlkit to a project that takes the libraries at v0.5.0 answered %+v, %v", added, err)
	}

	commit := s.Tag(pkgcache.CompilerRepo, "v0.5.2", libFiles("v0.5.2"))
	added, err = Add(app, "", libMarkdown+"@"+commit, Options{})
	if err != nil || added.Package.Version != commit || added.Pinned || len(added.Moved) != 1 {
		t.Fatalf("Add markdown at a commit answered %+v, %v; want the commit, with sqlkit moved to it", added, err)
	}
	if lock := readFile(t, filepath.Join(app, "nomi.lock")); strings.Count(lock, " commit "+commit+" ") != 3 {
		t.Errorf("nomi.lock after Add at a commit:\n%s\nwant markdown, sqlkit and sqlite at the commit", lock)
	}

	asRelease(t, "v0.6.0")
	app, _ = setup(t)
	before := readFile(t, filepath.Join(app, "nomi.toml"))
	_, err = Add(app, "", libSqlkit, Options{})
	want := "this nomi is release v0.6.0, which takes the first-party libraries at v0.6.0, and github.com/nomi-language/nomi has no tag v0.6.0: " +
		"the release was published without it; `nomi add github.com/nomi-language/nomi/lib/sql/sqlkit@<commit>` takes it at a commit, and `nomi add <directory>` from a checkout"
	if err == nil || err.Error() != want {
		t.Fatalf("Add of a library at a release the repository has no tag for: %v\nwant %s", err, want)
	}
	if after := readFile(t, filepath.Join(app, "nomi.toml")); after != before {
		t.Errorf("a failed Add changed nomi.toml:\n%s", after)
	}
}

// A release takes every requirement on the compiler's repository at its
// own version, from the project and from a fetched package, at any version
// or line, with no conflict between them, and the lock records that
// version. A fetched package that needs a later nomi than the release fails
// with that floor, not with a conflict over its library's version. A
// commit entry is honored, and one beside a version entry is still a
// conflict. A `nomi` built from source takes the requirements as written,
// so two 0.0.x releases of the repository conflict there.
func TestFetch_AReleaseTakesTheCompilersRepositoryAtItsVersion(t *testing.T) {
	s := serveLib(t, "v0.0.3", "v0.0.4", "v0.0.5")
	useHosts(t, map[string]host{
		"github.com":  {base: s.URL, archive: knownHosts["github.com"].archive, depth: 2},
		"example.com": {base: s.URL, archive: knownHosts["github.com"].archive},
	})
	entry := func(key, v string) string {
		return "markdown = { repo = \"" + pkgcache.CompilerRepo + "\", subdir = \"lib/markdown\", " + key + " = \"" + v + "\" }\n"
	}
	markdown := func(v string) string { return entry("version", v) }
	kit := func(repo, needs, dep string) string {
		s.Tag(repo, "v1.0.0", map[string]string{"nomi.toml": "[package]\nname = \"kit\"\n" + needs + "\n[dependencies]\n" + dep})
		return "kit = { repo = \"" + repo + "\", version = \"v1.0.0\" }\n"
	}
	project := func(deps string) string {
		app, _ := setup(t)
		writeFile(t, filepath.Join(app, "nomi.toml"), "[package]\nname = \"app\"\n\n[dependencies]\n"+deps)
		return app
	}
	commit := s.Tag(pkgcache.CompilerRepo, "v0.0.6", libFiles("v0.0.6"))
	old := kit("example.com/old", "", markdown("v0.0.3"))
	later := kit("example.com/later", "nomi = \"v0.0.5\"\n", markdown("v0.0.5"))
	atCommit := kit("example.com/pinned", "", entry("commit", commit))

	asRelease(t, "v0.0.4")
	app := project(markdown("v0.0.5") + old)
	g, err := Fetch(app, Options{Update: true})
	if err != nil {
		t.Fatalf("a release fetching markdown at v0.0.5 and v0.0.3: %v", err)
	}
	if d, _ := g.Find(pkgcache.ID{Repo: pkgcache.CompilerRepo, Subdir: "lib/markdown"}); d.Version != "v0.0.4" {
		t.Errorf("a release v0.0.4 took markdown at %s", d.Version)
	}
	if lock := readFile(t, filepath.Join(app, "nomi.lock")); !strings.Contains(lock, pkgcache.CompilerRepo+" lib/markdown v0.0.4 ") {
		t.Errorf("nomi.lock does not record the release's version:\n%s", lock)
	}
	lockedLater := project(old)
	writeFile(t, filepath.Join(lockedLater, "nomi.lock"), lockHeader()+
		pkgcache.CompilerRepo+" lib/markdown v0.0.5 "+strings.Repeat("a", 40)+" sha256:"+strings.Repeat("b", 64)+"\n")
	if g, err := Fetch(lockedLater, Options{Update: true}); err != nil {
		t.Errorf("a release fetching with a lock at a later tag of its repository: %v", err)
	} else if d, _ := g.Find(pkgcache.ID{Repo: pkgcache.CompilerRepo, Subdir: "lib/markdown"}); d.Version != "v0.0.4" {
		t.Errorf("a release v0.0.4 took markdown locked at v0.0.5 at %s", d.Version)
	}

	_, err = Fetch(project(markdown("v0.0.4")+later), Options{Update: true})
	var tooOld *pkgcache.NomiVersionError
	want := "package example.com/later v1.0.0 needs nomi v0.0.5 or later, and this nomi is v0.0.4; install a newer nomi"
	if !errors.As(err, &tooOld) || err.Error() != want {
		t.Errorf("a release fetching a package that needs a later nomi: %v\nwant %s", err, want)
	}

	if g, err := Fetch(project(entry("commit", commit)), Options{Update: true}); err != nil {
		t.Errorf("a release fetching markdown at a commit: %v", err)
	} else if d, _ := g.Find(pkgcache.ID{Repo: pkgcache.CompilerRepo, Subdir: "lib/markdown"}); d.Version != commit {
		t.Errorf("a release took markdown at a commit at %s", d.Version)
	}
	_, err = Fetch(project(markdown("v0.0.4")+atCommit), Options{Update: true})
	want = "app requires " + pkgcache.CompilerRepo + " v0.0.4 and example.com/pinned v1.0.0 requires " + pkgcache.CompilerRepo + " at commit " + commit +
		": a build takes a repository at one point, so its requirements are all at tags or all at one commit"
	if err == nil || err.Error() != want {
		t.Errorf("a release fetching a version entry and a commit entry: %v\nwant %s", err, want)
	}

	asRelease(t, "")
	_, err = Fetch(project(markdown("v0.0.5")+old), Options{Update: true})
	want = "app requires " + pkgcache.CompilerRepo + " v0.0.5 and example.com/old v1.0.0 requires " + pkgcache.CompilerRepo + " v0.0.3: " +
		"one tag versions a whole repository, so every requirement on it must be in one line: a major version from v1 on, a minor version from v0.1 on, and below v0.1 one release"
	if err == nil || err.Error() != want {
		t.Errorf("a nomi built from source fetching markdown at v0.0.5 and v0.0.3: %v\nwant %s", err, want)
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
	for _, want := range []string{pkgcache.CompilerRepo + " lib/sql/sqlkit v0.5.1 ", pkgcache.CompilerRepo + " lib/sql/sqlite v0.5.1 "} {
		if !strings.Contains(lock, want) {
			t.Errorf("nomi.lock lacks %q:\n%s", want, lock)
		}
	}

	// The repository's only tag names no version.
	s := serveLib(t)
	s.Tag(pkgcache.CompilerRepo, "nightly", libFiles("nightly"))
	app, _ = setup(t)
	_, err := Add(app, "", libMarkdown, Options{})
	want := "github.com/nomi-language/nomi has no release yet, so its first-party libraries have no version to take; add one from a checkout with `nomi add <directory>`"
	if err == nil || err.Error() != want {
		t.Errorf("Add of a library from a repository with no release: %v\nwant %s", err, want)
	}
}

// A release `nomi`'s Update moves the libraries on another version to its
// own, down from a later tag too, answers each move, writes that version
// into nomi.toml, and then leaves them there. A plain fetch by another
// release finds no lock line at its version and says why. A release whose
// tag the repository lacks says so.
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
		{ID: pkgcache.ID{Repo: pkgcache.CompilerRepo, Subdir: "lib/markdown"}, From: "v0.5.1", To: "v0.5.0"},
		{ID: pkgcache.ID{Repo: pkgcache.CompilerRepo, Subdir: "lib/sql/sqlite"}, From: "v0.5.1", To: "v0.5.0"},
		{ID: pkgcache.ID{Repo: pkgcache.CompilerRepo, Subdir: "lib/sql/sqlkit"}, From: "v0.5.1", To: "v0.5.0"},
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
	if manifest := readFile(t, filepath.Join(app, "nomi.toml")); strings.Count(manifest, `version = "v0.5.0"`) != 2 {
		t.Errorf("nomi.toml after Update does not state the release's version:\n%s", manifest)
	}

	// A plain fetch by another release finds the lock at v0.5.0, which is
	// not what that release takes, and says to run `nomi fetch`.
	asRelease(t, "v0.5.1")
	_, err = Fetch(app, Options{})
	wantErr := "nomi.lock has no entry for package " + libMarkdown + " v0.5.1: this nomi is release v0.5.1, and takes the first-party libraries at that version; run `nomi fetch` to add it"
	if err == nil || err.Error() != wantErr {
		t.Errorf("a plain fetch by release v0.5.1 of a lock at v0.5.0: %v\nwant %s", err, wantErr)
	}
	asRelease(t, "v0.6.0")
	if _, err := Update(app, "markdown", Options{}); err == nil || !strings.Contains(err.Error(), "has no tag v0.6.0: the release was published without it") {
		t.Errorf("Update to a release the repository lacks: %v", err)
	}
}
