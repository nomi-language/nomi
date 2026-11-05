// Package artifactcache is where a compiler keeps what it builds once and
// reads back in later processes: the standard library's interfaces and
// lowering (std's artifact, internal/irbuild's std bundle), and each
// program's checked and lowered image (internal/progcache).
//
// The cache is os.UserCacheDir()/nomi/artifacts, or $NOMI_ARTIFACT_CACHE.
// Under `go test` without the variable it is a directory under os.TempDir,
// and the variable is set to it for the rest of the process, so a test never
// writes the user's cache and neither does a `nomi` or a Go FFI wrapper a
// test runs, which inherit it. NOMI_ARTIFACT_CACHE=off turns the cache off.
//
// An entry is keyed by the compiler's identity (CompilerID), so a binary
// never reads another binary's entry. Writes go to a temporary sibling and
// are renamed into place, so a reader sees no file or a whole one; a reader
// still checks what it reads, since a file can be truncated or corrupted
// after it is written.
package artifactcache

import (
	"bytes"
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
	// A test binary's children (a built `nomi`, a Go FFI wrapper) are not
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
var CompilerID = sync.OnceValue(func() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	id, _ := ReadBuildID(exe)
	return id
})

// buildIDPrefix and buildIDSuffix bracket the build ID the Go linker writes
// near the start of every executable it links (cmd/internal/buildid).
const (
	buildIDPrefix = "\xff Go build ID: \""
	buildIDSuffix = "\"\n \xff"
	// buildIDWindow is how much of the file is searched: the linker puts the
	// ID in the first page of the text section, within the first 32 KB on
	// every platform Go links for.
	buildIDWindow = 64 << 10
)

// ReadBuildID reads the Go build ID from the executable at path.
func ReadBuildID(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
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

// tempPrefix starts the name of a file being written.
const tempPrefix = ".tmp-"

// WriteFile writes data to path through a temporary sibling renamed into
// place, so a reader of path sees the old file, no file or all of data.
func WriteFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, tempPrefix+filepath.Base(path)+"-")
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

// Touch marks path used now, at most once an hour, so Prune leaves it.
func Touch(path string, info os.FileInfo) {
	if now := time.Now(); now.Sub(info.ModTime()) > time.Hour {
		_ = os.Chtimes(path, now, now)
	}
}

// Prune removes the files in dir other than keep that nothing has used for
// unused, and temporary files left for an hour. Errors are ignored; pruning
// is housekeeping.
func Prune(dir, keep string, unused time.Duration) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	now := time.Now()
	for _, e := range entries {
		name := e.Name()
		if name == keep || e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		limit := unused
		if strings.HasPrefix(name, tempPrefix) {
			limit = time.Hour
		}
		if now.Sub(info.ModTime()) >= limit {
			os.Remove(filepath.Join(dir, name))
		}
	}
}
