package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/modfetch"
	"github.com/nomi-language/nomi/internal/parser"
)

const initUsage = "usage: nomi init [module path]"

// manifestText is the nomi.toml `nomi new` and `nomi init` write: the
// module's name, its path and its entry points.
func manifestText(name, path string, entries []string) string {
	quoted := make([]string, len(entries))
	for i, e := range entries {
		quoted[i] = fmt.Sprintf("%q", e)
	}
	return fmt.Sprintf("[module]\nname = %q\npath = %q\nentry_points = [%s]\n", name, path, strings.Join(quoted, ", "))
}

// checkModulePath refuses a path that is not a module path or whose last
// element gives no module name, and answers the name.
func checkModulePath(cmd, path string) (string, error) {
	if err := module.CheckImportPath(path); err != nil {
		return "", fmt.Errorf("nomi %s: %q is not a module path: %v; a module path is usually its repository's address, such as github.com/you/project", cmd, path, err)
	}
	name := moduleName(path)
	if name == "" || name == "std" {
		return "", fmt.Errorf("nomi %s: %s gives no usable module name (its last element names the module); choose another path", cmd, path)
	}
	return name, nil
}

// runInit is `nomi init [module path]`: make the current directory a Nomi
// project by writing its nomi.toml, never replacing one. The module path is
// the argument, else the `module` line of a go.mod already there: a module
// path is the module's address, which the directory's name does not give.
// nomi.toml's entry_points lists every file of the module with a top-level
// `fn main` other than its tools, which are entry points without being
// listed, or `main` when none has one. It writes the go.mod nomi keeps
// (modfetch.WriteGoMod) and no source file.
func runInit(args []string) error {
	var pos []string
	for _, arg := range args {
		if isFlag(arg) {
			return unknownFlag("nomi init", arg, initUsage)
		}
		pos = append(pos, arg)
	}
	if len(pos) > 1 {
		return errors.New(initUsage)
	}
	if isRegular("nomi.toml") {
		return errors.New("nomi init: this directory is already a Nomi project: it holds nomi.toml")
	}
	hasGoMod := isRegular("go.mod")
	path := ""
	if len(pos) == 1 {
		path = pos[0]
	}
	if hasGoMod {
		data, err := os.ReadFile("go.mod")
		if err != nil {
			return fmt.Errorf("nomi init: %w", err)
		}
		own := modfile.ModulePath(data)
		if own == "" {
			return errors.New("nomi init: go.mod has no `module` line")
		}
		if path != "" && path != own {
			return fmt.Errorf("nomi init: go.mod names module %s, not %s; run `nomi init` with no path to keep %s", own, path, own)
		}
		path = own
	}
	if path == "" {
		return errors.New("nomi init: give the module path this directory needs, such as `nomi init github.com/you/project`; " +
			"a module path is usually its repository's address")
	}
	name, err := checkModulePath("init", path)
	if err != nil {
		return err
	}
	entries, err := entryFiles(".")
	if err != nil {
		return fmt.Errorf("nomi init: %w", err)
	}
	if err := os.WriteFile("nomi.toml", []byte(manifestText(name, path, entries)), 0o644); err != nil {
		return fmt.Errorf("nomi init: %w", err)
	}
	wrote := []string{"nomi.toml"}
	wroteGoMod, err := modfetch.WriteGoMod(".")
	if err != nil {
		return fmt.Errorf("nomi init: %w", err)
	}
	if wroteGoMod {
		wrote = append(wrote, "go.mod")
	}
	fmt.Printf("wrote %s\n", joinAnd(wrote))
	if !wroteGoMod && hasGoMod {
		fmt.Println("kept go.mod, which was already there")
	}
	if len(entries) > 0 && isRegular(entries[0]+".nomi") {
		fmt.Printf("next: nomi run %s.nomi\n", entries[0])
	}
	noteUnreadGoFiles(os.Stderr, ".")
	return nil
}

// entryFiles is the module-relative name, without .nomi, of every file
// under root with a top-level `fn main`, the names entry_points must list,
// or ["main"] when there is none and root has no tools. A tool
// (analysis.IsToolFile), a test file, a hidden directory and a directory
// with a nomi.toml of its own (another module) are skipped.
func entryFiles(root string) ([]string, error) {
	var entries []string
	hasTools := false
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != root && (strings.HasPrefix(d.Name(), ".") || d.Name() == "node_modules" || isRegular(filepath.Join(p, "nomi.toml"))) {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(p) != ".nomi" || strings.HasSuffix(p, "_test.nomi") {
			return nil
		}
		if rel, err := filepath.Rel(root, p); err == nil && analysis.IsToolFile(strings.TrimSuffix(filepath.ToSlash(rel), ".nomi")) {
			hasTools = true
			return nil
		}
		src, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		nodes, _ := parser.ParseWithRecovery(lexer.Lex(string(src)))
		for _, n := range nodes {
			if fn, ok := n.(*ast.FuncDef); ok && fn.Name == "main" {
				rel, err := filepath.Rel(root, p)
				if err != nil {
					return err
				}
				entries = append(entries, strings.TrimSuffix(filepath.ToSlash(rel), ".nomi"))
				break
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 && !hasTools {
		return []string{"main"}, nil
	}
	sort.Strings(entries)
	return entries, nil
}
