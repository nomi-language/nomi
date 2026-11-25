package analysis

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/nomi-language/nomi/internal/pkgcache"
)

// PackageIndex is the packages a program's files import by name, read from
// the package graph (pkgcache). Each package other than the program's own
// has a key name, the first segment of each of its files' keys: "std" for
// the standard library, and a dependency's pkgcache.Dep.Name. Name
// resolution is per package: a file imports another package by a name its
// own package's nomi.toml gives it, never by one the program gives it.
type PackageIndex struct {
	// Roots is each package's root directory by its key name.
	Roots map[string]string
	// Names is, by the key name of the package whose files write them (""
	// for the program's own package), each name those files import a package
	// by, and that package's key name: "" for the program's own package. A
	// dependency's own [package].name is one of its names.
	Names map[string]map[string]string
	// Own is the root of the program's own package, which a dependency's
	// file reaches by a name that names it; "" for a program held in
	// memory, whose files' builders know their root.
	Own string
	// Fetched holds the key name of each dependency whose files a fetch put
	// in the package cache (pkgcache.Dep.Local unset): a repository's
	// package, a first-party lib/ package the compiler's repository supplies
	// among them. Those files are not the reader's to edit, so a
	// diagnostic about one of their declarations suggests no edit of it
	// (FileAnalysis.unseen). A package loaded from a directory, the
	// program's own and std are not in it.
	Fetched map[string]bool
}

// newPackageIndex is an index of no dependencies.
func newPackageIndex() PackageIndex {
	return PackageIndex{Roots: map[string]string{}, Names: map[string]map[string]string{}, Fetched: map[string]bool{}}
}

// HasPackage reports whether name is a key name of the index: "std", or a
// dependency's.
func (ix PackageIndex) HasPackage(name string) bool {
	_, ok := ix.Roots[name]
	return ok
}

// Dependency answers the root of the dependency the program's own files
// import as name, as `nomi tool <package>` names one.
func (ix PackageIndex) Dependency(name string) (string, bool) {
	key, ok := ix.Names[""][name]
	if !ok || key == "" {
		return "", false
	}
	root, ok := ix.Roots[key]
	return root, ok
}

// importDest is what an import path written in a file of the package
// keyed in ("" for the program's own) names.
type importDest struct {
	// package is the key name of the package the file is in: "" for the
	// program's own, "std", or a dependency's.
	pkg string
	// root is that package's root directory: projectRoot for the
	// program's own.
	root string
	// rest is the path within the package, empty when the path is a
	// package's name alone and the package has no file of that name at its
	// root (the package's root directory).
	rest []string
	// key is the key of the file rest names: rest joined with "/", after
	// the package's key name for any package but the program's own.
	key string
	// named is set when the path's first name is a package's name, its own
	// or another's, rather than a file or directory of the package it is
	// written in.
	named bool
}

// file is the file t names: rest under its package's root, the stdlib's
// under StdFilesDir.
func (t importDest) file() string {
	root := t.root
	if t.pkg == "std" {
		root = StdFilesDir(root)
	}
	return ResolveModulePath(root, t.rest)
}

// target is what path, an import path written in a file of the package
// keyed in, names, for a program whose own files are under projectRoot
// and whose own package is named self. The path's first name is looked up
// in the importing package's names: one that names a package leads into it,
// and any other is a path inside the importing package. A package's name
// alone names the file of that name at its root when there is one, and
// otherwise its root directory (spec §14 *Modules and packages*). `std`
// always names the standard library, whose files QualifyIntraStdlibImport
// spells in full.
func (ix PackageIndex) target(in, self, projectRoot string, path []string) importDest {
	t := importDest{pkg: in, root: ix.rootOf(in, projectRoot), rest: path}
	if len(path) > 0 {
		key, named := "", false
		switch {
		case path[0] == "std":
			key, named = "std", true
		case in == "" && self != "" && path[0] == self:
			named = true
		default:
			key, named = ix.Names[in][path[0]]
		}
		if named {
			t.pkg, t.root, t.named = key, ix.rootOf(key, projectRoot), true
			t.rest = path[1:]
			switch {
			case len(path) != 1 || key == "std":
			case key == in:
				// A package's own name alone is an ordinary path inside it:
				// its root file of that name, else its root directory,
				// which the loader answers, for files on disk or in memory.
				t.rest = path
			default:
				if info, err := os.Stat(filepath.Join(t.root, path[0]+".nomi")); err == nil && !info.IsDir() {
					t.rest = path
				}
			}
		}
	}
	t.key = strings.Join(t.rest, "/")
	if t.pkg != "" {
		t.key = strings.Join(append([]string{t.pkg}, t.rest...), "/")
	}
	return t
}

