package pkgcache

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/mod/semver"
)

// Dep is one package the project needs.
type Dep struct {
	// Package is the package's repository, subdirectory and the version
	// selected for it. A package loaded from a directory has the ID and
	// version its entry writes, which may be none.
	Package
	// Name is the package's name in the program: the first segment of each
	// of its files' keys, and the key of its entry under
	// [effects.deps.packages]. It is the project's name for the package when
	// the project names it, and otherwise the name the first package that
	// reaches it gives it, with a `~2` suffix (`~3`, ...) when another
	// package or a file at the project's root already has that name.
	Name string
	// Dir is the package's root directory: the local directory, or its
	// directory in the package cache. Empty when the package is Missing.
	Dir string
	// Local is set for a package loaded from a directory: one a dir entry
	// of the project names, or a dir entry of another package loaded from a
	// directory (localPackages).
	Local bool
	// Nomi is set when Dir holds a nomi.toml.
	Nomi bool
	// Direct is set for a package the project's nomi.toml names.
	Direct bool
	// Via is the Name of the package whose [dependencies] first reached
	// this one, "" for a package the project names.
	Via string
	// Self is the package's own [package].name, which names it in the
	// project only when the project's key for it is the same: an effects
	// list that writes it is told the key (Name).
	Self string
	// Names is each name the package's own files import a package by, its
	// own [package].name (Self) among them, and that package's Name.
	Names map[string]string
}

// Graph is what Load answers for a project.
type Graph struct {
	// Root is the directory holding the project's nomi.toml.
	Root string
	// Manifest is the project's nomi.toml, as this package reads it.
	Manifest *Manifest
	// Names is each name the project's files import a dependency by, and
	// that dependency's Name.
	Names map[string]string
	// Deps are the packages the project needs, in the order they were
	// found: the project's dependencies first, sorted by name, then what
	// they need.
	Deps []Dep
	// Missing are the needed packages that are not in the package cache, at
	// the versions selected for them.
	Missing []Package
}

// Find answers the dependency id, or false.
func (g *Graph) Find(id ID) (Dep, bool) {
	for _, d := range g.Deps {
		if d.ID == id && !id.IsZero() {
			return d, true
		}
	}
	return Dep{}, false
}

// Named answers the dependency whose Name is name, or false.
func (g *Graph) Named(name string) (Dep, bool) {
	for _, d := range g.Deps {
		if d.Name == name {
			return d, true
		}
	}
	return Dep{}, false
}

// At answers the dependency whose root is dir, or false.
func (g *Graph) At(dir string) (Dep, bool) {
	for _, d := range g.Deps {
		if d.Dir != "" && sameDir(d.Dir, dir) {
			return d, true
		}
	}
	return Dep{}, false
}

// Materialize answers the root directory of m's files in the package cache,
// or "" when it is not there. Load's cache-only answer reads the cache;
// a fetcher's fetches first.
type Materialize func(m Package) (string, error)

// MainPackageEnv names the environment variable `nomi tool` sets to the
// project's directory, the main package a tool runs in. A dependency's
// tool runs in the project's build, so the nomi.toml at the tool's own
// root, which may be a package cache copy whose dir entries name its
// neighbors in its repository, is not read as the main package's: the
// tool's graph is the project's (MainPackage). It is an environment
// variable so that a process the run starts reads the same graph.
const MainPackageEnv = "NOMI_MAIN_PACKAGE"

// MainPackage is the directory whose nomi.toml is the main package of a
// program rooted at root: MainPackageEnv's directory when root is the
// directory of a package that directory needs, else root. It reads only the
// disk.
func MainPackage(root string) string {
	main := os.Getenv(MainPackageEnv)
	if main == "" || sameDir(main, root) {
		return root
	}
	g, err := walk(main, cacheOnly, true, false, nil)
	if err != nil || g == nil {
		return root
	}
	if _, ok := g.At(root); ok {
		return main
	}
	return root
}

