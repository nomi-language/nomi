package modfetch

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/modcache"
)

// publishIndirect publishes greet v0.1.0, which names strutil v1.0.0, and
// strutil at v1.0.0, v1.1.0, which names words v1.0.0, and v2.0.0, and
// words at v1.0.0 and v1.3.0.
func publishIndirect(t *testing.T) {
	s := serve(t)
	publishGreet(s, "v0.1.0")
	s.Tag("example.com/strutil", "v1.1.0", map[string]string{
		"nomi.toml":    "[module]\nname = \"strutil\"\n\n[dependencies]\nwords = { repo = \"example.com/words\", version = \"v1.0.0\" }\n",
		"strutil.nomi": "pub fn twice(s: String) -> String\n    s + s\nend\n",
	})
	s.Tag("example.com/strutil", "v2.0.0", strutilFiles)
	for _, v := range []string{"v1.0.0", "v1.3.0"} {
		s.Tag("example.com/words", v, map[string]string{"nomi.toml": "[module]\nname = \"words\"\n"})
	}
}

// Update with no name covers the whole graph: a repository only a
// dependency names rises to its latest release in its line, and so does
// each one it then reaches, each recorded in the project's [indirect]
// table; the dependency's requirement no longer holds it down. Update of
// one entry raises none. Adding a module of such a repository drops its
// floor, and a floor of a repository the build no longer reaches goes.
func TestUpdate_RaisesTheRepositoriesOnlyADependencyNames(t *testing.T) {
	publishIndirect(t)
	app, _ := setup(t)
	if _, err := Add(app, "", "example.com/greet@v0.1.0", Options{}); err != nil {
		t.Fatal(err)
	}
	if updated, err := Update(app, "greet", Options{}); err != nil || len(updated.Changes) != 0 || len(updated.Indirect) != 0 {
		t.Fatalf("Update of greet alone answered %+v, %v", updated, err)
	}

	updated, err := Update(app, "", Options{})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	wantChanges := []Change{
		{ID: modcache.ID{Repo: "example.com/strutil"}, From: "v1.0.0", To: "v1.1.0"},
		{ID: modcache.ID{Repo: "example.com/words"}, To: "v1.3.0"},
	}
	wantFloors := []modcache.Floor{{Repo: "example.com/strutil", Version: "v1.1.0"}, {Repo: "example.com/words", Version: "v1.3.0"}}
	if !reflect.DeepEqual(updated.Changes, wantChanges) || !reflect.DeepEqual(updated.Indirect, wantFloors) {
		t.Fatalf("Update answered %+v\nwant changes %+v and floors %+v", updated, wantChanges, wantFloors)
	}
	manifest := readFile(t, filepath.Join(app, "nomi.toml"))
	if want := "\n[indirect]\n\"example.com/strutil\" = \"v1.1.0\"\n\"example.com/words\" = \"v1.3.0\"\n"; !strings.HasSuffix(manifest, want) {
		t.Errorf("nomi.toml after Update:\n%s\nwant it to end\n%s", manifest, want)
	}
	lock := readFile(t, filepath.Join(app, "nomi.lock"))
	for _, want := range []string{"example.com/strutil . v1.1.0 ", "example.com/words . v1.3.0 "} {
		_, line, found := strings.Cut(lock, want)
		if line, _, _ = strings.Cut(line, "\n"); !found || strings.HasSuffix(line, " direct") {
			t.Errorf("nomi.lock lacks %q as a module the project does not name:\n%s", want, lock)
		}
	}
	if updated, err := Update(app, "", Options{}); err != nil || len(updated.Changes) != 0 || len(updated.Indirect) != 0 {
		t.Errorf("a second Update answered %+v, %v; want nothing to move", updated, err)
	}

	// A plain fetch reads the floors as requirements, so the build stays.
	if g, err := Fetch(app, Options{Offline: true}); err != nil {
		t.Fatal(err)
	} else if d, _ := g.Find(modcache.ID{Repo: "example.com/strutil"}); d.Version != "v1.1.0" || d.Direct {
		t.Errorf("a fetch takes strutil as %+v", d)
	}

	if _, err := Add(app, "", "example.com/words", Options{}); err != nil {
		t.Fatal(err)
	}
	if manifest := readFile(t, filepath.Join(app, "nomi.toml")); strings.Contains(manifest, "\"example.com/words\" =") ||
		!strings.Contains(manifest, "words = { repo = \"example.com/words\", version = \"v1.3.0\" }") {
		t.Errorf("Add of a repository with a floor kept the floor:\n%s", manifest)
	}
	if _, err := Remove(app, "greet", Options{}); err != nil {
		t.Fatal(err)
	}
	if _, err := Update(app, "", Options{}); err != nil {
		t.Fatal(err)
	}
	if manifest := readFile(t, filepath.Join(app, "nomi.toml")); strings.Contains(manifest, "[indirect]") {
		t.Errorf("Update kept the floor of a repository the build no longer reaches:\n%s", manifest)
	}
}

