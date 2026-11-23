package artifactcache

import (
	"bytes"
	"debug/elf"
	"errors"
	"io"
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

// Prune removes the entries of dir, files and directories, other than
// keep, that nothing has used for unused, and temporary ones left for an
// hour. An entry's last use is its modification time: a file's moves when
// it is written, and a directory's only when an entry is added to or
// removed from it, so a cache whose entries are directories touches each
// as it reads it. Errors are ignored; pruning is housekeeping.
func Prune(dir, keep string, unused time.Duration) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
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
		limit := unused
		if strings.HasPrefix(name, TempPrefix) {
			limit = time.Hour
		}
		if now.Sub(info.ModTime()) >= limit {
			os.RemoveAll(filepath.Join(dir, name))
		}
	}
}

// dueMarker is the file in a pruned directory whose modification time is
// its last prune.
const dueMarker = ".pruned"

// Due reports whether a prune of dir is due, its last one being every ago
// or more, and if so marks it done now, so of the processes that ask
// within every, one prunes. It answers false when the mark cannot be
// written.
func Due(dir string, every time.Duration) bool {
	marker := filepath.Join(dir, dueMarker)
	if info, err := os.Stat(marker); err == nil && time.Since(info.ModTime()) < every {
		return false
	}
	return WriteFile(marker, nil) == nil
}