// rootOf is the root of the package keyed module.
func (ix PackageIndex) rootOf(module, projectRoot string) string {
	switch {
	case module != "":
		return ix.Roots[module]
	case ix.Own != "":
		return ix.Own
	}
	return projectRoot
}

// packageOfKey is the key name of the package the file keyed key is in: the
// key's first segment when that is a package's key name, else "" for the
// program's own package.
func (ix PackageIndex) packageOfKey(key string) string {
	head, _, _ := strings.Cut(key, "/")
	if ix.HasPackage(head) && head != key {
		return head
	}
	return ""
}

// selfName is the name the files of the package keyed module write for
// their own package, or "".
func (ix PackageIndex) selfName(module, self string) string {
	if module == "" {
		return self
	}
	for name, key := range ix.Names[module] {
		if key == module {
			return name
		}
	}
	return ""
}

// BuildPackageIndex reads the package graph of the program rooted at
// projectRoot (pkgcache.Load) and indexes every package it needs: one
// the project's nomi.toml names with a directory, or one in the package
// cache (Fetched), by its pkgcache.Dep.Name, with the names each package's
// own files import others by. The program's own package is the one rooted at
// projectRoot: the project, or, for a dependency's tool (pkgcache.MainPackage),
// that dependency.
//
// It never downloads. In the `nomi` CLI, pkgcache.Load first runs the
// fetcher the CLI installs (internal/pkgfetch), so a package the project needs
// is in the cache by then, or the fetch's error is this one. Elsewhere (the
// language server) a package missing from the cache is left out of the
// index, and an import of it reports the package missing with a hint to run
// `nomi fetch` (pkgcache.MissingHint).
//
// Returns an empty index (not an error) when there is no nomi.toml: the
// single-file `nomi run` mode stays valid. A needed package with no
// nomi.toml is not indexed, and a dependency directory that does not
// exist, or is not a directory, is an error.
func BuildPackageIndex(projectRoot string) (PackageIndex, error) {
	index := newPackageIndex()
	index.Own = projectRoot
	g, err := pkgcache.Load(projectRoot)
	if err != nil {
		return PackageIndex{}, err
	}
	if g == nil {
		return index, nil
	}
	// A dependency's tool reads the main package's graph
	// (pkgcache.MainPackage), which holds the tool's own package: that one
	// is the program's own package here.
	own := ""
	if d, ok := g.At(projectRoot); ok {
		own = d.Name
	}
	indexed := map[string]bool{}
	for _, d := range g.Deps {
		if d.Nomi && d.Dir != "" && d.Name != own {
			index.Roots[d.Name] = d.Dir
			indexed[d.Name] = true
			if !d.Local {
				index.Fetched[d.Name] = true
			}
		}
	}
	names := func(of map[string]string) map[string]string {
		out := map[string]string{}
		for name, key := range of {
			switch {
			case key == own:
				out[name] = ""
			case indexed[key]:
				out[name] = key
			}
		}
		return out
	}
	index.Names[""] = names(g.NamesAt(projectRoot))
	for _, d := range g.Deps {
		if indexed[d.Name] {
			index.Names[d.Name] = names(d.Names)
		}
	}
	return index, nil
}

// PackageRoot is the root of the package the project at root names name, as
// `nomi tool <package>` names one: the project itself when its own
// [package].name is name, else the dependency its nomi.toml names name; ""
// when it is neither.
func PackageRoot(root, name string) (string, error) {
	if m, err := LoadManifest(root); err == nil && m.Name == name {
		return root, nil
	}
	index, err := BuildPackageIndex(root)
	if err != nil {
		return "", err
	}
	dir, _ := index.Dependency(name)
	return dir, nil
}
