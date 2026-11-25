package pkgfetch

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
	"github.com/nomi-language/nomi/internal/pkgcache"
)

// Added is what Add or AddLocal put in nomi.toml.
type Added struct {
	// Name is the dependency's name in [dependencies], the name the
	// project's files import it by.
	Name string
	// Package is its repository, subdirectory and the version selected for
	// it; a local package's is its entry's, often none.
	Package pkgcache.Package
	// Dir is the package's directory: its copy in the package cache, or the
	// directory AddLocal added.
	Dir string
	// Local is the dir AddLocal wrote, relative to the project root; ""
	// for a fetched package.
	Local string
	// Held is set when no version was named and Add took the version the
	// project already takes the package's repository at.
	Held bool
	// Pinned is set when a version was named, `latest` among them, and Add
	// took the running release's in its place (pkgcache.Pinned).
	Pinned bool
	// Moved are the names of the project's other entries of the package's
	// repository that Add moved to the version named with it.
	Moved []string
	// MovedIn are the other members of the project's workspace whose
	// entries of the repository Add moved to the version named, in the
	// workspace's order.
	MovedIn []MovedIn
}

// MovedIn is a member of a workspace whose entries Add moved: its
// directory from the workspace's root, as members writes it, and the names
// of the entries.
type MovedIn struct {
	Member string
	Names  []string
}

// editDependencies writes the [dependencies] table of path, a package's
// nomi.toml, as edit makes it from the table's entries
// (analysis.WriteRequirements, which keeps the file's other lines and its
// layout), and answers the file's text before, which restore puts back
// when what follows the edit fails.
func editDependencies(path string, edit func([]pkgcache.Requirement) ([]pkgcache.Requirement, error)) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	mf, err := pkgcache.ParseManifest(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	deps, err := edit(mf.Requires)
	if err != nil {
		return nil, err
	}
	out, err := analysis.WriteRequirements(data, deps)
	if err != nil {
		return nil, fmt.Errorf("writing %s: %w", path, err)
	}
	if bytes.Equal(out, data) {
		return data, nil
	}
	return data, writeFileAtomic(path, out, 0o644)
}

// restore puts file back to old, the text an edit replaced, after the
// fetch that followed failed, and answers err.
func restore(file string, old []byte, err error) error {
	_ = writeFileAtomic(file, old, 0o644)
	return err
}

// edits is the files one command edits (editDependencies), each with its
// text before the command's first edit of it, which undo puts back.
type edits struct {
	olds  map[string][]byte
	order []string
}

// edit is editDependencies of file, remembering its text before.
func (e *edits) edit(file string, fn func([]pkgcache.Requirement) ([]pkgcache.Requirement, error)) error {
	was, err := editDependencies(file, fn)
	if _, ok := e.olds[file]; !ok && was != nil {
		if e.olds == nil {
			e.olds = map[string][]byte{}
		}
		e.olds[file] = was
		e.order = append(e.order, file)
	}
	return err
}

// undo puts every file edited back as it was, after what followed the
// edits failed with err, and answers err.
func (e *edits) undo(err error) error {
	for _, file := range e.order {
		err = restore(file, e.olds[file], err)
	}
	return err
}

// manifestOf is the nomi.toml of the package rooted at root.
func manifestOf(root string) string { return filepath.Join(root, pkgcache.ManifestName) }

// requirements are the entries of every package the fetch's selection
// covers: own's, the project's, or with a workspace, every member's.
func (f *fetcher) requirements(own *pkgcache.Manifest) ([]pkgcache.Requirement, error) {
	if f.ws == nil {
		return own.Requires, nil
	}
	var reqs []pkgcache.Requirement
	for _, m := range f.ws.Members {
		mf, err := pkgcache.ReadManifest(m)
		if err != nil {
			return nil, err
		}
		reqs = append(reqs, mf.Requires...)
	}
	return reqs, nil
}