// Walk reads the nomi.toml of root's main package (MainPackage) and the
// nomi.toml of each package it needs, and answers the graph, with each
// fetched package in the directory materialize answers. A missing
// nomi.toml answers a nil graph and no error.
//
// A version is selected for each repository the walk reaches, since one
// tag versions a whole repository: the highest of the version the
// nomi.lock locks it at (the workspace's, for a member of one: WorkspaceOf)
// and the versions any reached package's [dependencies] asks for, and every
// package of that repository is taken at it. The walk reads the
// requirements of the selected versions only, and repeats until no reached
// package asks for a higher one. With the lock current, that is the lock's
// build list, read without a version the lock does not hold. Two
// requirements on one repository in different lines (Line: a major version
// from v1 on, a minor version from v0.1 on, one release below v0.1) are an
// error that names both requirers. The lock's version is held to the
// requirements the same way: one in another line than a requirement on its
// repository, or a tag where a requirement names a commit or the reverse,
// is a ClashError that names nomi.lock, since the manifests changed after
// it was written, and `nomi fetch --update` selects again from them. A
// requirement lower in the lock's line is a raise the lock keeps. A
// release nomi takes the compiler's
// repository at its own version whatever tag a requirement or a lock line
// names (Pinned), so requirements on it never conflict by version.
func Walk(root string, materialize Materialize) (*Graph, error) {
	return walk(MainPackage(root), materialize, true, false, nil)
}

// Resolve is WalkAll with the selection starting from the versions from
// holds, by repository, in place of the lock's: what `nomi fetch`, `add`,
// `remove` and `update` lock. They start from the lock's versions
// (Lock.Versions), changed where the command means to change them; `nomi
// fetch --update` starts from none, so the requirements alone select. The
// versions from holds are held to the requirements as the lock's are
// (Walk).
func Resolve(root string, materialize Materialize, from map[string]string) (*Graph, error) {
	return walk(MainPackage(root), materialize, false, true, from)
}

// WalkAll is Walk over the whole workspace root is in, or root's own when
// it is in none. A workspace's graph has every member's requirements as
// its roots, so its selection holds one version of each repository for
// all of them: its Root is the workspace's root, its Deps every package a
// member needs, once, Direct when a member names it, and it has no Names,
// since each member names its dependencies itself. Two members whose
// requirements one build cannot take together (clash), or that take one
// package from two places, a directory and a repository or two
// directories, are an error that names both.
func WalkAll(root string, materialize Materialize) (*Graph, error) {
	return walk(MainPackage(root), materialize, true, true, nil)
}

// start is one package a walk starts from: its root and its nomi.toml.
type start struct {
	root string
	mf   *Manifest
}

// pass is one main package's explore.
type pass struct {
	start
	g        *Graph
	asked    map[string]ask
	conflict error
}

// walk is the selection for the main package at root, or with whole the
// workspace's: starting from the versions of the nomi.lock that governs
// root when locked, else from from's, and raised by the requirements.
func walk(root string, materialize Materialize, locked, whole bool, from map[string]string) (*Graph, error) {
	mf, err := ReadManifest(root)
	if tooOld := (*NomiVersionError)(nil); errors.As(err, &tooOld) {
		tooOld.Package += " (this project)"
	}
	if err != nil {
		return nil, err
	}
	ws, err := WorkspaceOf(root)
	if err != nil {
		return nil, err
	}
	mains := []start{{root, mf}}
	lockDir := root
	if ws != nil {
		lockDir = ws.Root
		if whole {
			mains = nil
			// The lookup checked each member but root (expand), and names
			// a member in error.
			for _, m := range ws.Members {
				mf, err := ReadManifest(m)
				if err != nil {
					return nil, err
				}
				mains = append(mains, start{m, mf})
			}
		}
	}
	if mains[0].mf == nil {
		return nil, nil
	}
	selected := maps.Clone(from)
	if locked {
		lock, err := ReadLock(lockDir)
		if err != nil {
			return nil, err
		}
		selected = lock.Versions()
	}
	if selected == nil {
		selected = map[string]string{}
	}
	// Each version the selection starts from is one more ask on its
	// repository, held to the requirements as they are held to each other.
	held := maps.Clone(selected)
	for {
		passes := make([]pass, len(mains))
		want := map[string]string{}
		for i, m := range mains {
			p := pass{start: m}
			var w map[string]string
			if p.g, w, p.asked, p.conflict, err = explore(m.root, m.mf, ws, selected, materialize); err != nil {
				return nil, err
			}
			for repo, v := range w {
				if semver.Compare(v, want[repo]) > 0 {
					want[repo] = v
				}
			}
			passes[i] = p
		}
		raised := false
		for repo, v := range want {
			if semver.Compare(v, selected[repo]) > 0 {
				selected[repo] = v
				raised = true
			}
		}
		if raised {
			continue
		}
		for _, p := range passes {
			for _, repo := range slices.Sorted(maps.Keys(p.asked)) {
				if v, ok := held[repo]; ok {
					if err := clash(repo, ask{version: v, held: true}, p.asked[repo]); err != nil {
						return nil, err
					}
				}
			}
		}
		for _, p := range passes {
			if p.conflict != nil {
				return nil, p.conflict
			}
		}
		if !whole || ws == nil {
			return passes[0].g, nil
		}
		return joined(ws, passes)
	}
}

