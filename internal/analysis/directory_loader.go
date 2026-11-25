package analysis

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// ErrNoSuchModule is what a FileLoader answers, wrapped, for a path it knows
// names neither a file nor a directory, so resolution asks the disk
// nothing: the embedded stdlib's loaders, for a path such as `option/Option`
// that an import of a type below its module tries first.
var ErrNoSuchModule = errors.New("no such module")

// DirectoryError is what a FileLoader answers for a path that names a
// directory and no file: `views/admin` when views/admin.nomi does not exist
// and views/admin/ does. Resolution reads it as a directory import (spec
// §14 *Directory imports*). A loader asked for an empty path answers the
// listing of the root it was given.
type DirectoryError struct {
	// Files are the directory's .nomi files, without `.nomi`, sorted.
	Files []string
	// Dirs are its subdirectories that are not packages of their own,
	// sorted.
	Dirs []string
	// Package is set when the directory holds a nomi.toml: it is another
	// package's root, which no path of this package reaches.
	Package bool
	// Dir is the directory's path on disk, "" for one held in memory.
	Dir string
}

func (e *DirectoryError) Error() string {
	if e.Package {
		return "a directory holding another package"
	}
	return "a directory, not a file"
}

// DiskDirectory answers the DirectoryError for modulePath below root on
// disk, or nil when it names no directory there. A loader that reads the
// disk returns it for a path whose file it does not find.
func DiskDirectory(root string, modulePath []string) *DirectoryError {
	if root == "" {
		return nil
	}
	dir := filepath.Join(append([]string{root}, modulePath...)...)
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	out := &DirectoryError{Dir: dir}
	for _, e := range entries {
		name := e.Name()
		switch {
		case e.IsDir():
			if strings.HasPrefix(name, ".") || isPackageRoot(filepath.Join(dir, name)) {
				continue
			}
			out.Dirs = append(out.Dirs, name)
		case strings.HasSuffix(name, ".nomi"):
			out.Files = append(out.Files, strings.TrimSuffix(name, ".nomi"))
		case name == "nomi.toml" && len(modulePath) > 0:
			out.Package = true
		}
	}
	return out
}

// isPackageRoot reports whether dir holds a nomi.toml.
func isPackageRoot(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "nomi.toml"))
	return err == nil
}

// VirtualDirectory answers the DirectoryError for modulePath among the
// module keys of an in-memory project (`views/admin/users`), or nil when no
// key is below it. manifest reports whether a key's directory holds a
// nomi.toml of its own; nil means none does.
func VirtualDirectory(modulePath []string, keys []string, manifest func(dir string) bool) *DirectoryError {
	prefix := strings.Join(modulePath, "/")
	if prefix != "" {
		prefix += "/"
	}
	files, dirs := map[string]bool{}, map[string]bool{}
	found := false
	for _, key := range keys {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		rest := strings.TrimPrefix(key, prefix)
		if rest == "" {
			continue
		}
		found = true
		if i := strings.IndexByte(rest, '/'); i >= 0 {
			dirs[rest[:i]] = true
			continue
		}
		files[rest] = true
	}
	if !found {
		return nil
	}
	out := &DirectoryError{}
	if manifest != nil && len(modulePath) > 0 && manifest(strings.TrimSuffix(prefix, "/")) {
		out.Package = true
	}
	for f := range files {
		out.Files = append(out.Files, f)
	}
	for d := range dirs {
		if manifest != nil && manifest(prefix+d) {
			continue
		}
		out.Dirs = append(out.Dirs, d)
	}
	slices.Sort(out.Files)
	slices.Sort(out.Dirs)
	return out
}