// put sets the entry named r.Name in deps to r, keeping the effects keys of
// the entry it replaces, and refuses a package another entry already names.
func put(deps []pkgcache.Requirement, r pkgcache.Requirement) ([]pkgcache.Requirement, error) {
	var out []pkgcache.Requirement
	for _, d := range deps {
		switch {
		case d.Name == r.Name:
			r.Effects, r.EffectsInTests = d.Effects, d.EffectsInTests
			continue
		case !r.ID.IsZero() && d.ID == r.ID:
			return nil, fmt.Errorf("nomi.toml already names %s %s", r.ID, d.Name)
		}
		out = append(out, d)
	}
	return append(out, r), nil
}

// Add names the package arg (`repo[/subdir]`, `repo[/subdir]@version` or
// `repo[/subdir]@<commit>`, a commit's 40 hex digits, which it writes as
// the entry's `commit`) in the project at root's [dependencies], under
// name, or when name is ""
// under the name an entry already gives the package, else the package's own
// [package].name. One tag versions a repository, so with no version Add
// takes the version the project's entries already take the repository at
// (versions), else the one nomi.lock holds for it, which the build takes
// through a dependency, and with neither, the latest release; `@latest` is the latest
// release. A release `nomi` takes the compiler's repository at its own
// version whatever version is named or held (pkgcache.Pinned), and writes
// that version, unless a commit is. A version named, `@latest` among them,
// moves every entry of the repository with no dir to it, as Update moves
// them together, across a major version too. It fetches what the project
// then needs and writes nomi.lock. The package must hold a nomi.toml. When
// the fetch fails, nomi.toml is left as it was. In a workspace, the
// version held is the one the workspace's members take the repository at,
// a version named moves every member's entries of the repository with
// it, editing each of those members' nomi.toml (Added.MovedIn), and Add
// writes the workspace's nomi.lock; with no version named it edits the
// nomi.toml at root alone.
func Add(root, name, arg string, opts Options) (Added, error) {
	opts.Update = true
	f, err := newFetcher(root, opts)
	if err != nil {
		return Added{}, err
	}
	target, query, _ := strings.Cut(arg, "@")
	switch {
	case query == "" || query == "latest" || pkgcache.IsVersion(query) || pkgcache.IsCommit(query):
	case semver.IsValid(query):
		return Added{}, fmt.Errorf("%s: write the full version, %s", arg, semver.Canonical(query))
	case query[0] != 'v' && len(query) >= 4 && strings.Trim(strings.ToLower(query), "0123456789abcdef") == "":
		return Added{}, fmt.Errorf("%s: %s", arg, pkgcache.CommitProblem(query))
	default:
		return Added{}, fmt.Errorf("%s: a version is `latest`, a tag such as v1.2.3, or a commit's 40 hex digits", arg)
	}
	if err := pkgcache.CheckRepo(target); err != nil {
		return Added{}, err
	}
	id, err := f.locate(target)
	if err != nil {
		return Added{}, err
	}
	own, err := pkgcache.ReadManifest(root)
	if err != nil {
		return Added{}, err
	}
	if own == nil {
		return Added{}, fmt.Errorf("no nomi.toml in %s", root)
	}
	all, err := f.requirements(own)
	if err != nil {
		return Added{}, err
	}
	current := versions(all)[id.Repo]
	if current == "" {
		// A repository the build takes only through a dependency is at
		// the version nomi.lock holds.
		current = f.from[id.Repo]
	}
	version, err := f.version(id, query, current)
	if err != nil {
		return Added{}, err
	}
	held := query == "" && version == current
	pinned := query != "" && version != query && version == pkgcache.Pinned(id.Repo)
	m := pkgcache.Package{ID: id, Version: version}
	dir, err := f.materialize(m)
	if err != nil {
		return Added{}, err
	}
	file, err := pkgcache.ReadManifest(dir)
	if tooOld := (*pkgcache.NomiVersionError)(nil); errors.As(err, &tooOld) {
		tooOld.Package = "package " + m.String()
	}
	if err != nil {
		return Added{}, err
	}
	var kept pkgcache.Requirement
	for _, d := range own.Requires {
		if d.ID == id && (name == "" || name == d.Name) {
			kept = d
		}
	}
	name = cmp.Or(name, kept.Name, file.Name)
	to := map[string]string{id.Repo: version}
	var moved []string
	var e edits
	if err := e.edit(manifestOf(root), func(deps []pkgcache.Requirement) ([]pkgcache.Requirement, error) {
		deps, err := put(deps, pkgcache.Requirement{Name: name, ID: id, Version: version, Dir: kept.Dir})
		if err != nil {
			return nil, err
		}
		moved = moveTo(deps, to)
		return deps, nil
	}); err != nil {
		return Added{}, e.undo(err)
	}
	// A version named moves every member's entries of the repository, as it
	// moves the project's own: the workspace takes one version of it.
	var movedIn []MovedIn
	if f.ws != nil && query != "" {
		for _, member := range f.ws.Members {
			mf, err := pkgcache.ReadManifest(member)
			if err != nil {
				return Added{}, e.undo(err)
			}
			if same(member, root) || len(moveTo(slices.Clone(mf.Requires), to)) == 0 {
				continue
			}
			rel, err := filepath.Rel(f.ws.Root, member)
			if err != nil {
				return Added{}, e.undo(err)
			}
			in := MovedIn{Member: filepath.ToSlash(rel)}
			if err := e.edit(manifestOf(member), func(deps []pkgcache.Requirement) ([]pkgcache.Requirement, error) {
				in.Names = moveTo(deps, to)
				return deps, nil
			}); err != nil {
				return Added{}, e.undo(err)
			}
			movedIn = append(movedIn, in)
		}
	}
	// A version named sets the repository's version, lower than the lock's
	// too, so the lock no longer holds it; a dependency that asks for more
	// still does, and the lock's other versions stay.
	if query != "" {
		delete(f.from, id.Repo)
	}
	g, err := f.fetch(root)
	if err != nil {
		return Added{}, e.undo(err)
	}
	added := Added{Name: name, Package: m, Held: held, Pinned: pinned, Moved: moved, MovedIn: movedIn}
	if g != nil {
		if d, ok := g.Find(id); ok {
			added.Package, added.Dir = d.Package, d.Dir
		}
	}
	return added, nil
}

