package progcache

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/nomi-language/nomi/internal/artifactcache"
)

// project writes a small project under a new directory: an entry, a
// sibling, a manifest and a file no build reads, and answers its root.
func project(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "proj")
	write(t, filepath.Join(root, "nomi.toml"), "[module]\nname = \"proj\"\n")
	write(t, filepath.Join(root, "main.nomi"), "fn main() end\n")
	write(t, filepath.Join(root, "lib", "util.nomi"), "pub fn one() -> Int\n    1\nend\n")
	write(t, filepath.Join(root, "notes.txt"), "not read\n")
	return root
}

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// useCache points the artifact cache at a directory of the test's own.
func useCache(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv(artifactcache.RootEnv, dir)
	if artifactcache.CompilerID() == "" {
		t.Skip("this test binary has no build ID, so nothing is cached")
	}
	return dir
}

// keep opens root's program and stores payload for it, and requires the
// store to have been written.
func keep(t *testing.T, root string, payload string) {
	t.Helper()
	_, before := Counts()
	e := Open("load", filepath.Join(root, "main.nomi"), []Tree{{Dir: root}})
	if e == nil {
		t.Fatal("the cache is off")
	}
	e.Store([]Tree{{Dir: root}}, []string{filepath.Join(root, "main.nomi")}, []byte(payload))
	if _, after := Counts(); after != before+1 {
		t.Fatal("the record was not written")
	}
}

// lookup answers root's kept payload, or "" for a miss.
func lookup(t *testing.T, root string) string {
	t.Helper()
	e := Open("load", filepath.Join(root, "main.nomi"), []Tree{{Dir: root}})
	return string(e.Payload())
}

func TestARecordIsReadUntilAFileItReadChanges(t *testing.T) {
	useCache(t)
	root := project(t)
	if got := lookup(t, root); got != "" {
		t.Fatalf("an empty cache answered %q", got)
	}
	keep(t, root, "image")
	if got := lookup(t, root); got != "image" {
		t.Fatalf("the kept payload reads back as %q", got)
	}
	// A file no build of a program reads changes nothing.
	write(t, filepath.Join(root, "notes.txt"), "changed\n")
	write(t, filepath.Join(root, "out", "run.log"), "written by the program\n")
	if got := lookup(t, root); got != "image" {
		t.Fatalf("after a file no build reads changed, the payload reads as %q", got)
	}
	// Each change to a file a build reads is a miss.
	for _, change := range []struct {
		name string
		do   func()
	}{
		{"a source edited", func() { write(t, filepath.Join(root, "lib", "util.nomi"), "pub fn one() -> Int\n    2\nend\n") }},
		{"a source added", func() { write(t, filepath.Join(root, "lib", "more.nomi"), "pub fn two() -> Int\n    2\nend\n") }},
		{"a source removed", func() { os.Remove(filepath.Join(root, "lib", "more.nomi")) }},
		{"the manifest edited", func() { write(t, filepath.Join(root, "nomi.toml"), "[module]\nname = \"other\"\n") }},
		{"a catalog added", func() { write(t, filepath.Join(root, "messages", "fr", "main.toml"), "") }},
		{"a go.mod added", func() { write(t, filepath.Join(root, "go.mod"), "module x\n") }},
		{"a manifest above the root", func() { write(t, filepath.Join(filepath.Dir(root), "nomi.toml"), "") }},
		{"the manifest above the root removed", func() { os.Remove(filepath.Join(filepath.Dir(root), "nomi.toml")) }},
	} {
		change.do()
		if got := lookup(t, root); got != "" {
			t.Fatalf("after %s, the stale payload %q was read", change.name, got)
		}
		keep(t, root, "image")
	}
}

