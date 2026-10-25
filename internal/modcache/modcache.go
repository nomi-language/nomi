// Package modcache answers which Go modules a Nomi project needs the source
// of, and where each one is: a local `replace` directory, or its extracted
// copy in the Go module cache (GOMODCACHE). It reads only the disk and never
// downloads, so the language server, the checker and the FFI wrapper can use
// it. internal/modfetch puts missing modules in the cache; a process that may
// download (the `nomi` CLI) installs it with SetFetcher, and Load calls it
// before reading.
//
// The modules a project needs are the direct requires of its go.mod (those
// without `// indirect`), and, for each of them that is a Nomi module (its
// root holds a nomi.toml), every module its own go.mod requires that the
// project's go.mod also lists, transitively. Each is taken at the version the
// project's go.mod requires: since Go 1.17 a tidy go.mod lists every module
// its build needs at the version minimal version selection picks, and
// `nomi add` and `nomi fetch` keep it that way. A Nomi module's requirement
// the project's go.mod does not list is Unlisted, which `nomi fetch` adds.
package modcache

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
)

// Dep is one module the project needs.
type Dep struct {
	module.Version
	// Dir is the module's root directory: the local replace target, or the
	// extracted copy in the module cache. Empty when the module is Missing.
	Dir string
	// Local is set for a module the project's go.mod replaces with a local
	// directory.
	Local bool
	// Nomi is set when Dir holds a nomi.toml.
	Nomi bool
	// Direct is set for a direct require of the project's go.mod.
	Direct bool
	// Source is the module whose files Dir holds when the project's go.mod
	// replaces Version with another module version; otherwise Version.
	Source module.Version
}

// Graph is what Load answers for a project.
type Graph struct {
	// Root is the directory holding the project's go.mod.
	Root string
	// GoMod is the project's parsed go.mod.
	GoMod *modfile.File
	// Deps are the modules the project needs, in the order they were
	// found: direct requires first, sorted by path, then what Nomi modules
	// among them require.
	Deps []Dep
	// Missing are the needed modules that are not in the module cache, by
	// the version the project's go.mod requires.
	Missing []module.Version
	// Unlisted are modules a needed Nomi module requires that the project's
	// go.mod does not list, at the highest version any needed Nomi module
	// requires.
	Unlisted []module.Version
}

// Find answers the dependency dir path, or false.
func (g *Graph) Find(path string) (Dep, bool) {
	for _, d := range g.Deps {
		if d.Path == path {
			return d, true
		}
	}
	return Dep{}, false
}

// Materialize answers the root directory of m's files in the module cache,
// or "" when it is not there. Load's cache-only answer reads the cache;
// a fetcher's downloads first.
type Materialize func(m module.Version) (string, error)

