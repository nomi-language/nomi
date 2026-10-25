package modfetch

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"

	"github.com/nomi-language/nomi/internal/modcache"
)

// AddLocal requires the Nomi module in directory dir (absolute, or relative
// to the working directory) in the project at root's go.mod: a direct
// `require <its module path> v0.0.0`, or the version go.mod already
// requires, and a `replace <its module path> => <dir>` with dir relative to
// root. It then fetches what the module requires and writes go.sum.
func AddLocal(root, dir string, opts Options) (Added, error) {
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
	data, err := os.ReadFile(filepath.Join(abs, "go.mod"))
	if errors.Is(err, os.ErrNotExist) {
		return Added{}, fmt.Errorf("%s has no go.mod, so it has no module path; run `nomi new <module path> %s` to give it one", dir, dir)
	}
	if err != nil {
		return Added{}, err
	}
	path := modfile.ModulePath(data)
	if path == "" {
		return Added{}, fmt.Errorf("%s has no `module` line", filepath.Join(dir, "go.mod"))
	}
	if _, err := os.Stat(filepath.Join(abs, "nomi.toml")); err != nil {
		return Added{}, fmt.Errorf("%s has no nomi.toml, so it is not a Nomi module", dir)
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		rel = abs
	}
	target := modcache.AddArg(rel)
	m := module.Version{Path: path, Version: "v0.0.0"}
	if err := editGoMod(root, func(mf *modfile.File) error {
		if mf.Module != nil && mf.Module.Mod.Path == path {
			return fmt.Errorf("%s is this project's own module path", path)
		}
		var list []*modfile.Require
		for _, r := range mf.Require {
			if r.Mod.Path == path {
				m.Version = r.Mod.Version
				continue
			}
			list = append(list, &modfile.Require{Mod: r.Mod, Indirect: r.Indirect})
		}
		list = append(list, &modfile.Require{Mod: m})
		mf.SetRequireSeparateIndirect(list)
		for _, r := range append([]*modfile.Replace(nil), mf.Replace...) {
			if r.Old.Path == path {
				if err := mf.DropReplace(path, r.Old.Version); err != nil {
					return err
				}
			}
		}
		return mf.AddReplace(path, "", target, "")
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
	return Added{Module: m, Dir: abs, Replace: target}, nil
}

func same(a, b string) bool {
	ai, errA := os.Stat(a)
	bi, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(ai, bi)
}

// Removed is what Remove took out of go.mod.
type Removed struct {
	Module module.Version
	// Replace is the replace target go.mod had for the module, if any.
	Replace string
	// Unneeded are the indirect requirements nothing else needs any more,
	// which went with it.
	Unneeded []module.Version
}

// Remove takes module path's requirement, and any replace of it, out of the
// project at root's go.mod, with each indirect requirement that no module
// still required needs, and drops their go.sum lines. Which requirements are
// still needed is read from the go.mod files of the modules that stay (their
// directories, or the module cache); when one of those is not on disk, every
// indirect requirement stays.
func Remove(root, path string) (Removed, error) {
	gomod := filepath.Join(root, "go.mod")
	data, err := os.ReadFile(gomod)
	if err != nil {
		return Removed{}, err
	}
	mf, err := modfile.Parse(gomod, data, nil)
	if err != nil {
		return Removed{}, err
	}
	var out Removed
	found := false
	for _, r := range mf.Require {
		if r.Mod.Path == path {
			out.Module, found = r.Mod, true
		}
	}
	for _, r := range mf.Replace {
		if r.Old.Path == path {
			out.Replace = r.New.Path
			if r.New.Version != "" {
				out.Replace += " " + r.New.Version
			}
			if !found {
				out.Module = module.Version{Path: path}
			}
			found = true
		}
	}
	if !found {
		return Removed{}, fmt.Errorf("go.mod does not require %s", path)
	}
	out.Unneeded = unneededAfter(mf, root, path)
	gone := map[string]bool{path: true}
	for _, m := range out.Unneeded {
		gone[m.Path] = true
	}
	if err := editGoMod(root, func(mf *modfile.File) error {
		for p := range gone {
			if err := mf.DropRequire(p); err != nil {
				return err
			}
			for _, r := range append([]*modfile.Replace(nil), mf.Replace...) {
				if r.Old.Path == p {
					if err := mf.DropReplace(p, r.Old.Version); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}); err != nil {
		return Removed{}, err
	}
	sums, err := readGoSum(filepath.Join(root, "go.sum"))
	if err != nil {
		return Removed{}, err
	}
	sums.drop(func(key string) bool {
		p, _, _ := strings.Cut(key, " ")
		return !gone[p]
	})
	return out, sums.write()
}

// unneededAfter answers the indirect requirements of mf that nothing needs
// once path is no longer required: those no module reachable from the
// remaining direct requirements requires, through the requirements mf
// lists. nil when a reachable module's go.mod is not on disk.
func unneededAfter(mf *modfile.File, root, path string) []module.Version {
	cache, err := modcache.Root()
	if err != nil {
		return nil
	}
	listed := map[string]module.Version{}
	for _, r := range mf.Require {
		listed[r.Mod.Path] = r.Mod
	}
	seen := map[string]bool{path: true}
	var queue []module.Version
	for _, r := range mf.Require {
		if !r.Indirect && r.Mod.Path != path {
			queue = append(queue, r.Mod)
		}
	}
	for len(queue) > 0 {
		m := queue[0]
		queue = queue[1:]
		if seen[m.Path] {
			continue
		}
		seen[m.Path] = true
		var modFile string
		if dir, local := modcache.LocalReplace(mf, root, m); local {
			modFile = filepath.Join(dir, "go.mod")
		} else {
			modFile, err = modcache.VersionFile(cache, modcache.ReplacedVersion(mf, m), ".mod")
			if err != nil {
				return nil
			}
		}
		reqs, err := readRequires(modFile)
		if err != nil {
			return nil
		}
		if reqs == nil {
			if _, statErr := os.Stat(modFile); statErr != nil {
				return nil
			}
		}
		for _, r := range reqs {
			if l, ok := listed[r.Path]; ok && !seen[r.Path] {
				queue = append(queue, l)
			}
		}
	}
	var out []module.Version
	for _, r := range mf.Require {
		if r.Indirect && !seen[r.Mod.Path] {
			out = append(out, r.Mod)
		}
	}
	return out
}

// Change is one requirement Update moved: From is "" for one it added.
type Change struct {
	Path, From, To string
}

// Update moves module only's requirement, or with only "" every
// requirement go.mod lists, to its latest release, as `go get -u` does: the
// highest release of the same module path, so a new major version (a /v2
// path) is never taken, and a pre-release only when go.mod already requires
// a later one. A module go.mod replaces with a directory is skipped. It
// raises what the new versions require, downloads what the project then
// needs, writes go.sum, and drops the old versions' go.sum lines. It
// answers each requirement that changed.
func Update(root, only string, opts Options) ([]Change, error) {
	opts.Update = true
	f, err := newFetcher(root, opts)
	if err != nil {
		return nil, err
	}
	gomod := filepath.Join(root, "go.mod")
	data, err := os.ReadFile(gomod)
	if err != nil {
		return nil, err
	}
	before, err := modfile.Parse(gomod, data, nil)
	if err != nil {
		return nil, err
	}
	var targets []module.Version
	for _, r := range before.Require {
		if only != "" && r.Mod.Path != only {
			continue
		}
		if dir, local := modcache.LocalReplace(before, root, r.Mod); local {
			if only != "" {
				return nil, fmt.Errorf("go.mod replaces %s with the directory %s, which has no releases to update to", only, dir)
			}
			continue
		}
		targets = append(targets, r.Mod)
	}
	if only != "" && len(targets) == 0 {
		return nil, fmt.Errorf("go.mod does not require %s", only)
	}
	moved := map[string]string{}
	var raise []module.Version
	for _, m := range targets {
		v, err := f.upgrade(m)
		if err != nil {
			return nil, fmt.Errorf("finding the latest version of %s: %w", m.Path, err)
		}
		if v == m.Version {
			continue
		}
		moved[m.Path] = v
		modData, err := f.goModOf(module.Version{Path: m.Path, Version: v})
		if err != nil {
			return nil, err
		}
		reqs, err := parseRequires(m.Path+"@"+v+"/go.mod", modData)
		if err != nil {
			return nil, err
		}
		raise = append(raise, reqs...)
	}
	if len(moved) == 0 {
		return nil, nil
	}
	if err := editGoMod(root, func(mf *modfile.File) error {
		self := ""
		if mf.Module != nil {
			self = mf.Module.Mod.Path
		}
		for p, v := range moved {
			if err := mf.AddRequire(p, v); err != nil {
				return err
			}
		}
		listed := map[string]string{}
		for _, r := range mf.Require {
			listed[r.Mod.Path] = r.Mod.Version
		}
		for _, r := range raise {
			if r.Path == self {
				continue
			}
			have, ok := listed[r.Path]
			switch {
			case !ok:
				mf.AddNewRequire(r.Path, r.Version, true)
				listed[r.Path] = r.Version
			case semver.Compare(r.Version, have) > 0:
				if err := mf.AddRequire(r.Path, r.Version); err != nil {
					return err
				}
				listed[r.Path] = r.Version
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if _, err := f.fetch(root); err != nil {
		return nil, err
	}
	data, err = os.ReadFile(gomod)
	if err != nil {
		return nil, err
	}
	after, err := modfile.Parse(gomod, data, nil)
	if err != nil {
		return nil, err
	}
	was := map[string]string{}
	for _, r := range before.Require {
		was[r.Mod.Path] = r.Mod.Version
	}
	var changes []Change
	stale := map[string]bool{}
	for _, r := range after.Require {
		if old := was[r.Mod.Path]; old != r.Mod.Version {
			changes = append(changes, Change{Path: r.Mod.Path, From: old, To: r.Mod.Version})
			if old != "" {
				stale[r.Mod.Path+" "+old] = true
			}
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	sums, err := readGoSum(filepath.Join(root, "go.sum"))
	if err != nil {
		return nil, err
	}
	// The old version's zip is no longer built; its go.mod line stays, as
	// the go command keeps the go.mod hashes of the versions it read.
	sums.drop(func(key string) bool { return !stale[key] })
	return changes, sums.write()
}

// upgrade answers the version `go get -u` would move m to: the highest
// release of m.Path above m.Version, else m.Version. A +incompatible
// version is taken only when m.Version is one.
func (f *fetcher) upgrade(m module.Version) (string, error) {
	list, err := f.proxyGet(m.Path, "@v/list")
	if errors.Is(err, errDirect) {
		v, err := f.goLatest(m.Path)
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
