package ffirun

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// cacheRootEnv lets tests override the cache root so we don't write
// into the user's ~/.cache/nomi during `go test`. Empty string means
// "use the user-cache default".
const cacheRootEnv = "NOMI_FFIRUN_CACHE_ROOT"

// cacheDirForProject returns ~/.cache/nomi/builds/<project-hash>/
// for the `nomi run` path (creating the directory tree if
// necessary). <project-hash> is the first 16 hex chars of
// SHA256(absolute project root) so two different projects on the
// same machine don't collide and the same project always lands in
// the same dir across runs.
//
// When NOMI_FFIRUN_CACHE_ROOT is set, that directory replaces
// ~/.cache/nomi/builds — used by tests to point at a t.TempDir().
func cacheDirForProject(projectRoot string) (string, error) {
	return cacheDirForKey(projectRoot, projectRoot, cacheKindRun)
}

// cacheDirForKey is the single funnel every cache directory is
// created through. It hashes key for the directory name, records
// projectRoot inside the directory, and sweeps the root.
//
// The order is load-bearing. The marker is written BEFORE the sweep,
// so this process's own entry carries a stamp of now and is
// protected by the grace window as well as by name. And the marker
// is written on every call including a warm hit, which is what makes
// its mtime a last-USE reading rather than a last-regeneration one —
// ensureWrapper's short-circuit means a warm hit otherwise writes
// nothing at all.
//
// A sweep failure is discarded deliberately: cache hygiene must
// never fail a build. The same goes for the marker — an entry
// without one is judged by the bound instead of by its root, which
// is exactly how the pre-existing directories are handled.
func cacheDirForKey(key, projectRoot, kind string) (string, error) {
	root, err := cacheRoot()
	if err != nil {
		return "", err
	}
	hash := cacheKeyHash(key)
	dir := filepath.Join(root, hash)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("ffirun: creating cache dir %s: %w", dir, err)
	}
	_ = recordOrigin(dir, projectRoot, kind)
	_, _ = evictCache(root, hash)
	return dir, nil
}

// cacheKeyHash is the directory name for a cache key: the first 16
// hex chars of its SHA256. Sole definition of the naming, so a test
// can plant an entry for a key without reimplementing it.
func cacheKeyHash(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])[:16]
}

// cacheRoot returns the absolute path of the builds-cache root,
// honoring the NOMI_FFIRUN_CACHE_ROOT override.
//
// An in-process test that has NOT set the override is refused rather
// than served the user's real cache. Two tests in this package used
// to reach Prepare without it, and every directory they created was
// keyed on a t.TempDir() that `go test` deleted on the way out —
// permanent orphans, and the only source of new ones in the week
// before eviction existed. Now that a cache directory can be
// DELETED, the same omission would evict a developer's working set
// mid-test, so the class is closed here instead of being fixed one
// test at a time. `testing.Testing()` is false in a subprocess, so
// tests that exec the real `nomi` binary are unaffected (they pass
// the override in the child's environment anyway).
func cacheRoot() (string, error) {
	if override := os.Getenv(cacheRootEnv); override != "" {
		return override, nil
	}
	if testing.Testing() {
		return "", fmt.Errorf("ffirun: %s is unset under `go test`; "+
			"set it to a t.TempDir() — a test must never read or evict the real build cache",
			cacheRootEnv)
	}
	uc, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("ffirun: locating user cache dir: %w", err)
	}
	return filepath.Join(uc, "nomi", "builds"), nil
}

