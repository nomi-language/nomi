package modfetch

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"golang.org/x/mod/semver"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/modcache"
)

// Added is what Add or AddLocal put in nomi.toml.
type Added struct {
	// Name is the dependency's name in [dependencies], the name the
	// project's files import it by.
	Name string
	// Module is its repository, subdirectory and the version selected for
	// it; a local module's is its entry's, often none.
	Module modcache.Module
	// Dir is the module's directory: its copy in the module cache, or the
	// directory AddLocal added.
	Dir string
	// Local is the dir AddLocal wrote, relative to the project root; ""
	// for a fetched module.
	Local string
	// Held is set when no version was named and Add took the version the
	// project already takes the module's repository at.
	Held bool
	// Moved are the names of the project's other entries of the module's
	// repository that Add moved to the version named with it.
	Moved []string
}

// editDependencies writes the project at root's [dependencies] and
// [indirect] tables as edit makes them from the tables' entries
// (analysis.WriteRequirements, which keeps the file's other lines and its
// layout), and answers the file's text before, which restore puts back
// when what follows the edit fails. A floor of a repository an entry with
// no dir then names goes: the entry gives the repository its version.
func editDependencies(root string, edit func([]modcache.Requirement, []modcache.Floor) ([]modcache.Requirement, []modcache.Floor, error)) ([]byte, error) {
	path := filepath.Join(root, modcache.ManifestName)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	mf, err := modcache.ParseModuleFile(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	deps, floors, err := edit(mf.Requires, mf.Indirect)
	if err != nil {
		return nil, err
	}
	named := versions(deps)
	floors = slices.DeleteFunc(slices.Clone(floors), func(f modcache.Floor) bool { return named[f.Repo] != "" })
	out, err := analysis.WriteRequirements(data, deps, floors)
	if err != nil {
		return nil, fmt.Errorf("writing %s: %w", path, err)
	}
	if bytes.Equal(out, data) {
		return data, nil
	}
	return data, writeFileAtomic(path, out, 0o644)
}

// restore puts the project at root's nomi.toml back to old, the text an
// edit replaced, after the fetch that followed failed, and answers err.
func restore(root string, old []byte, err error) error {
	_ = writeFileAtomic(filepath.Join(root, modcache.ManifestName), old, 0o644)
	return err
}

// put sets the entry named r.Name in deps to r, and refuses a module
// another entry already names.
func put(deps []modcache.Requirement, r modcache.Requirement) ([]modcache.Requirement, error) {
	var out []modcache.Requirement
	for _, d := range deps {
		switch {
		case d.Name == r.Name:
			continue
		case !r.ID.IsZero() && d.ID == r.ID:
			return nil, fmt.Errorf("nomi.toml already names %s %s", r.ID, d.Name)
		}
		out = append(out, d)
	}
	return append(out, r), nil
}

// nomiRepo is the repository of the first-party libraries, each a
// directory under its lib/. A release tags the repository once, and a
// release `nomi` takes them at its own version.
const nomiRepo = "github.com/nomi-language/nomi"

// Add names the module arg (`repo[/subdir]`, `repo[/subdir]@version` or
// `repo[/subdir]@<commit>`, a commit's 40 hex digits, which it writes as
// the entry's `commit`) in the project at root's [dependencies], under
// name, or when name is ""
// under the name an entry already gives the module, else the module's own
// [module].name. One tag versions a repository, so with no version Add
// takes the version the project's entries already take the repository at
// (versions), and with none, the latest release, or a release `nomi`'s own
// for a first-party library (modcache.Release); `@latest` is the latest
// release. A version named, `@latest` among them, moves every entry of
// the repository with no dir to it, as Update moves them together, across
// a major version too. It fetches what the project then needs and writes
// nomi.lock. The module must hold a nomi.toml. When the fetch fails,
// nomi.toml is left as it was.
func Add(root, name, arg string, opts Options) (Added, error) {
	opts.Update = true
	f, err := newFetcher(root, opts)
	if err != nil {
		return Added{}, err
	}
	target, query, _ := strings.Cut(arg, "@")
	switch {
	case query == "" || query == "latest" || modcache.IsVersion(query) || modcache.IsCommit(query):
	case semver.IsValid(query):
		return Added{}, fmt.Errorf("%s: write the full version, %s", arg, semver.Canonical(query))
	case query[0] != 'v' && len(query) >= 4 && strings.Trim(strings.ToLower(query), "0123456789abcdef") == "":
		return Added{}, fmt.Errorf("%s: %s", arg, modcache.CommitProblem(query))
	default:
		return Added{}, fmt.Errorf("%s: a version is `latest`, a tag such as v1.2.3, or a commit's 40 hex digits", arg)
	}
	if err := modcache.CheckRepo(target); err != nil {
		return Added{}, err
	}
	id, err := f.locate(target)
	if err != nil {
		return Added{}, err
	}
	own, err := modcache.ReadModuleFile(root)
	if err != nil {
		return Added{}, err
	}
	if own == nil {
		return Added{}, fmt.Errorf("no nomi.toml in %s", root)
	}
	version, held := versions(own.Requires)[id.Repo], query == ""
	if !held || version == "" {
		held = false
		if version, err = f.version(id, query); err != nil {
			return Added{}, err
		}
	}
	m := modcache.Module{ID: id, Version: version}
	dir, err := f.materialize(m)
	if err != nil {
		return Added{}, err
	}
	file, err := modcache.ReadModuleFile(dir)
	if tooOld := (*modcache.NomiVersionError)(nil); errors.As(err, &tooOld) {
		tooOld.Module = "module " + m.String()
	}
	if err != nil {
		return Added{}, err
	}
	var kept modcache.Requirement
	for _, d := range own.Requires {
		if d.ID == id && (name == "" || name == d.Name) {
			kept = d
		}
	}
	name = cmp.Or(name, kept.Name, file.Name)
	var moved []string
	old, err := editDependencies(root, func(deps []modcache.Requirement, floors []modcache.Floor) ([]modcache.Requirement, []modcache.Floor, error) {
		deps, err := put(deps, modcache.Requirement{Name: name, ID: id, Version: version, Dir: kept.Dir})
		if err != nil {
			return nil, nil, err
		}
		moved = moveTo(deps, map[string]string{id.Repo: version})
		return deps, floors, nil
	})
	if err != nil {
		return Added{}, err
	}
	g, err := f.fetch(root)
	if err != nil {
		return Added{}, restore(root, old, err)
	}
	added := Added{Name: name, Module: m, Held: held, Moved: moved}
	if g != nil {
		if d, ok := g.Find(id); ok {
			added.Module, added.Dir = d.Module, d.Dir
		}
	}
	return added, nil
}

// versions is the version the entries reqs take each repository at: the
// commit an entry with no dir names, else the highest tag one names. A
// build takes a repository at one point, so Add takes another of its
// modules there, and Update moves them together (moveTo).
func versions(reqs []modcache.Requirement) map[string]string {
	out := map[string]string{}
	for _, r := range reqs {
		switch {
		case r.Dir != "" || r.ID.IsZero() || modcache.IsCommit(out[r.Repo]):
		case modcache.IsCommit(r.Version) || semver.Compare(r.Version, out[r.Repo]) > 0:
			out[r.Repo] = r.Version
		}
	}
	return out
}

// moveTo sets each entry of deps with no dir whose repository to holds to
// the version to gives it, and answers the names of those it changed.
func moveTo(deps []modcache.Requirement, to map[string]string) []string {
	var moved []string
	for i, d := range deps {
		if v, ok := to[d.Repo]; ok && d.Dir == "" && !d.ID.IsZero() && d.Version != v {
			deps[i].Version = v
			moved = append(moved, d.Name)
		}
	}
	return moved
}

// locate answers the module target names, `repo[/subdir]`: on a known host
// whose repositories have a fixed number of path elements, the elements
// past them are the subdirectory; elsewhere the repository is the longest
// prefix of target whose tags its host answers.
func (f *fetcher) locate(target string) (modcache.ID, error) {
	elems := strings.Split(target, "/")
	name, h, known := hostOf(target)
	if known && h.depth > 0 {
		if len(elems) <= h.depth {
			return modcache.ID{}, fmt.Errorf("%s names no repository: a repository on %s has %d path elements after the host", target, name, h.depth)
		}
		return modcache.ID{Repo: strings.Join(elems[:h.depth+1], "/"), Subdir: strings.Join(elems[h.depth+1:], "/")}, nil
	}
	var first error
	for n := len(elems); n >= 2; n-- {
		repo := strings.Join(elems[:n], "/")
		_, err := f.tagsOf(repo)
		if err == nil {
			return modcache.ID{Repo: repo, Subdir: strings.Join(elems[n:], "/")}, nil
		}
		if first == nil {
			first = err
		}
		var g *gitError
		if !refused(err) && !errors.As(err, &g) {
			return modcache.ID{}, err
		}
	}
	return modcache.ID{}, first
}

// version answers the version Add takes id at for query: the version or
// commit named, else a release `nomi`'s own for a first-party library,
// else the repository's latest release.
func (f *fetcher) version(id modcache.ID, query string) (string, error) {
	if query != "" && query != "latest" {
		return query, nil
	}
	tags, err := f.tagsOf(id.Repo)
	if err != nil {
		return "", err
	}
	if query == "" && f.release != "" && id.Repo == nomiRepo {
		if _, ok := tags[f.release]; !ok {
			return "", &releaseTagError{release: f.release, id: id}
		}
		return f.release, nil
	}
	v := highest(tags, "")
	switch {
	case v != "":
		return v, nil
	case id.Repo == nomiRepo:
		return "", fmt.Errorf("%s has no release yet, so its first-party libraries have no version to take; add one from a checkout with `nomi add <directory>`", nomiRepo)
	}
	return "", fmt.Errorf("repository %s has no release tag, such as v1.0.0; name a version: `nomi add %s@<version>`", id.Repo, id)
}

// highest is the highest release among tags, prereleases left out, in
// the line line (modcache.Line) when it is not "", or "".
func highest(tags map[string]string, line string) string {
	best := ""
	for tag := range tags {
		if semver.Prerelease(tag) != "" || line != "" && modcache.Line(tag) != line {
			continue
		}
		if best == "" || semver.Compare(tag, best) > 0 {
			best = tag
		}
	}
	return best
}

// releaseTagError is a release `nomi`'s own version of the first-party
// libraries that their repository has no tag for: the release was
// published without it.
type releaseTagError struct {
	release string
	id      modcache.ID
}

func (e *releaseTagError) Error() string {
	return fmt.Sprintf("this nomi is release %s, which takes the first-party libraries at %s, and %s has no tag %s: "+
		"the release was published without it; `nomi add %s@<version>` takes another version",
		e.release, e.release, nomiRepo, e.release, e.id)
}

// AddLocal names the Nomi module in directory dir (absolute, or relative to
// the working directory) in the project at root's [dependencies] with a dir
// entry, dir relative to root, under name, or when name is "" under the
// module's own [module].name. An entry of that name that names a
// repository keeps it and its version and gains the dir, which then
// stands for that module. It then fetches what the module needs and
// writes nomi.lock.
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
	r := modcache.Requirement{Name: cmp.Or(name, file.Name), Dir: modcache.AddArg(rel)}
	old, err := editDependencies(root, func(deps []modcache.Requirement, floors []modcache.Floor) ([]modcache.Requirement, []modcache.Floor, error) {
		for _, d := range deps {
			if d.Name == r.Name && !d.ID.IsZero() {
				r.ID, r.Version = d.ID, d.Version
			}
		}
		deps, err := put(deps, r)
		return deps, floors, err
	})
	if err != nil {
		return Added{}, err
	}
	opts.Update = true
	f, err := newFetcher(root, opts)
	if err != nil {
		return Added{}, restore(root, old, err)
	}
	if _, err := f.fetch(root); err != nil {
		return Added{}, restore(root, old, err)
	}
	return Added{Name: r.Name, Module: modcache.Module{ID: r.ID, Version: r.Version}, Dir: abs, Local: r.Dir}, nil
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
	Unneeded []modcache.Module
}