// versions is the version the entries reqs take each repository at: the
// commit an entry with no dir names, else the highest tag one names. A
// build takes a repository at one point, so Add takes another of its
// packages there, and Update moves them together (moveTo).
func versions(reqs []pkgcache.Requirement) map[string]string {
	out := map[string]string{}
	for _, r := range reqs {
		switch {
		case r.Dir != "" || r.ID.IsZero() || pkgcache.IsCommit(out[r.Repo]):
		case pkgcache.IsCommit(r.Version) || semver.Compare(r.Version, out[r.Repo]) > 0:
			out[r.Repo] = r.Version
		}
	}
	return out
}

// moveTo sets each entry of deps with no dir whose repository to holds to
// the version to gives it, and answers the names of those it changed.
func moveTo(deps []pkgcache.Requirement, to map[string]string) []string {
	var moved []string
	for i, d := range deps {
		if v, ok := to[d.Repo]; ok && d.Dir == "" && !d.ID.IsZero() && d.Version != v {
			deps[i].Version = v
			moved = append(moved, d.Name)
		}
	}
	return moved
}

// locate answers the package target names, `repo[/subdir]`: on a known host
// whose repositories have a fixed number of path elements, the elements
// past them are the subdirectory; elsewhere the repository is the longest
// prefix of target whose tags its host answers.
func (f *fetcher) locate(target string) (pkgcache.ID, error) {
	elems := strings.Split(target, "/")
	name, h, known := hostOf(target)
	if known && h.depth > 0 {
		if len(elems) <= h.depth {
			return pkgcache.ID{}, fmt.Errorf("%s names no repository: a repository on %s has %d path elements after the host", target, name, h.depth)
		}
		return pkgcache.ID{Repo: strings.Join(elems[:h.depth+1], "/"), Subdir: strings.Join(elems[h.depth+1:], "/")}, nil
	}
	var first error
	for n := len(elems); n >= 2; n-- {
		repo := strings.Join(elems[:n], "/")
		_, err := f.tagsOf(repo)
		if err == nil {
			return pkgcache.ID{Repo: repo, Subdir: strings.Join(elems[n:], "/")}, nil
		}
		if first == nil {
			first = err
		}
		var g *gitError
		if !refused(err) && !errors.As(err, &g) {
			return pkgcache.ID{}, err
		}
	}
	return pkgcache.ID{}, first
}

