package pkgfetch

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/nomi-language/nomi/internal/pkgcache"
)

// publishThroughGreet publishes greet v0.1.0, which names strutil v1.0.0,
// and strutil at v1.0.0, v1.1.0, which names words v1.0.0, and v2.0.0,
// words at v1.0.0 and v1.3.0, and kit at v1.0.0, which names none of them.
func publishThroughGreet(t *testing.T) {
	s := serve(t)
	publishGreet(s, "v0.1.0")
	s.Tag("example.com/strutil", "v1.1.0", map[string]string{
		"nomi.toml":    "[package]\nname = \"strutil\"\n\n[dependencies]\nwords = { repo = \"example.com/words\", version = \"v1.0.0\" }\n",
		"strutil.nomi": "pub fn twice(s: String) -> String\n    s + s\nend\n",
	})
	s.Tag("example.com/strutil", "v2.0.0", strutilFiles)
	for _, v := range []string{"v1.0.0", "v1.3.0"} {
		s.Tag("example.com/words", v, map[string]string{"nomi.toml": "[package]\nname = \"words\"\n"})
	}
	s.Tag("example.com/kit", "v1.0.0", kitFiles)
}

// Update with no name covers the whole graph: a repository only a
// dependency names rises to its latest release in its line, and so does
// each one it then reaches. nomi.lock alone records them; nomi.toml does
// not change. Update of one entry raises none. Every later command that
// does not mean to re-resolve keeps the raise: a plain walk, a fetch, Add
// of an unrelated package, Remove of one. A fetch that re-resolves from
// the manifests returns the repository to the version its dependency asks
// for and answers the change, and Add with a version named takes that
// version below the lock's.
func TestUpdate_RaisesTheRepositoriesOnlyADependencyNames(t *testing.T) {
	publishThroughGreet(t)
	app, _ := setup(t)
	if _, err := Add(app, "", "example.com/greet@v0.1.0", Options{}); err != nil {
		t.Fatal(err)
	}
	if updated, err := Update(app, "greet", Options{}); err != nil || len(updated.Changes) != 0 || len(updated.Raised) != 0 {
		t.Fatalf("Update of greet alone answered %+v, %v", updated, err)
	}
	manifest := readFile(t, filepath.Join(app, "nomi.toml"))

	updated, err := Update(app, "", Options{})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	wantChanges := []Change{
		{ID: pkgcache.ID{Repo: "example.com/strutil"}, From: "v1.0.0", To: "v1.1.0"},
		{ID: pkgcache.ID{Repo: "example.com/words"}, To: "v1.3.0"},
	}
	if wantRaised := []string{"example.com/strutil", "example.com/words"}; !reflect.DeepEqual(updated.Changes, wantChanges) || !reflect.DeepEqual(updated.Raised, wantRaised) {
		t.Fatalf("Update answered %+v\nwant changes %+v and raised %v", updated, wantChanges, wantRaised)
	}
	if after := readFile(t, filepath.Join(app, "nomi.toml")); after != manifest {
		t.Errorf("Update changed nomi.toml for a repository only a dependency names:\n%s\nwas\n%s", after, manifest)
	}
	held := func(when string) {
		t.Helper()
		for repo, want := range map[string]string{"example.com/strutil": "v1.1.0", "example.com/words": "v1.3.0"} {
			if v, direct := lockedAt(t, app, repo); v != want || direct {
				t.Errorf("%s, nomi.lock takes %s at %q (direct %v), want %s", when, repo, v, direct, want)
			}
		}
	}
	held("after Update")
	if updated, err := Update(app, "", Options{}); err != nil || len(updated.Changes) != 0 || len(updated.Raised) != 0 {
		t.Errorf("a second Update answered %+v, %v; want nothing to move", updated, err)
	}

	// A plain walk, as run and check make, reads the lock.
	if g, err := Fetch(app, Options{Offline: true}); err != nil {
		t.Fatal(err)
	} else if d, _ := g.Find(pkgcache.ID{Repo: "example.com/strutil"}); d.Version != "v1.1.0" || d.Direct {
		t.Errorf("a walk takes strutil as %+v", d)
	}
	if _, err := Fetch(app, Options{Update: true}); err != nil {
		t.Fatal(err)
	}
	held("after a fetch")
	if _, err := Add(app, "", "example.com/kit", Options{}); err != nil {
		t.Fatal(err)
	}
	held("after adding kit")
	if _, err := Remove(app, "kit", Options{}); err != nil {
		t.Fatal(err)
	}
	held("after removing kit")
	if after := readFile(t, filepath.Join(app, "nomi.toml")); after != manifest {
		t.Errorf("nomi.toml after adding and removing kit:\n%s\nwant\n%s", after, manifest)
	}

	_, changes, err := Reselect(app, Options{})
	if err != nil {
		t.Fatal(err)
	}
	wantChanges = []Change{
		{ID: pkgcache.ID{Repo: "example.com/strutil"}, From: "v1.1.0", To: "v1.0.0"},
		{ID: pkgcache.ID{Repo: "example.com/words"}, From: "v1.3.0"},
	}
	if !reflect.DeepEqual(changes, wantChanges) {
		t.Errorf("Reselect answered %+v, want %+v", changes, wantChanges)
	}
	if v, _ := lockedAt(t, app, "example.com/strutil"); v != "v1.0.0" {
		t.Errorf("after Reselect nomi.lock takes strutil at %q, want v1.0.0", v)
	}

	if _, err := Update(app, "", Options{}); err != nil {
		t.Fatal(err)
	}
	held("after a second Update")
	if _, err := Add(app, "", "example.com/strutil@v1.0.0", Options{}); err != nil {
		t.Fatal(err)
	}
	if v, direct := lockedAt(t, app, "example.com/strutil"); v != "v1.0.0" || !direct {
		t.Errorf("Add of strutil@v1.0.0 left nomi.lock taking it at %q (direct %v)", v, direct)
	}
}