// joined is the graph of a whole workspace (WalkAll) from each member's
// pass, or the error for two members that one build cannot hold.
func joined(ws *Workspace, passes []pass) (*Graph, error) {
	// A member the walk starts from is not checked (expand), so it may
	// have no name.
	name := func(p pass) string {
		if p.mf.Name != "" {
			return p.mf.Name
		}
		return filepath.ToSlash(relTo(ws.Root, p.root))
	}
	type first struct {
		member string
		ask
	}
	asked := map[string]first{}
	for _, p := range passes {
		repos := make([]string, 0, len(p.asked))
		for repo := range p.asked {
			repos = append(repos, repo)
		}
		slices.Sort(repos)
		for _, repo := range repos {
			a := p.asked[repo]
			f, ok := asked[repo]
			if !ok {
				asked[repo] = first{name(p), a}
			} else if err := clash(repo, f.ask, a); err != nil {
				return nil, fmt.Errorf("workspace members %s and %s cannot share one build: %w", f.member, name(p), err)
			}
		}
	}
	g := &Graph{Root: ws.Root, Manifest: &Manifest{}, Names: map[string]string{}}
	from := func(d Dep) string {
		if d.Local {
			return "from the directory " + filepath.ToSlash(relTo(ws.Root, d.Dir))
		}
		return "fetched at " + d.Version
	}
	type place struct {
		member string
		at     int
	}
	byID := map[ID]place{}
	byDir := map[string]int{}
	for _, p := range passes {
		for _, d := range p.g.Deps {
			if !d.ID.IsZero() {
				if f, ok := byID[d.ID]; ok {
					was := g.Deps[f.at]
					if was.Local != d.Local || d.Local && !sameDir(was.Dir, d.Dir) {
						return nil, fmt.Errorf("workspace members %s and %s take %s from two places: %s %s, and %s %s; a workspace takes each package from one place",
							f.member, name(p), d.ID, f.member, from(was), name(p), from(d))
					}
					g.Deps[f.at].Direct = was.Direct || d.Direct
					continue
				}
			} else if i, ok := byDir[d.Dir]; ok && d.Local {
				g.Deps[i].Direct = g.Deps[i].Direct || d.Direct
				continue
			}
			if !d.ID.IsZero() {
				byID[d.ID] = place{name(p), len(g.Deps)}
			}
			if d.Local {
				byDir[d.Dir] = len(g.Deps)
			}
			g.Deps = append(g.Deps, d)
		}
		for _, m := range p.g.Missing {
			if !slices.Contains(g.Missing, m) {
				g.Missing = append(g.Missing, m)
			}
		}
	}
	return g, nil
}

// local is a package loaded from a directory: one a dir entry of the main
// package names, or a dir entry of another package loaded from a directory.
type local struct {
	dir  string
	file *Manifest
	req  Requirement
	// requires is file's [dependencies] as the walk reads them, and dirs
	// the package each of its dir entries is, by the entry's name. An entry
	// dirs lacks is read by its ID and version.
	requires []Requirement
	dirs     map[string]*local
	// top is the key of the main package's [dependencies] entry the walk
	// first reached this package through.
	top string
}

// reachedError is err, the problem ReadManifest found in the nomi.toml of
// the package r names, an entry of the nomi.toml in the directory owner,
// as the walk answers it: a package that needs a later nomi as itself,
// named by name, and any other problem with the entry that names the
// package (PackageManifestError). top is the main package's entry the walk
// reached owner through, "" when owner is the main package, at root.
func reachedError(err error, name, root, owner string, r Requirement, top string, fetched Package) error {
	if tooOld := (*NomiVersionError)(nil); errors.As(err, &tooOld) {
		tooOld.Package = name
		return tooOld
	}
	e := &PackageManifestError{Err: err, Fetched: fetched, By: filepath.Join(owner, ManifestName), Entry: r.Name}
	if top != "" {
		e.Main, e.Top = filepath.Join(root, ManifestName), top
	}
	return e
}

