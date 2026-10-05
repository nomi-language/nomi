package ffirun

import (
	"os"
	"path/filepath"
	"sort"
)

// localsIdentityVersion prefixes the local-module identity hash, for the reason
// identityVersion prefixes the compiler one: a change to WHAT is selected has
// to be a change of identity, or every cache directory on disk keeps claiming
// an identity computed by the old selector.
const localsIdentityVersion = "ffirun-local-module-identity/1"

// projectIdentity is the PROJECT half of the FFI-run cache key: a content hash
// of every LOCAL Go tree the cached wrapper links which no other field speaks
// for.
//
// Why the key needs it, observed on a project outside the compiler
// tree whose Go binding returned `strings.ToUpper(s)`. Editing that body to
// `strings.ToUpper(s) + "!"` — signature untouched — moved nothing in the key:
// `go.mod`/`go.sum` unchanged, `Template` unchanged, `Discovered` unchanged
// (it carries import paths and binding NAMES, not bodies), `Compiler`
// unchanged (the binding is not under the compiler roots). The warm cache
// printed `FFI OK` while a cold cache on the same source printed `FFI OK!`.
// That is the same defect the Compiler field closed, one module over: a user
// editing their own Go binding and re-running is the inner loop of writing an
// FFI binding.
//
// Why nobody hit it in this repository: every in-tree FFI fixture
// (`cmd/nomi/testdata/go_bindings`, `tests/18-ffi-and-dynamic/*`) lives UNDER
// the compiler module's root, so its Go was already selected by the compiler identity as a
// side effect of sitting inside a compiler root. Only a project outside that
// tree — which is every real user project — was exposed.
//
// What go.sum already covers, and why this is not it. A dependency resolved
// from the module cache is immutable and checksummed, so `GoSum` is a sound
// identity for it. A dependency reached through a LOCAL `replace` has no
// checksum and can change under the cache without any recorded input moving.
// Those are exactly the roots hashed here.
//
// NOT memoized, unlike compilerIdentity. A compiler's identity cannot change
// while that compiler runs; a project's tree is the thing the user is editing
// between runs, and `Prepare` is called per project.
func projectIdentity(projectRoot string) (string, error) {
	roots, err := localIdentityRoots(projectRoot)
	if err != nil {
		return "", err
	}
	return hashTrees(localsIdentityVersion, roots, projectTreeScope)
}

// localIdentityRoots is every local filesystem tree the staged wrapper's
// go.mod will name, minus the ones the Compiler field already hashes.
//
// The set mirrors writeAbsolutizedGoMod, which is what actually writes those
// `replace` lines: the project root (required and replaced back to itself, so
// wrapper code can import project-local Go packages) plus every path `replace`
// target, absolutized against the project root exactly as it is there. A
// version `replace` is skipped — modfile leaves New.Version empty for a path
// replace, and a module-to-module replace is covered by go.sum.
//
// Compiler roots are dropped rather than hashed twice. A project whose go.mod
// replaces `nomi` itself names the local compiler tree (prepareWrapperModule overrides
// that replace with the same root anyway); hashing it here as well would walk
// 601 files a second time for an identical answer.
// Containment is NOT unwound — a project root that happens to CONTAIN a
// compiler root is hashed whole. That costs a redundant walk, which is the
// safe direction, and detecting it would mean guessing which of two overlapping
// trees the user meant.
//
// A project with no go.mod contributes only its own root, which selects nothing
// (no `.go`, no `go.mod`) and hashes to the version stamp. That is correct:
// writeAbsolutizedGoMod stages a SYNTHETIC go.mod for such a project, naming
// only the compiler's module, so there is no project-side Go in the
// artifact to key on.
func localIdentityRoots(projectRoot string) ([]string, error) {
	skip := map[string]bool{}
	// A compiler root that cannot be located is not an error here. The wrapper
	// path fails on its own terms in that case (errNoCompilerSource), and a
	// missing root must never make the key unavailable — the failure mode this
	// whole field exists to prevent is a key that silently stops moving.
	if nomiRoot := localCompilerRoot(); nomiRoot != "" {
		skip[filepath.Clean(nomiRoot)] = true
	}

	seen := map[string]bool{}
	var roots []string
	add := func(dir string) {
		clean := filepath.Clean(dir)
		if skip[clean] || seen[clean] {
			return
		}
		seen[clean] = true
		roots = append(roots, clean)
	}
	add(projectRoot)

	if _, err := os.Stat(filepath.Join(projectRoot, "go.mod")); err == nil {
		f, err := parseProjectGoMod(projectRoot)
		if err != nil {
			return nil, err
		}
		for _, r := range f.Replace {
			if r.New.Version != "" {
				continue
			}
			target := r.New.Path
			if !filepath.IsAbs(target) {
				target = filepath.Join(projectRoot, target)
			}
			add(target)
		}
	}

	// Sorted so the hash does not depend on go.mod's replace ORDER. Reordering
	// replace lines cannot change the artifact, so it must not change the key.
	sort.Strings(roots)
	return roots, nil
}
