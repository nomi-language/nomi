package ffirun

import (
	"bytes"
	"crypto/sha256"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestIdentity_VanishedInputIsNotFatal pins the behaviour that the first draft
// of this selector got wrong, caught by `go test ./...`: `stdcompilerrun`'s size
// guard creates a real `main.go` in a scratch directory INSIDE this module and
// removes it, and while it existed `nomi run` failed outright with
// "hashing compiler input ...: no such file or directory".
//
// A live source tree always has something transient in it — editor swap files,
// `go build` scratch dirs, a sibling test's probe. The identity may DIFFER
// because of one (an extra rebuild, the safe direction); it must never refuse to
// produce an identity at all.
func TestIdentity_VanishedInputIsNotFatal(t *testing.T) {
	dir := t.TempDir()
	present := filepath.Join(dir, "present.go")
	if err := os.WriteFile(present, []byte("package p\n"), 0o644); err != nil {
		t.Fatalf("write present.go: %v", err)
	}

	withPresent := sha256.New()
	if err := hashOneFile(withPresent, present, "present.go"); err != nil {
		t.Fatalf("hashing a present file: %v", err)
	}
	withGone := sha256.New()
	if err := hashOneFile(withGone, filepath.Join(dir, "gone.go"), "present.go"); err != nil {
		t.Fatalf("a file that vanished between selection and hashing must be folded "+
			"in as absent, not reported: %v", err)
	}
	if bytes.Equal(withPresent.Sum(nil), withGone.Sum(nil)) {
		t.Error("an absent input folded identically to a present one, so the " +
			"identity cannot tell a deleted compiler file from its contents")
	}

	// A read error that is NOT absence still has to surface: it means a build
	// input exists and cannot be read, which no rebuild will fix.
	unreadable := filepath.Join(dir, "unreadable")
	if err := os.MkdirAll(filepath.Join(unreadable, "child"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := hashOneFile(sha256.New(), unreadable, "unreadable"); err == nil {
		t.Error("hashing a directory as a file silently succeeded")
	}
}

// withCompilerIdentity substitutes a fixed compiler identity for the duration
// of the test. Two runs of `nomi` built from different source are two
// processes, which is why the production value is memoized and this seam
// exists: a test cannot rebuild itself mid-run.
func withCompilerIdentity(t *testing.T, id string) {
	t.Helper()
	prev := compilerIdentity
	t.Cleanup(func() { compilerIdentity = prev })
	compilerIdentity = func() (string, error) { return id, nil }
}

// TestCache_CompilerIdentityInvalidates checks the field that names the
// compiler in the cache key: the cached artifact is an ~18 MB wrapper BINARY
// linking `nomi/analysis` and the VM, so without it a warm cache would
// re-execute a binary built by a different compiler and report that
// compiler's answer.
//
// The test drives the HIT/MISS mechanism rather than inspecting the key. A
// sentinel is written at the cached wrapper binary's path; ensureWrapper
// removes that binary on any invalidation and leaves it untouched on a warm
// hit, so the sentinel's survival IS the hit/miss reading.
//
// Both directions are asserted, and the negative control is the load-bearing
// half: a key that changed on every invocation would pass the positive test
// while destroying the cache.
func TestCache_CompilerIdentityInvalidates(t *testing.T) {
	t.Setenv("NOMI_FFIRUN_CACHE_ROOT", t.TempDir())
	projectRoot := stageMinimalProject(t)
	cacheDir, err := cacheDirForProject(projectRoot)
	if err != nil {
		t.Fatalf("cacheDirForProject: %v", err)
	}
	discovered := makeDiscovered()
	requires := []string{"example.com/binding"}

	withCompilerIdentity(t, "sha256:compiler-A")
	if err := ensureWrapper(cacheDir, projectRoot, discovered, requires); err != nil {
		t.Fatalf("first ensureWrapper: %v", err)
	}

	sentinel := cachedWrapperBinaryPathForDir(cacheDir)
	writeSentinel := func() {
		t.Helper()
		if err := os.WriteFile(sentinel, []byte("wrapper built by compiler A"), 0o755); err != nil {
			t.Fatalf("write sentinel wrapper binary: %v", err)
		}
	}
	sentinelSurvives := func() bool {
		t.Helper()
		_, err := os.Stat(sentinel)
		if err != nil && !os.IsNotExist(err) {
			t.Fatalf("stat sentinel: %v", err)
		}
		return err == nil
	}

	// Negative control FIRST: the same compiler must still hit the cache.
	writeSentinel()
	if err := ensureWrapper(cacheDir, projectRoot, discovered, requires); err != nil {
		t.Fatalf("warm ensureWrapper: %v", err)
	}
	if !sentinelSurvives() {
		t.Fatal("cache MISSED for an unchanged compiler: the cached wrapper binary " +
			"was discarded. A key that changes every invocation is worse than no " +
			"key at all — every `nomi run` would relink an 18 MB binary.")
	}

	// Positive: a different compiler must miss.
	withCompilerIdentity(t, "sha256:compiler-B")
	if err := ensureWrapper(cacheDir, projectRoot, discovered, requires); err != nil {
		t.Fatalf("ensureWrapper after compiler change: %v", err)
	}
	if sentinelSurvives() {
		t.Fatal("cache HIT after the compiler changed: the wrapper binary built by " +
			"the previous compiler survived and would be re-executed, so a fixed " +
			"compiler reports the old answer and a defective one reports the fix")
	}

	// And it settles: the new identity is recorded, so the next run hits again.
	writeSentinel()
	if err := ensureWrapper(cacheDir, projectRoot, discovered, requires); err != nil {
		t.Fatalf("ensureWrapper after re-record: %v", err)
	}
	if !sentinelSurvives() {
		t.Fatal("cache did not settle: the new compiler identity was not recorded, " +
			"so every subsequent run misses")
	}
}

// stageIdentityTree writes a stand-in for the compiler module's tree: non-test
// Go source, a runtime package, a test file, an embedding package with an
// asset, and a file that is neither. Returns the module root.
func stageIdentityTree(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	compiler := filepath.Join(base, "compiler")
	writeAt := func(rel, content string) {
		t.Helper()
		path := filepath.Join(base, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	writeAt("compiler/go.mod", "module nomi\n\ngo 1.27.0\n")
	writeAt("compiler/analysis/check.go", "package analysis\n\nfunc Check() int { return 1 }\n")
	writeAt("compiler/analysis/check_test.go", "package analysis\n\nfunc unused() int { return 1 }\n")
	writeAt("compiler/std/std.go", "package std\n\nimport \"embed\"\n\n//go:embed *.nomi\nvar fs embed.FS\n")
	writeAt("compiler/std/io.nomi", "pub fn print(s: String)\n")
	writeAt("compiler/README.md", "not a build input\n")
	writeAt("compiler/rt/prelude.go", "package rt\n\ntype Maybe[T any] struct{}\n")
	return compiler
}

// TestIdentity_HashTracksLinkInputsOnly exercises the DERIVED function, not the
// seam: the hash must move for every input a wrapper links and stay put for
// everything else. The "stays put" rows are the ones that keep the cache useful;
// the "moves" rows are the ones that keep it honest.
func TestIdentity_HashTracksLinkInputsOnly(t *testing.T) {
	compiler := stageIdentityTree(t)
	roots := []string{compiler}
	base := filepath.Dir(compiler)

	hash := func(label string) string {
		t.Helper()
		h, err := hashCompilerTrees(roots)
		if err != nil {
			t.Fatalf("hashCompilerTrees (%s): %v", label, err)
		}
		if h == "" {
			t.Fatalf("hashCompilerTrees (%s) returned an empty identity", label)
		}
		return h
	}
	edit := func(rel, content string) {
		t.Helper()
		path := filepath.Join(base, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	start := hash("initial")
	if again := hash("recomputed"); again != start {
		t.Fatalf("identity is not deterministic across two reads of an unchanged "+
			"tree: %s then %s. A key that moves on its own is worse than a manual "+
			"tag, because nobody suspects it.", start, again)
	}

	cases := []struct {
		name   string
		rel    string
		body   string
		moves  bool
		reason string
	}{
		{
			name:   "non-test Go source",
			rel:    "compiler/analysis/check.go",
			body:   "package analysis\n\nfunc Check() int { return 2 }\n",
			moves:  true,
			reason: "the wrapper links this package's source through a local replace",
		},
		{
			name:   "module graph",
			rel:    "compiler/go.mod",
			body:   "module nomi\n\ngo 1.27.0\n\n// edited\n",
			moves:  true,
			reason: "the staged wrapper go.mod inherits the compiler's go directive",
		},
		{
			name:   "embedded asset",
			rel:    "compiler/std/io.nomi",
			body:   "pub fn print(s: String)\npub fn eprint(s: String)\n",
			moves:  true,
			reason: "the stdlib reaches the wrapper as //go:embed bytes, not as Go source",
		},
		{
			name:   "embedded asset in a new subdirectory",
			rel:    "compiler/std/extra/extra.nomi",
			body:   "pub fn get(url: String)\n",
			moves:  true,
			reason: "an `all:*` pattern reaches nested files, so the subtree is the unit",
		},
		{
			name:   "runtime package source",
			rel:    "compiler/rt/prelude.go",
			body:   "package rt\n\ntype Maybe[T any] struct{ Tag int }\n",
			moves:  true,
			reason: "rt is a package of the compiler module, which the wrapper links",
		},
		{
			name:   "Go test file",
			rel:    "compiler/analysis/check_test.go",
			body:   "package analysis\n\nfunc unused() int { return 99 }\n",
			moves:  false,
			reason: "Go never links a _test.go file into a non-test binary",
		},
		{
			name:   "a file that is not a build input",
			rel:    "compiler/README.md",
			body:   "still not a build input\n",
			moves:  false,
			reason: "docs churn on every commit here; invalidating on it would relink 18 MB for nothing",
		},
	}
	prev := start
	for _, tc := range cases {
		edit(tc.rel, tc.body)
		got := hash(tc.name)
		switch {
		case tc.moves && got == prev:
			t.Errorf("editing %s (%s) did NOT change the compiler identity — %s",
				tc.rel, tc.name, tc.reason)
		case !tc.moves && got != prev:
			t.Errorf("editing %s (%s) changed the compiler identity — %s",
				tc.rel, tc.name, tc.reason)
		}
		prev = got
	}
}

// TestIdentity_DefaultRootsSelectRealInputs is the guard against the failure
// mode a derived key has and a manual tag does not: silently selecting nothing.
// A selector that walked the wrong tree, or excluded everything, would produce a
// perfectly stable hash and no test would notice — the cache would be exactly as
// unsound as before, with a `compiler` field in hash.json claiming otherwise.
func TestIdentity_DefaultRootsSelectRealInputs(t *testing.T) {
	roots, err := compilerIdentityRoots()
	if err != nil {
		t.Fatalf("compilerIdentityRoots: %v", err)
	}
	if len(roots) != 1 {
		t.Fatalf("expected the compiler module root alone, got %v", roots)
	}
	if _, err := os.Stat(filepath.Join(roots[0], "go.mod")); err != nil {
		t.Fatalf("root %s has no go.mod: %v", roots[0], err)
	}

	compilerFiles, err := identityFiles(roots[0])
	if err != nil {
		t.Fatalf("identityFiles(%s): %v", roots[0], err)
	}

	// Named witnesses, one per selection rule, all in the real tree.
	for _, want := range []string{
		"go.mod",                       // module graph
		"internal/analysis/checker.go", // the analyzer, statically linked by the wrapper
		"internal/vm/vm.go",            // the VM, ditto
		"vmhost/vmhost.go",             // the embedding API the wrapper calls
		"internal/ffirun/cache.go",     // this file's own package
		"std/std.go",                   // the embedding package
		"std/io.nomi",                  // an embedded asset, reachable no other way
		"rt/rt.go",                     // the runtime library, a package of this module
	} {
		if !containsString(compilerFiles, want) {
			t.Errorf("compiler identity does not cover %s — %d files selected under %s",
				want, len(compilerFiles), roots[0])
		}
	}

	// A _test.go file outside an embedding package must NOT be selected. There
	// are 585 of them in this tree and none can reach a wrapper binary, so
	// selecting them would relink 18 MB on every test edit. The exception is a
	// _test.go inside an embedding package's subtree — `std/std_test.go` really
	// is embedded content, because `//go:embed all:*` matches it.
	for _, rel := range compilerFiles {
		if !strings.HasSuffix(rel, "_test.go") {
			continue
		}
		dir := filepath.Dir(filepath.Join(roots[0], filepath.FromSlash(rel)))
		embeds, err := dirHasEmbeddingGoFile(dir)
		if err != nil {
			t.Fatalf("scanning %s: %v", dir, err)
		}
		if !embeds {
			t.Errorf("compiler identity selected the test file %s, which no wrapper "+
				"can link and whose package embeds nothing", rel)
			break
		}
	}
}

// TestIdentity_EveryEmbedDirectiveIsCovered is the self-maintaining half of the
// selector. `std/**` reaches a wrapper as embedded bytes and is
// invisible to a `*.go` rule, so the selector detects embedding packages instead
// of naming them. This test asserts the detection actually holds across the
// whole tree: add a `//go:embed` anywhere in either module and its assets are
// covered without anyone remembering to extend a list.
func TestIdentity_EveryEmbedDirectiveIsCovered(t *testing.T) {
	roots, err := compilerIdentityRoots()
	if err != nil {
		t.Fatalf("compilerIdentityRoots: %v", err)
	}
	for _, root := range roots {
		selected, err := identityFiles(root)
		if err != nil {
			t.Fatalf("identityFiles(%s): %v", root, err)
		}
		index := make(map[string]bool, len(selected))
		for _, rel := range selected {
			index[rel] = true
		}
		var embedDirs []string
		err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if path != root && identitySkipDirs[d.Name()] {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(d.Name(), ".go") || strings.HasSuffix(d.Name(), "_test.go") {
				return nil
			}
			has, err := fileHasEmbedDirective(path)
			if err != nil {
				return err
			}
			if has {
				embedDirs = append(embedDirs, filepath.Dir(path))
			}
			return nil
		})
		if err != nil {
			t.Fatalf("scanning %s for embed directives: %v", root, err)
		}
		for _, dir := range embedDirs {
			err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if d.IsDir() {
					if path != dir && identitySkipDirs[d.Name()] {
						return filepath.SkipDir
					}
					return nil
				}
				if !d.Type().IsRegular() {
					return nil
				}
				rel, err := filepath.Rel(root, path)
				if err != nil {
					return err
				}
				if !index[filepath.ToSlash(rel)] {
					t.Errorf("%s carries a //go:embed directive but %s is not in the "+
						"compiler identity — an edit to it would serve a stale wrapper",
						dir, rel)
				}
				return nil
			})
			if err != nil {
				t.Fatalf("walking embed dir %s: %v", dir, err)
			}
		}
	}
}

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// dirHasEmbeddingGoFile reports whether dir, or any directory above it, holds a
// non-test Go file carrying a //go:embed directive — i.e. whether dir sits
// inside an embedded-asset subtree.
func dirHasEmbeddingGoFile(dir string) (bool, error) {
	for {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return false, err
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			has, err := fileHasEmbedDirective(filepath.Join(dir, e.Name()))
			if err != nil {
				return false, err
			}
			if has {
				return true, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false, nil
		}
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return false, nil
		}
		dir = parent
	}
}
