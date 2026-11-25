package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/buildversion"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/internal/pkgcache"
)

const initUsage = "usage: nomi init <name>"

// manifestText is the nomi.toml `nomi new` and `nomi init` write: the
// module's name, the release it needs, which is the running release
// (buildversion.Release) and is left out by a `nomi` built from source, and
// its entry points.
func manifestText(name, release string, entries []string) string {
	quoted := make([]string, len(entries))
	for i, e := range entries {
		quoted[i] = fmt.Sprintf("%q", e)
	}
	needs := ""
	if release != "" {
		needs = fmt.Sprintf("nomi = %q\n", release)
	}
	return fmt.Sprintf("[package]\nname = %q\n%sentry_points = [%s]\n", name, needs, strings.Join(quoted, ", "))
}

// packageArg reads the argument of `nomi new` and `nomi init`, before
// either writes anything: the package's name, by the rule nomi.toml is
// read with, a name an import path can start with
// (pkgcache.IsPackageName). A package does not name where it is published:
// a project that depends on it names its repository.
func packageArg(cmd, arg string) (string, error) {
	switch {
	case arg == "std":
		return "", fmt.Errorf("nomi %s: std is the standard library's name; choose another", cmd)
	case !pkgcache.IsPackageName(arg):
		hint := ""
		if strings.Contains(arg, "/") {
			hint = "; a package names no repository: a project that depends on it names the repository it is published in"
		}
		return "", fmt.Errorf("nomi %s: %q is not a package name: a name is lowercase letters, digits and _, not starting with a digit, such as hello_world%s", cmd, arg, hint)
	}
	return arg, nil
}

// runInit is `nomi init <name>`: make the current directory a Nomi project
// by writing its nomi.toml, never replacing one. nomi.toml's entry_points
// lists every file of the module with a top-level `fn main` other than its
// tools, which are entry points without being listed, or `main` when none
// has one. It writes no source file. Under a workspace that does not list
// the directory yet, it adds it to the workspace's members
// (joinWorkspace); at a workspace's root it refuses.
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
	if err := workspaceRootError("init", ".", "a package goes in a directory of its own, as its members do"); err != nil {
		return err
	}
	if len(pos) == 0 {
		return errors.New("nomi init: give the package's name, such as `nomi init hello`")
	}
	name, err := packageArg("init", pos[0])
	if err != nil {
		return err
	}
	entries, err := entryFiles(".")
	if err != nil {
		return fmt.Errorf("nomi init: %w", err)
	}
	if err := os.WriteFile("nomi.toml", []byte(manifestText(name, buildversion.Release(), entries)), 0o644); err != nil {
		return fmt.Errorf("nomi init: %w", err)
	}
	fmt.Println("wrote nomi.toml")
	if err := joinWorkspace("init", "."); err != nil {
		return err
	}
	if len(entries) > 0 && isRegular(entries[0]+".nomi") {
		fmt.Printf("next: nomi run %s.nomi\n", entries[0])
	}
	return nil
}

// entryFiles is the package-relative name, without .nomi, of every file
// under root with a top-level `fn main`, the names entry_points must list,
// or ["main"] when there is none and root has no tools. A tool
// (analysis.IsToolFile), a test file, a hidden directory and a directory
// with a nomi.toml of its own (another package) are skipped.
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
