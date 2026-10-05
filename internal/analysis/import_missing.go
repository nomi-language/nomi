package analysis

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// importMiss is an import path the loader has no file for.
type importMiss struct {
	// root is the directory the path was resolved against: the project
	// root, a dependency's root, or the standard library's.
	root string
	// path is the path below root, without the module head a self-name or
	// cross-module import starts with.
	path []string
	// dep is the cross-module short name the import started with, or "".
	dep string
	// std is set for a `std/...` import.
	std bool
}

// reportMissingModule records the error for an import whose file part (the
// path segments naming the file, written as segs) resolved to no file. A nil
// miss reports nothing.
func (b *builder) reportMissingModule(n *ast.ImportStmt, segs []ast.Node, miss *importMiss) {
	if miss == nil || len(segs) == 0 || len(miss.path) == 0 {
		return
	}
	written := make([]string, len(segs))
	for i, seg := range segs {
		written[i] = ast.ImportNodeName(seg)
	}
	name := strings.Join(written, "/")
	leaf := miss.path[len(miss.path)-1]
	file := leaf + ".nomi"

	var msg, hint string
	switch {
	case miss.std:
		rel := strings.Join(miss.path, "/")
		msg = fmt.Sprintf("no module `%s`: the standard library has no %s.nomi", name, rel)
		if best := closestName(rel, b.stdlibModuleNames()); best != "" {
			hint = "did you mean 'std/" + best + "'?"
		}
	default:
		dir := filepath.Join(append([]string{miss.root}, miss.path[:len(miss.path)-1]...)...)
		switch {
		case miss.dep != "":
			msg = fmt.Sprintf("no module `%s`: module `%s` has no file %s", name, miss.dep,
				filepath.ToSlash(filepath.Join(append(miss.path[:len(miss.path)-1:len(miss.path)-1], file)...)))
		case miss.root == "":
			msg = fmt.Sprintf("no module `%s`: no file %s", name, file)
		default:
			msg = fmt.Sprintf("no module `%s`: no file %s in %s", name, file, b.describeDir(dir))
		}
		if miss.root != "" {
			hint = missingModuleHint(dir, leaf, written)
		}
	}

	e := TypeError{Line: n.Line, Col: n.Col, Message: msg}
	if first, ok := spanOf(segs[0]); ok {
		e.Line, e.Col, e.EndLine, e.EndCol = first.StartLine, first.StartCol, first.EndLine, first.EndCol
		if last, ok := spanOf(segs[len(segs)-1]); ok {
			e.EndLine, e.EndCol = last.EndLine, last.EndCol
		}
	}
	b.file.TypeErrors = append(b.file.TypeErrors, e.WithHint(hint))
}

// describeDir names dir for a diagnostic in the file being built: relative
// to that file's directory, or "this file's directory" when it is that
// directory.
func (b *builder) describeDir(dir string) string {
	base := b.projectRoot
	if b.file != nil && b.file.FilePath != "" {
		base = filepath.Dir(b.file.FilePath)
	}
	rel, err := filepath.Rel(base, dir)
	if err != nil {
		return dir
	}
	if rel == "." {
		return "this file's directory"
	}
	return filepath.ToSlash(rel) + "/"
}

// missingModuleHint is the hint for an import of leaf, a file missing from
// dir, written as the path segments written: that leaf is a directory, or
// the sibling file its name is a misspelling of.
func missingModuleHint(dir, leaf string, written []string) string {
	spell := func(name string) string {
		return strings.Join(append(append([]string(nil), written[:len(written)-1]...), name), "/")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".nomi") && !strings.HasSuffix(e.Name(), "_test.nomi") {
			files = append(files, strings.TrimSuffix(e.Name(), ".nomi"))
		}
	}
	if info, err := os.Stat(filepath.Join(dir, leaf)); err == nil && info.IsDir() {
		inner, _ := os.ReadDir(filepath.Join(dir, leaf))
		for _, e := range inner {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".nomi") && !strings.HasSuffix(e.Name(), "_test.nomi") {
				return fmt.Sprintf("`%s` is a directory; import a file in it, such as `%s/%s`",
					spell(leaf), spell(leaf), strings.TrimSuffix(e.Name(), ".nomi"))
			}
		}
		return fmt.Sprintf("`%s` is a directory, and an import names a .nomi file", spell(leaf))
	}
	if best := closestName(leaf, files); best != "" {
		return "did you mean '" + spell(best) + "'?"
	}
	return ""
}

// stdlibModuleNames are the standard library's module paths below `std/`.
func (b *builder) stdlibModuleNames() []string {
	names := make([]string, 0, len(b.stdlibFAs))
	for name := range b.stdlibFAs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
