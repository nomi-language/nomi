package vmhost

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/nomi-language/nomi/internal/format"
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
// macro's), is a project of several modules, which resolve through the
// files the front end reads from disk: go.mod's requirements, each module's
// nomi.toml, a macro's file and the inputs it reads. So it is written as
// those files under dir, which is emptied first, and loaded from there as
// the same files written by hand would be. A diagnostic names a file by its
// path from dir, as the source marks it, and so does a run-time failure (a
// `todo` reached, a timed-out case's stack). The browser build passes a
// directory of the in-memory file system its worker gives Go
// (tour/src/lib/go-memfs.js); a test passes a temporary one.
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
	opts = append([]Option{WithFileSystem(NewMemoryFiles(DataFileBytes(project.DataFiles)))}, opts...)
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

// ExpandMarked answers what `nomi expand` prints for the first section of a
// marked source that names modules, a tour block whose output is the
// expansion of its first tab: the source is written under dir as
// LoadMarked writes it, and the file is expanded as Expansions expands it
// and printed as FormatExpansions prints it, each file named by its path
// from dir.
func ExpandMarked(src, dir string) (string, error) {
	project, err := virtualproject.FromMarkedSource(src)
	if err != nil {
		return "", err
	}
	if project.Files == nil {
		return "", errors.New("a macro runs only in a project whose go.mod requires its module: mark a `// FILE: go.mod` section")
	}
	if !strings.HasSuffix(project.First, ".nomi") {
		return "", fmt.Errorf("the first section, %s, is not a Nomi file", project.First)
	}
	root, err := project.Write(dir)
	if err != nil {
		return "", err
	}
	path := filepath.Join(root, filepath.FromSlash(project.First))
	exps, err := Expansions(path)
	if err != nil {
		return "", &underRoot{err: err, root: root}
	}
	return fromRoot(FormatExpansions(project.First, exps), root), nil
}

// FormatExpansions is what `nomi expand <file>` prints for file's
// expansions: each declaration's keyword, name and macro, what the macro
// reported, and its source, formatted.
func FormatExpansions(file string, exps []Expansion) string {
	if len(exps) == 0 {
		return fmt.Sprintf("// %s declares nothing a macro expands\n", file)
	}
	var out strings.Builder
	for i, x := range exps {
		if i > 0 {
			out.WriteString("\n")
		}
		fmt.Fprintf(&out, "// %s %s (line %d), expanded by the macro %s of module %s\n", x.Keyword, x.Name, x.Line, x.Macro, x.Module)
		for _, e := range x.Errors {
			fmt.Fprintf(&out, "// error: %s\n", e)
		}
		if x.Source == "" {
			continue
		}
		src := x.Source
		if formatted, err := format.Format(src); err == nil {
			src = formatted
		}
		out.WriteString(src)
		if !strings.HasSuffix(src, "\n") {
			out.WriteString("\n")
		}
	}
	return out.String()
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
