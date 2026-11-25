package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"

	"golang.org/x/mod/semver"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/buildversion"
	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/internal/pkgcache"
	"github.com/nomi-language/nomi/internal/pkgfetch"
	"github.com/nomi-language/nomi/vmhost"
)

// offlineFlag is `--offline`, which `run`, `test`, `check`, `build`,
// `fetch`, `add`, `types` and `effects` take: no package is downloaded,
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

// enableDownloads lets this command fetch the packages a project's
// nomi.toml names and theirs (internal/pkgfetch, run before the front end
// reads the package cache). Offline, it refuses and names what is missing.
// GOPROXY=off is set then for the go command alone, which a `nomi` built
// from source runs to build a runner, so it stays within Go's module
// cache; pkgfetch reads no Go variable.
func enableDownloads(offline bool, log io.Writer) {
	if offline {
		_ = os.Setenv("GOPROXY", "off")
	}
	// A package fetch during `run`, `test`, `check` or `build` is named
	// on a terminal only, so a program's captured stderr carries only what
	// it wrote. `nomi add` and `nomi fetch` always name theirs.
	var packageLog io.Writer
	if isTerminal(os.Stderr) {
		packageLog = log
	}
	pkgcache.SetFetcher(func(root string) error {
		_, err := pkgfetch.Fetch(root, pkgfetch.Options{Offline: offline, Log: packageLog})
		return err
	})
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

const fetchUsage = "usage: nomi fetch [--offline] [--update] [dir]"

// runFetch is `nomi fetch`: everything the project needs, so that a later
// `--offline` command succeeds. It fetches the repositories of the packages
// the project needs and writes nomi.lock, their versions, commits and
// hashes, keeping each version nomi.lock holds. With --update it selects
// the versions from the manifests alone (pkgfetch.Reselect) and prints a
// line for each package whose version went down.
func runFetch(args []string, offline bool) error {
	dir, update := ".", false
	for _, arg := range args {
		if arg == "--update" {
			update = true
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
	root, err := manifestRoot("fetch", dir)
	if err != nil {
		return err
	}
	opts := pkgfetch.Options{Offline: offline, Update: true, Log: os.Stderr}
	var g *pkgcache.Graph
	var changes []pkgfetch.Change
	if update {
		g, changes, err = pkgfetch.Reselect(root, opts)
	} else {
		g, err = pkgfetch.Fetch(root, opts)
	}
	if err != nil {
		return fmt.Errorf("nomi fetch: %w", err)
	}
	for _, c := range changes {
		if pkgcache.IsVersion(c.From) && pkgcache.IsVersion(c.To) && semver.Compare(c.To, c.From) < 0 {
			fmt.Printf("%s %s -> %s (down: no package asks for more)\n", c.ID, c.From, c.To)
		}
	}
	n := 0
	if g != nil {
		for _, d := range g.Deps {
			if !d.Local {
				n++
			}
		}
	}
	if g != nil {
		root = g.Root
	}
	fmt.Printf("%d package%s in the package cache for %s\n", n, plural(n), vmDisplay(root))
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

// runAdd is `nomi add <repository>[/<subdir>][@version]`: name the package
// in nomi.toml's [dependencies] at that version, or with `@<commit>`, a
// commit's 40 hex digits, at that commit, moving the project's
// other entries of the repository with it (without one, at the version the
// project already takes the repository at, else its latest release),
// fetch it and what the project then needs, and write nomi.lock. `nomi add <directory>` names the package in that
// directory with a dir entry. The entry's name, the name imports use, is
// the package's own [package].name, or the name before `=` (`nomi add
// db=github.com/owner/sqlite`).
// Then it prints what the package's own files reach, and what nomi.toml's
// [effects.deps] table needs for it (dependencyReach).
func runAdd(args []string, offline bool) error {
	var pkgs []string
	for _, arg := range args {
		if isFlag(arg) {
			return unknownFlag("nomi add", arg, addUsage)
		}
		pkgs = append(pkgs, arg)
	}
	if len(pkgs) != 1 {
		return errors.New(addUsage)
	}
	root, err := packageRoot("add", ".")
	if err != nil {
		return err
	}
	name, arg := "", pkgs[0]
	if n, rest, ok := strings.Cut(arg, "="); ok && !strings.ContainsAny(n, `/\.@`) {
		name, arg = n, rest
	}
	opts := pkgfetch.Options{Offline: offline, Log: os.Stderr}
	var added pkgfetch.Added
	if isLocalPath(arg) {
		added, err = pkgfetch.AddLocal(root, name, arg, opts)
	} else {
		added, err = pkgfetch.Add(root, name, arg, opts)
	}
	if err != nil {
		return fmt.Errorf("nomi add: %w", err)
	}
	switch {
	case added.Local != "":
		fmt.Printf("added %s from %s (import %s)\n", added.Name, added.Local, added.Name)
	case added.Held:
		fmt.Printf("added %s, %s (import %s), at the version the project already takes %s at\n", added.Name, added.Package, added.Name, added.Package.Repo)
	case added.Pinned:
		fmt.Printf("added %s, %s (import %s), at this nomi's own release: a release takes the first-party libraries at its version\n", added.Name, added.Package, added.Name)
	default:
		fmt.Printf("added %s, %s (import %s)\n", added.Name, added.Package, added.Name)
	}
	for _, name := range added.Moved {
		fmt.Printf("moved %s to %s with it, since one tag versions %s\n", name, added.Package.Version, added.Package.Repo)
	}
	for _, in := range added.MovedIn {
		fmt.Printf("moved %s in %s to %s with it, since the workspace takes one version of %s\n",
			joinAnd(in.Names), in.Member, added.Package.Version, added.Package.Repo)
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
	var pkgs []string
	for _, arg := range args {
		if isFlag(arg) {
			return unknownFlag("nomi remove", arg, removeUsage)
		}
		pkgs = append(pkgs, arg)
	}
	if len(pkgs) != 1 {
		return errors.New(removeUsage)
	}
	root, err := packageRoot("remove", ".")
	if err != nil {
		return err
	}
	req, err := requiredPackage(root, pkgs[0])
	if err != nil {
		return fmt.Errorf("nomi remove: %w", err)
	}
	if use, err := packageUse(root, req); err != nil {
		return fmt.Errorf("nomi remove: %w", err)
	} else if use != "" {
		return fmt.Errorf("nomi remove: %s; remove the import first", use)
	}
	removed, err := pkgfetch.Remove(root, req.Name, pkgfetch.Options{Log: os.Stderr})
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

// requiredPackage answers the entry of the project's [dependencies] that
// arg names: the entry's name, or the one entry whose module is arg.
func requiredPackage(root, arg string) (pkgcache.Requirement, error) {
	mf, err := pkgcache.ReadManifest(root)
	if err != nil {
		return pkgcache.Requirement{}, err
	}
	if mf == nil {
		return pkgcache.Requirement{}, fmt.Errorf("no nomi.toml in %s", vmDisplay(root))
	}
	if r, ok := mf.Require(arg); ok {
		return r, nil
	}
	for _, r := range mf.Requires {
		if !r.ID.IsZero() && r.ID.String() == arg {
			return r, nil
		}
	}
	return pkgcache.Requirement{}, fmt.Errorf("nomi.toml has no dependency %s", arg)
}

// packageUse answers where a Nomi file of the project at root uses the
// dependency req, as "main.nomi:3 imports csvdata", or "" when none does:
// an import whose first name is the dependency's name. Directories holding
// a nomi.toml of their own are other packages and are not read.
func packageUse(root string, req pkgcache.Requirement) (string, error) {
	name := req.Name
	var uses []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != root && (strings.HasPrefix(d.Name(), ".") || d.Name() == "node_modules" || isRegular(filepath.Join(p, pkgcache.ManifestName))) {
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
// its line (pkgcache.Line: its major version from v1 on, its minor
// version from v0.1 on, the release itself below v0.1), and print each
// package of the build list that changed, each repository's latest release
// past its line, which it does not take (printLater), then what each changed
// package's own files reach and how that changed (dependencyReach).
func runUpdate(args []string) error {
	var pkgs []string
	for _, arg := range args {
		if isFlag(arg) {
			return unknownFlag("nomi update", arg, updateUsage)
		}
		pkgs = append(pkgs, arg)
	}
	if len(pkgs) > 1 {
		return errors.New(updateUsage)
	}
	root, err := manifestRoot("update", ".")
	if err != nil {
		return err
	}
	only := ""
	if len(pkgs) == 1 {
		if root, err = packageRoot("update", root); err != nil {
			return err
		}
		req, err := requiredPackage(root, pkgs[0])
		if err != nil {
			return fmt.Errorf("nomi update: %w", err)
		}
		only = req.Name
	}
	// The reach before is worked out only to compare; an update goes ahead
	// whether or not the project checks.
	d := &dependencyReach{root: root}
	d.before, _ = reachOf(root, nil)
	updated, err := pkgfetch.Update(root, only, pkgfetch.Options{Log: os.Stderr})
	if err != nil {
		return fmt.Errorf("nomi update: %w", err)
	}
	for _, r := range updated.AtCommit {
		fmt.Printf("%s is at commit %s, which nomi update leaves as it is\n", r.Name, r.Version)
	}
	changes := updated.Changes
	if len(changes) == 0 {
		switch {
		case only != "" && len(updated.AtCommit) > 0:
		case only != "":
			fmt.Printf("%s is at the latest release of its line\n", only)
		default:
			fmt.Println("every repository of the build is at the latest release of its line")
		}
		printLater(updated.Later)
		return nil
	}
	ids := make([]pkgcache.ID, len(changes))
	for i, c := range changes {
		ids[i] = c.ID
		switch {
		case c.From == "":
			fmt.Printf("%s %s (added: an updated package requires it)\n", c.ID, c.To)
			continue
		case c.To == "":
			fmt.Printf("%s %s (removed: nothing needs it any more)\n", c.ID, c.From)
			continue
		}
		if slices.Contains(updated.Raised, c.ID.Repo) {
			fmt.Printf("%s %s -> %s (only a dependency names it)\n", c.ID, c.From, c.To)
			continue
		}
		fmt.Printf("%s %s -> %s\n", c.ID, c.From, c.To)
	}
	printLater(updated.Later)
	if nomi := nomiPackages(root, ids); len(nomi) > 0 {
		d.reachAfter()
		d.write(os.Stdout, nomi, nil)
	}
	return nil
}

// printLater writes one line per repository with a release past the line
// the project takes it in, which `nomi update` never takes
// (pkgfetch.Updated.Later): the release, the rule of the line's tier
// (pkgcache.LineRule), and the `nomi add` that takes it.
func printLater(later []pkgfetch.Later) {
	for _, l := range later {
		fmt.Printf("%s has %s, past the %s the project takes; %s, which nomi update does not leave: `nomi add %s@%s` takes it\n",
			l.Entry.Repo, l.Newest, l.Entry.Version, pkgcache.LineRule(l.Entry.Version), l.Entry.ID, l.Newest)
	}
}

const outdatedUsage = "usage: nomi outdated [dir]"

// runOutdated is `nomi outdated [dir]`: one line per repository of the
// project's build that has a later release than the one nomi.lock takes
// it at, with the latest release in its line (what `nomi update` takes),
// the latest of all, and whether the project names it or which modules
// reach it (pkgfetch.Outdated). It changes nothing, and exits 0 whether or
// not anything is behind. It takes no --offline: a repository's tags are
// read from its host each time, and nomi keeps no list of them.
func runOutdated(args []string, _ bool) error {
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
	behind, err := pkgfetch.Outdated(root, pkgfetch.Options{Log: os.Stderr})
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

// runNew is `nomi new <name> [dir]`: start a package in dir (default:
// a new directory named for the package) with a nomi.toml naming the
// package (packageArg), a main.nomi that prints hello and an AGENTS.md for
// coding agents, keeping either of the last two when already there. It
// refuses a directory that holds a nomi.toml, which is a project already,
// and a workspace's root. Under a workspace that does not list the
// directory yet, it adds it to the workspace's members (joinWorkspace).
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
	name, err := packageArg("new", pos[0])
	if err != nil {
		return err
	}
	dir := name
	if len(pos) == 2 {
		dir = pos[1]
	}
	if isRegular(filepath.Join(dir, pkgcache.ManifestName)) {
		return fmt.Errorf("nomi new: %s already holds a nomi.toml, so it is a project already", dir)
	}
	if err := workspaceRootError("new", dir, "a package goes in a directory of its own, as its members do"); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("nomi new: %w", err)
	}
	files := []struct{ name, text string }{
		{"nomi.toml", manifestText(name, buildversion.Release(), []string{"main"})},
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
	if err := joinWorkspace("new", dir); err != nil {
		return err
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

// joinWorkspace makes the new package at dir a member of the workspace it
// is under when that workspace's members and exclude name it nowhere
// (pkgcache.UnlistedError): it adds the package's directory to members
// with the fix the error carries in the editor (analysis.AddWorkspaceMember),
// and says so. A package a members or exclude entry names already, and one
// under no workspace, is left as it is.
func joinWorkspace(cmd, dir string) error {
	var unlisted *pkgcache.UnlistedError
	if _, err := pkgcache.WorkspaceOf(dir); !errors.As(err, &unlisted) {
		return nil
	}
	data, err := os.ReadFile(unlisted.Decl)
	if err != nil {
		return fmt.Errorf("nomi %s: %w", cmd, err)
	}
	fix, err := analysis.AddWorkspaceMember(unlisted.Decl, data, unlisted.Entry)
	if err != nil {
		return fmt.Errorf("nomi %s: %s: %w", cmd, vmDisplay(unlisted.Decl), err)
	}
	if err := os.WriteFile(unlisted.Decl, []byte(parser.ApplyEdits(string(data), fix.Edits)), 0o644); err != nil {
		return fmt.Errorf("nomi %s: %w", cmd, err)
	}
	fmt.Printf("added %s to the members of %s\n", unlisted.Entry, vmDisplay(unlisted.Decl))
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
		"- `nomi.toml` names the package, lists its entry points, the files with a `fn main`, and names its dependencies.\n" +
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

// manifestRoot is the nearest directory at or above dir that holds a
// nomi.toml or a nomi-workspace.toml: the project `nomi fetch`, `update`
// and `outdated` work on, which act on the whole workspace a package is in
// (pkgcache.WorkspaceOf), and the package `add` and `remove` edit
// (packageRoot).
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
		if isRegular(filepath.Join(d, pkgcache.ManifestName)) || isRegular(filepath.Join(d, pkgcache.WorkspaceName)) {
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

// packageRoot is manifestRoot for a command that edits one package's
// nomi.toml: `add`, `remove` and `update` of one dependency. A workspace's
// root is no package, so there it is an error that names the members.
func packageRoot(cmd, dir string) (string, error) {
	root, err := manifestRoot(cmd, dir)
	if err != nil {
		return "", err
	}
	if err := workspaceRootError(cmd, root, "run nomi "+cmd+" in the member whose nomi.toml it changes"); err != nil {
		return "", err
	}
	return root, nil
}

// workspaceRootError is the error of `nomi <cmd>` at dir when dir is a
// workspace's root, which is no package: it lists the members and says what
// to do instead (what); nil when dir is no workspace's root. `add`,
// `remove` and `update` of one entry need a package's nomi.toml, and `run`,
// `build`, `tool` and `types` one package's program or files.
func workspaceRootError(cmd, dir, what string) error {
	members, err := frontend.WorkspaceMembers(dir)
	switch {
	case err != nil:
		return fmt.Errorf("nomi %s: %w", cmd, err)
	case members == nil:
		return nil
	}
	for i, m := range members {
		members[i] = vmDisplay(m)
	}
	return fmt.Errorf("nomi %s: %s is a workspace's root, which is not a package; %s: %s",
		cmd, vmDisplay(dir), what, joinAnd(members))
}

// atWorkspaceRoot reports whether the current directory is a workspace's
// root, where `nomi check`, and `nomi fmt` with a mode flag, given no path
// act on every member (frontend.WorkspaceFiles).
func atWorkspaceRoot() bool {
	return isRegular(pkgcache.WorkspaceName)
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

// vmDisplay is how a command names path, which may be relative to the
// working directory: as a report does (vmhost.DisplayPath), relative to the
// working directory with `/` when under it, else absolute. A workspace's
// members are named this way, so at its root each is named as
// nomi-workspace.toml writes it on every platform.
func vmDisplay(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	return vmhost.DisplayPath(path)
}
