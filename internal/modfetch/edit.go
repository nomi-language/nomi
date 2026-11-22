package modfetch

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/modcache"
)

// Added is what Add or AddLocal put in nomi.toml.
type Added struct {
	// Name is the dependency's name in [dependencies], the name the
	// project's files import it by.
	Name string
	// Module is its module path and the version selected for it; a local
	// module's Version is its entry's, often none.
	Module module.Version
	// Dir is the module's directory: its extracted copy in the module
	// cache, or the directory AddLocal added.
	Dir string
	// Local is the dir AddLocal wrote, relative to the project root; ""
	// for a module from the proxy.
	Local string
}

// editDependencies writes the project at root's [dependencies] table as
// edit makes it from the table's entries (analysis.WriteDependencies,
// which keeps the file's other lines and its layout).
func editDependencies(root string, edit func([]modcache.Requirement) ([]modcache.Requirement, error)) error {
	path := filepath.Join(root, modcache.ManifestName)
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	mf, err := modcache.ParseModuleFile(data)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	deps, err := edit(mf.Requires)
	if err != nil {
		return err
	}
	out, err := analysis.WriteDependencies(data, deps)
	if err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return writeFileAtomic(path, out, 0o644)
}

// put sets the entry named r.Name in deps to r, and refuses a module path
// another entry already names.
func put(deps []modcache.Requirement, r modcache.Requirement) ([]modcache.Requirement, error) {
	var out []modcache.Requirement
	for _, d := range deps {
		switch {
		case d.Name == r.Name:
			continue
		case r.Module != "" && d.Module == r.Module:
			return nil, fmt.Errorf("nomi.toml already names %s %s", r.Module, d.Name)
		}
		out = append(out, d)
	}
	return append(out, r), nil
}

// Add names the module arg (`path` or `path@version`; no version, or
// `@latest`, is the latest release) in the project at root's
// [dependencies], under name, or when name is "" under the name an entry
// already gives the module, else the module's own [module].name. It
// downloads what the project then needs and writes nomi.lock. The module
// must hold a nomi.toml.
func Add(root, name, arg string, opts Options) (Added, error) {
	opts.Update = true
	f, err := newFetcher(root, opts)
	if err != nil {
		return Added{}, err
	}
	path, query, _ := strings.Cut(arg, "@")
	if err := module.CheckPath(path); err != nil {
		return Added{}, fmt.Errorf("%s: %w", arg, err)
	}
	switch {
	case query == "" || query == "latest":
	case semver.IsValid(query) && semver.Canonical(query) == query || module.IsPseudoVersion(query):
	case semver.IsValid(query):
		return Added{}, fmt.Errorf("%s: write the full version, %s", arg, semver.Canonical(query))
	default:
		return Added{}, fmt.Errorf("%s: a version is `latest` or a full semantic version such as v1.2.3", arg)
	}
	// A path inside a module names the module that holds it, as `go get`
	// reads one: the longest prefix of it the proxy has.
	var m module.Version
	var dir string
	var firstErr error
	for candidate := path; ; {
		m, dir, err = f.resolve(candidate, query)
		if err == nil {
			break
		}
		var off *offlineError
		if firstErr == nil {
			firstErr = err
		}
		parent := candidate[:max(strings.LastIndex(candidate, "/"), 0)]
		if errors.As(err, &off) || parent == "" || module.CheckPath(parent) != nil {
			return Added{}, firstErr
		}
		candidate = parent
	}
	file, err := modcache.ReadModuleFile(dir)
	if err != nil {
		return Added{}, err
	}
	if file == nil {
		return Added{}, fmt.Errorf("%s %s has no nomi.toml, so it is not a Nomi module", m.Path, m.Version)
	}
	own, err := modcache.ReadModuleFile(root)
	if err != nil {
		return Added{}, err
	}
	if own.Path == m.Path {
		return Added{}, fmt.Errorf("%s is this project's own module", m.Path)
	}
	var kept modcache.Requirement
	for _, d := range own.Requires {
		if d.Module == m.Path && (name == "" || name == d.Name) {
			kept = d
		}
	}
	name = cmp.Or(name, kept.Name, file.Name)
	if err := editDependencies(root, func(deps []modcache.Requirement) ([]modcache.Requirement, error) {
		deps, err := put(deps, modcache.Requirement{Name: name, Module: m.Path, Version: m.Version, Dir: kept.Dir})
		if err != nil {
			return nil, err
		}
		version := ""
		if strings.HasPrefix(m.Path, libPrefix) {
			version = m.Version
		}
		return alignLib(deps, version), nil
	}); err != nil {
		return Added{}, err
	}
	g, err := f.fetch(root)
	if err != nil {
		return Added{}, err
	}
	added := Added{Name: name, Module: m}
	if g != nil {
		if d, ok := g.Find(m.Path); ok {
			added.Module, added.Dir = d.Version, d.Dir
		}
	}
	return added, nil
}