// hashRecord is what we serialize to hash.json. Each field is a
// SHA256 of the corresponding input, prefixed by "sha256:" so the
// scheme is greppable and future-extensible (a different algorithm
// would carry a different prefix).
//
// Field meaning:
//   - GoMod: the project's go.mod file content
//   - GoSum: the project's go.sum file content (empty string hash if
//     the project has no go.sum yet)
//   - Template: the wrapper template text baked into the nomi binary
//   - Discovered: each discovered package's bindings as the wrapper
//     compiles them (discoveredHashInput), plus the direct requires
//   - Shapes: every Nomi type shape the generated adapters convert, through
//     the project's own struct, enum and distinct declarations
//     (adapterShapesHashInput)
//   - Compiler: the content identity of the two Nomi modules the
//     cached wrapper BINARY links — see identity.go.
//   - Locals: the content identity of the project's own Go tree and
//     of every module it reaches through a local `replace` — see
//     identitylocal.go.
//   - GoStd: the Go toolchain's version, when a binding names a Go standard
//     library package, and empty otherwise. The adapters were generated
//     from that toolchain's source and the wrapper links it (gostd.go).
//
// The first five fields describe the generated main.go. The next two
// describe the ~18 MB executable beside it, which is the artifact a
// warm cache actually re-executes; between them they cover every
// build input that no checksum speaks for.
type hashRecord struct {
	GoMod      string `json:"go_mod"`
	GoSum      string `json:"go_sum"`
	Template   string `json:"template"`
	Discovered string `json:"discovered"`
	Shapes     string `json:"shapes"`
	Compiler   string `json:"compiler"`
	Locals     string `json:"locals"`
	GoStd      string `json:"go_std,omitempty"`
}

// computeHashes builds a hashRecord for the supplied inputs. Missing
// go.sum is treated as the empty string (hashed) — that way a
// project with no go.sum yet still gets a deterministic hash that
// transitions cleanly once a go.sum appears.
//
// allRequires participates in the Discovered hash field alongside
// the discovered list — both are part of the FFIContext baked into
// the generated wrapper, so a change in either invalidates the
// cache.
func computeHashes(projectRoot string, discovered []DiscoveredPackage, allRequires []string) (hashRecord, error) {
	compilerID, err := compilerIdentity()
	if err != nil {
		return hashRecord{}, err
	}
	localsID, err := projectIdentity(projectRoot)
	if err != nil {
		return hashRecord{}, err
	}
	goModBytes, err := os.ReadFile(filepath.Join(projectRoot, "go.mod"))
	if err != nil && !os.IsNotExist(err) {
		return hashRecord{}, fmt.Errorf("reading go.mod: %w", err)
	}
	goSumBytes, err := os.ReadFile(filepath.Join(projectRoot, "go.sum"))
	if err != nil && !os.IsNotExist(err) {
		return hashRecord{}, fmt.Errorf("reading go.sum: %w", err)
	}
	imports := make([]string, len(discovered))
	for i, d := range discovered {
		imports[i] = discoveredHashInput(d)
	}
	sort.Strings(imports)
	sortedRequires := append([]string(nil), allRequires...)
	sort.Strings(sortedRequires)
	goStd := ""
	if discoversStd(discovered) {
		t := stdToolchain()
		goStd = t.version + " " + t.root
	}
	// The discovered hash covers both lists so either changing
	// triggers cache invalidation. Use a separator that can't appear
	// in a Go import path so the two lists can't accidentally collide.
	combined := strings.Join(imports, "\n") + "\x00" + strings.Join(sortedRequires, "\n")
	return hashRecord{
		GoMod:      sha256Hex(goModBytes),
		GoSum:      sha256Hex(goSumBytes),
		Template:   "sha256:" + templateHash, // already in hex
		Discovered: sha256Hex([]byte(combined)),
		Shapes:     sha256Hex([]byte(adapterShapesHashInput(discovered))),
		Compiler:   compilerID,
		Locals:     localsID,
		GoStd:      goStd,
	}, nil
}

// discoveredHashInput is one discovered package as the Discovered hash field
// reads it: every field the generated wrapper's code depends on, and no
// source position.
//
// The declaring file and the entry-scoped key are in it because the wrapper
// compiles them in: it registers a module-qualified binding under its bare
// name only when the entry it runs is the file that declared it. Keyed on
// names alone, a script that ran as hi at the go.mod root and then as bin/hi
// shared one wrapper, which still named the root file, so bin/hi crossed
// under a key nothing answered. The declaration text is in it because the
// wrapper's adapters are generated from it. Positions stay out, so an edit that
// only moves a binding's line does not rebuild the wrapper.
func discoveredHashInput(d DiscoveredPackage) string {
	q := strconv.Quote
	parts := []string{q(d.ImportPath)}
	for _, typ := range d.Types {
		parts = append(parts, "type:"+strings.Join([]string{
			q(typ.Key), q(typ.EntryKey), q(typ.TypeName), q(typ.GoTypeExpr),
			q(typ.Declaration), q(typ.SourceFile),
		}, ","))
	}
	for _, ex := range d.Exports {
		fields := []string{
			q(ex.Key), q(ex.EntryKey), q(ex.FuncName), q(ex.Declaration),
			q(ex.SourceFile),
		}
		for _, f := range ex.AlsoDeclaredIn {
			fields = append(fields, q(f))
		}
		parts = append(parts, "func:"+strings.Join(fields, ","))
	}
	return strings.Join(parts, "\t")
}