// Remove takes the entry named name out of the project at root's
// [dependencies], and writes nomi.lock as the build list without it: the
// modules only it needed go too.
func Remove(root, name string, opts Options) (Removed, error) {
	var out Removed
	old, err := editDependencies(root, func(deps []modcache.Requirement, floors []modcache.Floor) ([]modcache.Requirement, []modcache.Floor, error) {
		var kept []modcache.Requirement
		for _, d := range deps {
			if d.Name == name {
				out.Entry = d
				continue
			}
			kept = append(kept, d)
		}
		if out.Entry.Name == "" {
			return nil, nil, fmt.Errorf("nomi.toml has no dependency %s", name)
		}
		return kept, floors, nil
	})
	if err != nil {
		return Removed{}, err
	}
	opts.Update = true
	f, err := newFetcher(root, opts)
	if err != nil {
		return Removed{}, restore(root, old, err)
	}
	g, err := f.fetch(root)
	if err != nil {
		return Removed{}, restore(root, old, err)
	}
	for _, m := range f.lock.Modules {
		if g == nil {
			out.Unneeded = append(out.Unneeded, m.Module)
		} else if _, ok := g.Find(m.ID); !ok {
			out.Unneeded = append(out.Unneeded, m.Module)
		}
	}
	return out, nil
}