// TestAnUnchangedFileWithANewStatIsStillAHit holds the hash fallback: a
// file written again with the same text, or given a new modification time,
// is the same input.
func TestAnUnchangedFileWithANewStatIsStillAHit(t *testing.T) {
	useCache(t)
	root := project(t)
	keep(t, root, "image")
	path := filepath.Join(root, "lib", "util.nomi")
	write(t, path, "pub fn one() -> Int\n    1\nend\n")
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
	if got := lookup(t, root); got != "image" {
		t.Fatalf("a file rewritten with its own text reads as a change (%q)", got)
	}
}

// TestASameSizedEditWithItsTimeRestoredIsAMiss is the racy case: an edit
// that keeps the file's size and modification time must still miss.
func TestASameSizedEditWithItsTimeRestoredIsAMiss(t *testing.T) {
	useCache(t)
	root := project(t)
	path := filepath.Join(root, "lib", "util.nomi")
	keep(t, root, "image")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	write(t, path, "pub fn one() -> Int\n    3\nend\n")
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if got := lookup(t, root); got != "" {
		t.Fatalf("a same-sized edit with its time put back read the stale payload %q", got)
	}
}

// TestAFileChangedWhileBuildingIsNotKept: a record names only what the
// build read, so a source changed between the snapshot and the store keeps
// nothing.
func TestAFileChangedWhileBuildingIsNotKept(t *testing.T) {
	useCache(t)
	root := project(t)
	e := Open("load", filepath.Join(root, "main.nomi"), []Tree{{Dir: root}})
	write(t, filepath.Join(root, "lib", "util.nomi"), "pub fn one() -> Int\n    4\nend\n")
	_, before := Counts()
	e.Store([]Tree{{Dir: root}}, nil, []byte("image"))
	if _, after := Counts(); after != before {
		t.Fatal("a build whose input changed while it ran was kept")
	}
	if got := lookup(t, root); got != "" {
		t.Fatalf("read %q", got)
	}
}

// TestADamagedRecordIsAMiss truncates, empties and changes a record, and
// puts another program's in its place.
func TestADamagedRecordIsAMiss(t *testing.T) {
	cache := useCache(t)
	root := project(t)
	other := project(t)
	keep(t, root, "image")
	keep(t, other, "other image")
	var records []string
	filepath.WalkDir(filepath.Join(cache, dirName), func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && filepath.Base(p)[0] != '.' {
			records = append(records, p)
		}
		return nil
	})
	if len(records) != 2 {
		t.Fatalf("%d records for two programs", len(records))
	}
	mine := records[0]
	if data, _ := os.ReadFile(mine); !bytes.Contains(data, []byte("proj")) || !bytes.HasSuffix(data, []byte("image")) || bytes.HasSuffix(data, []byte("other image")) {
		mine = records[1]
	}
	theirs := records[0]
	if theirs == mine {
		theirs = records[1]
	}
	good, err := os.ReadFile(mine)
	if err != nil {
		t.Fatal(err)
	}
	otherData, err := os.ReadFile(theirs)
	if err != nil {
		t.Fatal(err)
	}
	flipped := bytes.Clone(good)
	flipped[len(flipped)/2] ^= 1
	for name, data := range map[string][]byte{
		"truncated": good[:len(good)-3],
		"emptied":   nil,
		"changed":   flipped,
		"swapped":   otherData,
	} {
		if err := os.WriteFile(mine, data, 0o644); err != nil {
			t.Fatal(err)
		}
		if got := lookup(t, root); got != "" {
			t.Fatalf("a %s record answered %q", name, got)
		}
	}
	if err := os.WriteFile(mine, good, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := lookup(t, root); got != "image" {
		t.Fatalf("the restored record answered %q", got)
	}
}

// TestTheEnvironmentIsInTheKey: a NOMI_ variable a build may read keys
// the record, and the cache's own report does not.
func TestTheEnvironmentIsInTheKey(t *testing.T) {
	useCache(t)
	root := project(t)
	keep(t, root, "image")
	t.Setenv(DebugEnv, "1")
	if got := lookup(t, root); got != "image" {
		t.Fatalf("turning on the cache's report changed its key (%q)", got)
	}
	t.Setenv("NOMI_SOMETHING", "1")
	if got := lookup(t, root); got != "" {
		t.Fatalf("a NOMI_ variable is not in the key (%q)", got)
	}
}