// A package that needs a repository it reaches through a dependency at a
// higher version than that dependency asks for names it in its own
// [dependencies], though no file imports it: the entry is a requirement
// like any other, so its consumers take the repository at least there,
// with or without a lock.
func TestFetch_AnEntryNoFileImportsRaisesTheConsumersBuild(t *testing.T) {
	s := serve(t)
	publishGreet(s, "v0.1.0")
	s.Tag("example.com/strutil", "v1.1.0", strutilFiles)
	s.Tag("example.com/shop", "v1.0.0", map[string]string{
		"nomi.toml": "[package]\nname = \"shop\"\n\n[dependencies]\n" +
			"greet = { repo = \"example.com/greet\", version = \"v0.1.0\" }\n" +
			"strutil = { repo = \"example.com/strutil\", version = \"v1.1.0\" }\n",
		"shop.nomi": "import greet\n\npub fn welcome() -> String\n    greet.hello(\"you\")\nend\n",
	})
	app, _ := setup(t)
	if _, err := Add(app, "", "example.com/shop@v1.0.0", Options{}); err != nil {
		t.Fatal(err)
	}
	if v, direct := lockedAt(t, app, "example.com/strutil"); v != "v1.1.0" || direct {
		t.Errorf("nomi.lock takes strutil at %q (direct %v), want v1.1.0, which shop names", v, direct)
	}
	g, changes, err := Reselect(app, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if d, _ := g.Find(pkgcache.ID{Repo: "example.com/strutil"}); d.Version != "v1.1.0" || len(changes) != 0 {
		t.Errorf("a resolve from the manifests takes strutil as %+v and changes %+v; want v1.1.0 and no change", d, changes)
	}
}

// Outdated lists each repository of the build with a later release: the
// version the lock takes it at, the latest in its line, the latest of all,
// and the packages that reach it, or none for one the project names. A
// repository at its latest release is not listed.
func TestOutdated_ListsWhatIsBehind(t *testing.T) {
	publishThroughGreet(t)
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
}