// Change is one module of the build list Update moved: From is "" for one
// it added, To "" for one the project no longer needs.
type Change struct {
	ID       modcache.ID
	From, To string
}

// Updated is what Update did.
type Updated struct {
	// Changes are the modules of the build list that moved, sorted by
	// module.
	Changes []Change
	// AtCommit are the entries Update was asked to move that name a
	// commit, which it leaves as they are: a commit has no later release.
	AtCommit []modcache.Requirement
	// Indirect are the repositories only the project's dependencies name
	// that Update raised, each at the version [indirect] now holds for it.
	Indirect []modcache.Floor
}

// Update moves the entry named only, or with only "" every entry the
// project at root's [dependencies] names with a version and no dir, to its
// repository's latest release in the same line (newest). One tag versions
// a whole repository, so every entry of a repository that moves moves
// with it. An entry with a dir has no releases to move to, and an entry at
// a commit is left as it is (Updated.AtCommit). With only "", it then
// raises each repository the build reaches only through its dependencies
// to its latest release in its line as well, recording each in the
// project's [indirect] table (Updated.Indirect), until none rises; a floor
// of a repository the build no longer reaches goes. It fetches what the
// project then needs, writes nomi.lock, and answers each module of the
// build list that changed from the one nomi.lock held. When a fetch
// fails, nomi.toml is left as it was.
func Update(root, only string, opts Options) (Updated, error) {
	opts.Update = true
	var out Updated
	f, err := newFetcher(root, opts)
	if err != nil {
		return out, err
	}
	own, err := modcache.ReadModuleFile(root)
	if err != nil {
		return out, err
	}
	if own == nil {
		return out, fmt.Errorf("no nomi.toml in %s", root)
	}
	repos := map[string]bool{}
	found := false
	for _, r := range own.Requires {
		if only != "" && r.Name != only {
			continue
		}
		found = true
		switch {
		case r.Dir != "" || r.ID.IsZero():
			if only != "" {
				return out, fmt.Errorf("%s is the directory %s, which has no releases to update to", only, r.Dir)
			}
		case modcache.IsCommit(r.Version):
			out.AtCommit = append(out.AtCommit, r)
		default:
			repos[r.Repo] = true
		}
	}
	if only != "" && !found {
		return out, fmt.Errorf("nomi.toml has no dependency %s", only)
	}
	current := versions(own.Requires)
	to := map[string]string{}
	for repo := range repos {
		if to[repo], err = f.newest(repo, current[repo], true); err != nil {
			return out, err
		}
	}
	var old []byte
	edit := func(e func([]modcache.Requirement, []modcache.Floor) ([]modcache.Requirement, []modcache.Floor, error)) error {
		was, err := editDependencies(root, e)
		if old == nil {
			old = was
		}
		return err
	}
	failed := func(err error) (Updated, error) {
		if old != nil {
			return Updated{}, restore(root, old, err)
		}
		return Updated{}, err
	}
	if len(moveTo(slices.Clone(own.Requires), to)) > 0 {
		if err := edit(func(deps []modcache.Requirement, floors []modcache.Floor) ([]modcache.Requirement, []modcache.Floor, error) {
			moveTo(deps, to)
			return deps, floors, nil
		}); err != nil {
			return failed(err)
		}
	}
	if only == "" {
		raised := map[string]string{}
		for {
			g, err := modcache.Resolve(root, f.materialize)
			if err != nil {
				return failed(err)
			}
			named := versions(own.Requires)
			floors := map[string]string{}
			reached := map[string]bool{}
			if g != nil {
				for _, d := range g.Deps {
					if d.Local || d.ID.IsZero() || named[d.Repo] != "" || modcache.IsCommit(d.Version) || reached[d.Repo] {
						continue
					}
					reached[d.Repo] = true
					v, err := f.newest(d.Repo, d.Version, false)
					if err != nil {
						return failed(err)
					}
					if semver.Compare(v, d.Version) > 0 {
						floors[d.Repo] = v
					}
				}
			}
			if len(floors) == 0 {
				// The last pass: a floor of a repository the build no
				// longer reaches goes.
				if err := edit(func(deps []modcache.Requirement, fls []modcache.Floor) ([]modcache.Requirement, []modcache.Floor, error) {
					return deps, slices.DeleteFunc(fls, func(fl modcache.Floor) bool { return !reached[fl.Repo] }), nil
				}); err != nil {
					return failed(err)
				}
				break
			}
			if err := edit(func(deps []modcache.Requirement, fls []modcache.Floor) ([]modcache.Requirement, []modcache.Floor, error) {
				for i, fl := range fls {
					if v, ok := floors[fl.Repo]; ok {
						fls[i].Version = v
						delete(floors, fl.Repo)
					}
				}
				for repo, v := range floors {
					fls = append(fls, modcache.Floor{Repo: repo, Version: v})
				}
				return deps, fls, nil
			}); err != nil {
				return failed(err)
			}
			for repo, v := range floors {
				raised[repo] = v
			}
		}
		for repo, v := range raised {
			out.Indirect = append(out.Indirect, modcache.Floor{Repo: repo, Version: v})
		}
		slices.SortFunc(out.Indirect, func(a, b modcache.Floor) int { return strings.Compare(a.Repo, b.Repo) })
	}
	g, err := f.fetch(root)
	if err != nil {
		return failed(err)
	}
	was := map[modcache.ID]string{}
	for _, m := range f.lock.Modules {
		was[m.ID] = m.Version
	}
	if g != nil {
		for _, d := range g.Deps {
			if d.Local || d.ID.IsZero() {
				continue
			}
			if old := was[d.ID]; old != d.Version {
				out.Changes = append(out.Changes, Change{ID: d.ID, From: old, To: d.Version})
			}
			delete(was, d.ID)
		}
	}
	for id, old := range was {
		out.Changes = append(out.Changes, Change{ID: id, From: old})
	}
	sort.Slice(out.Changes, func(i, j int) bool { return out.Changes[i].ID.String() < out.Changes[j].ID.String() })
	return out, nil
}