// TestAnEmbeddedTreeTracksEveryFile: a tree with All set tracks files of
// any name, so a new asset under it is a change.
func TestAnEmbeddedTreeTracksEveryFile(t *testing.T) {
	useCache(t)
	root := project(t)
	assets := filepath.Join(root, "assets")
	write(t, filepath.Join(assets, "logo.svg"), "<svg/>")
	trees := []Tree{{Dir: root}, {Dir: assets, All: true}}
	e := Open("load", filepath.Join(root, "main.nomi"), trees)
	e.Store(trees, nil, []byte("image"))
	if got := lookup(t, root); got != "image" {
		t.Fatalf("read %q", got)
	}
	write(t, filepath.Join(assets, "icon.png"), "png")
	if got := lookup(t, root); got != "" {
		t.Fatalf("a new embedded asset read the stale payload %q", got)
	}
}

// TestSingleFilesOutsideTheTreesAreTracked: a file the build read outside
// every tree is tracked on its own.
func TestSingleFilesOutsideTheTreesAreTracked(t *testing.T) {
	useCache(t)
	// The file is written just now, which a store does not trust for a file
	// its snapshot did not hold (racyWindow).
	defer func(w time.Duration) { racyWindow = w }(racyWindow)
	racyWindow = 0
	root := project(t)
	outside := filepath.Join(t.TempDir(), "shared.nomi")
	write(t, outside, "pub fn x() -> Int\n    1\nend\n")
	e := Open("load", filepath.Join(root, "main.nomi"), []Tree{{Dir: root}})
	e.Store([]Tree{{Dir: root}}, []string{outside}, []byte("image"))
	if got := lookup(t, root); got != "image" {
		t.Fatalf("read %q", got)
	}
	write(t, outside, "pub fn x() -> Int\n    2\nend\n")
	if got := lookup(t, root); got != "" {
		t.Fatalf("an edited file outside the trees read the stale payload %q", got)
	}
}

// TestConcurrentStoresLeaveOneRecord races writers on one key: every
// lookup after them reads a whole record.
func TestConcurrentStoresLeaveOneRecord(t *testing.T) {
	useCache(t)
	root := project(t)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e := Open("load", filepath.Join(root, "main.nomi"), []Tree{{Dir: root}})
			e.Store([]Tree{{Dir: root}}, nil, []byte("image"))
		}()
	}
	wg.Wait()
	if got := lookup(t, root); got != "image" {
		t.Fatalf("after racing stores, read %q", got)
	}
}

// TestPruneRemovesRecordsNothingRead ages a record past unused and requires
// the next write to remove it.
func TestPruneRemovesRecordsNothingRead(t *testing.T) {
	cache := useCache(t)
	root := project(t)
	keep(t, root, "image")
	dir := filepath.Join(cache, dirName)
	old := time.Now().Add(-unused - time.Hour)
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			os.Chtimes(p, old, old)
		}
		return nil
	})
	keep(t, project(t), "image")
	if got := lookup(t, root); got != "" {
		t.Fatalf("a record unused for longer than %v was kept and read (%q)", unused, got)
	}
}

