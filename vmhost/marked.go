package vmhost

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/nomi-language/nomi/internal/analyzedlowering"
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
//
// A block is loaded for its run and its tests at once, so the load holds
// it to its nomi.toml's [capabilities] lists as `nomi check` does
// (checkedRoots): main, the boot and the exports to std and go, and each
// test file's cases to those with std_in_tests and go_in_tests. A block
// over its lists fails
// to load, with the diagnostics at the calls, as a block that does not
// compile does; the tour shows it as a compile error.
func LoadMarked(src, dir string, opts ...Option) (p *Program, hasTests bool, err error) {
	project, err := virtualproject.FromMarkedSource(src)
	if err != nil {
		return nil, false, err
	}
	hasTests, err = SourceContainsTests(project.EntrySource)
	if err != nil {
		return nil, false, err
	}
	p, root, err := loadMarkedProject(project, dir, "", opts, true)
	if err != nil {
		return nil, false, err
	}
	for _, name := range project.TestFiles {
		tp, err := virtualproject.FromMarkedSourceAt(src, name)
		if err != nil {
			return nil, false, err
		}
		t, _, err := loadMarkedProject(tp, dir, root, opts, true)
		if err != nil {
			return nil, false, err
		}
		p.blockTests = append(p.blockTests, t)
	}
	return p, hasTests || len(project.TestFiles) > 0, nil
}

// loadMarkedProject loads a block's project at its entry, held to its
// lists when hold is set (held). A project that names modules is written
// under dir, unless root is where an earlier load of the same block wrote
// it, and answers the root it is loaded from.
func loadMarkedProject(project virtualproject.Project, dir, root string, opts []Option, hold bool) (*Program, string, error) {
	load := func(p *Program, err error) (*Program, error) {
		if hold {
			return held(p, err)
		}
		return p, err
	}
	opts = append([]Option{WithFileSystem(blockFiles(project))}, opts...)
	if project.Files == nil {
		opts = append([]Option{WithVirtualFiles(project.VirtualFiles), WithVirtualManifest(project.Manifest), WithVirtualData(project.DataFiles)}, opts...)
		p, err := load(LoadSource(project.EntryName, project.EntrySource, opts...))
		return p, "", err
	}
	if root == "" {
		var err error
		if root, err = project.Write(dir); err != nil {
			return nil, "", err
		}
	}
	p, err := load(Load(filepath.Join(root, filepath.FromSlash(project.EntryName)), opts...))
	if err != nil {
		return nil, "", &underRoot{err: err, root: root}
	}
	p.fileNames = func(path string) string { return pathFromRoot(path, root) }
	return p, root, nil
}

// ReachedCall is one outside call a program reaches, as an editor's hover
// shows it (analyzedlowering.Reached).
type ReachedCall = analyzedlowering.Reached

// ReachedMarked is each outside call the entry of a tour-style source
// reaches from the roots `nomi check` holds to its nomi.toml's lists, as an
// editor's hover shows it (reachedCalls, hoverdoc.AppendReach), each with
// its file named as the block's tabs name it. The block is loaded as
// LoadMarked loads it, but not held to its lists, so a call they refuse is
// shown refused; its other test files are not loaded.
func ReachedMarked(src, dir string, opts ...Option) ([]ReachedCall, error) {
	project, err := virtualproject.FromMarkedSource(src)
	if err != nil {
		return nil, err
	}
	p, _, err := loadMarkedProject(project, dir, "", opts, false)
	if err != nil {
		return nil, err
	}
	calls := p.reachedCalls(p.walks(p.checkedRoots()))
	for i := range calls {
		if p.fileNames != nil {
			calls[i].Path = p.fileNames(calls[i].Path)
		}
	}
	return calls, nil
}

// held is a block's program once its nomi.toml's [capabilities] lists
// allow what its run and its tests reach (LoadMarked).
func held(p *Program, err error) (*Program, error) {
	if err != nil {
		return nil, err
	}
	if err := p.capabilityError(p.checkedRoots()...); err != nil {
		return nil, err
	}
	return p, nil
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

// fromRoot is text with each file under root named by its path from root,
// with `/` as the source marks it. The front end names a file by its path
// from the working directory when it is under it, and by its absolute path
// otherwise, so both prefixes go, each where a path starts.
func fromRoot(text, root string) string {
	var rel string
	if wd, err := os.Getwd(); err == nil {
		if r, err := filepath.Rel(wd, root); err == nil && r != "." && !strings.HasPrefix(r, "..") {
			rel = r
		}
	}
	return fromRootSep(text, root, rel, filepath.Separator)
}

// fromRootSep is fromRoot for a system whose separator is sep, with rel the
// root's path from the working directory ("" when it has none): the path
// after either prefix, up to a space, a colon or a quote, is written with
// `/`.
func fromRootSep(text, root, rel string, sep byte) string {
	prefixes := []string{root + string(sep)}
	if rel != "" {
		prefixes = append(prefixes, rel+string(sep))
	}
	for _, prefix := range prefixes {
		re := regexp.MustCompile(`(^|[\s"'(\x60])` + regexp.QuoteMeta(prefix) + `([^\s:"'()\x60]*)`)
		text = re.ReplaceAllStringFunc(text, func(m string) string {
			parts := re.FindStringSubmatch(m)
			return parts[1] + strings.ReplaceAll(parts[2], string(sep), "/")
		})
	}
	return text
}

func (e *underRoot) Unwrap() error { return e.err }

// LoadFailed reports whether err is a failure to load a project's program,
// its diagnostics (ToolMarked's when the tool's program does not compile),
// rather than a failure while the program ran: the tour shows a program
// that did not compile apart from one that stopped.
func LoadFailed(err error) bool {
	var u *underRoot
	return errors.As(err, &u)
}
