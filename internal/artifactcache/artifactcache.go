package artifactcache

import (
	"bytes"
	"debug/elf"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// RootEnv names the cache directory, or `off` for no cache.
const RootEnv = "NOMI_ARTIFACT_CACHE"

// testRoot is the cache under `go test` when RootEnv is unset.
func testRoot() string {
	return filepath.Join(os.TempDir(), "nomi-test-cache", "artifacts")
}

func init() {
	// A test binary's children (a built `nomi`) are not
	// test binaries, so they would otherwise use the user's cache.
	if testing.Testing() && os.Getenv(RootEnv) == "" {
		_ = os.Setenv(RootEnv, testRoot())
	}
}

// Root is the cache directory, or "" when there is none: RootEnv is `off`,
// or the user has no cache directory.
func Root() string {
	switch v := os.Getenv(RootEnv); {
	case v == "off":
		return ""
	case v != "":
		return v
	case testing.Testing():
		return testRoot()
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "nomi", "artifacts")
}

// ProgramRoot is where a program's own artifacts go (internal/progcache), or
// "" when they are not kept. It is Root, except in a test process that did
// not ask for a cache: there it is "" while RootEnv is the directory init
// chose, so every test that loads a program reads and writes nothing and
// behaves as it would with no cache, and a test of the cache sets RootEnv to
// a directory of its own. A `nomi` a test starts is not a test process, so
// it keeps its programs under the directory init chose, as it keeps std.
func ProgramRoot() string {
	root := Root()
	if root != "" && testing.Testing() && os.Getenv(RootEnv) == testRoot() {
		return ""
	}
	return root
}

// CompilerID is the running executable's Go build ID, or "" when it has none
// or the executable cannot be read (js/wasm has no executable file). The ID
// changes with any change to what the binary links, including the embedded
// std, so it identifies the compiler: two binaries that answer one ID lower
// a program alike.
//
// It is a func literal, statically initialized, and not a sync.OnceValue
// made at init: a binary that never calls it (nomi-vm, which links this
// package for Root) then links no executable reader, debug/elf among them.
var CompilerID = func() string {
	compilerID.once.Do(func() {
		if exe, err := os.Executable(); err == nil {
			compilerID.id, _ = ReadBuildID(exe)
		}
	})
	return compilerID.id
}

var compilerID struct {
	once sync.Once
	id   string
}

// buildIDPrefix and buildIDSuffix bracket the build ID the Go linker writes
// at the start of the text section of a Mach-O or PE executable
// (cmd/internal/buildid). An ELF executable has no such text: its ID is the
// desc of a note in a PT_NOTE segment, named "Go" and of type
// elfGoBuildIDNote, which the linker also names section .note.go.buildid.
const (
	buildIDPrefix = "\xff Go build ID: \""
	buildIDSuffix = "\"\n \xff"
	// buildIDWindow is how much of the file is searched: the linker puts the
	// ID in the first page of the text section, within the first 32 KB on
	// every platform Go links for.
	buildIDWindow = 64 << 10
	// elfGoBuildIDNote is the type of the ELF note holding the build ID
	// (cmd/link's ELF_NOTE_GOBUILDID_TAG).
	elfGoBuildIDNote = 4
)

// ReadBuildID reads the Go build ID from the executable at path: from its
// note when it is ELF, else from the start of its text.
func ReadBuildID(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	var magic [4]byte
	if _, err := f.ReadAt(magic[:], 0); err == nil && string(magic[:]) == elf.ELFMAG {
		return readELFBuildID(f, path)
	}
	buf := make([]byte, buildIDWindow)
	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", err
	}
	buf = buf[:n]
	i := bytes.Index(buf, []byte(buildIDPrefix))
	if i < 0 {
		return "", errors.New("artifactcache: no Go build ID in " + path)
	}
	rest := buf[i+len(buildIDPrefix):]
	j := bytes.Index(rest, []byte(buildIDSuffix))
	if j <= 0 {
		return "", errors.New("artifactcache: no Go build ID in " + path)
	}
	return string(rest[:j]), nil
}

// readELFBuildID reads the build ID note from the PT_NOTE segments of the
// ELF file f, which is at path. A note is a header of three words (name
// size, desc size, type), then the name and the desc, each padded to a
// word.
func readELFBuildID(f *os.File, path string) (string, error) {
	ef, err := elf.NewFile(f)
	if err != nil {
		return "", err
	}
	for _, p := range ef.Progs {
		if p.Type != elf.PT_NOTE {
			continue
		}
		notes, err := io.ReadAll(p.Open())
		if err != nil {
			return "", err
		}
		for len(notes) >= 12 {
			nameSize := int(ef.ByteOrder.Uint32(notes[0:]))
			descSize := int(ef.ByteOrder.Uint32(notes[4:]))
			kind := ef.ByteOrder.Uint32(notes[8:])
			name := 12
			desc := name + (nameSize+3)&^3
			end := desc + (descSize+3)&^3
			if nameSize < 0 || descSize < 0 || desc+descSize > len(notes) {
				break
			}
			if kind == elfGoBuildIDNote && string(notes[name:name+nameSize]) == "Go\x00\x00" {
				return string(notes[desc : desc+descSize]), nil
			}
			if end > len(notes) {
				break
			}
			notes = notes[end:]
		}
	}
	return "", errors.New("artifactcache: no Go build ID in " + path)
}

// TempPrefix starts the name of an entry being written, which Prune leaves
// an hour for the process writing it.
const TempPrefix = ".tmp-"