// TestChangedNamesTheFilesWhoseTextChanged holds Changed, Previous and
// Sibling: an edit of a file the record tracks is named with the record's
// payload still at hand, a file added or removed is not an edit Changed
// answers, and a sibling record is read from the same snapshot.
func TestChangedNamesTheFilesWhoseTextChanged(t *testing.T) {
	useCache(t)
	root := project(t)
	main := filepath.Join(root, "main.nomi")
	e := Open("load", main, []Tree{{Dir: root}})
	e.Store([]Tree{{Dir: root}}, []string{main}, []byte("image"))
	e.Sibling("files").Store([]Tree{{Dir: root}}, []string{main}, []byte("files"))

	write(t, filepath.Join(root, "lib", "util.nomi"), "pub fn one() -> Int\n    2\nend\n")
	e = Open("load", main, []Tree{{Dir: root}})
	if e.Payload() != nil {
		t.Fatal("a record answered after a file it read changed")
	}
	if string(e.Previous()) != "image" {
		t.Fatalf("the previous record holds %q", e.Previous())
	}
	changed, ok := e.Changed()
	if !ok || len(changed) != 1 || changed[0] != filepath.Join(root, "lib", "util.nomi") {
		t.Fatalf("Changed answers %v, %v", changed, ok)
	}
	s := e.Sibling("files")
	if s.Payload() != nil || string(s.Previous()) != "files" {
		t.Fatalf("the sibling answers %q, previous %q", s.Payload(), s.Previous())
	}
	if c, ok := s.Changed(); !ok || len(c) != 1 {
		t.Fatalf("the sibling's Changed answers %v, %v", c, ok)
	}
	if trees, files := s.Inputs(); len(trees) != 1 || len(files) != 0 {
		t.Fatalf("the sibling's inputs are %v and %v", trees, files)
	}

	write(t, filepath.Join(root, "lib", "more.nomi"), "pub fn two() -> Int\n    2\nend\n")
	e = Open("load", main, []Tree{{Dir: root}})
	if _, ok := e.Changed(); ok {
		t.Fatal("Changed answered for a file added")
	}
}

// TestChangedSourcesNamesSourcesAddedAndRemoved holds ChangedSources to
// answering a `.nomi` file added to or removed from a tree walked for
// sources, beside the files whose text changed, and to refusing any other
// file that came or went, and a source that came to a tree walked for
// every file (an embed's).
func TestChangedSourcesNamesSourcesAddedAndRemoved(t *testing.T) {
	useCache(t)
	root := project(t)
	main := filepath.Join(root, "main.nomi")
	assets := filepath.Join(root, "assets")
	if err := os.MkdirAll(assets, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(assets, "a.txt"), "a")
	trees := []Tree{{Dir: root}, {Dir: assets, All: true}}
	e := Open("load", main, trees)
	e.Store(trees, []string{main}, []byte("image"))

	util := filepath.Join(root, "lib", "util.nomi")
	more := filepath.Join(root, "lib", "more.nomi")
	write(t, util, "pub fn one() -> Int\n    2\nend\n")
	write(t, more, "pub fn two() -> Int\n    2\nend\n")
	e = Open("load", main, trees)
	changed, added, removed, ok := e.ChangedSources()
	if !ok || !slices.Equal(changed, []string{util}) || !slices.Equal(added, []string{more}) || len(removed) != 0 {
		t.Fatalf("after an edit and a file added, ChangedSources answers %v, %v, %v, %v", changed, added, removed, ok)
	}
	e.Store(trees, []string{main}, []byte("image"))

	if err := os.Remove(more); err != nil {
		t.Fatal(err)
	}
	e = Open("load", main, trees)
	changed, added, removed, ok = e.ChangedSources()
	if !ok || len(changed) != 0 || len(added) != 0 || !slices.Equal(removed, []string{more}) {
		t.Fatalf("after a file removed, ChangedSources answers %v, %v, %v, %v", changed, added, removed, ok)
	}
	e.Store(trees, []string{main}, []byte("image"))

	for name, path := range map[string]string{"a manifest added": filepath.Join(root, "lib", "nomi.toml"),
		"a source added to an embed's directory": filepath.Join(assets, "b.nomi")} {
		write(t, path, "x")
		e = Open("load", main, trees)
		if _, _, _, ok := e.ChangedSources(); ok {
			t.Fatalf("ChangedSources answered after %s", name)
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
}