// version answers the version Add takes id at for query, when the
// project's entries take id's repository at current ("" for none): a
// commit named, or current when no version is named and it is a commit;
// else the running release for the compiler's repository
// (pkgcache.Pinned), whatever version is named or held; else the version
// named, or current; else the repository's latest release.
func (f *fetcher) version(id pkgcache.ID, query, current string) (string, error) {
	pin := pkgcache.Pinned(id.Repo)
	switch {
	case pkgcache.IsCommit(query):
		return query, nil
	case query == "" && current != "" && (pin == "" || pkgcache.IsCommit(current)):
		return current, nil
	case pin != "":
		return f.pinned(id, pin)
	case query != "" && query != "latest":
		return query, nil
	}
	tags, err := f.tagsOf(id.Repo)
	if err != nil {
		return "", err
	}
	v := highest(tags, "")
	switch {
	case v != "":
		return v, nil
	case id.Repo == pkgcache.CompilerRepo:
		return "", fmt.Errorf("%s has no release yet, so its first-party libraries have no version to take; add one from a checkout with `nomi add <directory>`", id.Repo)
	}
	return "", fmt.Errorf("repository %s has no release tag, such as v1.0.0; name a version: `nomi add %s@<version>`", id.Repo, id)
}

// pinned answers pin, the version the running release takes id's
// repository at (pkgcache.Pinned), or a releaseTagError when the
// repository has no such tag.
func (f *fetcher) pinned(id pkgcache.ID, pin string) (string, error) {
	tags, err := f.tagsOf(id.Repo)
	if err != nil {
		return "", err
	}
	if _, ok := tags[pin]; !ok {
		return "", &releaseTagError{release: pin, id: id}
	}
	return pin, nil
}

