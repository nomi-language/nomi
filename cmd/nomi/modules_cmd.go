package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/mod/module"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ffirun"
	"github.com/nomi-language/nomi/internal/gotoolchain"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/modcache"
	"github.com/nomi-language/nomi/internal/modfetch"
	"github.com/nomi-language/nomi/internal/parser"
)

// offlineFlag is `--offline`, which `run`, `test`, `check`, `build`,
// `fetch` and `add` take: nothing is downloaded, and a module or toolchain
// the caches lack is an error that names it.
const offlineFlag = "--offline"

// takeOffline removes --offline from a command's arguments. For `run` only
// the flags before the program's file are the command's; the rest are the
// program's own.
func takeOffline(cmd string, args []string) ([]string, bool) {
	offline := false
	out := make([]string, 0, len(args))
	for i, arg := range args {
		if cmd == "run" && !isFlag(arg) {
			return append(out, args[i:]...), offline
		}
		if arg == offlineFlag {
			offline = true
			continue
		}
		out = append(out, arg)
	}
	return out, offline
}

// enableDownloads lets this command download what a project needs: the Nomi
// and Go modules its go.mod requires (internal/modfetch, run before the
// front end reads the module cache), and the managed Go toolchain for Go
// bindings. Offline, both refuse and name what is missing, and the go
// command, for a wrapper build, is held to its cache with GOPROXY=off.
func enableDownloads(offline bool, log io.Writer) {
	gotoolchain.AllowDownload(log)
	gotoolchain.SetOffline(offline)
	if offline {
		_ = os.Setenv("GOPROXY", "off")
	}
	// A module download during `run`, `test`, `check` or `build` is named
	// on a terminal only, so a program's captured stderr carries only what
	// it wrote. `nomi add` and `nomi fetch` always name theirs.
	var moduleLog io.Writer
	if isTerminal(os.Stderr) {
		moduleLog = log
	}
	modcache.SetFetcher(func(root string) error {
		_, err := modfetch.Fetch(root, modfetch.Options{Offline: offline, Log: moduleLog})
		return err
	})
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

const fetchUsage = "usage: nomi fetch [--offline] [--target <goos>/<goarch>]... [dir]"

// runFetch is `nomi fetch`: everything the project needs, so that a later
// `--offline` command succeeds. It downloads the modules go.mod requires
// (adding to go.mod what a Nomi dependency requires and to go.sum each
// module's checksums), and, for a project with Go bindings, the managed Go
// toolchain when there is no `go`, the compiler module and the Go modules the
// bindings' wrapper builds with, and the runner of each --target.
func runFetch(args []string, offline bool) error {
	dir := "."
	var targets []ffirun.Target
	for i := 0; i < len(args); i++ {
		arg := args[i]
		value, isTarget := strings.CutPrefix(arg, "--target=")
		if arg == "--target" {
			if i+1 >= len(args) {
				return fmt.Errorf("nomi fetch: --target needs <goos>/<goarch>\n%s", fetchUsage)
			}
			i++
			value, isTarget = args[i], true
		}
		if isTarget {
			goos, goarch, ok := strings.Cut(value, "/")
			if !ok || goos == "" || goarch == "" {
				return fmt.Errorf("nomi fetch: --target %q is not <goos>/<goarch>\n%s", value, fetchUsage)
			}
			targets = append(targets, ffirun.ResolveTarget(goos, goarch))
			continue
		}
		if isFlag(arg) {
			return unknownFlag("nomi fetch", arg, fetchUsage)
		}
		if dir != "." {
			return fmt.Errorf("nomi fetch: unexpected argument %q\n%s", arg, fetchUsage)
		}
		dir = arg
	}
	root, err := goModRoot("fetch", dir)
	if err != nil {
		return err
	}
	g, err := modfetch.Fetch(root, modfetch.Options{Offline: offline, Update: true, Log: os.Stderr})
	if err != nil {
		return fmt.Errorf("nomi fetch: %w", err)
	}
	n := 0
	if g != nil {
		for _, d := range g.Deps {
			if !d.Local {
				n++
			}
		}
	}
	if err := ffirun.Fetch(root, targets); err != nil {
		return fmt.Errorf("nomi fetch: %w", err)
	}
	fmt.Printf("%d module%s in the module cache for %s\n", n, plural(n), vmDisplay(root))
	return nil
}

const addUsage = "usage: nomi add [--offline] <module>[@version] | <directory>"

// isLocalPath reports whether `nomi add`'s argument names a directory rather
// than a module path: a module path never starts with a dot or a slash.
func isLocalPath(arg string) bool {
	return arg == "." || arg == ".." || filepath.IsAbs(arg) ||
		strings.HasPrefix(arg, "./") || strings.HasPrefix(arg, "../") ||
		strings.HasPrefix(arg, `.\`) || strings.HasPrefix(arg, `..\`)
}

// runAdd is `nomi add <module>[@version]`: require the module in go.mod
// (its latest release without a version), download it and what the project
// then needs, and record their checksums in go.sum. `nomi add <directory>`
// requires the Nomi module in that directory and replaces it with the
// directory.
func runAdd(args []string, offline bool) error {
	var mods []string
	for _, arg := range args {
		if isFlag(arg) {
			return unknownFlag("nomi add", arg, addUsage)
		}
		mods = append(mods, arg)
	}
	if len(mods) != 1 {
		return errors.New(addUsage)
	}
	root, err := goModRoot("add", ".")
	if err != nil {
		return err
	}
	opts := modfetch.Options{Offline: offline, Log: os.Stderr}
	var added modfetch.Added
	if isLocalPath(mods[0]) {
		added, err = modfetch.AddLocal(root, mods[0], opts)
	} else {
		added, err = modfetch.Add(root, mods[0], opts)
	}
	if err != nil {
		return fmt.Errorf("nomi add: %w", err)
	}
	what := "a Go module"
	if added.Dir != "" {
		if m, err := analysis.LoadManifest(added.Dir); err == nil {
			what = "module " + m.Name
			if base := filepath.Join(added.Dir, m.Name+".nomi"); isRegular(base) {
				what += "; import " + m.Name
			}
		}
	}
	if added.Replace != "" {
		fmt.Printf("added %s from %s (%s)\n", added.Module.Path, added.Replace, what)
		return nil
	}
	fmt.Printf("added %s %s (%s)\n", added.Module.Path, added.Module.Version, what)
	return nil
}

const removeUsage = "usage: nomi remove <module>"

// runRemove is `nomi remove <module>`: take the module's requirement, and a
// replace of it, out of go.mod, with the indirect requirements only it
// needed, and their lines out of go.sum. It refuses a module a Nomi file of
// the project still imports, naming the file.
func runRemove(args []string) error {
	var mods []string
	for _, arg := range args {
		if isFlag(arg) {
			return unknownFlag("nomi remove", arg, removeUsage)
		}
		mods = append(mods, arg)
	}
	if len(mods) != 1 {
		return errors.New(removeUsage)
	}
	root, err := goModRoot("remove", ".")
	if err != nil {
		return err
	}
	path, err := requiredModule(root, mods[0])
	if err != nil {
		return fmt.Errorf("nomi remove: %w", err)
	}
	if use, err := moduleUse(root, path); err != nil {
		return fmt.Errorf("nomi remove: %w", err)
	} else if use != "" {
		return fmt.Errorf("nomi remove: %s; remove the import first", use)
	}
	removed, err := modfetch.Remove(root, path)
	if err != nil {
		return fmt.Errorf("nomi remove: %w", err)
	}
	switch {
	case removed.Replace != "":
		fmt.Printf("removed %s (from %s)\n", removed.Module.Path, removed.Replace)
	default:
		fmt.Printf("removed %s %s\n", removed.Module.Path, removed.Module.Version)
	}
	for _, m := range removed.Unneeded {
		fmt.Printf("removed %s %s, which nothing else needs\n", m.Path, m.Version)
	}
	return nil
}

// requiredModule answers the module path go.mod requires that arg names:
// the path itself, or the one requirement whose last path element, or
// Nomi module name, is arg.
func requiredModule(root, arg string) (string, error) {
	g, err := modcache.Walk(root, cachedModule)
	if err != nil {
		return "", err
	}
	if g == nil {
		return "", fmt.Errorf("no go.mod in %s", vmDisplay(root))
	}
	for _, r := range g.GoMod.Require {
		if r.Mod.Path == arg {
			return arg, nil
		}
	}
	for _, r := range g.GoMod.Replace {
		if r.Old.Path == arg {
			return arg, nil
		}
	}
	var named []string
	for _, r := range g.GoMod.Require {
		name := modcache.PathBase(r.Mod.Path)
		if d, ok := g.Find(r.Mod.Path); ok && d.Nomi {
			if m, err := analysis.LoadManifest(d.Dir); err == nil {
				name = m.Name
			}
		}
		if name == arg {
			named = append(named, r.Mod.Path)
		}
	}
	switch len(named) {
	case 1:
		return named[0], nil
	case 0:
		return "", fmt.Errorf("go.mod does not require %s", arg)
	}
	return "", fmt.Errorf("%s names %s; write the one to remove", arg, strings.Join(named, " and "))
}

func cachedModule(m module.Version) (string, error) {
	cache, err := modcache.Root()
	if err != nil {
		return "", nil
	}
	return modcache.Cached(cache, m), nil
}

// moduleUse answers where a Nomi file of the project at root uses module
// path, as "main.nomi:3 imports csvdata", or "" when none does: an import
// whose first name is the module's Nomi name, or a `gopkg` of a Go package
// in the module. Directories holding a go.mod of their own are other
// modules and are not read.
func moduleUse(root, path string) (string, error) {
	name := modcache.PathBase(path)
	nomi := true
	g, err := modcache.Walk(root, cachedModule)
	if err != nil {
		return "", err
	}
	if g != nil {
		if d, ok := g.Find(path); ok && d.Dir != "" {
			nomi = d.Nomi
			if m, err := analysis.LoadManifest(d.Dir); err == nil {
				name = m.Name
			}
		}
	}
	var uses []string
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != root && (strings.HasPrefix(d.Name(), ".") || d.Name() == "node_modules" || isRegular(filepath.Join(p, "go.mod"))) {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(p) != ".nomi" {
			return nil
		}
		src, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		nodes, _ := parser.ParseWithRecovery(lexer.Lex(string(src)))
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		for _, n := range nodes {
			var imports []*ast.ImportStmt
			switch n := n.(type) {
			case *ast.ImportStmt:
				imports = append(imports, n)
			case *ast.ImportBlock:
				imports = append(imports, n.Entries...)
			case *ast.ExternPackage:
				if n.ImportPath == path || strings.HasPrefix(n.ImportPath, path+"/") {
					uses = append(uses, fmt.Sprintf("%s:%d binds Go package %q from it", rel, n.Line, n.ImportPath))
				}
			}
			for _, imp := range imports {
				if nomi && len(imp.ModulePath) > 0 && ast.ImportNodeName(imp.ModulePath[0]) == name {
					uses = append(uses, fmt.Sprintf("%s:%d imports %s", rel, imp.Line, name))
				}
			}
		}
		return nil
	})
	if err != nil || len(uses) == 0 {
		return "", err
	}
	if len(uses) == 1 {
		return uses[0], nil
	}
	return fmt.Sprintf("%s (and %d more use%s)", uses[0], len(uses)-1, plural(len(uses)-1)), nil
}

const updateUsage = "usage: nomi update [module]"

// runUpdate is `nomi update [module]`: move one requirement, or every one,
// to its latest release within its module path, and print each change.
func runUpdate(args []string) error {
	var mods []string
	for _, arg := range args {
		if isFlag(arg) {
			return unknownFlag("nomi update", arg, updateUsage)
		}
		mods = append(mods, arg)
	}
	if len(mods) > 1 {
		return errors.New(updateUsage)
	}
	root, err := goModRoot("update", ".")
	if err != nil {
		return err
	}
	only := ""
	if len(mods) == 1 {
		if only, err = requiredModule(root, mods[0]); err != nil {
			return fmt.Errorf("nomi update: %w", err)
		}
	}
	changes, err := modfetch.Update(root, only, modfetch.Options{Log: os.Stderr})
	if err != nil {
		return fmt.Errorf("nomi update: %w", err)
	}
	if len(changes) == 0 {
		if only != "" {
			fmt.Printf("%s is at its latest release\n", only)
		} else {
			fmt.Println("every requirement is at its latest release")
		}
		return nil
	}
	for _, c := range changes {
		if c.From == "" {
			fmt.Printf("%s %s (added: an updated module requires it)\n", c.Path, c.To)
			continue
		}
		fmt.Printf("%s %s -> %s\n", c.Path, c.From, c.To)
	}
	return nil
}

const newUsage = "usage: nomi new <module path> [dir]"

// runNew is `nomi new <module path> [dir]`: start a Nomi module in dir
// (default: a new directory named for the path's last element) with a
// nomi.toml, a go.mod and a main.nomi that prints hello. It refuses a
// directory that already holds a go.mod. In a directory that holds a
// nomi.toml and no go.mod, it writes the go.mod alone, so a module can
// take dependencies.
func runNew(args []string) error {
	var pos []string
	for _, arg := range args {
		if isFlag(arg) {
			return unknownFlag("nomi new", arg, newUsage)
		}
		pos = append(pos, arg)
	}
	if len(pos) < 1 || len(pos) > 2 {
		return errors.New(newUsage)
	}
	path := pos[0]
	if err := module.CheckImportPath(path); err != nil {
		return fmt.Errorf("nomi new: %q is not a module path: %v; a module path is usually its repository's address, such as github.com/you/project", path, err)
	}
	name := moduleName(path)
	if name == "" || name == "std" {
		return fmt.Errorf("nomi new: %s gives no usable module name (its last element names the module); choose another path", path)
	}
	dir := name
	if len(pos) == 2 {
		dir = pos[1]
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("nomi new: %w", err)
	}
	if isRegular(filepath.Join(dir, "go.mod")) {
		return fmt.Errorf("nomi new: %s already holds a go.mod", dir)
	}
	files := []struct{ name, text string }{
		{"nomi.toml", fmt.Sprintf("[module]\nname = %q\nentry_points = [\"main\"]\n", name)},
		{"go.mod", fmt.Sprintf("module %s\n\ngo %s\n", path, gotoolchain.GoLine())},
		{"main.nomi", "import std/io\n\nfn main() {\n    io.print(\"hello\")\n}\n"},
	}
	var wrote, kept []string
	keptManifest := false
	for _, f := range files {
		target := filepath.Join(dir, f.name)
		if _, err := os.Stat(target); err == nil {
			if f.name == "nomi.toml" {
				keptManifest = true
			}
			kept = append(kept, f.name)
			continue
		}
		if keptManifest && f.name == "main.nomi" {
			continue
		}
		if err := os.WriteFile(target, []byte(f.text), 0o644); err != nil {
			return fmt.Errorf("nomi new: %w", err)
		}
		wrote = append(wrote, filepath.ToSlash(filepath.Join(dir, f.name)))
	}
	fmt.Printf("wrote %s\n", joinAnd(wrote))
	if len(kept) > 0 {
		verb := "were"
		if len(kept) == 1 {
			verb = "was"
		}
		fmt.Printf("kept %s, which %s already there\n", joinAnd(kept), verb)
	}
	run := "nomi run main.nomi"
	if keptManifest {
		run = "nomi add <module>"
	}
	if abs, err := filepath.Abs(dir); err == nil {
		if wd, err := os.Getwd(); err == nil && !same(abs, wd) {
			run = "cd " + dir + " && " + run
		}
	}
	fmt.Printf("next: %s\n", run)
	return nil
}

// moduleName is the Nomi module name nomi new gives module path: its last
// element without a /vN suffix, lowercased, with each character an
// identifier cannot hold written as `_`.
func moduleName(path string) string {
	base := strings.ToLower(modcache.PathBase(path))
	var b strings.Builder
	for i, r := range base {
		switch {
		case r >= 'a' && r <= 'z' || r == '_':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			if i == 0 {
				b.WriteString("m")
			}
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return strings.Trim(b.String(), "_")
}

func joinAnd(items []string) string {
	switch len(items) {
	case 0:
		return "nothing"
	case 1:
		return items[0]
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}

func same(a, b string) bool {
	ai, errA := os.Stat(a)
	bi, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(ai, bi)
}

// goModRoot is the directory of the go.mod at or above dir, which `nomi
// fetch`, `add`, `remove` and `update` work on.
func goModRoot(cmd, dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	if info, err := os.Stat(abs); err != nil {
		return "", fmt.Errorf("nomi %s: %w", cmd, pathError(dir, err))
	} else if !info.IsDir() {
		abs = filepath.Dir(abs)
	}
	for d := abs; ; {
		if isRegular(filepath.Join(d, "go.mod")) {
			return d, nil
		}
		parent := filepath.Dir(d)
		if parent == d {
			break
		}
		d = parent
	}
	return "", fmt.Errorf("nomi %s: no go.mod in %s or above it, so there is no project to change; "+
		"start one with `nomi new <module path>`, or run `nomi new <module path> .` in a module that has only a nomi.toml", cmd, vmDisplay(abs))
}

func isRegular(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func vmDisplay(path string) string {
	if wd, err := os.Getwd(); err == nil {
		if rel, err := filepath.Rel(wd, path); err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	return path
}
