package progcache

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/nomi-language/nomi/internal/artifactcache"
)

// DebugEnv, set to 1, reports on stderr each lookup's answer and each
// record written or not written, and why.
const DebugEnv = "NOMI_DEBUG_ARTIFACT_CACHE"

const (
	// dirName is the programs' directory under the artifact cache.
	dirName = "programs"
	// recordMagic and recordVersion head every record. The version moves
	// whenever the record's layout does; the payload's own format is the
	// caller's, inside the compiler identity the key holds.
	recordMagic   = "NOMIPRG\x00"
	recordVersion = 1
	// unused is how long a record nothing reads is kept, and pruneEvery how
	// often a process that writes looks for such records.
	unused     = 5 * 24 * time.Hour
	pruneEvery = time.Hour
	// maxEntries and maxVisited bound a tree's walk, the files it tracks
	// and the names it reads: a program in a directory that holds far more
	// than a project (a home directory) is not kept, and costs a lookup no
	// more than this walk.
	maxEntries = 20000
	maxVisited = 50000
)

// racyWindow is how close to a snapshot a file's change may be for its stat
// to be trusted: a file changed within it is hashed when the record is
// checked, since a second change in the same timestamp tick could leave its
// stat as it was (the "racily clean" entries of git's index). A file the
// snapshot did not hold is kept only once it is older than this, as `go
// test` caches a result only over files older than two seconds. A variable
// for tests.
var racyWindow = 2 * time.Second

// Tree is a directory a build read files under. A tree with All unset
// tracks the files a build of a program can read in a project: `.nomi`
// sources, `.toml` manifests and catalogs, and `go.mod` and `go.sum`. One
// with All set tracks every file, for an `embed` that reads a directory's
// contents. Hidden names and node_modules are skipped in both.
type Tree struct {
	Dir string
	All bool
}

// entry is one tracked file: its path (from its tree's directory, slash
// separated, or absolute for a single file), whether it exists, and its stat
// and hash when it does.
type entry struct {
	path   string
	exists bool
	size   int64
	mtime  int64
	ctime  int64
	ino    uint64
	sum    [32]byte
	hashed bool
}

// state is a tree as one walk saw it: its entries sorted by path, and the
// markers above its directory that decide the program's root.
type state struct {
	tree    Tree
	entries []entry
	probes  []entry
	tooBig  bool
}

// record is a decoded record.
type record struct {
	key     string
	snap    int64
	trees   []*state
	files   []entry
	payload []byte
}

// Entry is one program's place in the cache, opened before its build.
type Entry struct {
	id, entryFile string
	path          string
	key           string
	label         string
	snap          int64
	states        map[Tree]*state
	files         map[string]entry
	prev          *record
	payload       []byte
	debug         bool
}

// Open looks up what kind was built from the program whose entry file is
// entry (an absolute path), and snapshots the trees a build of it is
// expected to read: predicted, and those its last record names. It answers
// nil when the cache is off.
func Open(kind, entryFile string, predicted []Tree) *Entry {
	root, id := artifactcache.ProgramRoot(), artifactcache.CompilerID()
	if root == "" || id == "" {
		return nil
	}
	key := keyOf(id, kind, entryFile)
	e := &Entry{
		id:        id,
		entryFile: entryFile,
		path:      filepath.Join(root, dirName, key[:2], key),
		key:       key,
		label:     entryFile,
		snap:      time.Now().UnixNano(),
		states:    map[Tree]*state{},
		files:     map[string]entry{},
		debug:     os.Getenv(DebugEnv) == "1",
	}
	rec, why := readRecord(e.path, key)
	e.prev = rec
	trees := slices.Clone(predicted)
	if rec != nil {
		for _, st := range rec.trees {
			if !slices.Contains(trees, st.tree) {
				trees = append(trees, st.tree)
			}
		}
	}
	prevStates := map[Tree]*state{}
	var prevFiles map[string]entry
	var prevSnap int64
	if rec != nil {
		for _, st := range rec.trees {
			prevStates[st.tree] = st
		}
		prevFiles = map[string]entry{}
		for _, f := range rec.files {
			prevFiles[f.path] = f
		}
		prevSnap = rec.snap
	}
	for _, t := range trees {
		e.states[t] = scan(t, prevStates[t], prevSnap, true)
	}
	if rec != nil {
		for _, f := range rec.files {
			e.files[f.path] = statFile(f.path, &f, prevSnap, true)
		}
	}
	if rec == nil {
		e.logf("miss: %s", why)
		return e
	}
	if diff := e.differs(rec); diff != "" {
		e.logf("miss: %s", diff)
		return e
	}
	e.payload = rec.payload
	hits.Add(1)
	if info, err := os.Stat(e.path); err == nil {
		artifactcache.Touch(e.path, info)
	}
	e.logf("hit")
	return e
}