// highest is the highest release among tags, prereleases left out, in
// the line line (pkgcache.Line) when it is not "", or "".
func highest(tags map[string]string, line string) string {
	best := ""
	for tag := range tags {
		if semver.Prerelease(tag) != "" || line != "" && pkgcache.Line(tag) != line {
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
	id      pkgcache.ID
}

func (e *releaseTagError) Error() string {
	return fmt.Sprintf("this nomi is release %s, which takes the first-party libraries at %s, and %s has no tag %s: "+
		"the release was published without it; `nomi add %s@<commit>` takes it at a commit, and `nomi add <directory>` from a checkout",
		e.release, e.release, e.id.Repo, e.release, e.id)
}

// AddLocal names the package in directory dir (absolute, or relative to
// the working directory) in the project at root's [dependencies] with a dir
// entry, dir relative to root, under name, or when name is "" under the
// package's own [package].name. An entry of that name that names a
// repository keeps it and its version and gains the dir, which then
// stands for that package. It then fetches what the package needs and
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
	file, err := pkgcache.ReadManifest(abs)
	if err != nil {
		return Added{}, err
	}
	if file == nil {
		return Added{}, fmt.Errorf("%s has no nomi.toml, so it is not a Nomi package", dir)
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		rel = abs
	}
	r := pkgcache.Requirement{Name: cmp.Or(name, file.Name), Dir: pkgcache.AddArg(rel)}
	old, err := editDependencies(manifestOf(root), func(deps []pkgcache.Requirement) ([]pkgcache.Requirement, error) {
		for _, d := range deps {
			if d.Name == r.Name && !d.ID.IsZero() {
				r.ID, r.Version = d.ID, d.Version
			}
		}
		return put(deps, r)
	})
	if err != nil {
		return Added{}, err
	}
	opts.Update = true
	f, err := newFetcher(root, opts)
	if err != nil {
		return Added{}, restore(manifestOf(root), old, err)
	}
	if _, err := f.fetch(root); err != nil {
		return Added{}, restore(manifestOf(root), old, err)
	}
	return Added{Name: r.Name, Package: pkgcache.Package{ID: r.ID, Version: r.Version}, Dir: abs, Local: r.Dir}, nil
}

func same(a, b string) bool {
	ai, errA := os.Stat(a)
	bi, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(ai, bi)
}

// Removed is what Remove took out of nomi.toml and nomi.lock.
type Removed struct {
	// Entry is the [dependencies] entry it took out.
	Entry pkgcache.Requirement
	// Unneeded are the packages nomi.lock held that the project no longer
	// needs, the dependency's own package among them.
	Unneeded []pkgcache.Package
}

// Remove takes the entry named name out of the project at root's
// [dependencies], and writes nomi.lock as the build list without it: the
// packages only it needed go too, and in a workspace, a package another
// member needs stays.
func Remove(root, name string, opts Options) (Removed, error) {
	var out Removed
	old, err := editDependencies(manifestOf(root), func(deps []pkgcache.Requirement) ([]pkgcache.Requirement, error) {
		var kept []pkgcache.Requirement
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
	})
	if err != nil {
		return Removed{}, err
	}
	opts.Update = true
	f, err := newFetcher(root, opts)
	if err != nil {
		return Removed{}, restore(manifestOf(root), old, err)
	}
	g, err := f.fetch(root)
	if err != nil {
		return Removed{}, restore(manifestOf(root), old, err)
	}
	for _, m := range f.lock.Packages {
		if g == nil {
			out.Unneeded = append(out.Unneeded, m.Package)
		} else if _, ok := g.Find(m.ID); !ok {
			out.Unneeded = append(out.Unneeded, m.Package)
		}
	}
	return out, nil
}

// Change is one package of the build list that moved from the one
// nomi.lock held (Update, Reselect): From is "" for one added, To "" for
// one the project no longer needs.
type Change struct {
	ID       pkgcache.ID
	From, To string
}

// changes is each package of g's build list that differs from the one
// f.lock held, sorted by package.
func (f *fetcher) changes(g *pkgcache.Graph) []Change {
	var out []Change
	was := map[pkgcache.ID]string{}
	for _, m := range f.lock.Packages {
		was[m.ID] = m.Version
	}
	if g != nil {
		for _, d := range g.Deps {
			if d.Local || d.ID.IsZero() {
				continue
			}
			if old := was[d.ID]; old != d.Version {
				out = append(out, Change{ID: d.ID, From: old, To: d.Version})
			}
			delete(was, d.ID)
		}
	}
	for id, old := range was {
		out = append(out, Change{ID: id, From: old})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID.String() < out[j].ID.String() })
	return out
}

// Updated is what Update did.
type Updated struct {
	// Changes are the packages of the build list that moved, sorted by
	// package.
	Changes []Change
	// AtCommit are the entries Update was asked to move that name a
	// commit, which it leaves as they are: a commit has no later release.
	AtCommit []pkgcache.Requirement
	// Raised are the repositories only the project's dependencies name
	// that Update raised past what they ask for, sorted. nomi.lock alone
	// holds the version each rose to.
	Raised []string
	// Later are the repositories Update was asked to move that have a
	// release past the line it leaves them in (pkgcache.Line), which Update
	// does not take and `nomi add` does, sorted by repository.
	Later []Later
}

// Later is a repository at the version Update leaves it at, and its
// latest release, which is in a later line (Updated.Later).
type Later struct {
	// Entry is the project's first entry of the repository, at the version
	// Update leaves it at: its package is the one `nomi add
	// <package>@<version>` names to move every entry of the repository.
	Entry  pkgcache.Requirement
	Newest string
}

