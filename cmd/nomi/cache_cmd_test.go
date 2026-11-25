package main

import (
	"bytes"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nomi-language/nomi/internal/artifactcache"
	"github.com/nomi-language/nomi/internal/hoverdoc"
	"github.com/nomi-language/nomi/internal/pkgcache"
	"github.com/nomi-language/nomi/internal/pkgfetch/pkgfetchtest"
	"github.com/nomi-language/nomi/internal/runners"
	"github.com/nomi-language/nomi/internal/stdcache"
)

// cacheNames is the caches in the order `nomi cache` lists them.
var cacheNames = []string{"packages", "artifacts", "runners", "std"}

// testCaches points every cache's variable at a directory under one
// temporary directory, beside files no cache holds, fills each with
// entries of its own shape, and answers that directory and each cache's.
func testCaches(t *testing.T) (parent string, dirs map[string]string) {
	t.Helper()
	parent = t.TempDir()
	t.Cleanup(func() { _ = artifactcache.RemoveTree(parent) })
	t.Setenv(pkgcache.CacheEnv, filepath.Join(parent, "packages"))
	t.Setenv(artifactcache.RootEnv, filepath.Join(parent, "artifacts"))
	t.Setenv(runners.CacheRootEnv, filepath.Join(parent, "builds"))
	t.Setenv(stdcache.RootEnv, filepath.Join(parent, "home"))
	dirs = map[string]string{
		"packages":  filepath.Join(parent, "packages"),
		"artifacts": filepath.Join(parent, "artifacts"),
		"runners":   filepath.Join(parent, "builds"),
		"std":       filepath.Join(parent, "home", "std"),
	}
	mustWrite(t, filepath.Join(parent, "keep.txt"), "not a cache's")
	mustWrite(t, filepath.Join(parent, "home", "notes.txt"), "beside the std cache, not in it")

	pkgfetchtest.CacheRepo(t, dirs["packages"], "example.com/greet", "v1.0.0", map[string]string{
		"nomi.toml":  "[package]\nname = \"greet\"\n",
		"greet.nomi": "pub fn hello() -> String\n    \"hello\"\nend\n",
	})
	pkgfetchtest.CacheRepo(t, dirs["packages"], "example.com/group/mono", "v0.1.0", map[string]string{
		"lib/a/nomi.toml": "[package]\nname = \"a\"\n",
	})
	art := dirs["artifacts"]
	mustWrite(t, filepath.Join(art, "std", strings.Repeat("0a", 16)+".iface"), strings.Repeat("i", 3000))
	mustWrite(t, filepath.Join(art, "programs", "ab", strings.Repeat("ab", 32)), strings.Repeat("p", 500))
	mustWrite(t, filepath.Join(art, "programs", ".pruned"), "")
	mustWrite(t, filepath.Join(art, "wasm", strings.Repeat("cd", 32), "wazero-1", "module"), strings.Repeat("w", 700))
	run := dirs["runners"]
	mustWrite(t, filepath.Join(run, "0123456789abcdef", "nomi-vm"), strings.Repeat("v", 2000))
	mustWrite(t, filepath.Join(run, "0123456789abcdef", "vm.json"), "{}")
	mustWrite(t, filepath.Join(run, "nomi-vm", "v0.0.1", "darwin_arm64", "nomi-vm"), strings.Repeat("r", 1500))
	mustWrite(t, filepath.Join(run, ".pruned"), "")
	option := filepath.Join(dirs["std"], "0123456789abcdef", "option.nomi")
	mustWrite(t, option, "pub enum Option<T>\n    Some(T)\n    None\nend\n")
	if err := os.Chmod(option, 0o444); err != nil {
		t.Fatal(err)
	}
	return parent, dirs
}

// dirMark is what snapshot holds for a directory.
const dirMark = "\x00dir"