// AddLocal names the Nomi module in directory dir (absolute, or relative to
// the working directory) in the project at root's [dependencies] with a dir
// entry, dir relative to root, under name, or when name is "" under the
// module's own [module].name. An entry that names the module's path keeps
// its name and version and gains the dir. It then fetches what the module
// needs and writes nomi.lock.
func AddLocal(root, name, dir string, opts Options) (Added, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return Added{}, err
	}
	if info, err := os.Stat(abs); err != nil || !info.IsDir() {
		return Added{}, fmt.Errorf("%s is not a directory", dir)
	}
	if same(abs, root) {
		return Added{}, fmt.Errorf("%s is this project's own directory", dir)
	}
	file, err := modcache.ReadModuleFile(abs)
	if err != nil {
		return Added{}, err
	}
	if file == nil {
		return Added{}, fmt.Errorf("%s has no nomi.toml, so it is not a Nomi module", dir)
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		rel = abs
	}
	r := modcache.Requirement{Name: name, Dir: modcache.AddArg(rel)}
	if err := editDependencies(root, func(deps []modcache.Requirement) ([]modcache.Requirement, error) {
		for _, d := range deps {
			if file.Path != "" && d.Module == file.Path && (name == "" || name == d.Name) {
				r.Module, r.Version, r.Name = d.Module, d.Version, d.Name
			}
		}
		r.Name = cmp.Or(r.Name, file.Name)
		return put(deps, r)
	}); err != nil {
		return Added{}, err
	}
	opts.Update = true
	f, err := newFetcher(root, opts)
	if err != nil {
		return Added{}, err
	}
	if _, err := f.fetch(root); err != nil {
		return Added{}, err
	}
	return Added{Name: r.Name, Module: module.Version{Path: cmp.Or(r.Module, file.Path), Version: r.Version}, Dir: abs, Local: r.Dir}, nil
}

func same(a, b string) bool {
	ai, errA := os.Stat(a)
	bi, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(ai, bi)
}

// Removed is what Remove took out of nomi.toml and nomi.lock.
type Removed struct {
	// Entry is the [dependencies] entry it took out.
	Entry modcache.Requirement
	// Unneeded are the modules nomi.lock held that the project no longer
	// needs, the dependency's own module among them.
	Unneeded []module.Version
}

// Remove takes the entry named name out of the project at root's
// [dependencies], and writes nomi.lock as the build list without it: the
// modules only it needed go too.
func Remove(root, name string, opts Options) (Removed, error) {
	var out Removed
	if err := editDependencies(root, func(deps []modcache.Requirement) ([]modcache.Requirement, error) {
		var kept []modcache.Requirement
		for _, d := range deps {
			if d.Name == name {
				out.Entry = d
				continue
			}
			kept = append(kept, d)
		}
		if out.Entry.Name == "" {
			return nil, fmt.Errorf("nomi.toml has no dependency %s", name)
		}
		return kept, nil
	}); err != nil {
		return Removed{}, err
	}
	opts.Update = true
	f, err := newFetcher(root, opts)
	if err != nil {
		return Removed{}, err
	}
	g, err := f.fetch(root)
	if err != nil {
		return Removed{}, err
	}
	for _, m := range f.lock.Modules {
		if _, ok := g.Find(m.Path); !ok {
			out.Unneeded = append(out.Unneeded, m.Version)
		}
	}
	return out, nil
}

// Change is one module of the build list Update moved: From is "" for one
// it added, To "" for one the project no longer needs.
type Change struct {
	Path, From, To string
}

