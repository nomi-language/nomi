package modcache

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/mod/semver"
)

// Dep is one module the project needs.
type Dep struct {
	// Module is the module's repository, subdirectory and the version
	// selected for it. A module loaded from a directory has the ID and
	// version its entry writes, which may be none.
	Module
	// Name is the module's name in the program: the first segment of each
	// of its files' keys, and the key of its entry under
	// [effects.deps.modules]. It is the project's name for the module when
	// the project names it, and otherwise the name the first module that
	// reaches it gives it, with a `~2` suffix (`~3`, ...) when another
	// module or a file at the project's root already has that name.
	Name string
	// Dir is the module's root directory: the local directory, or its
	// directory in the module cache. Empty when the module is Missing.
	Dir string
	// Local is set for a module loaded from a directory: one a dir entry
	// of the project names, or a dir entry of another module loaded from a
	// directory (localModules).
	Local bool
	// Nomi is set when Dir holds a nomi.toml.
	Nomi bool
	// Direct is set for a module the project's nomi.toml names.
	Direct bool
	// Via is the Name of the module whose [dependencies] first reached
	// this one, "" for a module the project names.
	Via string
	// Self is the module's own [module].name, which names it in the
	// project only when the project's key for it is the same: an effects
	// list that writes it is told the key (Name).
	Self string
	// Names is each name the module's own files import a module by, its
	// own [module].name (Self) among them, and that module's Name.
	Names map[string]string
}