// localPackages reads the packages loaded from directories: those the main
// package's dir entries name, then those the dir entries of each package so
// loaded name, each relative to the directory of the nomi.toml that writes
// it, since that directory is on disk. It answers the main package's, by
// entry name, and the package each ID stands for: a dir entry that also
// names a repository stands for that package wherever the graph reaches it.
//
// The main package's entry for a package wins: a dependency's dir entry for
// a package the main package names, by a dir or a version, is read by its
// ID. Otherwise the first directory reached for an ID stands for it. A
// directory reached again is the package read before, so a cycle of
// directories ends, and a dir entry for the main package's own directory is
// not read. A fetched package's dir entries are read as its neighbors in
// its repository (neighbor), never as directories on this disk.
func localPackages(root string, mf *Manifest) (top map[string]*local, byID map[ID]*local, err error) {
	named := map[ID]bool{}
	for _, r := range mf.Requires {
		if !r.ID.IsZero() {
			named[r.ID] = true
		}
	}
	byDir := map[string]*local{}
	byID = map[ID]*local{}
	var queue []*local
	read := func(owner string, r Requirement, top string) (*local, error) {
		dir := r.LocalDir(owner)
		key := dir
		if real, err := filepath.EvalSymlinks(dir); err == nil {
			key = real
		}
		if l, ok := byDir[key]; ok {
			return l, nil
		}
		info, err := os.Stat(dir)
		switch {
		case err != nil:
			return nil, fmt.Errorf("%s: [dependencies] %s: directory %s does not exist", filepath.Join(owner, ManifestName), r.Name, r.Dir)
		case !info.IsDir():
			return nil, fmt.Errorf("%s: [dependencies] %s: %s is not a directory", filepath.Join(owner, ManifestName), r.Name, r.Dir)
		}
		file, err := readReached(dir)
		if err != nil {
			return nil, reachedError(err, "the package in "+r.Dir, root, owner, r, top, Package{})
		}
		if top == "" {
			top = r.Name
		}
		l := &local{dir: dir, file: file, req: r, dirs: map[string]*local{}, top: top}
		if file != nil {
			l.requires = file.Requires
		}
		byDir[key] = l
		queue = append(queue, l)
		return l, nil
	}
	top = map[string]*local{}
	for _, r := range mf.Requires {
		if r.Dir == "" {
			continue
		}
		l, err := read(root, r, "")
		if err != nil {
			return nil, nil, err
		}
		if !r.ID.IsZero() {
			byID[r.ID] = l
		}
		top[r.Name] = l
	}
	for len(queue) > 0 {
		owner := queue[0]
		queue = queue[1:]
		for _, r := range slices.Clone(owner.requires) {
			if r.Dir == "" || !r.ID.IsZero() && named[r.ID] || sameDir(r.LocalDir(owner.dir), root) {
				continue
			}
			l, err := read(owner.dir, r, owner.top)
			if err != nil {
				return nil, nil, err
			}
			if !r.ID.IsZero() {
				if bound, ok := byID[r.ID]; ok {
					l = bound
				} else {
					byID[r.ID] = l
				}
			}
			owner.dirs[r.Name] = l
		}
	}
	return top, byID, nil
}

// ask is a requirement on a repository at a version, a tag or a commit,
// and who asked; or with held set, the version the selection starts from,
// which nomi.lock holds.
type ask struct {
	version, by string
	held        bool
}

// text is a as an error names it: who requires repo at what, or what
// nomi.lock holds it at.
func (a ask) text(repo string) string {
	at := a.version
	if IsCommit(at) {
		at = "at commit " + at
	}
	switch {
	case !a.held:
		return fmt.Sprintf("%s requires %s %s", a.by, repo, at)
	case IsCommit(a.version):
		return fmt.Sprintf("%s holds %s %s", LockName, repo, at)
	}
	return fmt.Sprintf("%s holds %s at %s", LockName, repo, at)
}

// ClashError is two asks on one repository that one build cannot take
// together (clash).
type ClashError struct {
	Repo string
	// Held is set when one of them is the version nomi.lock holds: the
	// manifests changed since it was written.
	Held bool
	msg  string
}

func (e *ClashError) Error() string { return e.msg }

