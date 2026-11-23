package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"

	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/modcache"
	"github.com/nomi-language/nomi/internal/modfetch"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/internal/runners"
	"github.com/nomi-language/nomi/vmhost"
)

// offlineFlag is `--offline`, which `run`, `test`, `check`, `build`,
// `fetch`, `add`, `types` and `effects` take: no module is downloaded,
// and a module the cache lacks is an error that names it.
const offlineFlag = "--offline"

// allCallsFlag is `--all-calls`, which `run`, `test`, `check` and `build`
// take: a refusal by nomi.toml's [effects] tables gives every call,
// not the first of each name (vmhost.AllCallsEnv).
const allCallsFlag = "--all-calls"

// takeFlag removes flag, a flag that takes no value, from c's arguments,
// and reports whether it was there. For a command with a program's
// arguments (`run`), only the flags before the program's file are the
// command's; the rest are the program's own.
func takeFlag(c command, flag string, args []string) ([]string, bool) {
	found := false
	out := make([]string, 0, len(args))
	for i, arg := range args {
		if c.programArgs && !isFlag(arg) {
			return append(out, args[i:]...), found
		}
		if arg == flag {
			found = true
			continue
		}
		out = append(out, arg)
	}
	return out, found
}

// enableDownloads lets this command download the Nomi modules a project's
// nomi.toml names and theirs (internal/modfetch, run before the front end
// reads the module cache). Offline, it refuses and names what is missing,
// and the go command a `nomi` built from source runs to build a runner is
// held to its module cache with GOPROXY=off. With libNote, each project's
// fetch first notes the lib/ modules off this release (noteLibRelease).
func enableDownloads(offline, libNote bool, log io.Writer) {
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
		if libNote {
			noteLibRelease(log, root)
		}
		_, err := modfetch.Fetch(root, modfetch.Options{Offline: offline, Log: moduleLog})
		return err
	})
}

// libRelease is the version a release `nomi` takes the first-party lib/
// modules at: the release it was cut as (runners.ReleaseVersion) when that
// is a release's semantic version, else "". A `nomi` built from source has
// none, and neither does one stamped with a pseudo-version or with `git
// describe`'s name for a commit past a tag (v0.5.0-3-gabc1234, -dirty);
// those take the lib/ modules at their latest commit.
func libRelease() string {
	v := runners.ReleaseVersion
	if !semver.IsValid(v) || semver.Canonical(v) != v || module.IsPseudoVersion(v) || describedCommit.MatchString(v) {
		return ""
	}
	return v
}

// describedCommit matches the suffix `git describe --tags --dirty` gives a
// commit that is not a tag, or a tree with changes.
var describedCommit = regexp.MustCompile(`-[0-9]+-g[0-9a-f]+(-dirty)?$|-dirty$`)