// hits and stores count this process's lookups that found a record and
// its writes (Counts).
var hits, stores atomic.Int64

// Counts is how many lookups in this process found their program's record,
// and how many records it wrote: what a test of the cache reads to know
// which path a command took.
func Counts() (hit, stored int64) { return hits.Load(), stores.Load() }

// Payload is what the record holds when the lookup found the program's
// files unchanged, or nil.
func (e *Entry) Payload() []byte {
	if e == nil {
		return nil
	}
	return e.payload
}

// Sibling looks up what kind was built from e's program, from e's snapshot:
// the trees e walked are not walked again, and any other tree or file the
// sibling's record names is walked or read now. Its records are under a
// directory of the artifact cache named kind, beside the programs'. It
// answers nil for a nil e.
func (e *Entry) Sibling(kind string) *Entry {
	if e == nil {
		return nil
	}
	key := keyOf(e.id, kind, e.entryFile)
	root := filepath.Dir(filepath.Dir(filepath.Dir(e.path)))
	s := &Entry{
		id: e.id, entryFile: e.entryFile,
		path:   filepath.Join(root, kind, key[:2], key),
		key:    key,
		label:  e.label + " (" + kind + ")",
		snap:   e.snap,
		states: map[Tree]*state{},
		files:  map[string]entry{},
		debug:  e.debug,
	}
	for t, st := range e.states {
		s.states[t] = st
	}
	for p, f := range e.files {
		s.files[p] = f
	}
	rec, why := readRecord(s.path, key)
	s.prev = rec
	if rec == nil {
		s.logf("none: %s", why)
		return s
	}
	for _, st := range rec.trees {
		if s.states[st.tree] == nil {
			s.states[st.tree] = scan(st.tree, st, rec.snap, true)
		}
	}
	for _, f := range rec.files {
		if _, ok := s.files[f.path]; !ok {
			s.files[f.path] = statFile(f.path, &f, rec.snap, true)
		}
	}
	if diff := s.differs(rec); diff != "" {
		s.logf("found, %s", diff)
		return s
	}
	s.payload = rec.payload
	if info, err := os.Stat(s.path); err == nil {
		artifactcache.Touch(s.path, info)
	}
	s.logf("found, unchanged")
	return s
}

// Logf reports on stderr, under DebugEnv, what a caller made of e's
// record.
func (e *Entry) Logf(format string, args ...any) {
	if e != nil {
		e.logf(format, args...)
	}
}

// Inputs are the trees and single files the record names, when there is
// one: what a build that reused the record's program read.
func (e *Entry) Inputs() ([]Tree, []string) {
	if e == nil || e.prev == nil {
		return nil, nil
	}
	var trees []Tree
	for _, st := range e.prev.trees {
		trees = append(trees, st.tree)
	}
	var files []string
	for _, f := range e.prev.files {
		files = append(files, f.path)
	}
	return trees, files
}

// Previous is what the record held when the lookup found one, whether or
// not the program's files are as they were then: what a build of the
// changed program reuses of the unchanged files (vmhost's file records).
func (e *Entry) Previous() []byte {
	if e == nil || e.prev == nil {
		return nil
	}
	return e.prev.payload
}

