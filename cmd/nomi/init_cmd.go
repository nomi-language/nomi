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
	"github.com/nomi-language/nomi/internal/modcache"
	"github.com/nomi-language/nomi/internal/modfetch"
	"github.com/nomi-language/nomi/internal/parser"
)

const initUsage = "usage: nomi init [name | module path]"

// manifestText is the nomi.toml `nomi new` and `nomi init` write: the
// module's name, its path when it has one, and its entry points.
func manifestText(name, path string, entries []string) string {
	quoted := make([]string, len(entries))
	for i, e := range entries {
		quoted[i] = fmt.Sprintf("%q", e)
	}
	text := fmt.Sprintf("[module]\nname = %q\n", name)
	if path != "" {
		text += fmt.Sprintf("path = %q\n", path)
	}
	return text + fmt.Sprintf("entry_points = [%s]\n", strings.Join(quoted, ", "))
}

// moduleArg reads the argument of `nomi new` and `nomi init`, before
// either writes anything: a module's name alone, for a module nobody
// downloads, or the module path it is published at, whose last element
// gives its name (moduleName). An argument with no `.` and no `/` is a
// name. It answers the name and the path, "" for a name alone, by the
// rules nomi.toml is read with: a name is one an import path can start
// with (modcache.IsModuleName), and a path is a module path
// (modcache.ParseModuleFile).
func moduleArg(cmd, arg string) (name, path string, err error) {
	if arg == "std" {
		return "", "", fmt.Errorf("nomi %s: std is the standard library's name; choose another", cmd)
	}
	if !strings.ContainsAny(arg, "./") {
		if !modcache.IsModuleName(arg) {
			return "", "", fmt.Errorf("nomi %s: %q is not a module name: a name is lowercase letters, digits and _, not starting with a digit, such as hello_world; "+
				"a module others download is named by the path it is published at, such as github.com/you/hello_world", cmd, arg)
		}
		return arg, "", nil
	}
	if err := module.CheckPath(arg); err != nil {
		return "", "", fmt.Errorf("nomi %s: %q is not a module path: %v; a module path is usually its repository's address, such as github.com/you/project", cmd, arg, err)
	}
	name = moduleName(arg)
	if !modcache.IsModuleName(name) || name == "std" {
		return "", "", fmt.Errorf("nomi %s: %s gives no usable module name (its last element names the module); choose another path", cmd, arg)
	}
	return name, arg, nil
}

// runInit is `nomi init [name | module path]`: make the current directory a
// Nomi project by writing its nomi.toml, never replacing one. The module's
// name or path is the argument, else the `module` line of a go.mod already
// there (moduleArg).
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
	arg := ""
	if len(pos) == 1 {
		arg = pos[0]
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
		if arg != "" && arg != own {
			return fmt.Errorf("nomi init: go.mod names module %s, not %s; run `nomi init` with no path to keep %s", own, arg, own)
		}
		arg = own
	}
	if arg == "" {
		return errors.New("nomi init: give the module's name, such as `nomi init hello`, or the module path it is published at, " +
			"such as `nomi init github.com/you/hello`")
	}
	name, path, err := moduleArg("init", arg)
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
	if !wroteGoMod && hasGoMod && path != "" {
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