// Update moves the entry named only, or with only "" every entry the
// project at root's [dependencies] names with a version and no dir, to its
// repository's latest release in the same line (newest). One tag versions
// a whole repository, so every entry of a repository that moves moves
// with it. An entry with a dir has no releases to move to, and an entry at
// a commit is left as it is (Updated.AtCommit). Update never leaves a
// line, and answers each repository's latest release past it
// (Updated.Later). The selection starts from the lock's versions, as
// every command that writes nomi.lock does. With only "", Update then
// raises each repository the build reaches only through its dependencies
// to its latest release in its line as well (Updated.Raised), until none
// rises; nomi.lock holds each raise, and no manifest changes for it. It
// fetches what the project then needs, writes nomi.lock, and answers each
// package of the build list that changed from the one nomi.lock held. When
// a fetch fails, nomi.toml is left as it was.
//
// In a workspace, with only "" Update covers every member: it moves the
// entries of every member's nomi.toml and writes the workspace's
// nomi.lock. An entry named is one of the nomi.toml at root, and only it
// moves.
func Update(root, only string, opts Options) (Updated, error) {
	opts.Update = true
	var out Updated
	f, err := newFetcher(root, opts)
	if err != nil {
		return out, err
	}
	// edited are the packages whose entries Update moves.
	edited := []string{root}
	if f.ws != nil && only == "" {
		edited = f.ws.Members
	}
	owns := map[string]*pkgcache.Manifest{}
	var own []pkgcache.Requirement
	for _, dir := range edited {
		mf, err := pkgcache.ReadManifest(dir)
		if err != nil {
			return out, err
		}
		if mf == nil {
			return out, fmt.Errorf("no nomi.toml in %s", dir)
		}
		owns[dir] = mf
		own = append(own, mf.Requires...)
	}
	all := own
	if only != "" {
		if all, err = f.requirements(owns[root]); err != nil {
			return out, err
		}
	}
	repos := map[string]pkgcache.Requirement{}
	found := false
	for _, r := range own {
		if only != "" && r.Name != only {
			continue
		}
		found = true
		switch _, seen := repos[r.Repo]; {
		case r.Dir != "" || r.ID.IsZero():
			if only != "" {
				return out, fmt.Errorf("%s is the directory %s, which has no releases to update to", only, r.Dir)
			}
		case pkgcache.IsCommit(r.Version):
			out.AtCommit = append(out.AtCommit, r)
		case !seen:
			repos[r.Repo] = r
		}
	}
	if only != "" && !found {
		return out, fmt.Errorf("nomi.toml has no dependency %s", only)
	}
	current := versions(all)
	to := map[string]string{}
	for repo, entry := range repos {
		if to[repo], err = f.newest(entry.ID, current[repo]); err != nil {
			return out, err
		}
		// A pinned repository moves with the release, not by `nomi add`.
		if _, newest := latest(f.tags[repo], to[repo]); pkgcache.Pinned(repo) == "" && pkgcache.Line(newest) != pkgcache.Line(to[repo]) {
			entry.Version = to[repo]
			out.Later = append(out.Later, Later{Entry: entry, Newest: newest})
		}
	}
	slices.SortFunc(out.Later, func(a, b Later) int { return strings.Compare(a.Entry.Repo, b.Entry.Repo) })
	var e edits
	edit := e.edit
	failed := func(err error) (Updated, error) { return Updated{}, e.undo(err) }
	for _, dir := range edited {
		if len(moveTo(slices.Clone(owns[dir].Requires), to)) > 0 {
			if err := edit(manifestOf(dir), func(deps []pkgcache.Requirement) ([]pkgcache.Requirement, error) {
				moveTo(deps, to)
				return deps, nil
			}); err != nil {
				return failed(err)
			}
		}
	}
	if only == "" {
		// Each repository only a dependency names starts at its newest
		// release in its line, until no pass reaches one that rises. A
		// raise of a repository the build then stops reaching selects
		// nothing.
		named := versions(own)
		for {
			g, err := pkgcache.Resolve(root, f.materialize, f.from)
			if err != nil {
				return failed(err)
			}
			if g == nil {
				break
			}
			rose := false
			reached := map[string]bool{}
			for _, d := range g.Deps {
				if d.Local || d.ID.IsZero() || named[d.Repo] != "" || pkgcache.IsCommit(d.Version) || reached[d.Repo] {
					continue
				}
				reached[d.Repo] = true
				v, err := f.newest(d.ID, d.Version)
				if err != nil {
					return failed(err)
				}
				if semver.Compare(v, d.Version) > 0 {
					f.from[d.Repo] = v
					if !slices.Contains(out.Raised, d.Repo) {
						out.Raised = append(out.Raised, d.Repo)
					}
					rose = true
				}
			}
			if !rose {
				break
			}
		}
		slices.Sort(out.Raised)
	}
	g, err := f.fetch(root)
	if err != nil {
		return failed(err)
	}
	out.Changes = f.changes(g)
	return out, nil
}

