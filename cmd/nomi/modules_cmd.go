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
	"text/tabwriter"

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

// enableDownloads lets this command fetch the Nomi modules a project's
// nomi.toml names and theirs (internal/modfetch, run before the front end
// reads the module cache). Offline, it refuses and names what is missing.
// GOPROXY=off is set then for the go command alone, which a `nomi` built
// from source runs to build a runner, so it stays within Go's module
// cache; modfetch reads no Go variable. With libNote, each project's
// fetch first notes the first-party libraries off this release
// (noteLibRelease).
func enableDownloads(offline, libNote bool, log io.Writer) {
	if offline {
		_ = os.Setenv("GOPROXY", "off")
	}
	// A module fetch during `run`, `test`, `check` or `build` is named
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

// release is this `nomi`'s release: the version it was cut as
// (runners.ReleaseVersion) when that is a tag's version, else "". A `nomi`
// built from source has none, and neither does one stamped with a Go
// pseudo-version or with `git describe`'s name for a commit past a tag
// (v0.5.0-3-gabc1234, -dirty). main records it for the module code
// (modcache.SetRelease): a release refuses a module that needs a later
// one, writes it into the nomi.toml `nomi new` and `nomi init` write, and
// takes the first-party libraries at it; one with none does none of that
// and takes the libraries at their repository's latest release.
func release() string {
	v := runners.ReleaseVersion
	if !modcache.IsVersion(v) || describedCommit.MatchString(v) {
		return ""
	}
	return v
}

// describedCommit matches the suffix `git describe --tags --dirty` gives a
// commit that is not a tag, or a tree with changes, and the suffix of a Go
// pseudo-version (v0.0.0-20261120000000-abcdefabcdef).
var describedCommit = regexp.MustCompile(`-[0-9]+-g[0-9a-f]+(-dirty)?$|-dirty$|[-.][0-9]{14}-[0-9a-f]{12}$`)

// noteLibRelease writes one line to w naming the first-party libraries
// nomi.lock in the project at root locks at another version than this
// release takes them at (modfetch.LibOffRelease), and the command that
// moves them, or nothing for a `nomi` built from source or a project with
// none. `run`, `test`, `check` and `build` call it before the project's
// first fetch, so it comes before any diagnostic.
func noteLibRelease(w io.Writer, root string) {
	release := modcache.Release()
	if release == "" {
		return
	}
	off, err := modfetch.LibOffRelease(root, release)
	if err != nil || len(off) == 0 {
		return
	}
	mods := make([]string, len(off))
	for i, m := range off {
		mods[i] = m.String()
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
// `--offline` command succeeds. It fetches the repositories of the modules
// the project needs and writes nomi.lock, their versions, commits and
// hashes.
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
	return nil
}

const addUsage = "usage: nomi add [--offline] [<name>=]<repository>[/<subdir>][@<version>|@<commit>] | [<name>=]<directory>"

// isLocalPath reports whether `nomi add`'s argument names a directory rather
// than a repository: a repository never starts with a dot or a slash.
func isLocalPath(arg string) bool {
	return arg == "." || arg == ".." || filepath.IsAbs(arg) ||
		strings.HasPrefix(arg, "./") || strings.HasPrefix(arg, "../") ||
		strings.HasPrefix(arg, `.\`) || strings.HasPrefix(arg, `..\`)
}

// runAdd is `nomi add <repository>[/<subdir>][@version]`: name the module
// in nomi.toml's [dependencies] at that version, or with `@<commit>`, a
// commit's 40 hex digits, at that commit, moving the project's
// other entries of the repository with it (without one, at the version the
// project already takes the repository at, else its latest release),
// fetch it and what the project then needs, and write nomi.lock. `nomi add <directory>` names the Nomi module in that
// directory with a dir entry. The entry's name, the name imports use, is
// the module's own [module].name, or the name before `=` (`nomi add
// db=github.com/owner/sqlite`).
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
	opts := modfetch.Options{Offline: offline, Log: os.Stderr}
	var added modfetch.Added
	if isLocalPath(arg) {
		added, err = modfetch.AddLocal(root, name, arg, opts)
	} else {
		added, err = modfetch.Add(root, name, arg, opts)
	}
	if err != nil {
		return fmt.Errorf("nomi add: %w", err)
	}
	switch {
	case added.Local != "":
		fmt.Printf("added %s from %s (import %s)\n", added.Name, added.Local, added.Name)
	case added.Held:
		fmt.Printf("added %s, %s (import %s), at the version the project already takes %s at\n", added.Name, added.Module, added.Name, added.Module.Repo)
	default:
		fmt.Printf("added %s, %s (import %s)\n", added.Name, added.Module, added.Name)
	}
	for _, name := range added.Moved {
		fmt.Printf("moved %s to %s with it, since one tag versions %s\n", name, added.Module.Version, added.Module.Repo)
	}
	d := &dependencyReach{root: root}
	d.reachAfter()
	d.write(os.Stdout, []string{added.Name}, func(r *vmhost.Reach) []string { return addToTable(r, added.Name) })
	return nil
}

const removeUsage = "usage: nomi remove <name>"

// runRemove is `nomi remove <name>`: take the dependency's entry out of
// nomi.toml's [dependencies], and write nomi.lock without it and the
// modules only it needed. It takes the entry's name or its module, and
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
		fmt.Printf("removed %s (%s %s)\n", e.Name, e.ID, e.Version)
	}
	for _, m := range removed.Unneeded {
		if m.ID != removed.Entry.ID {
			fmt.Printf("removed %s, which nothing else needs\n", m)
		}
	}
	return nil
}

// requiredModule answers the entry of the project's [dependencies] that
// arg names: the entry's name, or the one entry whose module is arg.
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
		if !r.ID.IsZero() && r.ID.String() == arg {
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
// [dependencies], or every one, to its repository's latest release within
// its line (modcache.Line: its major version from v1 on, its minor
// version below v1), and print each module of the build list that
// changed, then what
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
	updated, err := modfetch.Update(root, only, modfetch.Options{Log: os.Stderr})
	if err != nil {
		return fmt.Errorf("nomi update: %w", err)
	}
	for _, r := range updated.AtCommit {
		fmt.Printf("%s is at commit %s, which nomi update leaves as it is\n", r.Name, r.Version)
	}
	floor := map[string]bool{}
	for _, f := range updated.Indirect {
		floor[f.Repo] = true
	}
	changes := updated.Changes
	if len(changes) == 0 {
		if only != "" && len(updated.AtCommit) > 0 {
			return nil
		}
		if only != "" {
			fmt.Printf("%s is at the latest release of its line\n", only)
		} else {
			fmt.Println("every repository of the build is at the latest release of its line")
		}
		return nil
	}
	ids := make([]modcache.ID, len(changes))
	for i, c := range changes {
		ids[i] = c.ID
		switch {
		case c.From == "":
			fmt.Printf("%s %s (added: an updated module requires it)\n", c.ID, c.To)
			continue
		case c.To == "":
			fmt.Printf("%s %s (removed: nothing needs it any more)\n", c.ID, c.From)
			continue
		}
		if floor[c.ID.Repo] {
			fmt.Printf("%s %s -> %s (under [indirect]: only a dependency names it)\n", c.ID, c.From, c.To)
			continue
		}
		fmt.Printf("%s %s -> %s\n", c.ID, c.From, c.To)
	}
	if nomi := nomiModules(root, ids); len(nomi) > 0 {
		d.reachAfter()
		d.write(os.Stdout, nomi, nil)
	}
	return nil
}

const outdatedUsage = "usage: nomi outdated [dir]"

// runOutdated is `nomi outdated [dir]`: one line per repository of the
// project's build that has a later release than the one nomi.lock takes
// it at, with the latest release in its line (what `nomi update` takes),
// the latest of all, and whether the project names it or which modules
// reach it (modfetch.Outdated). It changes nothing, and exits 0 whether or
// not anything is behind.
func runOutdated(args []string, offline bool) error {
	dir := "."
	for _, arg := range args {
		if isFlag(arg) {
			return unknownFlag("nomi outdated", arg, outdatedUsage)
		}
		if dir != "." {
			return fmt.Errorf("nomi outdated: unexpected argument %q\n%s", arg, outdatedUsage)
		}
		dir = arg
	}
	root, err := manifestRoot("outdated", dir)
	if err != nil {
		return err
	}
	behind, err := modfetch.Outdated(root, modfetch.Options{Offline: offline, Log: os.Stderr})
	if err != nil {
		return fmt.Errorf("nomi outdated: %w", err)
	}
	if len(behind) == 0 {
		fmt.Println("every repository of the build is at its latest release")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "repository\tversion\tlatest in its line\tlatest\treached")
	for _, b := range behind {
		reached := "direct"
		if len(b.Via) > 0 {
			reached = "through " + joinAnd(b.Via)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", b.Repo, b.Version, b.Line, b.Newest, reached)
	}
	return w.Flush()
}

const newUsage = "usage: nomi new <name> [dir]"

// runNew is `nomi new <name> [dir]`: start a Nomi module in dir (default:
// a new directory named for the module) with a nomi.toml naming the
// module (moduleArg), a main.nomi that prints hello and an AGENTS.md for
// coding agents, keeping either of the last two when already there. It
// refuses a directory that holds a nomi.toml, which is a project already.
// It writes nothing until its arguments are checked.
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
	name, err := moduleArg("new", pos[0])
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
		{"nomi.toml", manifestText(name, modcache.Release(), []string{"main"})},
		{"main.nomi", "import std::io\n\nfn main()\n    io.print(\"hello\")\nend\n"},
		{"AGENTS.md", agentsText(name)},
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
	return nil
}

// agentsText is the AGENTS.md nomi new writes for a coding agent, for the
// module name. It names the commands and links the cheat sheet,
// docs/cheatsheet.md as the site publishes it, rather than holding a copy
// of its rules.
func agentsText(name string) string {
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
		"- `nomi add <repository>` adds a dependency to `nomi.toml` and `nomi.lock`, such as `nomi add github.com/owner/name`.\n" +
		"- `nomi mcp` serves the compiler to a coding agent over the Model Context Protocol: check, types, definitions, std search, tests and formatting.\n\n" +
		"## Layout\n\n" +
		"- `nomi.toml` names the module, lists its entry points, the files with a `fn main`, and names its dependencies.\n" +
		"- `main.nomi` is the program's entry point.\n" +
		"- Tests sit beside the code they test, in `test \"name\"` blocks closed by `end`.\n"
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
