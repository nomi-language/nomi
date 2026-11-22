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

	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
)

// Dep is one module the project needs.
type Dep struct {
	// Version is the module's path and the version selected for it. A
	// local module's Version is the one its entry writes, which may be
	// empty, and a local module whose nomi.toml gives no path has no
	// Path.
	module.Version
	// Name is the module's name in the program: the first segment of each
	// of its files' keys, and the key of its entry under
	// [effects.deps.modules]. It is the project's name for the module when
	// the project names it, and otherwise the name the first module that
	// reaches it gives it, with a `~2` suffix (`~3`, ...) when another
	// module or a file at the project's root already has that name.
	Name string
	// Dir is the module's root directory: the local directory, or the
	// extracted copy in the module cache. Empty when the module is Missing.
	Dir string
	// Local is set for a module loaded from a directory: one a dir entry
	// of the project names, or a dir entry of another module loaded from a
	// directory (localModules).
	Local bool
	// Nomi is set when Dir holds a nomi.toml.
	Nomi bool
	// Direct is set for a module the project's nomi.toml names.
	Direct bool
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
	Missing []module.Version
}

// Find answers the dependency with module path path, or false.
func (g *Graph) Find(path string) (Dep, bool) {
	for _, d := range g.Deps {
		if d.Path == path && path != "" {
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
// a fetcher's downloads first.
type Materialize func(m module.Version) (string, error)

// MainModuleEnv names the environment variable `nomi tool` sets to the
// project's directory, the main module a tool runs in. A downloaded
// module's dir entries are not read, and a dependency's tool runs in the
// project's build, so the nomi.toml at the tool's own root,
// whose dir entries name sibling directories of its checkout that a
// module cache copy does not hold, is not read as the main module's: the
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
// module from the proxy in the directory materialize answers. A missing
// nomi.toml answers a nil graph and no error.
//
// A version is selected for each module path the walk reaches: the
// highest of the version the main module's nomi.lock locks it at and the
// versions any reached module's [dependencies] asks for. The walk reads the
// requirements of the selected versions only, and repeats until no reached
// module asks for a higher one. With the lock current, that is the lock's
// build list, read without a version the lock does not hold.
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
			selected[m.Path] = m.Version.Version
		}
	}
	for {
		g, want, err := explore(root, mf, selected, materialize)
		if err != nil {
			return nil, err
		}
		raised := false
		for path, v := range want {
			if semver.Compare(v, selected[path]) > 0 {
				selected[path] = v
				raised = true
			}
		}
		if !raised {
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
	// dirs lacks is read by its module path and version.
	requires []Requirement
	dirs     map[string]*local
}

// localModules reads the modules loaded from directories: those the main
// module's dir entries name, then those the dir entries of each module so
// loaded name, each relative to the directory of the nomi.toml that writes
// it, since that directory is on disk. It answers the main module's, by
// entry name, and the module each module path stands for.
//
// The main module's entry for a module path wins: a dependency's dir entry
// for a path the main module names, by a dir or a version, is read by its
// path. Otherwise the first directory reached for a path stands for it. A
// directory reached again is the module read before, so a cycle of
// directories ends, and a dir entry for the main module's own directory is
// not read. A downloaded module's dir entries are never read: its
// directory is a module cache copy, and they name its author's disk.
func localModules(root string, mf *ModuleFile) (top, byPath map[string]*local, err error) {
	named := map[string]bool{}
	for _, r := range mf.Requires {
		if r.Module != "" {
			named[r.Module] = true
		}
	}
	byDir := map[string]*local{}
	byPath = map[string]*local{}
	var queue []*local
	read := func(owner string, r Requirement) (*local, string, error) {
		dir := r.LocalDir(owner)
		key := dir
		if real, err := filepath.EvalSymlinks(dir); err == nil {
			key = real
		}
		l, ok := byDir[key]
		if !ok {
			info, err := os.Stat(dir)
			switch {
			case err != nil:
				return nil, "", fmt.Errorf("%s: [dependencies] %s: directory %s does not exist", filepath.Join(owner, ManifestName), r.Name, r.Dir)
			case !info.IsDir():
				return nil, "", fmt.Errorf("%s: [dependencies] %s: %s is not a directory", filepath.Join(owner, ManifestName), r.Name, r.Dir)
			}
			file, err := ReadModuleFile(dir)
			if err != nil {
				return nil, "", err
			}
			l = &local{dir: dir, file: file, req: r, dirs: map[string]*local{}}
			if file != nil {
				l.requires = file.Requires
			}
			byDir[key] = l
			queue = append(queue, l)
		}
		path := r.Module
		if path == "" && l.file != nil {
			path = l.file.Path
		}
		return l, path, nil
	}
	top = map[string]*local{}
	for _, r := range mf.Requires {
		if r.Dir == "" {
			continue
		}
		l, path, err := read(root, r)
		if err != nil {
			return nil, nil, err
		}
		if path != "" {
			byPath[path] = l
		}
		top[r.Name] = l
	}
	for len(queue) > 0 {
		owner := queue[0]
		queue = queue[1:]
		requires := slices.Clone(owner.requires)
		for i, r := range requires {
			if r.Dir == "" || named[r.Module] || sameDir(r.LocalDir(owner.dir), root) {
				continue
			}
			l, path, err := read(owner.dir, r)
			if err != nil {
				return nil, nil, err
			}
			switch bound, ok := byPath[path]; {
			case named[path]:
				// An entry with only a dir, whose directory holds a module
				// path the main module names: it is read by that path.
				requires[i].Module, requires[i].Dir = path, ""
				continue
			case ok:
				l = bound
			case path != "":
				byPath[path] = l
			}
			owner.dirs[r.Name] = l
		}
		owner.requires = requires
	}
	return top, byPath, nil
}

// explore is one pass of walk: the modules reached from root's
// requirements through the requirements of each reached module, each from
// the proxy at selected's version when that is higher than the one asked
// for, and the highest version each module path is asked for.
func explore(root string, mf *ModuleFile, selected map[string]string, materialize Materialize) (*Graph, map[string]string, error) {
	g := &Graph{Root: root, Module: mf, Names: map[string]string{}}
	want := map[string]string{}
	localOf, locals, err := localModules(root, mf)
	if err != nil {
		return nil, nil, err
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
	}
	var queue []item
	for _, r := range mf.Requires {
		queue = append(queue, item{owner: g.Names, req: r, l: localOf[r.Name], root: true})
	}
	byID := map[string]int{}
	for len(queue) > 0 {
		it := queue[0]
		queue = queue[1:]
		r := it.req
		l := it.l
		version := ""
		if l == nil {
			if r.Module == "" {
				// A downloaded module's entry with only a dir names its
				// author's disk, and is not read.
				continue
			}
			l = locals[r.Module]
			if l == nil && r.Version == "" {
				// An entry with no version, for a path the main module
				// names, is the main module's: its queue items come first.
				if i, seen := byID[r.Module]; seen {
					it.owner[r.Name] = g.Deps[i].Name
				}
				continue
			}
		}
		id := ""
		if l != nil {
			id = "dir:" + l.dir
		} else {
			id = r.Module
			if semver.Compare(r.Version, want[r.Module]) > 0 {
				want[r.Module] = r.Version
			}
			version = r.Version
			if semver.Compare(selected[r.Module], version) > 0 {
				version = selected[r.Module]
			}
		}
		if i, seen := byID[id]; seen {
			it.owner[r.Name] = g.Deps[i].Name
			continue
		}
		name := r.Name
		if it.root {
			taken[name] = true
		} else {
			name = unique(name)
		}
		dep := Dep{Name: name, Direct: it.root, Names: map[string]string{}}
		var file *ModuleFile
		if l != nil {
			dep.Version = module.Version{Path: l.req.Module, Version: l.req.Version}
			if dep.Path == "" && l.file != nil {
				dep.Path = l.file.Path
			}
			dep.Dir, dep.Local, file = l.dir, true, l.file
		} else {
			dep.Version = module.Version{Path: r.Module, Version: version}
			dir, err := materialize(dep.Version)
			if err != nil {
				return nil, nil, err
			}
			if dir == "" {
				g.Missing = append(g.Missing, dep.Version)
			} else if file, err = ReadModuleFile(dir); err != nil {
				return nil, nil, err
			}
			dep.Dir = dir
		}
		byID[id] = len(g.Deps)
		it.owner[r.Name] = name
		dep.Nomi = file != nil
		if file != nil {
			dep.Self = file.Name
			if file.Name != "" {
				dep.Names[file.Name] = name
			}
			requires, dirs := file.Requires, map[string]*local(nil)
			if l != nil {
				requires, dirs = l.requires, l.dirs
			}
			for _, sub := range requires {
				queue = append(queue, item{owner: dep.Names, req: sub, l: dirs[sub.Name]})
			}
		}
		g.Deps = append(g.Deps, dep)
	}
	return g, want, nil
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

// Root is the Go module cache: GOMODCACHE, else pkg/mod in the first GOPATH
// entry, else $HOME/go/pkg/mod, as the go command decides it.
func Root() (string, error) {
	if v := os.Getenv("GOMODCACHE"); v != "" {
		return v, nil
	}
	if gp := os.Getenv("GOPATH"); gp != "" {
		first := filepath.SplitList(gp)[0]
		if first != "" {
			return filepath.Join(first, "pkg", "mod"), nil
		}
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", errors.New("no module cache: GOMODCACHE, GOPATH and the home directory are all unset")
	}
	return filepath.Join(home, "go", "pkg", "mod"), nil
}

// ExtractedDir is where the module cache holds m's extracted files.
func ExtractedDir(cache string, m module.Version) (string, error) {
	enc, err := module.EscapePath(m.Path)
	if err != nil {
		return "", err
	}
	ev, err := module.EscapeVersion(m.Version)
	if err != nil {
		return "", err
	}
	return filepath.Join(cache, filepath.FromSlash(enc)+"@"+ev), nil
}

// DownloadDir is the module cache's directory of m.Path's downloaded files:
// `<v>.mod`, `<v>.info`, `<v>.zip` and `<v>.ziphash`.
func DownloadDir(cache, path string) (string, error) {
	enc, err := module.EscapePath(path)
	if err != nil {
		return "", err
	}
	return filepath.Join(cache, "cache", "download", filepath.FromSlash(enc), "@v"), nil
}

// VersionFile is the downloaded file of m with suffix (".mod", ".zip",
// ".ziphash", ".info").
func VersionFile(cache string, m module.Version, suffix string) (string, error) {
	dir, err := DownloadDir(cache, m.Path)
	if err != nil {
		return "", err
	}
	ev, err := module.EscapeVersion(m.Version)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, ev+suffix), nil
}

// Cached answers m's extracted directory when the cache holds all of it: the
// directory, and its .ziphash, which the go command writes before it
// extracts and nomi writes the same way, so a directory without one is a
// partial extraction.
func Cached(cache string, m module.Version) string {
	dir, err := ExtractedDir(cache, m)
	if err != nil || !isDir(dir) {
		return ""
	}
	hash, err := VersionFile(cache, m, ".ziphash")
	if err != nil || !isFile(hash) {
		return ""
	}
	if partial, err := VersionFile(cache, m, ".partial"); err == nil && isFile(partial) {
		return ""
	}
	return dir
}

// cacheOnly materializes from the module cache alone.
func cacheOnly(m module.Version) (string, error) {
	cache, err := Root()
	if err != nil {
		return "", nil
	}
	return Cached(cache, m), nil
}

// CacheOnly is the Materialize that reads the module cache and never
// downloads.
func CacheOnly(m module.Version) (string, error) { return cacheOnly(m) }

var (
	fetchMu sync.Mutex
	fetcher func(root string) error
	fetched = map[string]error{}
)

// SetFetcher installs fetch, which puts the modules a project at root needs
// into the module cache before Load reads it. It runs once per root per
// process, for a root that holds a nomi.toml. Only a process that may download installs one: the `nomi` CLI
// does, the language server never does.
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
// "" when it holds them all. It never downloads.
func MissingHint(root string) string {
	g, err := Walk(root, cacheOnly)
	if err != nil || g == nil || len(g.Missing) == 0 {
		return ""
	}
	names := make([]string, 0, len(g.Missing))
	for _, m := range g.Missing {
		names = append(names, m.Path+" "+m.Version)
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
// It never downloads.
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
		return fmt.Sprintf("module `%s` has no nomi.toml, so it is not a Nomi module", d.Path), hint, false
	}
	problem = fmt.Sprintf("nomi.toml has no dependency named `%s`", name)
	if dir := findLocalModule(root, name); dir != "" {
		return problem, fmt.Sprintf("run `nomi add %s` to add the module in that directory", dir), true
	}
	return problem, "run `nomi add <module path>` to add a published module, or `nomi add <directory>` for one on disk", true
}

func relTo(root, dir string) string {
	if rel, err := filepath.Rel(root, dir); err == nil {
		return rel
	}
	return dir
}

// AddArg spells a directory, relative to the project root, as `nomi add`
// takes it: a local path starts with ./ or ../, so it is not read as a
// module path.
func AddArg(dir string) string {
	dir = filepath.ToSlash(dir)
	if filepath.IsAbs(dir) || dir == "." || dir == ".." || strings.HasPrefix(dir, "./") || strings.HasPrefix(dir, "../") {
		return dir
	}
	return "./" + dir
}

// findLocalModule answers, relative to root, a directory near root that
// holds a Nomi module of that name (a nomi.toml whose [module] name is
// name, or whose path ends in it): one under root, at most three levels
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
		if mf.Name == name || mf.Path != "" && PathBase(mf.Path) == name {
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

// PathBase is the last element of a module path, without a /vN major
// version suffix: `project` for github.com/you/project/v2.
func PathBase(p string) string {
	if prefix, _, ok := module.SplitPathVersion(p); ok && prefix != "" {
		p = prefix
	}
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