// newest is the version Update takes repo at from v: its repository's
// highest release in v's line (modcache.Line: the major version from v1
// on, the minor version below v1), not a prerelease, so a new major
// version, or a new 0.x line, is never taken, and a prerelease only when
// v is already a later one. For a direct entry of the first-party
// libraries' repository, a release `nomi` takes its own version
// (modcache.Release), up or down; a floor only ever rises.
func (f *fetcher) newest(repo, v string, direct bool) (string, error) {
	tags, err := f.tagsOf(repo)
	if err != nil {
		return "", fmt.Errorf("finding the latest version of %s: %w", repo, err)
	}
	switch best := highest(tags, modcache.Line(v)); {
	case direct && repo == nomiRepo && f.release != "":
		if _, ok := tags[f.release]; !ok {
			return "", &releaseTagError{release: f.release, id: modcache.ID{Repo: repo}}
		}
		return f.release, nil
	case semver.Compare(best, v) > 0:
		return best, nil
	}
	return v, nil
}

// Behind is a repository of the build with a later release than the one
// it is taken at (Outdated).
type Behind struct {
	Repo string
	// Version is the version the build takes it at, Line the latest
	// release in that version's line, which `nomi update` would take, and
	// Newest the latest release of all.
	Version, Line, Newest string
	// Via is the name of each module whose [dependencies] reaches the
	// repository, in the order the walk reached them, empty for a
	// repository the project's [dependencies] names.
	Via []string
}

