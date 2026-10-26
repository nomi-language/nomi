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

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/gotoolchain"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

const initUsage = "usage: nomi init [module path]"

// manifestText is the nomi.toml `nomi new` and `nomi init` write: the
// module's name and its entry points.
func manifestText(name string, entries []string) string {
	quoted := make([]string, len(entries))
	for i, e := range entries {
		quoted[i] = fmt.Sprintf("%q", e)
	}
	return fmt.Sprintf("[module]\nname = %q\nentry_points = [%s]\n", name, strings.Join(quoted, ", "))
}

// goModText is the go.mod `nomi new` and `nomi init` write: the module
// path and the go line of the Go release nomi pins.
func goModText(path string) string {
	return fmt.Sprintf("module %s\n\ngo %s\n", path, gotoolchain.GoLine())
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
// project by writing what it lacks of nomi.toml and go.mod, never
// replacing either. The module path is the go.mod's when there is one, and
// the argument otherwise: a module path is the module's address, which the
// directory's name does not give. nomi.toml's entry_points lists every file
// of the module with a top-level `fn main`, or `main` when none has one.
// It writes no source file.
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
	hasManifest := isRegular("nomi.toml")
	hasGoMod := isRegular("go.mod")
	if hasManifest && hasGoMod {
		return errors.New("nomi init: this directory is already a Nomi project: it holds nomi.toml and go.mod")
	}
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
		what := "this directory"
		if hasManifest {
			what = "this directory's nomi.toml"
		}
		return fmt.Errorf("nomi init: give the module path %s needs for its go.mod, such as `nomi init github.com/you/project`; "+
			"a module path is usually its repository's address", what)
	}
	name, err := checkModulePath("init", path)
	if err != nil {
		return err
	}
	var wrote, kept []string
	if hasManifest {
		kept = append(kept, "nomi.toml")
	} else {
		entries, err := entryFiles(".")
		if err != nil {
			return fmt.Errorf("nomi init: %w", err)
		}
		if err := os.WriteFile("nomi.toml", []byte(manifestText(name, entries)), 0o644); err != nil {
			return fmt.Errorf("nomi init: %w", err)
		}
		wrote = append(wrote, "nomi.toml")
	}
	if hasGoMod {
		kept = append(kept, "go.mod")
	} else {
		if err := os.WriteFile("go.mod", []byte(goModText(path)), 0o644); err != nil {
			return fmt.Errorf("nomi init: %w", err)
		}
		wrote = append(wrote, "go.mod")
	}
	fmt.Printf("wrote %s\n", joinAnd(wrote))
	if len(kept) > 0 {
		fmt.Printf("kept %s, which was already there\n", kept[0])
	}
	if !hasManifest {
		if entries, err := entryFiles("."); err == nil && isRegular(entries[0]+".nomi") {
			fmt.Printf("next: nomi run %s.nomi\n", entries[0])
		}
	}
	return nil
}

// entryFiles is the module-relative name, without .nomi, of every file
// under root with a top-level `fn main`, the names entry_points must list,
// or ["main"] when there is none. A test file, a hidden directory and a
// directory with a nomi.toml of its own (another module) are skipped.
func entryFiles(root string) ([]string, error) {
	var entries []string
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
	if len(entries) == 0 {
		return []string{"main"}, nil
	}
	sort.Strings(entries)
	return entries, nil
}
