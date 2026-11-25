package pkgfetch

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/nomi-language/nomi/internal/artifactcache"
	"github.com/nomi-language/nomi/internal/pkgcache"
)

// damage is a way a cache entry loses part of what a fetch installed.
type damage struct {
	name string
	// repo and subdir name the package the project needs.
	repo, subdir string
	// do damages the entry at dir, the repository's tree in the cache.
	do func(t *testing.T, dir string)
}

// damages are the incomplete entries a fetch repairs: `rm -rf` of the
// cache removes the list beside the read-only tree and cannot remove the
// tree; a prune or a removal that stopped part way can leave the list
// alone, or a tree that lacks a package's directory.
var damages = []damage{
	{"a tree without its list", "example.com/greet", "", func(t *testing.T, dir string) {
		if err := os.Remove(dir + "+files"); err != nil {
			t.Fatal(err)
		}
	}},
	{"a list without its tree", "example.com/greet", "", func(t *testing.T, dir string) {
		if err := artifactcache.RemoveTree(dir); err != nil {
			t.Fatal(err)
		}
	}},
	{"a tree without the package's directory", "example.com/mono", "lib/a", func(t *testing.T, dir string) {
		lib := filepath.Join(dir, "lib")
		if err := os.Chmod(lib, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := artifactcache.RemoveTree(filepath.Join(lib, "a")); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(lib, 0o555); err != nil {
			t.Fatal(err)
		}
	}},
}

// addDamaged adds d's package to a fresh project at v1.0.0, damages its
// cache entry, and answers the project, the package and its tree.
func addDamaged(t *testing.T, d damage) (app string, m pkgcache.Package, dir string) {
	t.Helper()
	s := serve(t)
	s.Tag("example.com/greet", "v1.0.0", greetFiles("v1.0.0", "hello"))
	s.Tag("example.com/strutil", "v1.0.0", strutilFiles)
	s.Tag("example.com/mono", "v1.0.0", monoFiles("v1.0.0"))
	app, cache := setup(t)
	path := d.repo
	if d.subdir != "" {
		path += "/" + d.subdir
	}
	if _, err := Add(app, "", path+"@v1.0.0", Options{}); err != nil {
		t.Fatal(err)
	}
	m = pkgcache.Package{ID: pkgcache.ID{Repo: d.repo, Subdir: d.subdir}, Version: "v1.0.0"}
	dir = pkgcache.RepoDir(cache, d.repo, "v1.0.0")
	d.do(t, dir)
	if _, held := pkgcache.Lookup(cache, m); held != pkgcache.Incomplete {
		t.Fatalf("%s: Lookup answers %d, want Incomplete", d.name, held)
	}
	return app, m, dir
}

// A fetch that finds an entry incomplete fetches the repository again,
// once, says so, and leaves it whole; the next fetch finds it there.
func TestFetch_AnIncompleteEntryIsFetchedAgain(t *testing.T) {
	for _, d := range damages {
		t.Run(d.name, func(t *testing.T) {
			app, m, dir := addDamaged(t, d)
			var log bytes.Buffer
			if _, err := Fetch(app, Options{Log: &log}); err != nil {
				t.Fatalf("a fetch over %s: %v", d.name, err)
			}
			want := "nomi: fetching " + d.repo + " v1.0.0 (the cached copy was incomplete)\n"
			if log.String() != want {
				t.Errorf("a fetch over %s logged\n%s\nwant\n%s", d.name, log.String(), want)
			}
			cache := os.Getenv(pkgcache.CacheEnv)
			got, held := pkgcache.Lookup(cache, m)
			if held != pkgcache.Present {
				t.Fatalf("after the fetch, Lookup answers %d, want Present", held)
			}
			if _, err := os.Stat(filepath.Join(got, pkgcache.ManifestName)); err != nil {
				t.Errorf("the repaired package has no nomi.toml: %v", err)
			}
			entries, err := os.ReadDir(filepath.Dir(dir))
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range entries {
				if strings.HasPrefix(e.Name(), artifactcache.TempPrefix) {
					t.Errorf("the repair left %s", e.Name())
				}
			}
			log.Reset()
			if _, err := Fetch(app, Options{Log: &log}); err != nil || log.Len() != 0 {
				t.Errorf("a fetch after the repair: %v, logged %q, want nothing fetched", err, log.String())
			}
		})
	}
}

// Offline, an incomplete entry is an error that says so and how to repair
// it, and the cache is left as it was.
func TestFetch_AnIncompleteEntryOfflineIsAnError(t *testing.T) {
	for _, d := range damages {
		t.Run(d.name, func(t *testing.T) {
			app, m, _ := addDamaged(t, d)
			_, err := Fetch(app, Options{Offline: true})
			want := "the package cache holds package " + m.String() + " incomplete, and --offline forbids fetching it again; " +
				"run the command without --offline to fetch it again"
			if err == nil || err.Error() != want {
				t.Fatalf("an offline fetch over %s: %v\nwant %s", d.name, err, want)
			}
			if _, held := pkgcache.Lookup(os.Getenv(pkgcache.CacheEnv), m); held != pkgcache.Incomplete {
				t.Errorf("an offline fetch changed the entry: Lookup answers %d", held)
			}
		})
	}
}

// Two fetches that repair one entry at once both succeed, and the entry is
// whole after them.
func TestFetch_TwoRepairsOfOneEntryLeaveItWhole(t *testing.T) {
	app, m, _ := addDamaged(t, damages[0])
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = Fetch(app, Options{})
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("repair %d: %v", i, err)
		}
	}
	if _, held := pkgcache.Lookup(os.Getenv(pkgcache.CacheEnv), m); held != pkgcache.Present {
		t.Errorf("after two repairs, Lookup answers %d, want Present", held)
	}
}

// A repository whose list names no file under a package's directory has no
// such package: the error names the directory, fetched or cached, and
// nothing is fetched again.
func TestFetch_ARepositoryWithoutThePackagesDirectoryIsAnError(t *testing.T) {
	s := serve(t)
	s.Tag("example.com/mono", "v1.0.0", monoFiles("v1.0.0"))
	app, _ := setup(t)
	writeFile(t, filepath.Join(app, "nomi.toml"), "[package]\nname = \"app\"\n\n[dependencies]\n"+
		"c = { repo = \"example.com/mono\", subdir = \"lib/c\", version = \"v1.0.0\" }\n")
	want := "repository example.com/mono has no directory lib/c at v1.0.0"
	for _, logged := range []string{"nomi: fetching example.com/mono v1.0.0\n", ""} {
		var log bytes.Buffer
		_, err := Fetch(app, Options{Update: true, Log: &log})
		if err == nil || err.Error() != want {
			t.Fatalf("a package directory the repository lacks: %v\nwant %s", err, want)
		}
		if log.String() != logged {
			t.Errorf("the fetch logged %q, want %q", log.String(), logged)
		}
	}
}