// Outdated answers each repository the project at root's build takes at
// a tag that has a later release, sorted by repository: the build as
// nomi.lock locks it, fetched as a plain fetch fetches it, and each
// repository's tags as its host answers them now. It writes nothing. A
// repository taken at a commit has no release to be behind. Offline it is
// an error: nomi keeps no list of a repository's tags.
func Outdated(root string, opts Options) ([]Behind, error) {
	if opts.Offline {
		return nil, errors.New("reading each repository's releases needs the network, and nomi keeps no list of them; run it without --offline")
	}
	opts.Update = false
	f, err := newFetcher(root, opts)
	if err != nil {
		return nil, err
	}
	g, err := f.fetch(root)
	if err != nil || g == nil {
		return nil, err
	}
	byRepo := map[string]*Behind{}
	var order []string
	for _, d := range g.Deps {
		if d.Local || d.ID.IsZero() || modcache.IsCommit(d.Version) {
			continue
		}
		b, ok := byRepo[d.Repo]
		if !ok {
			b = &Behind{Repo: d.Repo, Version: d.Version}
			byRepo[d.Repo] = b
			order = append(order, d.Repo)
		}
		if d.Via != "" && !direct(g, d.Repo) && !slices.Contains(b.Via, d.Via) {
			b.Via = append(b.Via, d.Via)
		}
	}
	var out []Behind
	for _, repo := range order {
		b := byRepo[repo]
		tags, err := f.tagsOf(repo)
		if err != nil {
			return nil, fmt.Errorf("finding the latest version of %s: %w", repo, err)
		}
		b.Line, b.Newest = cmp.Or(highest(tags, modcache.Line(b.Version)), b.Version), cmp.Or(highest(tags, ""), b.Version)
		if semver.Compare(b.Line, b.Version) < 0 {
			b.Line = b.Version
		}
		if semver.Compare(b.Newest, b.Line) < 0 {
			b.Newest = b.Line
		}
		if b.Newest != b.Version {
			out = append(out, *b)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Repo < out[j].Repo })
	return out, nil
}

// direct reports whether the project's [dependencies] names a module of
// repo.
func direct(g *modcache.Graph, repo string) bool {
	for _, d := range g.Deps {
		if d.Direct && d.Repo == repo {
			return true
		}
	}
	return false
}

// LibOffRelease answers the first-party libraries nomi.lock at root locks
// at a version other than release, in the lock's order: what a release
// `nomi` would move with Update. A module loaded from a directory has no
// lock line, and Update leaves one at a commit as it is, so neither is
// ever among them.
func LibOffRelease(root, release string) ([]modcache.Module, error) {
	lock, err := modcache.ReadLock(root)
	if err != nil {
		return nil, err
	}
	var off []modcache.Module
	for _, m := range lock.Modules {
		if m.Repo == nomiRepo && m.Version != release && !modcache.IsCommit(m.Version) {
			off = append(off, m.Module)
		}
	}
	return off, nil
}
