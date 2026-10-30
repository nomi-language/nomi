package vmhost

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/nomi-language/nomi/internal/virtualproject"
)

// LoadMarked loads a tour-style source holding `// FILE: <name>` markers as
// the tour's runner runs it, and answers whether its entry declares tests.
// Its data files seed an in-memory file system its std/fs calls reach
// (WithFileSystem), so every run starts from the files the source shows. A
// source with no markers is a single-file program named "main", and one
// whose sections are one module is loaded from memory (SplitMultiFile).
//
// A source that names modules, one with a `go.mod` section (a root module
// that requires and replaces a module of its own under it, such as a
// generator's), is a project of several modules, which resolve through the
// files the front end reads from disk: go.mod's requirements, each module's
// nomi.toml and the catalogs it reads. So it is written as
// those files under dir, which is emptied first, and loaded from there as
// the same files written by hand would be. A diagnostic names a file by its
// path from dir, as the source marks it, and so does a run-time failure (a
// `todo` reached, a timed-out case's stack). The browser build passes a
// directory of the in-memory file system its worker gives Go
// (tour/src/lib/go-memfs.js); a test passes a temporary one.
//
// A source may instead hold several projects side by side, each a
// top-level directory with a go.mod of its own (virtualproject's
// projectsDir): the first section's directory is the project, entered at
// its entry, and the others are modules its go.mod replaces
// (`../csvdata`). Its run's relative paths start at the project's
// directory, as `nomi run` there has them. A diagnostic still names a file
// by its path from dir (`rates/tables.nomi`, `csvdata/csvdata.nomi`), as
// the block's tabs name it, rather than from the project
// (`../csvdata/csvdata.nomi`).
//
// The block's tests are every Nomi file's that declares them, as `nomi
// test` runs a project's: the entry's, and each other test file's
// (virtualproject.Project.TestFiles), loaded entered at that file, whose
// cases the program's Cases answers after the entry's. hasTests reports
// whether any file declares tests.
func LoadMarked(src, dir string, opts ...Option) (p *Program, hasTests bool, err error) {
	project, err := virtualproject.FromMarkedSource(src)
	if err != nil {
		return nil, false, err
	}
	hasTests, err = SourceContainsTests(project.EntrySource)
	if err != nil {
		return nil, false, err
	}
	p, root, err := loadMarkedProject(project, dir, "", opts)
	if err != nil {
		return nil, false, err
	}
	for _, name := range project.TestFiles {
		tp, err := virtualproject.FromMarkedSourceAt(src, name)
		if err != nil {
			return nil, false, err
		}
		t, _, err := loadMarkedProject(tp, dir, root, opts)
		if err != nil {
			return nil, false, err
		}
		p.blockTests = append(p.blockTests, t)
	}
	return p, hasTests || len(project.TestFiles) > 0, nil
}

// loadMarkedProject loads a block's project at its entry. A project that
// names modules is written under dir, unless root is where an earlier load
// of the same block wrote it, and answers the root it is loaded from.
func loadMarkedProject(project virtualproject.Project, dir, root string, opts []Option) (*Program, string, error) {
	opts = append([]Option{WithFileSystem(blockFiles(project))}, opts...)
	if project.Files == nil {
		opts = append([]Option{WithVirtualFiles(project.VirtualFiles), WithVirtualManifest(project.Manifest)}, opts...)
		p, err := LoadSource(project.EntryName, project.EntrySource, opts...)
		return p, "", err
	}
	if root == "" {
		var err error
		if root, err = project.Write(dir); err != nil {
			return nil, "", err
		}
	}
	p, err := Load(filepath.Join(root, filepath.FromSlash(project.EntryName)), opts...)
	if err != nil {
		return nil, "", &underRoot{err: err, root: root}
	}
	p.fileNames = func(path string) string { return pathFromRoot(path, root) }
	return p, root, nil
}

// pathFromRoot is how a run of a project written under root names one of
// its files: by its slash-separated path from root, as the source marks it
// and its diagnostics name it. A file outside root (a stdlib module) is
// named by its path from the working directory, as any run names it.
func pathFromRoot(path, root string) string {
	if rel, err := filepath.Rel(root, path); err == nil && filepath.IsAbs(path) && rel != ".." &&
		!strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return filepath.ToSlash(rel)
	}
	return DisplayPath(path)
}

// underRoot is a load error of a project written under root, whose text
// names each file by its path from root.
type underRoot struct {
	err  error
	root string
}

func (e *underRoot) Error() string { return fromRoot(e.err.Error(), e.root) }

// fromRoot is text with each file under root named by its path from root.
// The front end names a file by its path from the working directory when it
// is under it, and by its absolute path otherwise, so both prefixes go,
// each where a path starts.
func fromRoot(text, root string) string {
	prefixes := []string{root + string(filepath.Separator)}
	if wd, err := os.Getwd(); err == nil {
		if rel, err := filepath.Rel(wd, root); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
			prefixes = append(prefixes, rel+string(filepath.Separator))
		}
	}
	for _, prefix := range prefixes {
		text = regexp.MustCompile(`(^|[\s"'(\x60])`+regexp.QuoteMeta(prefix)).ReplaceAllString(text, "${1}")
	}
	return text
}

func (e *underRoot) Unwrap() error { return e.err }
