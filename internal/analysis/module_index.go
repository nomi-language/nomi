package analysis

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/nomi-language/nomi/internal/modcache"
)

// ModuleIndex is the modules a program's files import by name, read from
// the module graph (modcache). Each module other than the program's own
// has a key name, the first segment of each of its files' keys: "std" for
// the standard library, and a dependency's modcache.Dep.Name. Name
// resolution is per module: a file imports another module by a name its
// own module's nomi.toml gives it, never by one the program gives it.
type ModuleIndex struct {
	// Roots is each module's root directory by its key name.
	Roots map[string]string
	// Names is, by the key name of the module whose files write them (""
	// for the program's own module), each name those files import a module
	// by, and that module's key name: "" for the program's own module. A
	// dependency's own [module].name is one of its names.
	Names map[string]map[string]string
	// Own is the root of the program's own module, which a dependency's
	// file reaches by a name that names it; "" for a program held in
	// memory, whose files' builders know their root.
	Own string
}

// newModuleIndex is an index of no dependencies.
func newModuleIndex() ModuleIndex {
	return ModuleIndex{Roots: map[string]string{}, Names: map[string]map[string]string{}}
}

// HasModule reports whether name is a key name of the index: "std", or a
// dependency's.
func (ix ModuleIndex) HasModule(name string) bool {
	_, ok := ix.Roots[name]
	return ok
}

// Dependency answers the root of the dependency the program's own files
// import as name, as `nomi tool <module>` names one.
func (ix ModuleIndex) Dependency(name string) (string, bool) {
	key, ok := ix.Names[""][name]
	if !ok || key == "" {
		return "", false
	}
	root, ok := ix.Roots[key]
	return root, ok
}

// importDest is what an import path written in a file of the module
// keyed in ("" for the program's own) names.
type importDest struct {
	// module is the key name of the module the file is in: "" for the
	// program's own, "std", or a dependency's.
	module string
	// root is that module's root directory: projectRoot for the
	// program's own.
	root string
	// rest is the path within the module, empty when the path is a
	// module's name alone and the module has no file of that name at its
	// root (the module's root directory).
	rest []string
	// key is the key of the file rest names: rest joined with "/", after
	// the module's key name for any module but the program's own.
	key string
	// named is set when the path's first name is a module's name, its own
	// or another's, rather than a file or directory of the module it is
	// written in.
	named bool
}

// file is the file t names: rest under its module's root, the stdlib's
// under StdFilesDir.
func (t importDest) file() string {
	root := t.root
	if t.module == "std" {
		root = StdFilesDir(root)
	}
	return ResolveModulePath(root, t.rest)
}

// target is what path, an import path written in a file of the module
// keyed in, names, for a program whose own files are under projectRoot
// and whose own module is named self. The path's first name is looked up
// in the importing module's names: one that names a module leads into it,
// and any other is a path inside the importing module. A module's name
// alone names the file of that name at its root when there is one, and
// otherwise its root directory (spec §14 *Files and modules*). `std`
// always names the standard library, whose files QualifyIntraStdlibImport
// spells in full.
func (ix ModuleIndex) target(in, self, projectRoot string, path []string) importDest {
	t := importDest{module: in, root: ix.rootOf(in, projectRoot), rest: path}
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
			t.module, t.root, t.named = key, ix.rootOf(key, projectRoot), true
			t.rest = path[1:]
			switch {
			case len(path) != 1 || key == "std":
			case key == in:
				// A module's own name alone is an ordinary path inside it:
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
	if t.module != "" {
		t.key = strings.Join(append([]string{t.module}, t.rest...), "/")
	}
	return t
}

// rootOf is the root of the module keyed module.
func (ix ModuleIndex) rootOf(module, projectRoot string) string {
	switch {
	case module != "":
		return ix.Roots[module]
	case ix.Own != "":
		return ix.Own
	}
	return projectRoot
}

// moduleOfKey is the key name of the module the file keyed key is in: the
// key's first segment when that is a module's key name, else "" for the
// program's own module.
func (ix ModuleIndex) moduleOfKey(key string) string {
	head, _, _ := strings.Cut(key, "/")
	if ix.HasModule(head) && head != key {
		return head
	}
	return ""
}

// selfName is the name the files of the module keyed module write for
// their own module, or "".
func (ix ModuleIndex) selfName(module, self string) string {
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

// BuildModuleIndex reads the module graph of the program rooted at
// projectRoot (modcache.Load) and indexes every Nomi module it needs: one
// the project's nomi.toml names with a directory, or one in the Go module
// cache, by its modcache.Dep.Name, with the names each module's own files
// import others by. The program's own module is the one rooted at
// projectRoot: the project, or, for a dependency's tool (modcache.MainModule),
// that dependency.
//
// It never downloads. In the `nomi` CLI, modcache.Load first runs the
// fetcher the CLI installs (internal/modfetch), so a module the project needs
// is in the cache by then, or the fetch's error is this one. Elsewhere (the
// language server) a module missing from the cache is left out of the
// index, and an import of it reports the module missing with a hint to run
// `nomi fetch` (modcache.MissingHint).
//
// Returns an empty index (not an error) when there is no nomi.toml: the
// single-file `nomi run` mode stays valid. A needed module with no
// nomi.toml is not indexed, and a dependency directory that does not
// exist, or is not a directory, is an error.
func BuildModuleIndex(projectRoot string) (ModuleIndex, error) {
	index := newModuleIndex()
	index.Own = projectRoot
	g, err := modcache.Load(projectRoot)
	if err != nil {
		return ModuleIndex{}, err
	}
	if g == nil {
		return index, nil
	}
	// A dependency's tool reads the main module's graph
	// (modcache.MainModule), which holds the tool's own module: that one
	// is the program's own module here.
	own := ""
	if d, ok := g.At(projectRoot); ok {
		own = d.Name
	}
	indexed := map[string]bool{}
	for _, d := range g.Deps {
		if d.Nomi && d.Dir != "" && d.Name != own {
			index.Roots[d.Name] = d.Dir
			indexed[d.Name] = true
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

// ModuleRoot is the root of the module the project at root names name, as
// `nomi tool <module>` names one: the project itself when its own
// [module].name is name, else the dependency its nomi.toml names name; ""
// when it is neither.
func ModuleRoot(root, name string) (string, error) {
	if m, err := LoadManifest(root); err == nil && m.Name == name {
		return root, nil
	}
	index, err := BuildModuleIndex(root)
	if err != nil {
		return "", err
	}
	dir, _ := index.Dependency(name)
	return dir, nil
}