// Changed answers the files, by absolute path, whose contents differ from
// the record's, when that is all that differs: no file was added or
// removed, and no tree became too large to track. ok is false otherwise,
// and when there is no record.
func (e *Entry) Changed() (paths []string, ok bool) {
	if e == nil || e.prev == nil {
		return nil, false
	}
	for _, was := range e.prev.trees {
		now := e.states[was.tree]
		if now == nil || now.tooBig {
			return nil, false
		}
		changed, same := changedEntries(was.entries, now.entries, was.tree.Dir)
		if !same {
			return nil, false
		}
		paths = append(paths, changed...)
		if _, same := changedEntries(was.probes, now.probes, ""); !same {
			return nil, false
		}
	}
	for _, was := range e.prev.files {
		now := e.files[was.path]
		if now.exists != was.exists {
			return nil, false
		}
		if was.exists && now.sum != was.sum {
			paths = append(paths, was.path)
		}
	}
	sort.Strings(paths)
	return paths, true
}

// ChangedSources is Changed that also answers the `.nomi` files added to
// and removed from a tree the record walked for sources (not one walked for
// every file, an `embed`'s directory), by absolute path: what a build that
// relinks after files came and went reads (vmhost's file records). Any
// other difference makes ok false.
func (e *Entry) ChangedSources() (changed, added, removed []string, ok bool) {
	if e == nil || e.prev == nil {
		return nil, nil, nil, false
	}
	for _, was := range e.prev.trees {
		now := e.states[was.tree]
		if now == nil || now.tooBig {
			return nil, nil, nil, false
		}
		c, a, r, same := changedSourceEntries(was.entries, now.entries, was.tree)
		if !same {
			return nil, nil, nil, false
		}
		changed, added, removed = append(changed, c...), append(added, a...), append(removed, r...)
		if _, same := changedEntries(was.probes, now.probes, ""); !same {
			return nil, nil, nil, false
		}
	}
	for _, was := range e.prev.files {
		now := e.files[was.path]
		if now.exists != was.exists {
			return nil, nil, nil, false
		}
		if was.exists && now.sum != was.sum {
			changed = append(changed, was.path)
		}
	}
	sort.Strings(changed)
	sort.Strings(added)
	sort.Strings(removed)
	return changed, added, removed, true
}

// changedSourceEntries is changedEntries over a tree's two walks that
// allows `.nomi` files to come and go in a tree walked for sources,
// answering them apart.
func changedSourceEntries(was, now []entry, t Tree) (changed, added, removed []string, same bool) {
	abs := func(p string) string { return filepath.Join(t.Dir, filepath.FromSlash(p)) }
	source := func(en entry) bool { return !t.All && en.exists && strings.HasSuffix(en.path, ".nomi") }
	i, j := 0, 0
	for i < len(was) || j < len(now) {
		switch {
		case j == len(now) || i < len(was) && was[i].path < now[j].path:
			if !source(was[i]) {
				return nil, nil, nil, false
			}
			removed = append(removed, abs(was[i].path))
			i++
		case i == len(was) || now[j].path < was[i].path:
			if !source(now[j]) {
				return nil, nil, nil, false
			}
			added = append(added, abs(now[j].path))
			j++
		default:
			a, b := was[i], now[j]
			if a.exists != b.exists {
				return nil, nil, nil, false
			}
			if a.exists && a.sum != b.sum {
				changed = append(changed, abs(a.path))
			}
			i++
			j++
		}
	}
	return changed, added, removed, true
}

// changedEntries answers the entries of two walks of one tree whose
// contents differ, when the walks hold the same files.
func changedEntries(was, now []entry, dir string) (changed []string, same bool) {
	if len(was) != len(now) {
		return nil, false
	}
	for i := range was {
		a, b := was[i], now[i]
		if a.path != b.path || a.exists != b.exists {
			return nil, false
		}
		if a.exists && a.sum != b.sum {
			path := a.path
			if dir != "" {
				path = filepath.Join(dir, filepath.FromSlash(a.path))
			}
			changed = append(changed, path)
		}
	}
	return changed, true
}