// snapshot is every path under dir with its contents, dirMark for a
// directory, leaving out what is under each of skip.
func snapshot(t *testing.T, dir string, skip ...string) map[string]string {
	t.Helper()
	got := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		for _, s := range skip {
			if strings.HasPrefix(p, s+string(filepath.Separator)) {
				return nil
			}
		}
		if d.IsDir() {
			got[p] = dirMark
			return nil
		}
		data, err := os.ReadFile(p)
		got[p] = string(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// filesSize is the size of the regular files under dir, worked out here.
func filesSize(t *testing.T, dir string) int64 {
	t.Helper()
	var n int64
	for _, text := range snapshot(t, dir) {
		if text != dirMark {
			n += int64(len(text))
		}
	}
	return n
}

// cacheLines is out's lines by their first word, each the words after it.
func cacheLines(t *testing.T, out string) map[string][]string {
	t.Helper()
	lines := map[string][]string{}
	for _, l := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		f := strings.Fields(l)
		if len(f) < 2 {
			t.Fatalf("a line of %q has no directory", out)
		}
		lines[f[0]] = f[1:]
	}
	return lines
}

// `nomi cache` prints each cache's directory, from the variable that moves
// it, and the size of its files, and changes nothing.
func TestCache_ListsEachCachesDirectoryAndSize(t *testing.T) {
	parent, dirs := testCaches(t)
	before := snapshot(t, parent)
	var out bytes.Buffer
	if err := runCache(&out, nil); err != nil {
		t.Fatal(err)
	}
	lines := cacheLines(t, out.String())
	if len(lines) != len(cacheNames) {
		t.Fatalf("nomi cache printed\n%s\nwant a line for each of %v", out.String(), cacheNames)
	}
	for _, name := range cacheNames {
		want := []string{dirs[name]}
		want = append(want, strings.Fields(hoverdoc.ByteSize(filesSize(t, dirs[name])))...)
		if got := lines[name]; !reflect.DeepEqual(got, want) {
			t.Errorf("nomi cache's line for %s is %v, want %v", name, got, want)
		}
	}
	if after := snapshot(t, parent); !reflect.DeepEqual(after, before) {
		t.Error("nomi cache changed what the caches hold")
	}
}

// `nomi cache clean` removes what every cache holds, read-only package
// trees included, keeps each cache's directory, prints the size it
// removed, and touches nothing outside the caches.
func TestCacheClean_RemovesEveryCacheAndNothingElse(t *testing.T) {
	parent, dirs := testCaches(t)
	sizes := map[string]int64{}
	var all []string
	for _, name := range cacheNames {
		sizes[name] = filesSize(t, dirs[name])
		all = append(all, dirs[name])
	}
	outside := snapshot(t, parent, all...)
	var out bytes.Buffer
	if err := runCache(&out, []string{"clean"}); err != nil {
		t.Fatalf("nomi cache clean: %v", err)
	}
	lines := cacheLines(t, out.String())
	for _, name := range cacheNames {
		want := append([]string{dirs[name], "removed"}, strings.Fields(hoverdoc.ByteSize(sizes[name]))...)
		if got := lines[name]; !reflect.DeepEqual(got, want) {
			t.Errorf("nomi cache clean's line for %s is %v, want %v", name, got, want)
		}
		entries, err := os.ReadDir(dirs[name])
		if err != nil || len(entries) != 0 {
			t.Errorf("after nomi cache clean, %s holds %v (%v), want an empty directory", dirs[name], entries, err)
		}
	}
	if after := snapshot(t, parent, all...); !reflect.DeepEqual(after, outside) {
		t.Errorf("nomi cache clean changed what is outside the caches:\n%v\nwant\n%v", after, outside)
	}
	out.Reset()
	if err := runCache(&out, []string{"clean"}); err != nil {
		t.Fatalf("nomi cache clean of empty caches: %v", err)
	}
	for name, words := range cacheLines(t, out.String()) {
		if want := []string{dirs[name], "empty"}; !reflect.DeepEqual(words, want) {
			t.Errorf("nomi cache clean of an empty %s printed %v, want %v", name, words, want)
		}
	}
}

// `nomi cache clean <name>` removes that cache alone.
func TestCacheClean_RemovesTheCacheNamed(t *testing.T) {
	for _, name := range cacheNames {
		t.Run(name, func(t *testing.T) {
			parent, dirs := testCaches(t)
			rest := snapshot(t, parent, dirs[name])
			size := filesSize(t, dirs[name])
			var out bytes.Buffer
			if err := runCache(&out, []string{"clean", name}); err != nil {
				t.Fatalf("nomi cache clean %s: %v", name, err)
			}
			want := name + "  " + dirs[name] + "  removed " + hoverdoc.ByteSize(size) + "\n"
			if out.String() != want {
				t.Errorf("nomi cache clean %s printed %q, want %q", name, out.String(), want)
			}
			if entries, err := os.ReadDir(dirs[name]); err != nil || len(entries) != 0 {
				t.Errorf("after nomi cache clean %s, it holds %v (%v)", name, entries, err)
			}
			if after := snapshot(t, parent, dirs[name]); !reflect.DeepEqual(after, rest) {
				t.Errorf("nomi cache clean %s changed another cache, or what is outside them", name)
			}
		})
	}
}

// A variable that names a directory holding something its cache does not
// write is refused by clean and by prune, and nothing in any cache is
// removed: the directory may be someone's files.
func TestCacheClean_RefusesADirectoryThatIsNotTheCache(t *testing.T) {
	for _, tc := range []struct{ name, foreign string }{
		{"packages", "example.com/README.md"},
		{"packages", "example.com/greet@v1.0.0+files/x"},
		{"artifacts", "notes.txt"},
		{"artifacts", "std/notes.txt"},
		{"runners", "project"},
		{"std", "option.nomi"},
	} {
		t.Run(tc.name+" "+tc.foreign, func(t *testing.T) {
			parent, dirs := testCaches(t)
			foreign := filepath.Join(dirs[tc.name], filepath.FromSlash(tc.foreign))
			if tc.foreign == "example.com/greet@v1.0.0+files/x" {
				list := filepath.Dir(foreign)
				if err := os.Remove(list); err != nil {
					t.Fatal(err)
				}
			}
			mustWrite(t, foreign, "someone's")
			before := snapshot(t, parent)
			env := map[string]string{"packages": pkgcache.CacheEnv, "artifacts": artifactcache.RootEnv,
				"runners": runners.CacheRootEnv, "std": stdcache.RootEnv}[tc.name]
			named := foreign
			if tc.foreign == "example.com/greet@v1.0.0+files/x" {
				named = filepath.Dir(foreign)
			}
			for _, args := range [][]string{{"clean"}, {"clean", tc.name}, {"prune"}, {"prune", tc.name}} {
				want := "nomi cache " + args[0] + ": " + dirs[tc.name] + " holds " + named + ", which the " + tc.name +
					" cache does not write, so " + env + " may name a directory that is not the cache. Nothing was removed"
				var out bytes.Buffer
				err := runCache(&out, args)
				if err == nil || err.Error() != want {
					t.Errorf("nomi cache %s: %v\nwant %s", strings.Join(args, " "), err, want)
				}
				if out.Len() != 0 {
					t.Errorf("a refused nomi cache %s printed %q", strings.Join(args, " "), out.String())
				}
				if after := snapshot(t, parent); !reflect.DeepEqual(after, before) {
					t.Fatalf("a refused nomi cache %s removed something", strings.Join(args, " "))
				}
			}
		})
	}
}

// With NOMI_ARTIFACT_CACHE=off there is no artifact cache to list or
// clean; a name no cache has, and other words, are errors.
func TestCache_AnArtifactCacheTurnedOffAndWordsItDoesNotTake(t *testing.T) {
	testCaches(t)
	t.Setenv(artifactcache.RootEnv, "off")
	for _, args := range [][]string{nil, {"clean", "artifacts"}} {
		var out bytes.Buffer
		if err := runCache(&out, args); err != nil {
			t.Fatal(err)
		}
		if got := cacheLines(t, out.String())["artifacts"]; !reflect.DeepEqual(got, []string{"off", "(NOMI_ARTIFACT_CACHE=off)"}) {
			t.Errorf("nomi cache %v with the artifact cache off printed\n%s", args, out.String())
		}
	}
	var out bytes.Buffer
	want := `nomi cache clean: no cache is named "programs"; the caches are packages, artifacts, runners and std`
	if err := runCache(&out, []string{"clean", "programs"}); err == nil || err.Error() != want {
		t.Errorf("nomi cache clean programs: %v\nwant %s", err, want)
	}
	for _, args := range [][]string{{"remove"}, {"clean", "std", "runners"}} {
		if err := runCache(&out, args); err == nil || err.Error() != cacheUsage {
			t.Errorf("nomi cache %s: %v, want the usage", strings.Join(args, " "), err)
		}
	}
}

// agedEntries adds to testCaches' caches an entry past each age rule its
// cache prunes by and one short of it, ages each with os.Chtimes, and
// answers, per cache, the entries a prune removes and those it keeps.
// testCaches' own entries are fresh, so a prune keeps them too.
func agedEntries(t *testing.T, parent string, dirs map[string]string) (old, kept map[string][]string) {
	t.Helper()
	t.Setenv("NOMI_RUNNER_CACHE_MAX_ENTRIES", "2")
	day := 24 * time.Hour
	old, kept = map[string][]string{}, map[string][]string{}
	var ages []func()
	// entry ages path, or stamp when it is not "", the file whose time is
	// path's last use.
	entry := func(name, path, stamp string, age time.Duration, removed bool) {
		if removed {
			old[name] = append(old[name], path)
		} else {
			kept[name] = append(kept[name], path)
		}
		if stamp == "" {
			stamp = path
		}
		ages = append(ages, func() {
			when := time.Now().Add(-age)
			if err := os.Chtimes(stamp, when, when); err != nil {
				t.Fatal(err)
			}
		})
	}

	// packages: a repository nothing resolved for two weeks, and an
	// extraction left for an hour.
	host := filepath.Dir(pkgcache.RepoDir(dirs["packages"], "example.com/greet", "v1.0.0"))
	listed, err := os.ReadDir(host)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range listed {
		if strings.Contains(e.Name(), "@") {
			entry("packages", filepath.Join(host, e.Name()), "", artifactcache.Unused+time.Hour, true)
		}
	}
	mustWrite(t, filepath.Join(host, ".tmp-old", "nomi.toml"), "unfinished")
	entry("packages", filepath.Join(host, ".tmp-old"), "", 2*time.Hour, true)
	mustWrite(t, filepath.Join(host, ".tmp-new", "nomi.toml"), "being extracted")
	entry("packages", filepath.Join(host, ".tmp-new"), "", 10*time.Minute, false)

	// artifacts: std's a day, a program's five days, a record of a sibling
	// kind's five days, and a compiled module's two weeks.
	art := dirs["artifacts"]
	files := []struct {
		path, text string
		age        time.Duration
		removed    bool
	}{
		{filepath.Join("std", strings.Repeat("1b", 16)+".bundle"), strings.Repeat("b", 900), day + time.Hour, true},
		{filepath.Join("std", strings.Repeat("1b", 16)+".state"), strings.Repeat("s", 90), day - time.Hour, false},
		{filepath.Join("programs", "cd", strings.Repeat("cd", 32)), strings.Repeat("p", 400), 5*day + time.Hour, true},
		{filepath.Join("programs", "ef", strings.Repeat("ef", 32)), strings.Repeat("p", 40), 5*day - time.Hour, false},
		{filepath.Join("files", "12", strings.Repeat("12", 32)), strings.Repeat("f", 300), 5*day + time.Hour, true},
	}
	for _, f := range files {
		mustWrite(t, filepath.Join(art, f.path), f.text)
		entry("artifacts", filepath.Join(art, f.path), "", f.age, f.removed)
	}
	for _, m := range []struct {
		sum     string
		age     time.Duration
		removed bool
	}{{"34", artifactcache.Unused + time.Hour, true}, {"56", artifactcache.Unused - time.Hour, false}} {
		dir := filepath.Join(art, "wasm", strings.Repeat(m.sum, 32))
		mustWrite(t, filepath.Join(dir, "wazero-1", "module"), strings.Repeat("w", 600))
		entry("artifacts", dir, "", m.age, m.removed)
	}

	// runners: a build whose compiler root is gone, one nothing used for
	// two weeks, the least recently used of those over the bound of two,
	// and a release nothing used for two weeks. A build's last use is the
	// time of the record of its compiler root.
	run := dirs["runners"]
	for _, b := range []struct {
		key, root string
		age       time.Duration
		removed   bool
	}{
		{"aaaaaaaaaaaaaaaa", filepath.Join(parent, "gone"), 2 * time.Hour, true},
		{"bbbbbbbbbbbbbbbb", parent, artifactcache.Unused + time.Hour, true},
		{"cccccccccccccccc", parent, 2 * time.Hour, false},
		{"dddddddddddddddd", parent, 3 * time.Hour, true},
	} {
		dir := filepath.Join(run, b.key)
		record := filepath.Join(dir, "origin.json")
		mustWrite(t, filepath.Join(dir, "nomi-vm"), strings.Repeat("v", 200))
		mustWrite(t, record, `{"compiler_root":`+strconv.Quote(b.root)+`,"kind":"vm"}`)
		entry("runners", dir, record, b.age, b.removed)
	}
	release := filepath.Join(run, "nomi-vm", "v0.0.0")
	mustWrite(t, filepath.Join(release, "darwin_arm64", "nomi-vm"), strings.Repeat("r", 800))
	entry("runners", release, "", artifactcache.Unused+time.Hour, true)

	// std: a version directory nothing used for two weeks, read-only as
	// every one is, and a temporary one left for an hour.
	version := filepath.Join(dirs["std"], "fedcba9876543210")
	mustWrite(t, filepath.Join(version, "option.nomi"), "pub enum Option<T>\n    Some(T)\n    None\nend\n")
	if err := os.Chmod(filepath.Join(version, "option.nomi"), 0o444); err != nil {
		t.Fatal(err)
	}
	entry("std", version, "", artifactcache.Unused+time.Hour, true)
	half := filepath.Join(dirs["std"], ".tmp-fedcba9876543210-1")
	mustWrite(t, filepath.Join(half, "option.nomi"), "half")
	entry("std", half, "", 2*time.Hour, true)

	for _, age := range ages {
		age()
	}
	return old, kept
}

// pruneLine is the words `nomi cache prune` prints after a cache's
// directory when it removed entries.
func pruneLine(t *testing.T, entries []string) []string {
	t.Helper()
	var size int64
	for _, e := range entries {
		size += filesSize(t, e)
	}
	switch len(entries) {
	case 0:
		return []string{"nothing", "old", "enough"}
	case 1:
		return append([]string{"removed", "1", "entry,"}, strings.Fields(hoverdoc.ByteSize(size))...)
	}
	return append([]string{"removed", strconv.Itoa(len(entries)), "entries,"}, strings.Fields(hoverdoc.ByteSize(size))...)
}

// `nomi cache prune` removes from every cache what its own prune removes
// by age, whatever its daily marker says, keeps the rest, prints the
// count and size it removed, touches nothing outside the caches, and
// finds nothing old enough when run again.
func TestCachePrune_RemovesWhatEachCachesAgesRemove(t *testing.T) {
	parent, dirs := testCaches(t)
	old, kept := agedEntries(t, parent, dirs)
	var all []string
	want := map[string][]string{}
	for _, name := range cacheNames {
		all = append(all, dirs[name])
		want[name] = append([]string{dirs[name]}, pruneLine(t, old[name])...)
	}
	outside := snapshot(t, parent, all...)
	var out bytes.Buffer
	if err := runCache(&out, []string{"prune"}); err != nil {
		t.Fatalf("nomi cache prune: %v", err)
	}
	lines := cacheLines(t, out.String())
	removed, notOld := 0, 0
	for _, name := range cacheNames {
		notOld += len(kept[name])
		if got := lines[name]; !reflect.DeepEqual(got, want[name]) {
			t.Errorf("nomi cache prune's line for %s is %v, want %v", name, got, want[name])
		}
		for _, p := range old[name] {
			if _, err := os.Lstat(p); !os.IsNotExist(err) {
				t.Errorf("nomi cache prune left %s (%v)", p, err)
			}
			removed++
		}
		for _, p := range kept[name] {
			if _, err := os.Lstat(p); err != nil {
				t.Errorf("nomi cache prune removed %s, which is not old enough: %v", p, err)
			}
		}
	}
	for _, p := range []string{
		filepath.Join(dirs["artifacts"], "std", strings.Repeat("0a", 16)+".iface"),
		filepath.Join(dirs["artifacts"], "programs", "ab", strings.Repeat("ab", 32)),
		filepath.Join(dirs["artifacts"], "wasm", strings.Repeat("cd", 32)),
		filepath.Join(dirs["runners"], "0123456789abcdef"),
		filepath.Join(dirs["runners"], "nomi-vm", "v0.0.1"),
		filepath.Join(dirs["std"], "0123456789abcdef"),
		pkgcache.RepoDir(dirs["packages"], "example.com/group/mono", "v0.1.0"),
	} {
		if _, err := os.Lstat(p); err != nil {
			t.Errorf("nomi cache prune removed %s, which was just written: %v", p, err)
		}
	}
	if after := snapshot(t, parent, all...); !reflect.DeepEqual(after, outside) {
		t.Errorf("nomi cache prune changed what is outside the caches:\n%v\nwant\n%v", after, outside)
	}
	t.Logf("%d old entries removed, %d short of their age kept", removed, notOld)
	out.Reset()
	if err := runCache(&out, []string{"prune"}); err != nil {
		t.Fatalf("a second nomi cache prune: %v", err)
	}
	for name, words := range cacheLines(t, out.String()) {
		if want := []string{dirs[name], "nothing", "old", "enough"}; !reflect.DeepEqual(words, want) {
			t.Errorf("a second nomi cache prune printed %v for %s, want %v", words, name, want)
		}
	}
}

// `nomi cache prune packages` prunes that cache alone.
func TestCachePrune_PrunesTheCacheNamed(t *testing.T) {
	parent, dirs := testCaches(t)
	old, _ := agedEntries(t, parent, dirs)
	want := dirs["packages"] + "  " + strings.Join(pruneLine(t, old["packages"]), " ") + "\n"
	rest := snapshot(t, parent, dirs["packages"])
	var out bytes.Buffer
	if err := runCache(&out, []string{"prune", "packages"}); err != nil {
		t.Fatalf("nomi cache prune packages: %v", err)
	}
	if out.String() != "packages  "+want {
		t.Errorf("nomi cache prune packages printed %q, want %q", out.String(), "packages  "+want)
	}
	for _, p := range old["packages"] {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Errorf("nomi cache prune packages left %s (%v)", p, err)
		}
	}
	if after := snapshot(t, parent, dirs["packages"]); !reflect.DeepEqual(after, rest) {
		t.Error("nomi cache prune packages changed another cache, or what is outside them")
	}
}