// clash is the error for asks a and b on repo, which one build cannot take
// together, or nil. A build takes a repository at one point: one tag, so
// every requirement on it is in one line (Line), or one commit, which
// every requirement on it names. The version nomi.lock holds is held to
// the requirements the same way.
func clash(repo string, a, b ask) error {
	var why string
	switch ca, cb := IsCommit(a.version), IsCommit(b.version); {
	case ca != cb:
		why = "a build takes a repository at one point, so its requirements are all at tags or all at one commit"
	case ca && a.version != b.version:
		why = "a build takes a repository at one point, so every requirement on it at a commit names the same commit"
	case !ca && Line(a.version) != Line(b.version):
		why = "one tag versions a whole repository, so every requirement on it must be in one line: a major version from v1 on, a minor version from v0.1 on, and below v0.1 one release"
	default:
		return nil
	}
	msg := a.text(repo) + " and " + b.text(repo) + ": " + why
	held := a.held || b.held
	if held {
		msg += "; `nomi fetch --update` selects again from the manifests"
	}
	return &ClashError{Repo: repo, Held: held, msg: msg}
}

// explore is one pass of walk: the packages reached from root's
// requirements through the requirements of each reached package, each
// fetched at selected's version for its repository when that is higher
// than the one asked for, and the highest version each repository is
// asked for, of the tags asked for, with the first ask on each. conflict
// is the error for two requirements on one repository that one build
// cannot take together (clash), which walk answers when the pass is its
// last. ws is the workspace root is a member of, or nil: an ask by root
// then names its entry, so an error between two members says which
// entries.
func explore(root string, mf *Manifest, ws *Workspace, selected map[string]string, materialize Materialize) (g *Graph, want map[string]string, asked map[string]ask, conflict, err error) {
	g = &Graph{Root: root, Manifest: mf, Names: map[string]string{}}
	want = map[string]string{}
	localOf, locals, err := localPackages(root, mf)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	taken := rootNames(root, mf)
	unique := func(name string) string {
		out := name
		for n := 2; taken[out]; n++ {
			out = name + "~" + strconv.Itoa(n)
		}
		taken[out] = true
		return out
	}
	for _, r := range mf.Requires {
		taken[r.Name] = true
	}
	type item struct {
		owner map[string]string
		req   Requirement
		l     *local
		root  bool
		// by names the package whose nomi.toml writes req, and is "" for
		// a neighbor a fetched package names by its directory, which is at
		// that package's own tag and asks for no version of its own.
		by string
		// via is the Name of the package whose nomi.toml writes req, ""
		// for the project's.
		via string
		// from is the directory of the nomi.toml that writes req, and top
		// the key of the project's entry the walk reached it through, ""
		// for the project's own entries (reachedError).
		from, top string
	}
	// The main package's nomi.toml is its caller's to check
	// (ReadManifest), and a command that edits [dependencies] walks one
	// with no name as it is.
	project := "this project"
	if mf.Name != "" {
		project = mf.Name
	}
	var queue []item
	for _, r := range mf.Requires {
		by := project
		if ws != nil {
			by = fmt.Sprintf("%s ([dependencies] %s)", project, r.Name)
		}
		queue = append(queue, item{owner: g.Names, req: r, l: localOf[r.Name], root: true, by: by, from: root})
	}
	type depKey struct {
		dir string
		id  ID
	}
	byKey := map[depKey]int{}
	asked = map[string]ask{}
	for len(queue) > 0 {
		it := queue[0]
		queue = queue[1:]
		r := it.req
		l := it.l
		if l == nil {
			if r.ID.IsZero() {
				// A dir entry for the main package's own directory.
				continue
			}
			l = locals[r.ID]
		}
		var key depKey
		version := ""
		if l != nil {
			key = depKey{dir: l.dir}
		} else {
			key = depKey{id: r.ID}
			version = Takes(r.Repo, r.Version)
			if it.by != "" {
				if first, ok := asked[r.Repo]; !ok {
					asked[r.Repo] = ask{version: version, by: it.by}
				} else if conflict == nil {
					conflict = clash(r.Repo, first, ask{version: version, by: it.by})
				}
				if semver.Compare(version, want[r.Repo]) > 0 {
					want[r.Repo] = version
				}
			}
			// A commit is taken as it is named; a tag at the version
			// selected for its repository when that is higher.
			if !IsCommit(version) && semver.Compare(selected[r.Repo], version) > 0 {
				version = selected[r.Repo]
			}
		}
		if i, seen := byKey[key]; seen {
			it.owner[r.Name] = g.Deps[i].Name
			continue
		}
		name := r.Name
		if it.root {
			taken[name] = true
		} else {
			name = unique(name)
		}
		dep := Dep{Name: name, Direct: it.root, Via: it.via, Names: map[string]string{}}
		var file *Manifest
		if l != nil {
			dep.Package = Package{ID: l.req.ID, Version: l.req.Version}
			dep.Dir, dep.Local, file = l.dir, true, l.file
		} else {
			dep.Package = Package{ID: r.ID, Version: version}
			dir, err := materialize(dep.Package)
			if err != nil {
				return nil, nil, nil, nil, err
			}
			if dir == "" {
				g.Missing = append(g.Missing, dep.Package)
			} else if file, err = readReached(dir); err != nil {
				return nil, nil, nil, nil, reachedError(err, "package "+dep.Package.String(), root, it.from, r, it.top, dep.Package)
			}
			dep.Dir = dir
		}
		byKey[key] = len(g.Deps)
		it.owner[r.Name] = name
		dep.Nomi = file != nil
		if file != nil {
			dep.Self = file.Name
			if file.Name != "" {
				dep.Names[file.Name] = name
			}
			by := dep.Package.String()
			if dep.Local {
				by = dep.Dir
			}
			requires, dirs := file.Requires, map[string]*local(nil)
			if l != nil {
				requires, dirs = l.requires, l.dirs
			}
			top := it.top
			if top == "" {
				top = r.Name
			}
			for _, sub := range requires {
				subBy := by
				if l == nil && sub.Dir != "" {
					var ok bool
					if sub, ok, err = neighbor(dep.Package, sub); err != nil {
						return nil, nil, nil, nil, err
					} else if !ok {
						continue
					}
					subBy = ""
				}
				queue = append(queue, item{owner: dep.Names, req: sub, l: dirs[sub.Name], by: subBy, via: name, from: dep.Dir, top: top})
			}

		}
		g.Deps = append(g.Deps, dep)
	}
	return g, want, asked, conflict, nil
}