// Store records payload as what was built from the program, whose files
// are under trees and in files. It writes nothing when a snapshotted file
// changed while the build ran, or a file it had not snapshotted changed
// too recently to be sure the build read what is there now.
func (e *Entry) Store(trees []Tree, files []string, payload []byte) {
	if e == nil {
		return
	}
	rec := &record{key: e.key, snap: e.snap, payload: payload}
	seenTree := map[Tree]bool{}
	for _, t := range trees {
		if seenTree[t] {
			continue
		}
		seenTree[t] = true
		before, ok := e.states[t]
		if !ok {
			st := scan(t, nil, 0, true)
			if why := recent(st.entries, e.snap); why != "" {
				e.logf("not stored: %s changed while it was built", filepath.Join(t.Dir, why))
				return
			}
			if why := recent(st.probes, e.snap); why != "" {
				e.logf("not stored: %s changed while it was built", why)
				return
			}
			before = st
		} else {
			now := scan(t, before, e.snap, false)
			if diff := statDiff(before, now); diff != "" {
				e.logf("not stored: %s changed while it was built", diff)
				return
			}
		}
		if before.tooBig {
			e.logf("not stored: %s holds more than %d files", t.Dir, maxEntries)
			return
		}
		rec.trees = append(rec.trees, before)
	}
	sort.Slice(rec.trees, func(i, j int) bool {
		a, b := rec.trees[i].tree, rec.trees[j].tree
		if a.Dir != b.Dir {
			return a.Dir < b.Dir
		}
		return !a.All && b.All
	})
	seenFile := map[string]bool{}
	for _, f := range files {
		if seenFile[f] || covered(rec.trees, f) {
			continue
		}
		seenFile[f] = true
		before, ok := e.files[f]
		if !ok {
			before = statFile(f, nil, 0, true)
			if why := recent([]entry{before}, e.snap); why != "" {
				e.logf("not stored: %s changed while it was built", why)
				return
			}
		} else if now := statFile(f, &before, e.snap, false); !sameStat(before, now) {
			e.logf("not stored: %s changed while it was built", f)
			return
		}
		rec.files = append(rec.files, before)
	}
	sort.Slice(rec.files, func(i, j int) bool { return rec.files[i].path < rec.files[j].path })
	if err := artifactcache.WriteFile(e.path, encodeRecord(rec)); err != nil {
		e.logf("not stored: %v", err)
		return
	}
	stores.Add(1)
	e.logf("stored")
	prune(filepath.Dir(filepath.Dir(e.path)))
}

func (e *Entry) logf(format string, args ...any) {
	if e.debug {
		fmt.Fprintf(os.Stderr, "artifact cache: %s: %s\n", e.label, fmt.Sprintf(format, args...))
	}
}

// differs is why the snapshot is not what rec recorded, or "".
func (e *Entry) differs(rec *record) string {
	for _, was := range rec.trees {
		now := e.states[was.tree]
		if now.tooBig {
			return was.tree.Dir + " holds too many files"
		}
		if d := entriesDiffer(was.entries, now.entries, was.tree.Dir); d != "" {
			return d
		}
		if d := entriesDiffer(was.probes, now.probes, ""); d != "" {
			return d
		}
	}
	for _, was := range rec.files {
		now := e.files[was.path]
		if now.exists != was.exists || (was.exists && now.sum != was.sum) {
			return was.path + " changed"
		}
	}
	return ""
}

// entriesDiffer is why two walks of one tree differ, or "".
func entriesDiffer(was, now []entry, dir string) string {
	name := func(p string) string {
		if dir == "" {
			return p
		}
		return filepath.Join(dir, filepath.FromSlash(p))
	}
	i, j := 0, 0
	for i < len(was) || j < len(now) {
		switch {
		case j == len(now) || (i < len(was) && was[i].path < now[j].path):
			return name(was[i].path) + " was removed"
		case i == len(was) || now[j].path < was[i].path:
			return name(now[j].path) + " was added"
		}
		a, b := was[i], now[j]
		if a.exists != b.exists {
			if b.exists {
				return name(b.path) + " was added"
			}
			return name(a.path) + " was removed"
		}
		if a.exists && a.sum != b.sum {
			return name(a.path) + " changed"
		}
		i++
		j++
	}
	return ""
}

// statDiff is the first entry whose stat differs between two walks of a
// tree, or "".
func statDiff(before, now *state) string {
	if len(before.entries) != len(now.entries) {
		return before.tree.Dir
	}
	for i := range before.entries {
		if before.entries[i].path != now.entries[i].path || !sameStat(before.entries[i], now.entries[i]) {
			return filepath.Join(before.tree.Dir, filepath.FromSlash(now.entries[i].path))
		}
	}
	if len(before.probes) != len(now.probes) {
		return before.tree.Dir
	}
	for i := range before.probes {
		if !sameStat(before.probes[i], now.probes[i]) {
			return now.probes[i].path
		}
	}
	return ""
}

