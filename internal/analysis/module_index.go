package analysis

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/mod/modfile"
)

// ModuleIndex maps a Nomi short-name (as declared in a require'd
// module's nomi.toml) to that module's absolute filesystem root.
// Built once per project at the start of analysis, this index is
// what lets a cross-module import like `import stringkit/pad` find
// its sibling-module source files without needing a network or Go
// proxy lookup at analyze time.
type ModuleIndex map[string]string

// BuildModuleIndex reads go.mod at projectRoot, follows each
// require directive's matching replace directive to a sibling
// directory, loads that directory's nomi.toml, and indexes the
// result by the manifest's Name field.
//
// Path/name distinction worth pinning down: the Go module path
// (`require example.com/foo`) is only used here to look up the
// replace target. The index key is the *manifest's* Name field —
// they often coincide for tidy setups but don't have to. Consumers
// resolve cross-module imports by matching the first import-path
// segment against the index's keys, so the manifest name is what
// matters at every use site downstream.
//
// Returns an empty index (not an error) if go.mod is absent — the
// single-file `nomi run` and no-deps modes stay valid. A require
// without a matching replace is also skipped silently: only
// local-replace deps are supported (no Go module proxy lookup).
//
// A require whose target directory has no nomi.toml is treated as
// a pure-Go dep (FFI use) and skipped. A require whose target path
// either does not exist or is not a directory surfaces as a
// descriptive error — that's almost certainly a typo in the
// replace path.
//
// Two requires whose nomi.toml claim the same Name produce an
// error: the index is keyed by short-name and can't disambiguate.
func BuildModuleIndex(projectRoot string) (ModuleIndex, error) {
	goModPath := filepath.Join(projectRoot, "go.mod")
	data, err := os.ReadFile(goModPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ModuleIndex{}, nil
		}
		return nil, fmt.Errorf("read %s: %w", goModPath, err)
	}
	f, err := modfile.Parse(goModPath, data, nil)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", goModPath, err)
	}

	// Build a require-path → filesystem-root map from the replace
	// directives. A replace whose New.Path is empty (shouldn't
	// happen in practice for the local-replace shape we support,
	// but defensive against malformed input) is skipped.
	//
	// `r.Old.Version` is intentionally ignored: the local-replace
	// shape supported here never pins versions (`replace foo => ../foo`
	// rather than `replace foo v1.0.0 => ../foo`). If you ever need to
	// distinguish versioned replaces, the map key would be a
	// `(Path, Version)` tuple — not a problem yet.
	replaces := make(map[string]string, len(f.Replace))
	for _, r := range f.Replace {
		if r.New.Path == "" {
			continue
		}
		target := r.New.Path
		if !filepath.IsAbs(target) {
			target = filepath.Join(projectRoot, target)
		}
		replaces[r.Old.Path] = target
	}

	index := ModuleIndex{}
	for _, req := range f.Require {
		target, ok := replaces[req.Mod.Path]
		if !ok {
			// No replace — would need Go-proxy resolution, and only
			// local-replace deps are supported; skip.
			continue
		}
		// Distinguish "target directory missing entirely" (typo in
		// the replace path — surface as an error) from "directory
		// exists but has no nomi.toml" (pure-Go dep — skip silently).
		// LoadManifest collapses both into ErrManifestMissing, so
		// stat the directory first. Also differentiate file-as-target
		// from missing-entirely so the diagnostic points at the actual
		// problem.
		info, statErr := os.Stat(target)
		switch {
		case statErr != nil:
			return nil, fmt.Errorf("replace target for %q does not exist: %s", req.Mod.Path, target)
		case !info.IsDir():
			return nil, fmt.Errorf("replace target for %q is not a directory: %s", req.Mod.Path, target)
		}
		m, err := LoadManifest(target)
		if err != nil {
			if errors.Is(err, ErrManifestMissing) {
				// Pure Go module (FFI use), not a Nomi module. Skip.
				continue
			}
			return nil, fmt.Errorf("load %s/nomi.toml: %w", target, err)
		}
		if existing, taken := index[m.Name]; taken {
			return nil, fmt.Errorf("two modules claim name %q: %s and %s — rename [module].name in one of them", m.Name, existing, target)
		}
		index[m.Name] = target
	}
	return index, nil
}