// WriteFile writes data to path through a temporary sibling renamed into
// place, so a reader of path sees the old file, no file or all of data.
func WriteFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, TempPrefix+filepath.Base(path)+"-")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, werr := f.Write(data)
	cerr := f.Close()
	if werr != nil || cerr != nil {
		os.Remove(tmp)
		return errors.Join(werr, cerr)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// RemoveTree removes path and everything under it, making each directory
// writable first: the package cache keeps an extracted repository
// read-only, and a directory's mode decides whether its entries can be
// removed. A read-only file is removed as any other.
func RemoveTree(path string) error {
	_ = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			_ = os.Chmod(p, 0o755)
		}
		return nil
	})
	return os.RemoveAll(path)
}

// ForeignEntry answers the path of the first entry of dir, a cache's
// directory, that the cache does not write, or "" when there is none or no
// dir. own answers that for each entry: "" for one the cache writes, or
// the path of what it does not, which may be below the entry when own
// looks inside it (with ForeignEntry again). A temporary entry (TempPrefix)
// and a prune's marker (Due) are every cache's. `nomi cache clean` refuses
// a directory an environment variable names when it holds a foreign entry,
// since it would remove someone's files.
func ForeignEntry(dir string, own func(path string, e fs.DirEntry) (string, error)) (string, error) {
	info, err := os.Stat(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "", nil
	case err != nil:
		return "", err
	case !info.IsDir():
		return dir, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), TempPrefix) || e.Name() == dueMarker && e.Type().IsRegular() {
			continue
		}
		if foreign, err := own(filepath.Join(dir, e.Name()), e); foreign != "" || err != nil {
			return foreign, err
		}
	}
	return "", nil
}

// Foreign answers the path of the first entry under dir that the artifact
// cache does not write (ForeignEntry), or "": the cache holds a directory
// per kind (the package doc, "Pruning"), and each holds entries named by a
// hash, in hex, with an extension or none.
func Foreign(dir string) (string, error) {
	return ForeignEntry(dir, func(kind string, e fs.DirEntry) (string, error) {
		if !e.IsDir() {
			return kind, nil
		}
		return ForeignEntry(kind, func(p string, e fs.DirEntry) (string, error) {
			if hash, _, _ := strings.Cut(e.Name(), "."); len(hash) < 2 || strings.Trim(hash, "0123456789abcdef") != "" {
				return p, nil
			}
			return "", nil
		})
	})
}

// Unused is how long an entry nothing has used is kept, where its kind
// keeps none shorter (the package doc, "Pruning").
const Unused = 14 * 24 * time.Hour

// Touch marks path, a file or a directory, used now, at most once an hour,
// so Prune leaves it.
func Touch(path string, info os.FileInfo) {
	if now := time.Now(); now.Sub(info.ModTime()) > time.Hour {
		_ = os.Chtimes(path, now, now)
	}
}

// StdDir is the directory under Root that holds the standard library's
// interfaces, state and bundle of each compiler build, and StdUnused how
// long they are kept after the last process of that build used them (the
// package doc, "Pruning").
const (
	StdDir    = "std"
	StdUnused = 24 * time.Hour
)

// Pruned is what a prune removed: how many entries, and the size of their
// files.
type Pruned struct {
	Entries int
	Size    int64
}

// Add adds q to p.
func (p *Pruned) Add(q Pruned) {
	p.Entries += q.Entries
	p.Size += q.Size
}

// Remove removes the entry at path with RemoveTree and, when it is gone,
// adds it to p.
func (p *Pruned) Remove(path string) {
	size, _ := TreeSize(path)
	if RemoveTree(path) == nil {
		p.Entries++
		p.Size += size
	}
}

// TreeSize is the size of the regular files under path, which links are
// not followed to; 0 when path does not exist.
func TreeSize(path string) (int64, error) {
	var size int64
	err := filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == path && errors.Is(err, fs.ErrNotExist) {
				return fs.SkipAll
			}
			return err
		}
		if d.Type().IsRegular() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			size += info.Size()
		}
		return nil
	})
	return size, err
}

// Prune removes the entries of dir, files and directories, other than
// keep, that nothing has used for unused, and temporary ones left for an
// hour, and answers what it removed. An entry's last use is its
// modification time: a file's moves when it is written, and a directory's
// only when an entry is added to or removed from it, so a cache whose
// entries are directories touches each as it reads it. Errors are
// ignored; pruning is housekeeping.
func Prune(dir, keep string, unused time.Duration) Pruned {
	var pruned Pruned
	entries, err := os.ReadDir(dir)
	if err != nil {
		return pruned
	}
	now := time.Now()
	for _, e := range entries {
		name := e.Name()
		if name == keep || name == dueMarker {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if Expired(name, info, unused, now) {
			pruned.Remove(filepath.Join(dir, name))
		}
	}
	return pruned
}

// Expired reports whether the cache entry name, whose file info is info,
// has gone unused long enough at now for a prune to remove it: unused for
// a kept entry, an hour for a temporary one (TempPrefix).
func Expired(name string, info os.FileInfo, unused time.Duration, now time.Time) bool {
	if strings.HasPrefix(name, TempPrefix) {
		unused = time.Hour
	}
	return now.Sub(info.ModTime()) >= unused
}

// dueMarker is the file in a pruned directory whose modification time is
// its last prune.
const dueMarker = ".pruned"

// Due reports whether a prune of dir is due, its last one being every ago
// or more, or force being set, and if so marks it done now, so of the
// processes that ask within every, one prunes. `nomi cache prune` forces
// it. It answers false when the mark cannot be written, and when dir does
// not exist, since it holds nothing to prune.
func Due(dir string, every time.Duration, force bool) bool {
	marker := filepath.Join(dir, dueMarker)
	info, err := os.Stat(marker)
	switch {
	case err == nil && !force && time.Since(info.ModTime()) < every:
		return false
	case err != nil:
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			return false
		}
	}
	return WriteFile(marker, nil) == nil
}