func sameStat(a, b entry) bool {
	return a.path == b.path && a.exists == b.exists && a.size == b.size && a.mtime == b.mtime &&
		a.ctime == b.ctime && a.ino == b.ino
}

// recent is the first entry changed within racyWindow of snap, or "".
func recent(entries []entry, snap int64) string {
	for _, en := range entries {
		if en.exists && isRacy(en, snap) {
			return en.path
		}
	}
	return ""
}

func isRacy(en entry, snap int64) bool {
	limit := snap - int64(racyWindow)
	return en.mtime >= limit || en.ctime >= limit
}

// covered reports whether path is a file one of trees tracks.
func covered(trees []*state, path string) bool {
	for _, st := range trees {
		rel, err := filepath.Rel(st.tree.Dir, path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		rel = filepath.ToSlash(rel)
		i := sort.Search(len(st.entries), func(i int) bool { return st.entries[i].path >= rel })
		if i < len(st.entries) && st.entries[i].path == rel {
			return true
		}
	}
	return false
}

// markers are the files above a tree whose presence decides a program's
// root (analysis.ProjectRoot) or its Go module (internal/ffirun).
var markers = []string{"nomi.toml", "main.nomi", "go.mod"}

// scan walks t. An entry whose stat matches prev's, recorded at prevSnap,
// and was not changed near it takes prev's hash; any other is hashed.
func scan(t Tree, prev *state, prevSnap int64, hash bool) *state {
	st := &state{tree: t}
	var prevByPath map[string]entry
	if prev != nil {
		prevByPath = make(map[string]entry, len(prev.entries))
		for _, en := range prev.entries {
			prevByPath[en.path] = en
		}
	}
	visited := 0
	_ = filepath.WalkDir(t.Dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if visited++; visited > maxVisited {
			st.tooBig = true
			return filepath.SkipAll
		}
		name := d.Name()
		if p != t.Dir && (strings.HasPrefix(name, ".") || name == "node_modules") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !t.All && !tracked(name) {
			return nil
		}
		if len(st.entries) >= maxEntries {
			st.tooBig = true
			return filepath.SkipAll
		}
		rel, err := filepath.Rel(t.Dir, p)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		var was *entry
		if w, ok := prevByPath[rel]; ok {
			was = &w
		}
		en := statPath(p, was, prevSnap, hash)
		en.path = rel
		if !en.exists {
			// A dangling link, or a directory reached through a link: no
			// file a build reads.
			return nil
		}
		st.entries = append(st.entries, en)
		return nil
	})
	sort.Slice(st.entries, func(i, j int) bool { return st.entries[i].path < st.entries[j].path })
	if !t.All {
		var prevProbes map[string]entry
		if prev != nil {
			prevProbes = map[string]entry{}
			for _, en := range prev.probes {
				prevProbes[en.path] = en
			}
		}
		for dir := filepath.Dir(t.Dir); ; dir = filepath.Dir(dir) {
			for _, m := range markers {
				p := filepath.Join(dir, m)
				var was *entry
				if w, ok := prevProbes[p]; ok {
					was = &w
				}
				st.probes = append(st.probes, statFile(p, was, prevSnap, hash))
			}
			if parent := filepath.Dir(dir); parent == dir {
				break
			}
		}
	}
	return st
}

// tracked reports whether a file named name is one a program's build can
// read in a project.
func tracked(name string) bool {
	switch {
	case strings.HasSuffix(name, ".nomi"), strings.HasSuffix(name, ".toml"):
		return true
	case name == "go.mod", name == "go.sum":
		return true
	}
	return false
}

// statFile is statPath for a single file, its path absolute.
func statFile(p string, prev *entry, prevSnap int64, hash bool) entry {
	en := statPath(p, prev, prevSnap, hash)
	en.path = p
	return en
}

