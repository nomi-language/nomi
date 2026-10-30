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
// internal/stdio's Memory, the same file system `fs_testing.run` uses. It
// is a FileSystem, so an embedder can wrap it as it can DiskFiles.
type MemoryFiles struct {
	backendFiles
	m *stdio.Memory
	// root is the directory Files and Read name files from: `/work`, which
	// is the working directory too, except in a block of sibling
	// projects' file system (BlockFiles).
	root string
}

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
	return &MemoryFiles{backendFiles: backendFiles{m}, m: m, root: "/work"}
}

// BlockFiles is the in-memory file system a marked source's run starts
// from (LoadMarked): its data files under `/work`, by their paths from the
// block's root. Its working directory is `/work`, except in a block of
// sibling projects, where it is the project's directory under `/work`
// (`/work/rates`), as `nomi run` in that directory would have it. Files
// and Read still name a file by its path from the block's root.
func BlockFiles(src string) (*MemoryFiles, error) {
	project, err := virtualproject.FromMarkedSource(src)
	if err != nil {
		return nil, err
	}
	return blockFiles(project), nil
}

func blockFiles(project virtualproject.Project) *MemoryFiles {
	return projectFiles(project, project.DataFiles)
}

// projectFiles is files, by their paths from a block's root, under `/work`,
// with the working directory at the block's project.
func projectFiles(project virtualproject.Project, files map[string]string) *MemoryFiles {
	if project.Dir == "" {
		return NewMemoryFiles(DataFileBytes(files))
	}
	m := stdio.NewMemory("/work/" + project.Dir)
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		m.Put("/work/"+p, []byte(files[p]))
	}
	return &MemoryFiles{backendFiles: backendFiles{m}, m: m, root: "/work"}
}

// Files is every file it holds now, by path: relative to `/work` for one
// under it, absolute for any other.
func (f *MemoryFiles) Files() map[string][]byte {
	prefix := f.root + "/"
	out := map[string][]byte{}
	for p, data := range f.m.Files() {
		if rel, under := strings.CutPrefix(p, prefix); under {
			p = rel
		}
		out[p] = data
	}
	return out
}

// Read is each file whose contents a run opened, once each, in the order
// first read, named as Files names it. A failed open and an operation
// that reads no contents (a listing, metadata) are not reads.
func (f *MemoryFiles) Read() []string {
	prefix := f.root + "/"
	read := f.m.Read()
	for i, p := range read {
		if rel, under := strings.CutPrefix(p, prefix); under {
			read[i] = rel
		}
	}
	return read
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