// rootNames are the names a package the project does not name may not take
// in the program: `std`, the project's own name, and each file and
// directory at its root, which the project's own imports name.
func rootNames(root string, mf *Manifest) map[string]bool {
	taken := map[string]bool{"std": true}
	if mf.Name != "" {
		taken[mf.Name] = true
	}
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			taken[name] = true
		} else if base, ok := strings.CutSuffix(name, ".nomi"); ok {
			taken[base] = true
		}
	}
	return taken
}

// NamesAt is each name the files of the package rooted at dir import a
// package by, and that package's Name: the project's own names when dir is
// the project's root, a dependency's when it is the dependency's, nil when
// it is neither.
func (g *Graph) NamesAt(dir string) map[string]string {
	if sameDir(dir, g.Root) {
		return g.Names
	}
	if d, ok := g.At(dir); ok {
		return d.Names
	}
	return nil
}

// cacheOnly materializes from the package cache alone.
func cacheOnly(m Package) (string, error) {
	cache, err := Root()
	if err != nil {
		return "", nil
	}
	return Cached(cache, m), nil
}

// CacheOnly is the Materialize that reads the package cache and never
// fetches.
func CacheOnly(m Package) (string, error) { return cacheOnly(m) }

var (
	fetchMu sync.Mutex
	fetcher func(root string) error
	fetched = map[string]error{}
)

// SetFetcher installs fetch, which puts the packages a project at root needs
// into the package cache before Load reads it. It runs once per root per
// process, for a root that holds a nomi.toml. Only a process that may
// fetch installs one: the `nomi` CLI does, the language server never does.
func SetFetcher(fetch func(root string) error) {
	fetchMu.Lock()
	defer fetchMu.Unlock()
	fetcher = fetch
	fetched = map[string]error{}
}

// Load is Walk over the package cache, after the installed fetcher (if any)
// has run for root. A fetch error is Load's error.
func Load(root string) (*Graph, error) {
	if err := fetchOnce(root); err != nil {
		return nil, err
	}
	return Walk(root, cacheOnly)
}