// statPath stats the file at p, following a link, and hashes it unless
// prev holds its hash: prev's stat is the same and was not racy.
func statPath(p string, prev *entry, prevSnap int64, hash bool) entry {
	info, err := os.Stat(p)
	if err != nil || info.IsDir() {
		return entry{}
	}
	en := entry{exists: true, size: info.Size(), mtime: info.ModTime().UnixNano()}
	en.ctime, en.ino = changeTime(info)
	if prev != nil && prev.exists && prev.hashed && sameStat(entry{path: prev.path, exists: true, size: en.size,
		mtime: en.mtime, ctime: en.ctime, ino: en.ino}, *prev) && !isRacy(*prev, prevSnap) {
		en.sum, en.hashed = prev.sum, true
		return en
	}
	if !hash {
		return en
	}
	f, err := os.Open(p)
	if err != nil {
		return entry{}
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return entry{}
	}
	copy(en.sum[:], h.Sum(nil))
	en.hashed = true
	return en
}

// keyEnv are the environment variables outside NOMI_ a build reads: where
// the module cache is, and the time zone and locale a compile-time check
// could see.
var keyEnv = []string{"GOMODCACHE", "GOPATH", "HOME", "TZ", "LANG", "LC_ALL"}

// unkeyedEnv are the NOMI_ variables that change no build: the cache's own,
// and how diagnostics are written (a record holds only a build that had
// none).
var unkeyedEnv = map[string]bool{
	artifactcache.RootEnv:   true,
	DebugEnv:                true,
	"NOMI_DEBUG_STD_BUNDLE": true,
	"NOMI_DIAGNOSTICS":      true,
}

// keyOf is the record key: the compiler, what was built, the entry and the
// environment.
func keyOf(id, kind, entryFile string) string {
	h := sha256.New()
	fmt.Fprintf(h, "nomi program\x00%d\x00%s\x00%s\x00%s\x00", recordVersion, id, kind, entryFile)
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if (strings.HasPrefix(name, "NOMI_") && !unkeyedEnv[name]) || slices.Contains(keyEnv, name) {
			env = append(env, kv)
		}
	}
	sort.Strings(env)
	for _, kv := range env {
		fmt.Fprintf(h, "%s\x00", kv)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// prune removes the records under dir nothing has read for unused, at most
// once per pruneEvery, which a marker file's time keeps.
func prune(dir string) {
	marker := filepath.Join(dir, ".pruned")
	if info, err := os.Stat(marker); err == nil && time.Since(info.ModTime()) < pruneEvery {
		return
	}
	if err := artifactcache.WriteFile(marker, nil); err != nil {
		return
	}
	subdirs, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, d := range subdirs {
		if d.IsDir() && len(d.Name()) == 2 {
			artifactcache.Prune(filepath.Join(dir, d.Name()), "", unused)
		}
	}
}

// The record's layout, after the magic: the version, a CRC-32 of the rest,
// and the rest: the key, the snapshot's time, the trees, the single files
// and the payload. Integers are varints; strings and the payload are a
// length and bytes.

func encodeRecord(rec *record) []byte {
	var body bytes.Buffer
	putString(&body, rec.key)
	putVarint(&body, rec.snap)
	putUvarint(&body, uint64(len(rec.trees)))
	for _, st := range rec.trees {
		putString(&body, st.tree.Dir)
		putBool(&body, st.tree.All)
		putEntries(&body, st.entries)
		putEntries(&body, st.probes)
	}
	putEntries(&body, rec.files)
	putUvarint(&body, uint64(len(rec.payload)))
	body.Write(rec.payload)
	var out bytes.Buffer
	out.WriteString(recordMagic)
	putUvarint(&out, recordVersion)
	var sum [4]byte
	binary.LittleEndian.PutUint32(sum[:], crc32.ChecksumIEEE(body.Bytes()))
	out.Write(sum[:])
	out.Write(body.Bytes())
	return out.Bytes()
}

func putEntries(w *bytes.Buffer, entries []entry) {
	putUvarint(w, uint64(len(entries)))
	for _, en := range entries {
		putString(w, en.path)
		putBool(w, en.exists)
		if !en.exists {
			continue
		}
		putVarint(w, en.size)
		putVarint(w, en.mtime)
		putVarint(w, en.ctime)
		putUvarint(w, en.ino)
		w.Write(en.sum[:])
	}
}

func putUvarint(w *bytes.Buffer, v uint64) {
	var b [binary.MaxVarintLen64]byte
	w.Write(b[:binary.PutUvarint(b[:], v)])
}

func putVarint(w *bytes.Buffer, v int64) {
	var b [binary.MaxVarintLen64]byte
	w.Write(b[:binary.PutVarint(b[:], v)])
}

func putString(w *bytes.Buffer, s string) {
	putUvarint(w, uint64(len(s)))
	w.WriteString(s)
}

func putBool(w *bytes.Buffer, b bool) {
	if b {
		w.WriteByte(1)
	} else {
		w.WriteByte(0)
	}
}

// readRecord reads the record at path, or answers nil and why there is none
// it can use.
func readRecord(path, key string) (*record, string) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, "no record"
	}
	if err != nil {
		return nil, err.Error()
	}
	rec, err := decodeRecord(data)
	if err != nil {
		return nil, "the record cannot be read: " + err.Error()
	}
	if rec.key != key {
		return nil, "the record is another program's"
	}
	return rec, ""
}

