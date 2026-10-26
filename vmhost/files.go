package vmhost

import (
	"sort"
	"strings"

	"github.com/nomi-language/nomi/internal/stdio"
	"github.com/nomi-language/nomi/internal/virtualproject"
)

// MemoryFiles is an in-memory file system a program's std/fs calls reach
// instead of the disk: the one the browser build gives every program, and
// the one the tour runs each block against. Its working directory is
// `/work`, which relative paths resolve against, and `/tmp` exists. It is
// internal/stdio's Memory, the same file system `fs_testing.run` uses.
type MemoryFiles struct{ m *stdio.Memory }

// NewMemoryFiles is an in-memory file system holding files, by path; a
// relative path is under `/work`, and the directories above each file
// exist.
func NewMemoryFiles(files map[string][]byte) *MemoryFiles {
	m := stdio.NewMemory("/work")
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		m.Put(p, files[p])
	}
	return &MemoryFiles{m: m}
}

// Files is every file it holds now, by path: relative to `/work` for one
// under it, absolute for any other.
func (f *MemoryFiles) Files() map[string][]byte { return f.m.Relative() }

// Read is each file whose contents a run opened, once each, in the order
// first read, named as Files names it. A failed open and an operation
// that reads no contents (a listing, metadata) are not reads.
func (f *MemoryFiles) Read() []string {
	prefix := strings.TrimSuffix(f.m.Cwd(), "/") + "/"
	read := f.m.Read()
	for i, p := range read {
		if rel, under := strings.CutPrefix(p, prefix); under {
			read[i] = rel
		}
	}
	return read
}

// WithFileSystem makes the program's std/fs calls reach files rather than
// the disk, for every run of the program. `fs_testing.run` still replaces it
// for its own call.
func WithFileSystem(files *MemoryFiles) Option {
	return func(c *config) { c.files = files }
}

// DataFiles are the files a marked source's `// FILE: <name>` sections hold
// that are not Nomi sources, `nomi.toml` or message catalogs (`notes.txt`,
// `data/prices.csv`), by name: what a tour block seeds its in-memory file
// system with (SplitMultiFile answers the rest).
func DataFiles(src string) (map[string]string, error) {
	project, err := virtualproject.FromMarkedSource(src)
	if err != nil {
		return nil, err
	}
	return project.DataFiles, nil
}

// DataFileBytes is files as NewMemoryFiles takes them.
func DataFileBytes(files map[string]string) map[string][]byte {
	out := make(map[string][]byte, len(files))
	for name, text := range files {
		out[name] = []byte(text)
	}
	return out
}