func fetchOnce(root string) error {
	fetchMu.Lock()
	defer fetchMu.Unlock()
	if fetcher == nil {
		return nil
	}
	abs, err := filepath.Abs(MainPackage(root))
	if err != nil {
		abs = root
	}
	if err, ok := fetched[abs]; ok {
		return err
	}
	if !isFile(filepath.Join(abs, ManifestName)) {
		return nil
	}
	err = fetcher(abs)
	fetched[abs] = err
	return err
}

// MissingHint is the hint a diagnostic about an import that names no file
// gives when the project needs packages the package cache does not hold, or
// "" when it holds them all. It never fetches.
func MissingHint(root string) string {
	g, err := Walk(root, cacheOnly)
	if err != nil || g == nil || len(g.Missing) == 0 {
		return ""
	}
	names := make([]string, 0, len(g.Missing))
	for _, m := range g.Missing {
		names = append(names, m.String())
	}
	if len(names) == 1 {
		return "the project needs package " + names[0] + ", which is not in the package cache; run `nomi fetch` to download it"
	}
	return "the project needs packages " + strings.Join(names, ", ") +
		", which are not in the package cache; run `nomi fetch` to download them"
}

// ImportProblem says what the project at root lacks for an import whose
// first name is name, for a diagnostic about an import that names no file.
// problem and hint are "" when root has no nomi.toml, or when name is a
// package the project depends on. A dependency whose directory holds
// no nomi.toml is that problem; unlisted is set when no dependency has the
// name at all, which a project file the import meant would also explain.
// It never fetches.
func ImportProblem(root, name string) (problem, hint string, unlisted bool) {
	g, err := Walk(root, cacheOnly)
	if err != nil || g == nil {
		return "", "", false
	}
	if key, ok := g.NamesAt(root)[name]; ok {
		d, _ := g.Named(key)
		if d.Dir == "" || d.Nomi {
			return "", "", false
		}
		hint = fmt.Sprintf("a Nomi package's root holds a nomi.toml naming it: `[package]`, then `name = %q`", name)
		if d.Local {
			return fmt.Sprintf("[dependencies] %s is the directory %s, which has no nomi.toml", name, AddArg(relTo(root, d.Dir))), hint, false
		}
		return fmt.Sprintf("package `%s` has no nomi.toml, so it is not a Nomi package", d.ID), hint, false
	}
	problem = fmt.Sprintf("nomi.toml has no dependency named `%s`", name)
	if dir := findLocalPackage(root, name); dir != "" {
		return problem, fmt.Sprintf("run `nomi add %s` to add the package in that directory", dir), true
	}
	return problem, "run `nomi add <repository>` to add a published package, such as `nomi add github.com/owner/name`, or `nomi add <directory>` for one on disk", true
}

func relTo(root, dir string) string {
	if rel, err := filepath.Rel(root, dir); err == nil {
		return rel
	}
	return dir
}

// AddArg spells a directory, relative to the project root, as `nomi add`
// takes it: a local path starts with ./ or ../, so it is not read as a
// repository.
func AddArg(dir string) string {
	dir = filepath.ToSlash(dir)
	if filepath.IsAbs(dir) || dir == "." || dir == ".." || strings.HasPrefix(dir, "./") || strings.HasPrefix(dir, "../") {
		return dir
	}
	return "./" + dir
}

// findLocalPackage answers, relative to root, a directory near root that
// holds a package of that name (a nomi.toml whose [package] name is
// name): one under root, at most three levels
// down, or a sibling of root. "" when there is none or several.
func findLocalPackage(root, name string) string {
	var found []string
	consider := func(dir string) {
		if sameDir(dir, root) {
			return
		}
		mf, err := ReadManifest(dir)
		if err != nil || mf == nil {
			return
		}
		if mf.Name == name {
			found = append(found, dir)
		}
	}
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		if depth > 3 {
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || e.Name() == "node_modules" {
				continue
			}
			sub := filepath.Join(dir, e.Name())
			consider(sub)
			walk(sub, depth+1)
		}
	}
	walk(root, 1)
	if parent := filepath.Dir(root); parent != root {
		if entries, err := os.ReadDir(parent); err == nil {
			for _, e := range entries {
				if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
					consider(filepath.Join(parent, e.Name()))
				}
			}
		}
	}
	if len(found) != 1 {
		return ""
	}
	rel, err := filepath.Rel(root, found[0])
	if err != nil {
		return ""
	}
	return AddArg(rel)
}

func sameDir(a, b string) bool {
	ai, errA := os.Stat(a)
	bi, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(ai, bi)
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
