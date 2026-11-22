package modcache

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
)

// LockName is the file beside nomi.toml that locks the project's build
// list.
const LockName = "nomi.lock"

// lockHeader is the comment nomi.lock starts with.
const lockHeader = "# nomi.lock locks the version of each module this project needs and the\n" +
	"# hash of its files. nomi fetch, add, remove and update write it; commit it.\n"

// Locked is one module of nomi.lock: the version selected for it, the h1:
// hash of its zip (golang.org/x/mod/sumdb/dirhash), and whether the
// project's nomi.toml names it.
type Locked struct {
	module.Version
	Hash   string
	Direct bool
}

// Lock is a project's nomi.lock, one line per module of its build list
// that comes from the module proxy: `<path> <version> <hash>`, with
// `direct` after it for a module the project's nomi.toml names. A local
// module (a dir entry) has no hash and no line. Lines are sorted by path,
// so a change of the build list reads as a diff of the lines that moved.
type Lock struct {
	Modules []Locked
}

// Find answers the locked module with path path.
func (l *Lock) Find(path string) (Locked, bool) {
	if l == nil {
		return Locked{}, false
	}
	for _, m := range l.Modules {
		if m.Path == path {
			return m, true
		}
	}
	return Locked{}, false
}

// ReadLock reads the nomi.lock in dir: an empty lock when there is none.
func ReadLock(dir string) (*Lock, error) {
	path := filepath.Join(dir, LockName)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &Lock{}, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	l, err := ParseLock(data)
	if err != nil {
		return nil, fmt.Errorf("%s:%w", path, err)
	}
	return l, nil
}

// ParseLock reads data, a nomi.lock's text. A line that is not a comment
// and not blank is a module's: its path, a full version, an h1: hash, and
// `direct` or nothing. A path listed twice is an error.
func ParseLock(data []byte) (*Lock, error) {
	l := &Lock{}
	seen := map[string]bool{}
	for i, line := range strings.Split(string(data), "\n") {
		text := strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		f := strings.Fields(text)
		bad := func(format string, args ...any) error {
			return fmt.Errorf("%d: %s", i+1, fmt.Sprintf(format, args...))
		}
		if len(f) < 3 || len(f) > 4 || len(f) == 4 && f[3] != "direct" {
			return nil, bad("a line is a module path, its version, its h1: hash and `direct` or nothing: %q", text)
		}
		if err := module.Check(f[0], f[1]); err != nil || semver.Canonical(f[1]) != f[1] && !module.IsPseudoVersion(f[1]) {
			return nil, bad("%s %s is not a module path and version", f[0], f[1])
		}
		if !strings.HasPrefix(f[2], "h1:") {
			return nil, bad("%s %s: %q is not an h1: hash", f[0], f[1], f[2])
		}
		if seen[f[0]] {
			return nil, bad("%s is listed twice", f[0])
		}
		seen[f[0]] = true
		l.Modules = append(l.Modules, Locked{Version: module.Version{Path: f[0], Version: f[1]}, Hash: f[2], Direct: len(f) == 4})
	}
	return l, nil
}

// Bytes is l as nomi.lock writes it: the header, then a line per module,
// sorted by path.
func (l *Lock) Bytes() []byte {
	mods := append([]Locked(nil), l.Modules...)
	sort.Slice(mods, func(i, j int) bool { return mods[i].Path < mods[j].Path })
	var b strings.Builder
	b.WriteString(lockHeader)
	for _, m := range mods {
		b.WriteString(m.Path + " " + m.Version.Version + " " + m.Hash)
		if m.Direct {
			b.WriteString(" direct")
		}
		b.WriteString("\n")
	}
	return []byte(b.String())
}