// Graph is what Load answers for a project.
type Graph struct {
	// Root is the directory holding the project's nomi.toml.
	Root string
	// Module is the project's nomi.toml, as this package reads it.
	Module *ModuleFile
	// Names is each name the project's files import a dependency by, and
	// that dependency's Name.
	Names map[string]string
	// Deps are the modules the project needs, in the order they were
	// found: the project's dependencies first, sorted by name, then what
	// they need.
	Deps []Dep
	// Missing are the needed modules that are not in the module cache, at
	// the versions selected for them.
	Missing []Module
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

// Materialize answers the root directory of m's files in the module cache,
// or "" when it is not there. Load's cache-only answer reads the cache;
// a fetcher's fetches first.
type Materialize func(m Module) (string, error)

// MainModuleEnv names the environment variable `nomi tool` sets to the
// project's directory, the main module a tool runs in. A dependency's
// tool runs in the project's build, so the nomi.toml at the tool's own
// root, which may be a module cache copy whose dir entries name its
// neighbors in its repository, is not read as the main module's: the
// tool's graph is the project's (MainModule). It is an environment
// variable so that a process the run starts reads the same graph.
const MainModuleEnv = "NOMI_MAIN_MODULE"

// MainModule is the directory whose nomi.toml is the main module of a
// program rooted at root: MainModuleEnv's directory when root is the
// directory of a module that directory needs, else root. It reads only the
// disk.
func MainModule(root string) string {
	main := os.Getenv(MainModuleEnv)
	if main == "" || sameDir(main, root) {
		return root
	}
	g, err := walk(main, cacheOnly, true)
	if err != nil || g == nil {
		return root
	}
	if _, ok := g.At(root); ok {
		return main
	}
	return root
}

// Walk reads the nomi.toml of root's main module (MainModule) and the
// nomi.toml of each module it needs, and answers the graph, with each
// fetched module in the directory materialize answers. A missing
// nomi.toml answers a nil graph and no error.
//
// A version is selected for each repository the walk reaches, since one
// tag versions a whole repository: the highest of the version the main
// module's nomi.lock locks it at and the versions any reached module's
// [dependencies] asks for, and every module of that repository is taken
// at it. The walk reads the requirements of the selected versions only,
// and repeats until no reached module asks for a higher one. With the lock
// current, that is the lock's build list, read without a version the lock
// does not hold. Two requirements on one repository in different lines
// (Line: a major version from v1 on, a minor version below it) are an
// error that names both requirers.
func Walk(root string, materialize Materialize) (*Graph, error) {
	return walk(MainModule(root), materialize, true)
}

// Resolve is Walk without the lock: the versions the requirements alone
// select, which `nomi fetch`, `add`, `remove` and `update` lock.
func Resolve(root string, materialize Materialize) (*Graph, error) {
	return walk(MainModule(root), materialize, false)
}

func walk(root string, materialize Materialize, locked bool) (*Graph, error) {
	mf, err := ReadModuleFile(root)
	if tooOld := (*NomiVersionError)(nil); errors.As(err, &tooOld) {
		tooOld.Module += " (this project)"
	}
	if err != nil || mf == nil {
		return nil, err
	}
	selected := map[string]string{}
	if locked {
		lock, err := ReadLock(root)
		if err != nil {
			return nil, err
		}
		for _, m := range lock.Modules {
			if semver.Compare(m.Version, selected[m.Repo]) > 0 {
				selected[m.Repo] = m.Version
			}
		}
	}
	for {
		g, want, conflict, err := explore(root, mf, selected, materialize)
		if err != nil {
			return nil, err
		}
		raised := false
		for repo, v := range want {
			if semver.Compare(v, selected[repo]) > 0 {
				selected[repo] = v
				raised = true
			}
		}
		if !raised {
			if conflict != nil {
				return nil, conflict
			}
			return g, nil
		}
	}
}

// local is a module loaded from a directory: one a dir entry of the main
// module names, or a dir entry of another module loaded from a directory.
type local struct {
	dir  string
	file *ModuleFile
	req  Requirement
	// requires is file's [dependencies] as the walk reads them, and dirs
	// the module each of its dir entries is, by the entry's name. An entry
	// dirs lacks is read by its ID and version.
	requires []Requirement
	dirs     map[string]*local
}

// localModules reads the modules loaded from directories: those the main
// module's dir entries name, then those the dir entries of each module so
// loaded name, each relative to the directory of the nomi.toml that writes
// it, since that directory is on disk. It answers the main module's, by
// entry name, and the module each ID stands for: a dir entry that also
// names a repository stands for that module wherever the graph reaches it.
//
// The main module's entry for a module wins: a dependency's dir entry for
// a module the main module names, by a dir or a version, is read by its
// ID. Otherwise the first directory reached for an ID stands for it. A
// directory reached again is the module read before, so a cycle of
// directories ends, and a dir entry for the main module's own directory is
// not read. A fetched module's dir entries are read as its neighbors in
// its repository (neighbor), never as directories on this disk.
func localModules(root string, mf *ModuleFile) (top map[string]*local, byID map[ID]*local, err error) {
	named := map[ID]bool{}
	for _, r := range mf.Requires {
		if !r.ID.IsZero() {
			named[r.ID] = true
		}
	}
	byDir := map[string]*local{}
	byID = map[ID]*local{}
	var queue []*local
	read := func(owner string, r Requirement) (*local, error) {
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
		file, err := ReadModuleFile(dir)
		if tooOld := (*NomiVersionError)(nil); errors.As(err, &tooOld) {
			tooOld.Module = "the module in " + r.Dir
		}
		if err != nil {
			return nil, err
		}
		l := &local{dir: dir, file: file, req: r, dirs: map[string]*local{}}
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
		l, err := read(root, r)
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
			l, err := read(owner.dir, r)
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
// and who asked.
type ask struct{ version, by string }

// atText is v as an error names a requirement at it: its tag, or `at commit`
// and the commit.
func atText(v string) string {
	if IsCommit(v) {
		return "at commit " + v
	}
	return v
}

// clash is the error for asks a and b on repo, which one build cannot take
// together, or nil. A build takes a repository at one point: one tag, so
// every requirement on it is in one line (Line), or one commit, which
// every requirement on it names.
func clash(repo string, a, b ask) error {
	var why string
	switch ca, cb := IsCommit(a.version), IsCommit(b.version); {
	case ca != cb:
		why = "a build takes a repository at one point, so its requirements are all at tags or all at one commit"
	case ca && a.version != b.version:
		why = "a build takes a repository at one point, so every requirement on it at a commit names the same commit"
	case !ca && Line(a.version) != Line(b.version):
		why = "one tag versions a whole repository, so every requirement on it must be in one line, a major version from v1 on and a minor version below v1"
	default:
		return nil
	}
	return fmt.Errorf("%s requires %s %s and %s requires %s %s: %s", a.by, repo, atText(a.version), b.by, repo, atText(b.version), why)
}

// explore is one pass of walk: the modules reached from root's
// requirements through the requirements of each reached module, each
// fetched at selected's version for its repository when that is higher
// than the one asked for, and the highest version each repository is
// asked for, of the tags asked for. conflict is the error for two
// requirements on one repository that one build cannot take together
// (clash), which walk answers when the pass is its
// last.
func explore(root string, mf *ModuleFile, selected map[string]string, materialize Materialize) (g *Graph, want map[string]string, conflict, err error) {
	g = &Graph{Root: root, Module: mf, Names: map[string]string{}}
	want = map[string]string{}
	localOf, locals, err := localModules(root, mf)
	if err != nil {
		return nil, nil, nil, err
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
		// by names the module whose nomi.toml writes req, and is "" for
		// a neighbor a fetched module names by its directory, which is at
		// that module's own tag and asks for no version of its own.
		by string
		// via is the Name of the module whose nomi.toml writes req, ""
		// for the project's.
		via string
	}
	project := "this project"
	if mf.Name != "" {
		project = mf.Name
	}
	var queue []item
	for _, r := range mf.Requires {
		queue = append(queue, item{owner: g.Names, req: r, l: localOf[r.Name], root: true, by: project})
	}
	type depKey struct {
		dir string
		id  ID
	}
	byKey := map[depKey]int{}
	asked := map[string]ask{}
	// floors are the [indirect] entries of every module read, each an ask
	// on its repository by the module that writes it, which counts once
	// the pass knows the repository is reached.
	type floor struct {
		repo string
		ask
	}
	var floors []floor
	for _, fl := range mf.Indirect {
		floors = append(floors, floor{fl.Repo, ask{fl.Version, project}})
	}
	for len(queue) > 0 {
		it := queue[0]
		queue = queue[1:]
		r := it.req
		l := it.l
		if l == nil {
			if r.ID.IsZero() {
				// A dir entry for the main module's own directory.
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
			if it.by != "" {
				if first, ok := asked[r.Repo]; !ok {
					asked[r.Repo] = ask{r.Version, it.by}
				} else if conflict == nil {
					conflict = clash(r.Repo, first, ask{r.Version, it.by})
				}
				if semver.Compare(r.Version, want[r.Repo]) > 0 {
					want[r.Repo] = r.Version
				}
			}
			// A commit is taken as it is named; a tag at the version
			// selected for its repository when that is higher.
			version = r.Version
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
		var file *ModuleFile
		if l != nil {
			dep.Module = Module{ID: l.req.ID, Version: l.req.Version}
			dep.Dir, dep.Local, file = l.dir, true, l.file
		} else {
			dep.Module = Module{ID: r.ID, Version: version}
			dir, err := materialize(dep.Module)
			if err != nil {
				return nil, nil, nil, err
			}
			if dir == "" {
				g.Missing = append(g.Missing, dep.Module)
			} else if file, err = ReadModuleFile(dir); err != nil {
				if tooOld := (*NomiVersionError)(nil); errors.As(err, &tooOld) {
					tooOld.Module = "module " + dep.Module.String()
				}
				return nil, nil, nil, err
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
			by := dep.Module.String()
			if dep.Local {
				by = dep.Dir
			}
			requires, dirs := file.Requires, map[string]*local(nil)
			if l != nil {
				requires, dirs = l.requires, l.dirs
			}
			for _, fl := range file.Indirect {
				floors = append(floors, floor{fl.Repo, ask{fl.Version, by}})
			}
			for _, sub := range requires {
				subBy := by
				if l == nil && sub.Dir != "" {
					var ok bool
					if sub, ok, err = neighbor(dep.Module, sub); err != nil {
						return nil, nil, nil, err
					} else if !ok {
						continue
					}
					subBy = ""
				}
				queue = append(queue, item{owner: dep.Names, req: sub, l: dirs[sub.Name], by: subBy, via: name})
			}

		}
		g.Deps = append(g.Deps, dep)
	}
	// A floor raises a repository a requirement reaches, and is held to
	// the requirements on it as another requirement is (clash).
	for _, fl := range floors {
		first, reached := asked[fl.repo]
		if !reached {
			continue
		}
		if conflict == nil {
			conflict = clash(fl.repo, first, fl.ask)
		}
		if semver.Compare(fl.version, want[fl.repo]) > 0 {
			want[fl.repo] = fl.version
		}
	}
	return g, want, conflict, nil
}

// rootNames are the names a module the project does not name may not take
// in the program: `std`, the project's own name, and each file and
// directory at its root, which the project's own imports name.
func rootNames(root string, mf *ModuleFile) map[string]bool {
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

// NamesAt is each name the files of the module rooted at dir import a
// module by, and that module's Name: the project's own names when dir is
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

// cacheOnly materializes from the module cache alone.
func cacheOnly(m Module) (string, error) {
	cache, err := Root()
	if err != nil {
		return "", nil
	}
	return Cached(cache, m), nil
}

// CacheOnly is the Materialize that reads the module cache and never
// fetches.
func CacheOnly(m Module) (string, error) { return cacheOnly(m) }

var (
	fetchMu sync.Mutex
	fetcher func(root string) error
	fetched = map[string]error{}
)

// SetFetcher installs fetch, which puts the modules a project at root needs
// into the module cache before Load reads it. It runs once per root per
// process, for a root that holds a nomi.toml. Only a process that may
// fetch installs one: the `nomi` CLI does, the language server never does.
func SetFetcher(fetch func(root string) error) {
	fetchMu.Lock()
	defer fetchMu.Unlock()
	fetcher = fetch
	fetched = map[string]error{}
}

// Load is Walk over the module cache, after the installed fetcher (if any)
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
	abs, err := filepath.Abs(MainModule(root))
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
// gives when the project needs modules the module cache does not hold, or
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
		return "the project needs module " + names[0] + ", which is not in the module cache; run `nomi fetch` to download it"
	}
	return "the project needs modules " + strings.Join(names, ", ") +
		", which are not in the module cache; run `nomi fetch` to download them"
}

// ImportProblem says what the project at root lacks for an import whose
// first name is name, for a diagnostic about an import that names no file.
// problem and hint are "" when root has no nomi.toml, or when name is a
// Nomi module the project depends on. A dependency whose directory holds
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
		hint = fmt.Sprintf("a Nomi module's root holds a nomi.toml naming it: `[module]`, then `name = %q`", name)
		if d.Local {
			return fmt.Sprintf("[dependencies] %s is the directory %s, which has no nomi.toml", name, AddArg(relTo(root, d.Dir))), hint, false
		}
		return fmt.Sprintf("module `%s` has no nomi.toml, so it is not a Nomi module", d.ID), hint, false
	}
	problem = fmt.Sprintf("nomi.toml has no dependency named `%s`", name)
	if dir := findLocalModule(root, name); dir != "" {
		return problem, fmt.Sprintf("run `nomi add %s` to add the module in that directory", dir), true
	}
	return problem, "run `nomi add <repository>` to add a published module, such as `nomi add github.com/owner/name`, or `nomi add <directory>` for one on disk", true
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

// findLocalModule answers, relative to root, a directory near root that
// holds a Nomi module of that name (a nomi.toml whose [module] name is
// name): one under root, at most three levels
// down, or a sibling of root. "" when there is none or several.
func findLocalModule(root, name string) string {
	var found []string
	consider := func(dir string) {
		if sameDir(dir, root) {
			return
		}
		mf, err := ReadModuleFile(dir)
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