// noteLibRelease writes one line to w naming the lib/ modules nomi.lock in
// the project at root locks at another version than this release takes
// them at (modfetch.LibOffRelease), and the command that moves them, or
// nothing for a `nomi` built from source or a project with none. `run`,
// `test`, `check` and `build` call it before the project's first fetch,
// so it comes before any diagnostic.
func noteLibRelease(w io.Writer, root string) {
	release := libRelease()
	if release == "" {
		return
	}
	off, err := modfetch.LibOffRelease(root, release)
	if err != nil || len(off) == 0 {
		return
	}
	mods := make([]string, len(off))
	for i, m := range off {
		mods[i] = m.Path + " " + m.Version
	}
	fmt.Fprintf(w, "note: nomi.lock takes %s, and this nomi is release %s, which takes the first-party libraries at %s; run `nomi update` to move them to it\n",
		joinAnd(mods), release, release)
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

const fetchUsage = "usage: nomi fetch [--offline] [dir]"

// runFetch is `nomi fetch`: everything the project needs, so that a later
// `--offline` command succeeds. It downloads the modules the project needs
// and writes nomi.lock, their versions and hashes.
func runFetch(args []string, offline bool) error {
	dir := "."
	for _, arg := range args {
		if isFlag(arg) {
			return unknownFlag("nomi fetch", arg, fetchUsage)
		}
		if dir != "." {
			return fmt.Errorf("nomi fetch: unexpected argument %q\n%s", arg, fetchUsage)
		}
		dir = arg
	}
	root, err := manifestRoot("fetch", dir)
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
	fmt.Printf("%d module%s in the module cache for %s\n", n, plural(n), vmDisplay(root))
	noteUnreadGoFiles(os.Stderr, root)
	return nil
}

const addUsage = "usage: nomi add [--offline] [<name>=]<module>[@version] | [<name>=]<directory>"

// isLocalPath reports whether `nomi add`'s argument names a directory rather
// than a module path: a module path never starts with a dot or a slash.
func isLocalPath(arg string) bool {
	return arg == "." || arg == ".." || filepath.IsAbs(arg) ||
		strings.HasPrefix(arg, "./") || strings.HasPrefix(arg, "../") ||
		strings.HasPrefix(arg, `.\`) || strings.HasPrefix(arg, `..\`)
}

// runAdd is `nomi add <module>[@version]`: name the module in nomi.toml's
// [dependencies] at that version (its latest release without one),
// download it and what the project then needs, and write nomi.lock. `nomi
// add <directory>` names the Nomi module in that directory with a dir
// entry. The entry's name, the name imports use, is the module's own
// [module].name, or the name before `=` (`nomi add db=example.com/sqlite`).
// Then it prints what the module's own files reach, and what nomi.toml's
// [effects.deps] table needs for it (dependencyReach).
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
	root, err := manifestRoot("add", ".")
	if err != nil {
		return err
	}
	name, arg := "", mods[0]
	if n, rest, ok := strings.Cut(arg, "="); ok && !strings.ContainsAny(n, `/\.@`) {
		name, arg = n, rest
	}
	opts := modfetch.Options{Offline: offline, Log: os.Stderr, Release: libRelease()}
	var added modfetch.Added
	if isLocalPath(arg) {
		added, err = modfetch.AddLocal(root, name, arg, opts)
	} else {
		added, err = modfetch.Add(root, name, arg, opts)
	}
	if err != nil {
		return fmt.Errorf("nomi add: %w", err)
	}
	if added.Local != "" {
		fmt.Printf("added %s from %s (import %s)\n", added.Name, added.Local, added.Name)
	} else {
		fmt.Printf("added %s, %s %s (import %s)\n", added.Name, added.Module.Path, added.Module.Version, added.Name)
	}
	d := &dependencyReach{root: root}
	d.reachAfter()
	d.write(os.Stdout, []string{added.Name}, func(r *vmhost.Reach) []string { return addToTable(r, added.Name) })
	noteUnreadGoFiles(os.Stderr, root)
	return nil
}

const removeUsage = "usage: nomi remove <name>"

// runRemove is `nomi remove <name>`: take the dependency's entry out of
// nomi.toml's [dependencies], and write nomi.lock without it and the
// modules only it needed. It takes the entry's name or its module path, and
// refuses a dependency a Nomi file of the project still imports, naming the
// file.
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
	root, err := manifestRoot("remove", ".")
	if err != nil {
		return err
	}
	req, err := requiredModule(root, mods[0])
	if err != nil {
		return fmt.Errorf("nomi remove: %w", err)
	}
	if use, err := moduleUse(root, req); err != nil {
		return fmt.Errorf("nomi remove: %w", err)
	} else if use != "" {
		return fmt.Errorf("nomi remove: %s; remove the import first", use)
	}
	removed, err := modfetch.Remove(root, req.Name, modfetch.Options{Log: os.Stderr})
	if err != nil {
		return fmt.Errorf("nomi remove: %w", err)
	}
	switch e := removed.Entry; {
	case e.Dir != "":
		fmt.Printf("removed %s (from %s)\n", e.Name, e.Dir)
	default:
		fmt.Printf("removed %s (%s %s)\n", e.Name, e.Module, e.Version)
	}
	for _, m := range removed.Unneeded {
		if m.Path != removed.Entry.Module {
			fmt.Printf("removed %s %s, which nothing else needs\n", m.Path, m.Version)
		}
	}
	noteUnreadGoFiles(os.Stderr, root)
	return nil
}

// requiredModule answers the entry of the project's [dependencies] that
// arg names: the entry's name, or the one entry whose module path is arg.
func requiredModule(root, arg string) (modcache.Requirement, error) {
	mf, err := modcache.ReadModuleFile(root)
	if err != nil {
		return modcache.Requirement{}, err
	}
	if mf == nil {
		return modcache.Requirement{}, fmt.Errorf("no nomi.toml in %s", vmDisplay(root))
	}
	if r, ok := mf.Require(arg); ok {
		return r, nil
	}
	for _, r := range mf.Requires {
		if r.Module == arg {
			return r, nil
		}
	}
	return modcache.Requirement{}, fmt.Errorf("nomi.toml has no dependency %s", arg)
}

// moduleUse answers where a Nomi file of the project at root uses the
// dependency req, as "main.nomi:3 imports csvdata", or "" when none does:
// an import whose first name is the dependency's name. Directories holding
// a nomi.toml of their own are other modules and are not read.
func moduleUse(root string, req modcache.Requirement) (string, error) {
	name := req.Name
	var uses []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != root && (strings.HasPrefix(d.Name(), ".") || d.Name() == "node_modules" || isRegular(filepath.Join(p, modcache.ManifestName))) {
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
			}
			for _, imp := range imports {
				if len(imp.ModulePath) > 0 && ast.ImportNodeName(imp.ModulePath[0]) == name {
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

const updateUsage = "usage: nomi update [name]"

// runUpdate is `nomi update [name]`: move one entry of nomi.toml's
// [dependencies], or every one, to its latest release within its module
// path, and print each module of the build list that changed, then what
// each changed Nomi module's own files reach and how that changed
// (dependencyReach).
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
	root, err := manifestRoot("update", ".")
	if err != nil {
		return err
	}
	only := ""
	if len(mods) == 1 {
		req, err := requiredModule(root, mods[0])
		if err != nil {
			return fmt.Errorf("nomi update: %w", err)
		}
		only = req.Name
	}
	// The reach before is worked out only to compare; an update goes ahead
	// whether or not the project checks.
	d := &dependencyReach{root: root}
	d.before, _ = reachOf(root, nil)
	changes, err := modfetch.Update(root, only, modfetch.Options{Log: os.Stderr, Release: libRelease()})
	if err != nil {
		return fmt.Errorf("nomi update: %w", err)
	}
	defer noteUnreadGoFiles(os.Stderr, root)
	if len(changes) == 0 {
		if only != "" {
			fmt.Printf("%s is at its latest release\n", only)
		} else {
			fmt.Println("every dependency is at its latest release")
		}
		return nil
	}
	paths := make([]string, len(changes))
	for i, c := range changes {
		paths[i] = c.Path
		switch {
		case c.From == "":
			fmt.Printf("%s %s (added: an updated module requires it)\n", c.Path, c.To)
			continue
		case c.To == "":
			fmt.Printf("%s %s (removed: nothing needs it any more)\n", c.Path, c.From)
			continue
		}
		fmt.Printf("%s %s -> %s\n", c.Path, c.From, c.To)
	}
	if nomi := nomiModules(root, paths); len(nomi) > 0 {
		d.reachAfter()
		d.write(os.Stdout, nomi, nil)
	}
	return nil
}

const newUsage = "usage: nomi new <name | module path> [dir]"

// runNew is `nomi new <name | module path> [dir]`: start a Nomi module in
// dir (default: a new directory named for the module) with a nomi.toml
// naming the module and its path, if it has one (moduleArg), the go.mod nomi
// keeps for that path (modfetch.WriteGoMod), a main.nomi that prints hello
// and an AGENTS.md for coding agents, keeping either of the last two, and a
// go.mod of Go code, already there. It refuses a directory that holds a
// nomi.toml, which is a project already. It writes nothing until its
// arguments are checked.
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
	name, path, err := moduleArg("new", pos[0])
	if err != nil {
		return err
	}
	dir := name
	if len(pos) == 2 {
		dir = pos[1]
	}
	if isRegular(filepath.Join(dir, modcache.ManifestName)) {
		return fmt.Errorf("nomi new: %s already holds a nomi.toml, so it is a project already", dir)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("nomi new: %w", err)
	}
	files := []struct{ name, text string }{
		{"nomi.toml", manifestText(name, path, []string{"main"})},
		{"main.nomi", "import std::io\n\nfn main()\n    io.print(\"hello\")\nend\n"},
		{"AGENTS.md", agentsText(name, path)},
	}
	var wrote, kept []string
	for _, f := range files {
		target := filepath.Join(dir, f.name)
		if _, err := os.Stat(target); err == nil {
			kept = append(kept, f.name)
			continue
		}
		if err := os.WriteFile(target, []byte(f.text), 0o644); err != nil {
			return fmt.Errorf("nomi new: %w", err)
		}
		wrote = append(wrote, filepath.ToSlash(filepath.Join(dir, f.name)))
	}
	if wroteGoMod, err := modfetch.WriteGoMod(dir); err != nil {
		return fmt.Errorf("nomi new: %w", err)
	} else if wroteGoMod {
		wrote = append(wrote, filepath.ToSlash(filepath.Join(dir, modfetch.GoModName)))
	} else if path != "" && isRegular(filepath.Join(dir, modfetch.GoModName)) {
		kept = append(kept, modfetch.GoModName)
	}
	fmt.Printf("wrote %s\n", joinAnd(wrote))
	if len(kept) > 0 {
		fmt.Printf("kept %s, which was already there\n", joinAnd(kept))
	}
	run := "nomi run main.nomi"
	if abs, err := filepath.Abs(dir); err == nil {
		if wd, err := os.Getwd(); err == nil && !same(abs, wd) {
			run = "cd " + dir + " && " + run
		}
	}
	fmt.Printf("next: %s\n", run)
	noteUnreadGoFiles(os.Stderr, dir)
	return nil
}

// agentsText is the AGENTS.md nomi new writes for a coding agent, for the
// module name and its path, "" for none. It names the commands and links
// the cheat sheet, docs/cheatsheet.md as the site publishes it, rather than
// holding a copy of its rules.
func agentsText(name, path string) string {
	goMod := ""
	if path != "" {
		goMod = "- `go.mod` holds only the module line, which `nomi` writes from `nomi.toml` so Go's module proxy can serve the module. Do not edit it.\n"
	}
	return "# " + name + "\n\n" +
		"This is a Nomi project. Nomi is a statically typed language where every value is immutable. " +
		"It looks like Rust, Go and Elixir in places, and code written from their habits does not compile: " +
		"there is no `let`, no `x.method()`, no `for` or `while`, no null and no exceptions, and a block closes with `end`.\n\n" +
		"Read the cheat sheet before writing Nomi: https://nomi-lang.org/cheatsheet.md. " +
		"The whole language and standard library are in one file at https://nomi-lang.org/llms-full.txt.\n\n" +
		"## Commands\n\n" +
		"- `nomi run .` runs the program, the one entry point `nomi.toml` lists. With several, name the file: `nomi run main.nomi`.\n" +
		"- `nomi test` runs every `test` block under this directory.\n" +
		"- `nomi check .` reports errors without running. `--format json` writes them as JSON Lines, with fixes to apply.\n" +
		"- `nomi fmt -w .` formats every file. The formatter owns layout.\n" +
		"- `nomi add <module>` adds a dependency to `nomi.toml` and `nomi.lock`.\n" +
		"- `nomi mcp` serves the compiler to a coding agent over the Model Context Protocol: check, types, definitions, std search, tests and formatting.\n\n" +
		"## Layout\n\n" +
		"- `nomi.toml` names the module, lists its entry points, the files with a `fn main`, and names its dependencies.\n" +
		goMod +
		"- `main.nomi` is the program's entry point.\n" +
		"- Tests sit beside the code they test, in `test \"name\"` blocks closed by `end`.\n"
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

// manifestRoot is the directory of the nomi.toml at or above dir, the
// project `nomi fetch`, `add`, `remove` and `update` work on.
func manifestRoot(cmd, dir string) (string, error) {
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
		if isRegular(filepath.Join(d, modcache.ManifestName)) {
			return d, nil
		}
		parent := filepath.Dir(d)
		if parent == d {
			break
		}
		d = parent
	}
	return "", fmt.Errorf("nomi %s: no nomi.toml in %s or above it, so there is no project to change; "+
		"start one with `nomi new <name>`, or make this directory one with `nomi init <name>`", cmd, vmDisplay(abs))
}

// goModFailure is the go.mod beside the nomi.toml of the module that holds
// path, and why `nomi check` fails on it (modfetch.GoModProblem): it is
// missing, or not the text nomi writes for the module's [module].path. The
// error is nil when nomi keeps no go.mod there or the one there is current.
func goModFailure(path string) (string, error) {
	root := moduleOf(path)
	if root == "" {
		return "", nil
	}
	// A nomi.toml it cannot read is an error the check of the files reports
	// at its line.
	problem, err := modfetch.GoModProblem(root)
	if err != nil || problem == "" {
		return "", nil
	}
	return filepath.Join(root, modfetch.GoModName), errors.New(problem)
}

// noteUnreadGoFiles writes one line to w naming the go.mod and go.sum
// beside the nomi.toml of the module that holds path that nothing reads
// (modfetch.UnreadGoFiles), or nothing when there are none. `nomi check`
// and the commands that write a module's nomi.toml, nomi.lock or go.mod
// call it once each.
func noteUnreadGoFiles(w io.Writer, path string) {
	root := moduleOf(path)
	if root == "" {
		return
	}
	names, err := modfetch.UnreadGoFiles(root)
	if err != nil || len(names) == 0 {
		return
	}
	files := make([]string, len(names))
	for i, name := range names {
		files[i] = vmDisplay(filepath.Join(root, name))
	}
	verb, them := "is", "it"
	if len(files) > 1 {
		verb, them = "are", "they"
	}
	fmt.Fprintf(w, "note: %s %s not read: nomi.toml and nomi.lock hold this module's dependencies, and it has no Go code, so %s can be deleted\n",
		joinAnd(files), verb, them)
}

// moduleOf is the directory of the nomi.toml of the module that holds
// path, a file or a directory, or "" when there is none.
func moduleOf(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return ""
	}
	if info, err := os.Stat(abs); err == nil && !info.IsDir() {
		abs = filepath.Dir(abs)
	}
	return moduleRootOf(abs)
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