var errTruncated = errors.New("truncated")

func decodeRecord(data []byte) (rec *record, err error) {
	if !bytes.HasPrefix(data, []byte(recordMagic)) {
		return nil, errors.New("not a program record")
	}
	r := &reader{b: data[len(recordMagic):]}
	if v := r.uvarint(); r.err == nil && v != recordVersion {
		return nil, fmt.Errorf("record version %d, this build reads %d", v, recordVersion)
	}
	if len(r.b) < 4 {
		return nil, errTruncated
	}
	sum := binary.LittleEndian.Uint32(r.b[:4])
	r.b = r.b[4:]
	if crc32.ChecksumIEEE(r.b) != sum {
		return nil, errors.New("checksum mismatch")
	}
	rec = &record{}
	rec.key = r.string()
	rec.snap = r.varint()
	n := r.uvarint()
	for i := uint64(0); i < n && r.err == nil; i++ {
		st := &state{}
		st.tree.Dir = r.string()
		st.tree.All = r.bool()
		st.entries = r.entries()
		st.probes = r.entries()
		rec.trees = append(rec.trees, st)
	}
	rec.files = r.entries()
	size := r.uvarint()
	if r.err == nil && uint64(len(r.b)) != size {
		return nil, errTruncated
	}
	rec.payload = r.b
	if r.err != nil {
		return nil, r.err
	}
	return rec, nil
}

type reader struct {
	b   []byte
	err error
}

func (r *reader) uvarint() uint64 {
	if r.err != nil {
		return 0
	}
	v, n := binary.Uvarint(r.b)
	if n <= 0 {
		r.err = errTruncated
		return 0
	}
	r.b = r.b[n:]
	return v
}

func (r *reader) varint() int64 {
	if r.err != nil {
		return 0
	}
	v, n := binary.Varint(r.b)
	if n <= 0 {
		r.err = errTruncated
		return 0
	}
	r.b = r.b[n:]
	return v
}

func (r *reader) bytes(n uint64) []byte {
	if r.err != nil {
		return nil
	}
	if uint64(len(r.b)) < n {
		r.err = errTruncated
		return nil
	}
	out := r.b[:n]
	r.b = r.b[n:]
	return out
}

func (r *reader) string() string { return string(r.bytes(r.uvarint())) }

func (r *reader) bool() bool {
	b := r.bytes(1)
	return len(b) == 1 && b[0] == 1
}

func (r *reader) entries() []entry {
	n := r.uvarint()
	if r.err != nil || n > uint64(len(r.b)) {
		r.err = errTruncated
		return nil
	}
	out := make([]entry, 0, n)
	for i := uint64(0); i < n && r.err == nil; i++ {
		en := entry{path: r.string(), exists: r.bool()}
		if en.exists {
			en.size = r.varint()
			en.mtime = r.varint()
			en.ctime = r.varint()
			en.ino = r.uvarint()
			copy(en.sum[:], r.bytes(32))
			en.hashed = true
		}
		out = append(out, en)
	}
	return out
}
