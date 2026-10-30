package vmhost

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/virtualproject"
)

// ToolMarked runs `nomi tool <module> <name>` in the project a marked
// source names (one with a `go.mod` section, as LoadMarked writes it under
// dir), and answers the text of each of the source's files the tool
// changed, by its path (ReplaceSections puts them in a source). The tool is
// tools/<name>.nomi at the root of the module named module, the project's
// own or one its go.mod requires, run with no arguments; what it prints
// goes to out, and its failure is the error.
//
// A tool runs in the project's root, as `nomi tool` runs in the current
// directory: its std/fs calls reach an in-memory file system holding the
// project's files, with the root as its working directory, and its Go
// bindings reach the files as written under dir, through the process's
// working directory, which ToolMarked changes to the root for the run and
// back. So it is for a process that runs one thing at a time, such as the
// tour's worker; a file the tool writes reaches the source only through
// std/fs. A file it writes that the source does not hold, or one in a
// module of the project other than its root (a directory with a go.mod of
// its own), is left out.
//
// In a source of sibling projects (LoadMarked), the tool runs in the
// project's directory (`rates/`), as a reader runs `nomi tool` there, and
// the files it may write are that project's; they come back by their
// paths from the source's root (`rates/tables.nomi`).
func ToolMarked(src, dir, module, name string, out io.Writer, opts ...Option) (map[string]string, error) {
	project, err := virtualproject.FromMarkedSource(src)
	if err != nil {
		return nil, err
	}
	if project.Files == nil {
		return nil, fmt.Errorf("a tool runs in a project whose go.mod requires its module: mark a `// FILE: go.mod` section")
	}
	root, err := project.Write(dir)
	if err != nil {
		return nil, err
	}
	projectRoot := filepath.Join(root, filepath.FromSlash(project.Dir))
	path, err := toolFile(projectRoot, module, name)
	if err != nil {
		return nil, err
	}
	files := projectFiles(project, project.Files)
	wd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	if err := os.Chdir(projectRoot); err != nil {
		return nil, err
	}
	defer os.Chdir(wd)
	p, err := Load(path, append(opts, WithFileSystem(files))...)
	if err != nil {
		return nil, &underRoot{err: err, root: root}
	}
	if err := p.Run(context.Background(), out, nil, false); err != nil {
		return nil, err
	}
	written := map[string]string{}
	for name, data := range files.Files() {
		if old, ok := project.Files[name]; ok && old != string(data) && !inSubmodule(project.Files, project.Dir, name) {
			written[name] = string(data)
		}
	}
	return written, nil
}

// toolFile is tools/<name>.nomi of the module named module, seen from the
// project at root.
func toolFile(root, module, name string) (string, error) {
	if name == "" || strings.Contains(name, "..") || strings.ContainsAny(name, `\`) || filepath.IsAbs(name) {
		return "", fmt.Errorf("%q is not a tool name", name)
	}
	modRoot := ""
	if m, err := analysis.LoadManifest(root); err == nil && m.Name == module {
		modRoot = root
	} else {
		index, err := analysis.BuildModuleIndex(root)
		if err != nil {
			return "", err
		}
		modRoot = index[module]
	}
	if modRoot == "" {
		return "", fmt.Errorf("no module %s: it is neither this module nor one its go.mod requires", module)
	}
	path := filepath.Join(modRoot, "tools", filepath.FromSlash(name)+".nomi")
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("module %s has no tool %s: there is no tools/%s.nomi", module, name, name)
	}
	return path, nil
}

// inSubmodule reports whether the file name is in a module other than the
// project's root, the block's root or its project directory dir: outside
// dir, or under a directory below it that holds a go.mod of its own.
func inSubmodule(files map[string]string, dir, name string) bool {
	stop := "."
	if dir != "" {
		if !strings.HasPrefix(name, dir+"/") {
			return true
		}
		stop = dir
	}
	for d := filepath.ToSlash(filepath.Dir(name)); d != stop && d != "." && d != "/"; d = filepath.ToSlash(filepath.Dir(d)) {
		if _, ok := files[d+"/go.mod"]; ok {
			return true
		}
	}
	return false
}

// ReplaceSections is the marked source src with the text of each section
// files names, by its path, replaced by its text there.
func ReplaceSections(src string, files map[string]string) string {
	if len(files) == 0 {
		return src
	}
	var b strings.Builder
	lines := strings.SplitAfter(src, "\n")
	for i := 0; i < len(lines); i++ {
		b.WriteString(lines[i])
		name, marked := markedName(lines[i])
		text, replaced := files[name]
		if !marked || !replaced {
			continue
		}
		// Skip the section's old lines, keeping the blank line that ends
		// it before the next marker.
		j := i + 1
		for j < len(lines) {
			if _, next := markedName(lines[j]); next {
				break
			}
			j++
		}
		b.WriteString(text)
		if !strings.HasSuffix(text, "\n") {
			b.WriteString("\n")
		}
		if j < len(lines) {
			b.WriteString("\n")
		}
		i = j - 1
	}
	return b.String()
}

// markedName is the path a `// FILE:` line marks, cleaned as the project
// names its files.
func markedName(line string) (string, bool) {
	names := virtualproject.FileNames(strings.TrimRight(line, "\n"))
	if len(names) != 1 {
		return "", false
	}
	return filepath.ToSlash(filepath.Clean(names[0])), true
}
