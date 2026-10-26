package ffirun

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// unprovided explains a `gopkg` importPath, declared in nomiFile, that is
// not a Go standard library package and that no go.mod provides: why, in
// words that follow `Go package "x" is` and `gopkg "x":`, and the `nomi`
// command that fixes it.
//
// Most often the package is the author's own Go code beside the file, in a
// directory that is not a project yet: no go.mod names that code, so the
// fix is `nomi init`, not `nomi add`. A nomi.toml with no go.mod is half a
// project, which `nomi init` finishes. Code beside the file inside a go.mod
// that is another module's is either a package of that module or a project
// of its own, and the fix names both.
func unprovided(nomiFile, importPath string) (why, fix string) {
	dir := filepath.Dir(nomiFile)
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	goCode := holdsGoCode(dir)
	root, hasGoMod := findGoModRoot(dir)
	if !hasGoMod {
		manifest, hasManifest := findNomiManifestRoot(dir)
		where := "in the directory that holds nomi.toml"
		if hasManifest && samePath(manifest, dir) {
			where = "in this file's directory"
		}
		switch {
		case goCode && !hasManifest:
			return "not in the Go standard library, and the Go code beside this file is not in a Nomi project yet",
				fmt.Sprintf("run `nomi init %s` in this file's directory to make it one "+
					"(it writes nomi.toml, and a go.mod whose module path %s names that Go code)", importPath, importPath)
		case goCode:
			return "not in the Go standard library, and this project has a nomi.toml but no go.mod to name the Go code beside this file",
				fmt.Sprintf("run `nomi init %s` %s to add the go.mod", initPath(importPath, manifest, dir), where)
		case hasManifest:
			return "not in the Go standard library, and this project has a nomi.toml but no go.mod to require its module",
				fmt.Sprintf("run `nomi init <module path>` %s, then `nomi add %s`", where, importPath)
		}
		return "not in the Go standard library, and no go.mod above the declaring file provides it",
			fmt.Sprintf("make this file's directory a project with `nomi init <module path>`, then run `nomi add %s`", importPath)
	}
	modPath := ""
	if f, err := parseProjectGoMod(root); err == nil && f.Module != nil {
		modPath = f.Module.Mod.Path
	}
	why = "outside the project's own Go module " + modPath +
		", the Go standard library, and every module its go.mod requires or replaces"
	if !goCode || modPath == "" {
		return why, fmt.Sprintf("run `nomi add %s` to add its module", importPath)
	}
	own := modPath
	if rel, err := filepath.Rel(root, dir); err == nil && rel != "." {
		own = path.Join(modPath, filepath.ToSlash(rel))
	}
	if !samePath(root, dir) && !manifestBetween(dir, root) {
		return fmt.Sprintf("outside the Go module %s of the go.mod at %s, and the Go code beside this file is not in a Nomi project of its own", modPath, root),
			fmt.Sprintf("run `nomi init %s` in this file's directory to make it one, "+
				"or write `gopkg %q` to bind that Go code as a package of %s", importPath, own, modPath)
	}
	return why, fmt.Sprintf("the Go code beside this file is package %s: write `gopkg %q` to bind it, "+
		"or run `nomi add %s` if %s is a package of another module", own, own, importPath, importPath)
}

// initPath is the module path `nomi init` in manifestRoot needs for
// importPath to name the Go package in dir, or `<module path>` when
// importPath cannot.
func initPath(importPath, manifestRoot, dir string) string {
	rel, err := filepath.Rel(manifestRoot, dir)
	if err != nil {
		return "<module path>"
	}
	if rel == "." {
		return importPath
	}
	if trimmed, ok := strings.CutSuffix(importPath, "/"+filepath.ToSlash(rel)); ok && trimmed != "" {
		return trimmed
	}
	return "<module path>"
}

// holdsGoCode reports whether dir holds a Go source file other than a test.
func holdsGoCode(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() && strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go") {
			return true
		}
	}
	return false
}

// manifestBetween reports whether a nomi.toml is in dir, in root, or in a
// directory between them.
func manifestBetween(dir, root string) bool {
	for d := dir; ; {
		if isFile(filepath.Join(d, "nomi.toml")) {
			return true
		}
		if samePath(d, root) {
			return false
		}
		parent := filepath.Dir(d)
		if parent == d {
			return false
		}
		d = parent
	}
}
