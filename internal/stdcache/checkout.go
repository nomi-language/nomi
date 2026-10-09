package stdcache

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Navigator picks the file navigation into std opens: the file in a std
// source tree (a checkout's std/) when its content is the std this process
// embeds, and otherwise the copy in the version directory.
//
// The choice is per file. A checkout edited after the server was built still
// sends go-to-definition into every module whose file is unchanged, where
// the author can edit it, and sends it into the materialized copy for a
// module whose file no longer matches, so the jump lands on the line the
// server's own analysis names.
type Navigator struct {
	checkout string
	dir      *Dir

	mu    sync.Mutex
	files map[string]checkoutFile
}

// checkoutFile is what Navigator last saw of a checkout file: its size and
// modification time when it was compared, and whether it matched.
type checkoutFile struct {
	size    int64
	modTime time.Time
	matches bool
}

// NewNavigator chooses between checkout, a std source tree ("" for none), and
// dir, whose files are the std this process runs.
func NewNavigator(checkout string, dir *Dir) *Navigator {
	return &Navigator{checkout: checkout, dir: dir, files: map[string]checkoutFile{}}
}

// Path is the file navigation into rel (a slash-separated path under std/,
// such as "maybe.nomi") opens. A checkout file is compared with the embedded
// one the first time it is asked for, and again only after its size or
// modification time changes, so a repeated ask costs one stat.
func (n *Navigator) Path(rel string) string {
	if n.checkout != "" {
		path := filepath.Join(n.checkout, filepath.FromSlash(rel))
		if n.matches(rel, path) {
			if abs, err := filepath.Abs(path); err == nil {
				return abs
			}
			return path
		}
	}
	return n.dir.Path(rel)
}

// matches reports whether the checkout file at path holds the embedded
// content of rel.
func (n *Navigator) matches(rel, path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if seen, ok := n.files[rel]; ok && seen.size == info.Size() && seen.modTime.Equal(info.ModTime()) {
		return seen.matches
	}
	matches := false
	if embedded, err := fs.ReadFile(n.dir.files, rel); err == nil && int64(len(embedded)) == info.Size() {
		if onDisk, err := os.ReadFile(path); err == nil {
			matches = bytes.Equal(onDisk, embedded)
		}
	}
	n.files[rel] = checkoutFile{size: info.Size(), modTime: info.ModTime(), matches: matches}
	return matches
}