// Walk reads root/go.mod and answers the graph, with each non-local module's
// directory from materialize. A go.mod that is missing answers a nil graph
// and no error.
func Walk(root string, materialize Materialize) (*Graph, error) {
	path := filepath.Join(root, "go.mod")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	f, err := modfile.Parse(path, data, nil)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	g := &Graph{Root: root, GoMod: f}
	self := ""
	if f.Module != nil {
		self = f.Module.Mod.Path
	}
	listed := map[string]*modfile.Require{}
	for _, r := range f.Require {
		listed[r.Mod.Path] = r
	}
	seen := map[string]bool{}
	unlisted := map[string]string{}
	var queue []module.Version
	direct := map[string]bool{}
	for _, r := range f.Require {
		if !r.Indirect {
			direct[r.Mod.Path] = true
		}
		// A local replace costs nothing to read, so an indirect require
		// with one is read too.
		if _, local := localReplace(f, root, r.Mod); !r.Indirect || local {
			queue = append(queue, r.Mod)
		}
	}
	sort.Slice(queue, func(i, j int) bool { return queue[i].Path < queue[j].Path })
	for len(queue) > 0 {
		m := queue[0]
		queue = queue[1:]
		if seen[m.Path] {
			continue
		}
		seen[m.Path] = true
		dep := Dep{Version: m, Direct: direct[m.Path], Source: m}
		if dir, local := localReplace(f, root, m); local {
			info, statErr := os.Stat(dir)
			switch {
			case statErr != nil:
				return nil, fmt.Errorf("replace target for %q does not exist: %s", m.Path, dir)
			case !info.IsDir():
				return nil, fmt.Errorf("replace target for %q is not a directory: %s", m.Path, dir)
			}
			dep.Dir, dep.Local = dir, true
		} else {
			dep.Source = replacedVersion(f, m)
			dir, err := materialize(dep.Source)
			if err != nil {
				return nil, err
			}
			if dir == "" {
				g.Missing = append(g.Missing, m)
				g.Deps = append(g.Deps, dep)
				continue
			}
			dep.Dir = dir
		}
		dep.Nomi = isFile(filepath.Join(dep.Dir, "nomi.toml"))
		g.Deps = append(g.Deps, dep)
		if !dep.Nomi {
			continue
		}
		reqs, err := requiresOf(dep.Dir)
		if err != nil {
			return nil, err
		}
		for _, r := range reqs {
			if r.Path == self {
				continue
			}
			if l, ok := listed[r.Path]; ok {
				queue = append(queue, l.Mod)
				continue
			}
			if v, ok := unlisted[r.Path]; !ok || semver.Compare(r.Version, v) > 0 {
				unlisted[r.Path] = r.Version
			}
		}
	}
	for path, v := range unlisted {
		g.Unlisted = append(g.Unlisted, module.Version{Path: path, Version: v})
	}
	sort.Slice(g.Unlisted, func(i, j int) bool { return g.Unlisted[i].Path < g.Unlisted[j].Path })
	return g, nil
}

// requiresOf is the require list of the go.mod in dir.
func requiresOf(dir string) ([]module.Version, error) {
	path := filepath.Join(dir, "go.mod")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	f, err := modfile.ParseLax(path, data, nil)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	out := make([]module.Version, 0, len(f.Require))
	for _, r := range f.Require {
		out = append(out, r.Mod)
	}
	return out, nil
}

// localReplace answers the directory f replaces m with, when it is a local
// one. A replace naming a version applies to that version only.
func localReplace(f *modfile.File, root string, m module.Version) (string, bool) {
	r := replaceFor(f, m)
	if r == nil || r.New.Version != "" || r.New.Path == "" {
		return "", false
	}
	dir := r.New.Path
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(root, dir)
	}
	return filepath.Clean(dir), true
}

// replacedVersion answers the module version whose files stand for m: the
// target of a module-to-module replace, else m.
func replacedVersion(f *modfile.File, m module.Version) module.Version {
	r := replaceFor(f, m)
	if r == nil || r.New.Version == "" {
		return m
	}
	return r.New
}

func replaceFor(f *modfile.File, m module.Version) *modfile.Replace {
	var any *modfile.Replace
	for _, r := range f.Replace {
		if r.Old.Path != m.Path {
			continue
		}
		if r.Old.Version == m.Version {
			return r
		}
		if r.Old.Version == "" {
			any = r
		}
	}
	return any
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
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	if err, ok := fetched[abs]; ok {
		return err
	}
	// Only a Nomi module (nomi.toml beside go.mod) has dependencies fetched
	// for it. A Go module that happens to hold .nomi files, such as a Go
	// project with a Nomi script in it, keeps its Go dependencies to the go
	// command; what of them is in the cache is still read.
	if !isFile(filepath.Join(abs, "nomi.toml")) {
		return nil
	}
	err = fetcher(abs)
	fetched[abs] = err
	return err
}

// MissingHint is the hint a diagnostic about an import that names no file
// gives when the project's go.mod requires modules the module cache does
// not hold, or "" when it holds them all. It never downloads.
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
		return "go.mod requires module " + names[0] + ", which is not in the module cache; run `nomi fetch` to download it"
	}
	return "go.mod requires modules " + strings.Join(names, ", ") +
		", which are not in the module cache; run `nomi fetch` to download them"
}