// A floor counts only for a repository a requirement reaches, and is held
// to the requirements on it as another requirement is: one in another line
// is an error that names the project.
func TestFetch_AFloorIsARequirementOnAReachedRepository(t *testing.T) {
	publishIndirect(t)
	app, _ := setup(t)
	writeFile(t, filepath.Join(app, "nomi.toml"), "[module]\nname = \"app\"\n\n[dependencies]\n"+
		"greet = { repo = \"example.com/greet\", version = \"v0.1.0\" }\n\n[indirect]\n"+
		"\"example.com/strutil\" = \"v1.1.0\"\n\"example.com/unused\" = \"v1.0.0\"\n")
	g, err := Fetch(app, Options{Update: true})
	if err != nil {
		t.Fatal(err)
	}
	if d, _ := g.Find(modcache.ID{Repo: "example.com/strutil"}); d.Version != "v1.1.0" {
		t.Errorf("the floor did not raise strutil: %+v", d)
	}
	if _, ok := g.Find(modcache.ID{Repo: "example.com/unused"}); ok {
		t.Error("a floor of a repository nothing requires put it in the build")
	}
	writeFile(t, filepath.Join(app, "nomi.toml"), "[module]\nname = \"app\"\n\n[dependencies]\n"+
		"greet = { repo = \"example.com/greet\", version = \"v0.1.0\" }\n\n[indirect]\n\"example.com/strutil\" = \"v2.0.0\"\n")
	_, err = Fetch(app, Options{Update: true})
	want := "example.com/greet v0.1.0 requires example.com/strutil v1.0.0 and app requires example.com/strutil v2.0.0: one tag versions a whole repository"
	if err == nil || !strings.HasPrefix(err.Error(), want) {
		t.Errorf("a floor in another line: %v\nwant %s", err, want)
	}
}

// Outdated lists each repository of the build with a later release: the
// version the lock takes it at, the latest in its line, the latest of all,
// and the modules that reach it, or none for one the project names. A
// repository at its latest release is not listed. Offline it says it
// needs the network.
func TestOutdated_ListsWhatIsBehind(t *testing.T) {
	publishIndirect(t)
	app, _ := setup(t)
	if _, err := Add(app, "", "example.com/greet@v0.1.0", Options{}); err != nil {
		t.Fatal(err)
	}
	behind, err := Outdated(app, Options{})
	if err != nil {
		t.Fatal(err)
	}
	want := []Behind{{Repo: "example.com/strutil", Version: "v1.0.0", Line: "v1.1.0", Newest: "v2.0.0", Via: []string{"greet"}}}
	if !reflect.DeepEqual(behind, want) {
		t.Errorf("Outdated answered %+v, want %+v", behind, want)
	}
	if _, err := Outdated(app, Options{Offline: true}); err == nil || !strings.Contains(err.Error(), "needs the network") {
		t.Errorf("Outdated offline: %v", err)
	}
}