// `nomi cache prune` marks each daily prune it ran done, so the next
// process that would prune that directory finds the prune not due.
func TestCachePrune_MarksTheDailyPrunesDone(t *testing.T) {
	_, dirs := testCaches(t)
	marked := []string{
		dirs["packages"],
		filepath.Join(dirs["artifacts"], "programs"),
		filepath.Join(dirs["artifacts"], "wasm"),
		dirs["runners"],
		filepath.Join(dirs["runners"], "nomi-vm"),
	}
	stale := time.Now().Add(-48 * time.Hour)
	for _, dir := range marked {
		marker := filepath.Join(dir, ".pruned")
		if _, err := os.Stat(marker); err == nil {
			if err := os.Chtimes(marker, stale, stale); err != nil {
				t.Fatal(err)
			}
		}
	}
	start := time.Now().Add(-time.Minute)
	if err := runCache(io.Discard, []string{"prune"}); err != nil {
		t.Fatalf("nomi cache prune: %v", err)
	}
	for _, dir := range marked {
		info, err := os.Stat(filepath.Join(dir, ".pruned"))
		if err != nil || info.ModTime().Before(start) {
			t.Errorf("after nomi cache prune, %s's marker is %v (%v), want one written now", dir, info, err)
			continue
		}
		if artifactcache.Due(dir, 24*time.Hour, false) {
			t.Errorf("after nomi cache prune, a daily prune of %s is due", dir)
		}
	}
}