// sha256Hex returns the SHA256 of data prefixed by "sha256:" — the
// shape used throughout hashRecord. Empty input produces the SHA256
// of the empty string, NOT the empty string itself, so missing-vs-
// empty distinctions are preserved by the surrounding logic.
func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// readHashRecord deserializes hash.json from cacheDir. Returns the
// zero value + nil if the file doesn't exist (treated as a cache
// miss). A malformed file is treated as a cache miss too: the
// surrounding logic regenerates rather than failing.
func readHashRecord(cacheDir string) (hashRecord, bool) {
	data, err := os.ReadFile(filepath.Join(cacheDir, "hash.json"))
	if err != nil {
		return hashRecord{}, false
	}
	var r hashRecord
	if err := json.Unmarshal(data, &r); err != nil {
		return hashRecord{}, false
	}
	return r, true
}

// writeHashRecord serializes r to <cacheDir>/hash.json. The file is
// written via the standard "write-temp + rename" dance so a crashed
// run can't leave a half-written hash.json that would be read as a
// false cache hit on the next attempt.
func writeHashRecord(cacheDir string, r hashRecord) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(cacheDir, "hash.json.tmp")
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(cacheDir, "hash.json"))
}

// ensureWrapper guarantees that <cacheDir>/main.go, go.mod, go.sum,
// discovered.json, and hash.json reflect the current inputs. If the
// hash record on disk matches the just-computed hashes for every
// input, the function is a no-op (the warm-cache fast path). On any
// mismatch — or first-run — every file is regenerated and the hash
// record updated.
//
// The mtime preservation property the cache test pins down comes
// from this short-circuit: a .nomi edit that changes no binding and
// no type shape a binding converts leaves main.go untouched, so its
// mtime stays fixed across runs.
func ensureWrapper(cacheDir, projectRoot string, discovered []DiscoveredPackage, allRequires []string) error {
	want, err := computeHashes(projectRoot, discovered, allRequires)
	if err != nil {
		return err
	}
	if got, ok := readHashRecord(cacheDir); ok && got == want {
		// Warm cache hit; nothing to do.
		return nil
	}
	_ = os.Remove(cachedWrapperBinaryPathForDir(cacheDir))
	// Regenerate everything from scratch.
	if err := writeDiscoveredJSON(cacheDir, discovered); err != nil {
		return err
	}
	wrapper, err := renderWrapper(projectRoot, discovered)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(cacheDir, "main.go"), wrapper, 0o644); err != nil {
		return fmt.Errorf("writing wrapper main.go: %w", err)
	}
	// Absolutize relative `replace` paths so they still point at the
	// right tree from the cache dir's location. See
	// writeAbsolutizedGoMod's doc.
	if err := writeAbsolutizedGoMod(projectRoot, filepath.Join(cacheDir, "go.mod")); err != nil {
		return fmt.Errorf("staging go.mod: %w", err)
	}
	// The project's go.sum is optional; the compiler's and the runtime's
	// checksums are merged in either way. See stageGoSum.
	if err := stageGoSum(projectRoot, filepath.Join(cacheDir, "go.sum")); err != nil {
		return fmt.Errorf("staging go.sum: %w", err)
	}
	if err := writeHashRecord(cacheDir, want); err != nil {
		return err
	}
	return nil
}

// writeDiscoveredJSON serializes the discovered list to
// <cacheDir>/discovered.json — kept as a forensic artifact so a
// human staring at the cache can see what discovery turned up
// without re-running the scan. Not consumed by the hot path; the
// hash.json record is the authoritative invalidation input.
func writeDiscoveredJSON(cacheDir string, discovered []DiscoveredPackage) error {
	data, err := json.MarshalIndent(discovered, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(cacheDir, "discovered.json"), data, 0o644)
}