// newest is the version Update takes id's repository at from v: its
// highest release in v's line (pkgcache.Line: the major version from v1
// on, the minor version from v0.1 on, the release below v0.1), not a
// prerelease, so a new major version, a new 0.x line or a new 0.0.x
// release is never taken, and a prerelease only when v is already a later
// one. A release `nomi` takes the compiler's repository at its own version
// (pkgcache.Pinned), up or down.
func (f *fetcher) newest(id pkgcache.ID, v string) (string, error) {
	if pin := pkgcache.Pinned(id.Repo); pin != "" {
		return f.pinned(id, pin)
	}
	tags, err := f.tagsOf(id.Repo)
	if err != nil {
		return "", fmt.Errorf("finding the latest version of %s: %w", id.Repo, err)
	}
	inLine, _ := latest(tags, v)
	return inLine, nil
}

// latest is, of tags with prereleases left out, the highest release in v's
// line (pkgcache.Line), which `nomi update` takes from v (newest), and the
// highest of all, each at least v and the second at least the first. A
// newest past inLine's line is the release Update names and does not take
// (Updated.Later), and the one `nomi outdated` lists as the latest
// (Outdated).
func latest(tags map[string]string, v string) (inLine, newest string) {
	inLine, newest = highest(tags, pkgcache.Line(v)), highest(tags, "")
	if semver.Compare(inLine, v) < 0 {
		inLine = v
	}
	if semver.Compare(newest, inLine) < 0 {
		newest = inLine
	}
	return inLine, newest
}

// Behind is a repository of the build with a later release than the one
// it is taken at (Outdated).
type Behind struct {
	Repo string
	// Version is the version the build takes it at, Line the latest
	// release in that version's line, which `nomi update` would take, and
	// Newest the latest release of all.
	Version, Line, Newest string
	// Via is the name of each package whose [dependencies] reaches the
	// repository, in the order the walk reached them, empty for a
	// repository the project's [dependencies] names.
	Via []string
}

// Outdated answers each repository the project at root's build takes at
// a tag that has a later release, sorted by repository: the build as
// nomi.lock locks it, every member's in a workspace (pkgcache.WalkAll),
// fetched as a plain fetch fetches it, and each
// repository's tags as its host answers them now. It writes nothing. A
// repository taken at a commit has no release to be behind. It reads the
// tags each time, and nomi keeps no list of them, so `nomi outdated` takes
// no --offline.
func Outdated(root string, opts Options) ([]Behind, error) {
	opts.Update = false
	f, err := newFetcher(root, opts)
	if err != nil {
		return nil, err
	}
	g, err := pkgcache.WalkAll(root, f.materialize)
	if err != nil || g == nil {
		return nil, err
	}
	byRepo := map[string]*Behind{}
	var order []string
	for _, d := range g.Deps {
		if d.Local || d.ID.IsZero() || pkgcache.IsCommit(d.Version) {
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
		b.Line, b.Newest = latest(tags, b.Version)
		if b.Newest != b.Version {
			out = append(out, *b)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Repo < out[j].Repo })
	return out, nil
}

// direct reports whether the project's [dependencies] names a package of
// repo.
func direct(g *pkgcache.Graph, repo string) bool {
	for _, d := range g.Deps {
		if d.Direct && d.Repo == repo {
			return true
		}
	}
	return false
}