// Update moves the entry named only, or with only "" every entry the
// project at root's [dependencies] names with a version and no dir, to its
// latest release, as `go get -u` does: the highest release of the same
// module path, so a new major version (a /v2 path) is never taken, and a
// pre-release only when the entry already names a later one. An entry with
// a dir has no releases to move to. It downloads what the project then
// needs, writes nomi.lock, and answers each module of the build list that
// changed from the one nomi.lock held.
func Update(root, only string, opts Options) ([]Change, error) {
	opts.Update = true
	f, err := newFetcher(root, opts)
	if err != nil {
		return nil, err
	}
	own, err := modcache.ReadModuleFile(root)
	if err != nil {
		return nil, err
	}
	if own == nil {
		return nil, fmt.Errorf("no nomi.toml in %s", root)
	}
	moved := map[string]string{}
	found := false
	for _, r := range own.Requires {
		if only != "" && r.Name != only {
			continue
		}
		found = true
		if r.Dir != "" || r.Version == "" {
			if only != "" {
				return nil, fmt.Errorf("%s is the directory %s, which has no releases to update to", only, r.Dir)
			}
			continue
		}
		m := module.Version{Path: r.Module, Version: r.Version}
		upgrade := f.upgrade
		if strings.HasPrefix(m.Path, libPrefix) {
			// A lib/ module has no tags: its latest is the latest commit.
			upgrade = func(m module.Version) (string, error) {
				v, err := f.latest(m.Path)
				if err != nil || semver.Compare(v, m.Version) <= 0 {
					return m.Version, err
				}
				return v, nil
			}
		}
		v, err := upgrade(m)
		if err != nil {
			return nil, fmt.Errorf("finding the latest version of %s: %w", m.Path, err)
		}
		if v != m.Version {
			moved[r.Name] = v
		}
	}
	if only != "" && !found {
		return nil, fmt.Errorf("nomi.toml has no dependency %s", only)
	}
	if len(moved) > 0 {
		if err := editDependencies(root, func(deps []modcache.Requirement) ([]modcache.Requirement, error) {
			for i, d := range deps {
				if v, ok := moved[d.Name]; ok {
					deps[i].Version = v
				}
			}
			return alignLib(deps, ""), nil
		}); err != nil {
			return nil, err
		}
	}
	g, err := f.fetch(root)
	if err != nil {
		return nil, err
	}
	was := map[string]string{}
	for _, m := range f.lock.Modules {
		was[m.Path] = m.Version.Version
	}
	var changes []Change
	if g != nil {
		for _, d := range g.Deps {
			if d.Local || d.Path == "" {
				continue
			}
			if old := was[d.Path]; old != d.Version.Version {
				changes = append(changes, Change{Path: d.Path, From: old, To: d.Version.Version})
			}
			delete(was, d.Path)
		}
	}
	for path, old := range was {
		changes = append(changes, Change{Path: path, From: old})
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	return changes, nil
}

// libPrefix is the path prefix of the first-party modules under the nomi
// repository's lib/ (the sqlite driver, sqlkit, ...). They are versioned
// by nomi's commits and tested together, and one requires another only as
// a floor, so a project takes them all from one commit.
const libPrefix = "github.com/nomi-language/nomi/lib/"

// alignLib names every lib/ module deps names with a version at one
// version: version, the one a project just added, else the newest of
// them, which is the latest commit since each is a pseudo-version of a
// nomi commit.
func alignLib(deps []modcache.Requirement, version string) []modcache.Requirement {
	if version == "" {
		for _, d := range deps {
			if strings.HasPrefix(d.Module, libPrefix) && semver.Compare(d.Version, version) > 0 {
				version = d.Version
			}
		}
	}
	if version == "" {
		return deps
	}
	for i, d := range deps {
		if strings.HasPrefix(d.Module, libPrefix) && d.Version != "" {
			deps[i].Version = version
		}
	}
	return deps
}

// resolve answers module path at query (`latest` when empty, else a full
// version), downloaded and verified, and its directory in the module
// cache.
func (f *fetcher) resolve(path, query string) (module.Version, string, error) {
	version := query
	if query == "" || query == "latest" {
		v, err := f.latest(path)
		if err != nil {
			return module.Version{}, "", fmt.Errorf("finding the latest version of %s: %w", path, err)
		}
		version = v
	}
	m := module.Version{Path: path, Version: version}
	if err := module.Check(m.Path, m.Version); err != nil {
		return module.Version{}, "", err
	}
	dir, err := f.materialize(m)
	if err != nil {
		return module.Version{}, "", err
	}
	return m, dir, nil
}

// upgrade answers the version `go get -u` would move m to: the highest
// release of m.Path above m.Version, else m.Version. A +incompatible
// version is taken only when m.Version is one.
func (f *fetcher) upgrade(m module.Version) (string, error) {
	list, err := f.proxyGet(m.Path, "@v/list")
	if d := (*directError)(nil); errors.As(err, &d) {
		v, err := f.goLatest(m.Path, d)
		if err != nil {
			return "", err
		}
		if semver.Compare(v, m.Version) > 0 {
			return v, nil
		}
		return m.Version, nil
	}
	if err != nil {
		return "", err
	}
	incompatible := strings.HasSuffix(m.Version, "+incompatible")
	best := m.Version
	for _, v := range strings.Fields(string(list)) {
		if !semver.IsValid(v) || semver.Prerelease(v) != "" {
			continue
		}
		if strings.HasSuffix(v, "+incompatible") && !incompatible {
			continue
		}
		if semver.Compare(v, best) > 0 {
			best = v
		}
	}
	if best == m.Version && len(strings.Fields(string(list))) == 0 {
		// A proxy may list no tagged versions and still answer @latest.
		if v, err := f.latest(m.Path); err == nil && semver.Prerelease(v) == "" && semver.Compare(v, best) > 0 {
			best = v
		}
	}
	return best, nil
}
