package analysis

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/nomi-language/nomi/internal/modcache"
)

// ModuleIndex maps a Nomi short-name (as declared in a require'd
// module's nomi.toml) to that module's absolute filesystem root.
// Built once per project at the start of analysis, this index is
// what lets a cross-module import like `import stringkit/pad` find
// its dependency's source files.
type ModuleIndex map[string]string

// BuildModuleIndex reads go.mod at projectRoot and indexes every Nomi module
// the project needs (modcache.Load): a require replaced with a local
// directory, or one in the Go module cache. Each is indexed by its
// nomi.toml's [module].name, not by its Go module path.
//
// It never downloads. In the `nomi` CLI, modcache.Load first runs the
// fetcher the CLI installs (internal/modfetch), so a module the project needs
// is in the cache by then, or the fetch's error is this one. Elsewhere (the
// language server) a module missing from the cache is left out of the
// index, and an import of it reports the module missing with a hint to run
// `nomi fetch` (modcache.MissingHint).
//
// Returns an empty index (not an error) if go.mod is absent: the single-file
// `nomi run` and no-deps modes stay valid. A needed module with no nomi.toml
// is a Go module (FFI use) and is not indexed. A replace whose target does not
// exist, or is not a directory, is an error, and so are two modules whose
// nomi.toml claim the same name, since the index cannot tell them apart.
func BuildModuleIndex(projectRoot string) (ModuleIndex, error) {
	g, err := modcache.Load(projectRoot)
	if err != nil {
		return nil, err
	}
	index := ModuleIndex{}
	if g == nil {
		return index, nil
	}
	for _, dep := range g.Deps {
		if !dep.Nomi {
			continue
		}
		m, err := LoadManifest(dep.Dir)
		if err != nil {
			if errors.Is(err, ErrManifestMissing) {
				continue
			}
			return nil, fmt.Errorf("load %s: %w", filepath.Join(dep.Dir, "nomi.toml"), err)
		}
		if existing, taken := index[m.Name]; taken {
			return nil, fmt.Errorf("two modules claim name %q: %s and %s — rename [module].name in one of them", m.Name, existing, dep.Dir)
		}
		index[m.Name] = dep.Dir
	}
	return index, nil
}