// ImportProblem says what the go.mod at root lacks for an import of the
// module named name (its path, or the last element of its path), for a
// diagnostic about an import that names no file. problem and hint are ""
// when there is no go.mod, or when go.mod requires a Nomi module of that
// name. A module go.mod requires whose directory holds no nomi.toml is
// that problem; unlisted is set when go.mod has no require line for it at
// all, which a project file the import meant would also explain. It never
// downloads.
func ImportProblem(root, name string) (problem, hint string, unlisted bool) {
	g, err := Walk(root, cacheOnly)
	if err != nil || g == nil {
		return "", "", false
	}
	named := func(p string) bool { return p == name || pathBase(p) == name }
	for _, d := range g.Deps {
		if !d.Direct || !named(d.Path) || d.Dir == "" {
			continue
		}
		if d.Nomi {
			return "", "", false
		}
		hint = fmt.Sprintf("a Nomi module's root holds a nomi.toml naming it: `[module]`, then `name = %q`", name)
		if d.Local {
			return fmt.Sprintf("go.mod replaces `%s` with %s, which has no nomi.toml", d.Path, replaceFor(g.GoMod, d.Version).New.Path), hint, false
		}
		return fmt.Sprintf("module `%s` has no nomi.toml, so it is not a Nomi module", d.Path), hint, false
	}
	problem = fmt.Sprintf("go.mod has no `require` line for `%s`", name)
	for _, r := range g.GoMod.Replace {
		if named(r.Old.Path) && r.New.Version == "" {
			return problem, fmt.Sprintf("go.mod replaces `%s` with %s but does not require it; run `nomi add %s`",
				r.Old.Path, r.New.Path, AddArg(r.New.Path)), true
		}
	}
	if dir := findLocalModule(root, name); dir != "" {
		return problem, fmt.Sprintf("run `nomi add %s` to add the module in that directory", dir), true
	}
	return problem, "run `nomi add <module path>` to add a published module, or `nomi add <directory>` for one on disk", true
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
// holds a Nomi module of that name (a nomi.toml naming it, or a go.mod whose
// module path ends in it, beside a nomi.toml): one under root, at most three
// levels down, or a sibling of root. "" when there is none or several.
func findLocalModule(root, name string) string {
	var found []string
	consider := func(dir string) {
		if sameDir(dir, root) || !isFile(filepath.Join(dir, "nomi.toml")) {
			return
		}
		data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err != nil {
			return
		}
		path := modfile.ModulePath(data)
		manifest, _ := os.ReadFile(filepath.Join(dir, "nomi.toml"))
		if path == name || pathBase(path) == name || manifestNames(string(manifest), name) {
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

// manifestNames reports whether a nomi.toml's text gives its module the
// name name, read loosely: a `name = "..."` line.
func manifestNames(text, name string) bool {
	for _, line := range strings.Split(text, "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok && strings.TrimSpace(key) == "name" && strings.Trim(strings.TrimSpace(value), `"'`) == name {
			return true
		}
	}
	return false
}

func sameDir(a, b string) bool {
	ai, errA := os.Stat(a)
	bi, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(ai, bi)
}

// LocalReplace answers the directory f, the go.mod at root, replaces m with
// when it is a local one.
func LocalReplace(f *modfile.File, root string, m module.Version) (string, bool) {
	return localReplace(f, root, m)
}

// ReplacedVersion answers the module version whose files stand for m under
// f's replace directives.
func ReplacedVersion(f *modfile.File, m module.Version) module.Version {
	return replacedVersion(f, m)
}

// PathBase is the last element of a module path, without a /vN major
// version suffix: `project` for github.com/you/project/v2.
func PathBase(p string) string {
	if prefix, _, ok := module.SplitPathVersion(p); ok && prefix != "" {
		p = prefix
	}
	return pathBase(p)
}

// pathBase is the last element of a module path.
func pathBase(p string) string {
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
