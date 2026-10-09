// Package stdcache writes a std's embedded source to disk, so an editor can
// open the file go-to-definition names when the server has no source tree to
// point at.
//
// Each std version gets its own directory, named by a hash of its files:
// <root>/<version>/<module>.nomi. A server writes and reads only its own
// version, so two servers built from different std sources (an old one still
// running beside a newly installed one) never rewrite each other's files, and
// an editor buffer opened from one server's directory keeps the text that
// server analyzed.
package stdcache

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// RootEnv names a directory that replaces ~/.cache/nomi, so the std source is
// written to $NOMI_STD_CACHE_ROOT/std. Under `go test` without it, DefaultRoot
// answers a directory under the system temp directory, never the user's
// cache.
const RootEnv = "NOMI_STD_CACHE_ROOT"

// versionLen is the length of a version directory's name, in hex digits.
const versionLen = 16

// staleAfter is how long a version directory may go unused before another
// server removes it. A server touches its own directory when it first uses it
// and at most every touchEvery after, whenever it hands out a path in it, so
// a directory older than this belongs to no server that has navigated into
// std in that time. A server that finds its directory gone writes it again.
const (
	staleAfter = 14 * 24 * time.Hour
	touchEvery = time.Hour
	// tempAfter is how long a half-written temporary directory is left for
	// the process writing it.
	tempAfter = time.Hour
)

const tempPrefix = ".tmp-"

// DefaultRoot is the directory version directories live under:
// ~/.cache/nomi/std, or $NOMI_STD_CACHE_ROOT/std. Under `go test` without the
// variable it is a directory under os.TempDir, so no test writes into the
// user's cache.
//
// Its last element is always `std`: a file is a std source to the analyzer
// when its directory is `std` or a version directory directly under one
// (analysis.stdlibModuleForPath). A root passed to New must end in `std` too.
func DefaultRoot() string {
	base := os.Getenv(RootEnv)
	switch {
	case base != "":
	case testing.Testing():
		base = filepath.Join(os.TempDir(), "nomi-test-cache")
	default:
		home, err := os.UserHomeDir()
		if err != nil {
			base = filepath.Join(os.TempDir(), "nomi-cache")
		} else {
			base = filepath.Join(home, ".cache", "nomi")
		}
	}
	return filepath.Join(base, "std")
}

// IsVersion reports whether name is shaped like a version directory's name.
func IsVersion(name string) bool {
	if len(name) != versionLen {
		return false
	}
	for _, r := range name {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

// Dir is one std version's directory under a root.
type Dir struct {
	root    string
	files   fs.FS
	version string

	mu      sync.Mutex
	touched time.Time
	pruned  bool
}

// New is the directory for the .nomi files of files under root. It computes
// the version (a hash of every .nomi file's path and content) and writes
// nothing.
func New(root string, files fs.FS) *Dir {
	return &Dir{root: root, files: files, version: Version(files)}
}

// Version is the version directory name for the .nomi files of files.
func Version(files fs.FS) string {
	h := sha256.New()
	for _, p := range nomiFiles(files) {
		data, _ := fs.ReadFile(files, p)
		h.Write([]byte(p))
		h.Write([]byte{0})
		h.Write(data)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:versionLen]
}

// Version is the directory's version name.
func (d *Dir) Version() string { return d.version }

// Location is where rel (a slash-separated path such as "maybe.nomi") is
// written, without writing anything.
func (d *Dir) Location(rel string) string {
	return filepath.Join(d.root, d.version, filepath.FromSlash(rel))
}

// Path writes the version directory when it is missing and returns rel's
// path in it. A failure to write is not reported: the path is returned
// anyway, and an editor opening it finds no file.
func (d *Dir) Path(rel string) string {
	d.ensure()
	return d.Location(rel)
}

// ensure writes the version directory if it does not exist, removes stale
// siblings once per process, and refreshes the directory's modification
// time at most every touchEvery, which is what keeps another server's prune
// away from it.
func (d *Dir) ensure() {
	d.mu.Lock()
	defer d.mu.Unlock()
	dir := filepath.Join(d.root, d.version)
	now := time.Now()
	if _, err := os.Stat(dir); err != nil {
		if d.write(dir) != nil {
			return
		}
		d.touched = now
	} else if now.Sub(d.touched) >= touchEvery {
		_ = os.Chtimes(dir, now, now)
		d.touched = now
	}
	if !d.pruned {
		d.pruned = true
		d.prune(now)
	}
}

// write materializes the version into a temporary sibling and renames it to
// dir, so a reader sees either no directory or a complete one. When another
// process wins the rename, its directory holds the same files and this one's
// is removed.
func (d *Dir) write(dir string) error {
	if err := os.MkdirAll(d.root, 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(d.root, tempPrefix+d.version+"-")
	if err != nil {
		return err
	}
	for _, p := range nomiFiles(d.files) {
		data, err := fs.ReadFile(d.files, p)
		if err != nil {
			os.RemoveAll(tmp)
			return err
		}
		target := filepath.Join(tmp, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			os.RemoveAll(tmp)
			return err
		}
		// Read-only: the file is a copy of the source this server runs,
		// and an editor warns before saving over it.
		if err := os.WriteFile(target, data, 0o444); err != nil {
			os.RemoveAll(tmp)
			return err
		}
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		os.RemoveAll(tmp)
		return err
	}
	if err := os.Rename(tmp, dir); err != nil {
		os.RemoveAll(tmp)
		if _, statErr := os.Stat(dir); statErr == nil {
			return nil
		}
		return err
	}
	return nil
}

// prune removes what no running server can be reading: other version
// directories not touched for staleAfter, files and directories of the
// unversioned layout older than that, and temporary directories older than
// tempAfter. Errors are ignored; pruning is housekeeping.
func (d *Dir) prune(now time.Time) {
	entries, err := os.ReadDir(d.root)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if name == d.version {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		limit := staleAfter
		if strings.HasPrefix(name, tempPrefix) {
			limit = tempAfter
		}
		if now.Sub(info.ModTime()) < limit {
			continue
		}
		os.RemoveAll(filepath.Join(d.root, name))
	}
}

// nomiFiles is every .nomi file in files, sorted.
func nomiFiles(files fs.FS) []string {
	var out []string
	_ = fs.WalkDir(files, ".", func(p string, e fs.DirEntry, err error) error {
		if err == nil && !e.IsDir() && strings.HasSuffix(p, ".nomi") {
			out = append(out, p)
		}
		return nil
	})
	sort.Strings(out)
	return out
}
