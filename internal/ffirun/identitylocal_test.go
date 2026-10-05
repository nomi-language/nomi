package ffirun

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// stageLocalIdentityProject stages an FFI project shaped like a real one and
// deliberately UNLIKE the in-tree fixtures. Three of its properties are load-
// bearing, and mutation testing found the first two AFTER a first draft passed
// every row while defending nothing:
//
//   - The replace target is OUTSIDE the project root (`../binding`). With the
//     binding at `./binding` the whole `replace` resolution is redundant: the
//     project-root walk already picks up the file by containment, so a mutant
//     that dropped replace targets entirely still passed.
//   - There is an EMBEDDING package (`assets/embedder.go`) with both an asset
//     and a `.nomi` beside it. Without a `//go:embed` in the project, no
//     `.nomi` is ever a selection candidate and the scope split is untested,
//     so a mutant that swept project `.nomi` into the key also passed.
//   - Go lives in the main module too (`app.go`), which the wrapper requires
//     and replaces back to the project root.
//
// Returns the project root; the sibling binding tree is at `../binding`.
func stageLocalIdentityProject(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "project")
	write := func(rel, content string) {
		t.Helper()
		path := filepath.Join(base, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	write("project/go.mod", "module localidentity\n\ngo 1.26.3\n\n"+
		"require example.com/binding v0.0.0\n\n"+
		"replace example.com/binding => ../binding\n")
	write("binding/go.mod", "module example.com/binding\n\ngo 1.26.3\n")
	write("binding/binding.go", "package binding\n\nimport \"strings\"\n\n"+
		"func EchoUpper(s string) string { return strings.ToUpper(s) }\n")
	write("project/app.go", "package main\n\nfunc helper() int { return 1 }\n")
	write("project/assets/embedder.go", "package assets\n\nimport \"embed\"\n\n"+
		"//go:embed *.txt\nvar fs embed.FS\n")
	write("project/assets/asset.txt", "an embedded asset\n")
	write("project/assets/query.nomi", "fn q() {}\n")
	write("project/main.nomi", "fn main() {\n  io.print(\"hello\")\n}\n")
	return root
}

// TestCache_LocalGoIdentityInvalidates is the regression test for the residue
// the Compiler field left behind. The cached artifact is an ~18 MB wrapper
// BINARY, and the Compiler field covers the module the COMPILER owns —
// but the same binary also links the user's own Go and every module the user
// reaches through a local `replace`, and nothing in the key named those.
//
// Observed on a project outside the compiler tree, with a binding
// whose body was `strings.ToUpper(s)`:
//
//	edit body to `strings.ToUpper(s) + "!"`, signature untouched
//	warm cache  -> FFI OK      (the PREVIOUS body)
//	cold cache  -> FFI OK!     (the current body)
//
// go.mod, go.sum, the template and the discovered set were all unchanged,
// because `Discovered` carries import paths and binding NAMES and not bodies.
//
// The test drives the HIT/MISS mechanism rather than inspecting the key, for
// the reason the compiler-side test gives: a sentinel at the cached wrapper
// binary's path survives a warm hit and is removed on any invalidation, so its
// survival IS the reading.
//
// The `.nomi` row is not decoration. `.nomi` sources are excluded from the
// whole key on purpose — the wrapper calls rt.LoadFile(entry), so a source edit
// must propagate WITHOUT an 18 MB relink. A project-side identity that swept
// `.nomi` in would pass every positive row here and quietly destroy that.
func TestCache_LocalGoIdentityInvalidates(t *testing.T) {
	t.Setenv("NOMI_FFIRUN_CACHE_ROOT", t.TempDir())
	withCompilerIdentity(t, "sha256:compiler-pinned")
	projectRoot := stageLocalIdentityProject(t)
	cacheDir, err := cacheDirForProject(projectRoot)
	if err != nil {
		t.Fatalf("cacheDirForProject: %v", err)
	}
	discovered := []DiscoveredPackage{{ImportPath: "example.com/binding", Alias: "binding"}}
	requires := []string{"example.com/binding"}

	if err := ensureWrapper(cacheDir, projectRoot, discovered, requires); err != nil {
		t.Fatalf("first ensureWrapper: %v", err)
	}

	sentinel := cachedWrapperBinaryPathForDir(cacheDir)
	rerun := func(label string) bool {
		t.Helper()
		if err := os.WriteFile(sentinel, []byte("wrapper built by the previous tree"), 0o755); err != nil {
			t.Fatalf("write sentinel (%s): %v", label, err)
		}
		if err := ensureWrapper(cacheDir, projectRoot, discovered, requires); err != nil {
			t.Fatalf("ensureWrapper (%s): %v", label, err)
		}
		_, err := os.Stat(sentinel)
		if err != nil && !os.IsNotExist(err) {
			t.Fatalf("stat sentinel (%s): %v", label, err)
		}
		return err == nil
	}
	edit := func(rel, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(projectRoot, rel), []byte(content), 0o644); err != nil {
			t.Fatalf("edit %s: %v", rel, err)
		}
	}

	// Negative control FIRST. A key that moved every invocation would pass
	// every row below while relinking 18 MB on each `nomi run`.
	if !rerun("unchanged") {
		t.Fatal("cache MISSED with nothing changed: the project identity is not " +
			"stable across runs, so every `nomi run` relinks an 18 MB binary")
	}

	// A local `replace` target's Go BODY, signature identical. This is the
	// measured defect: no checksum speaks for a locally replaced module, so
	// go.sum cannot catch it and Discovered does not look at bodies.
	edit("../binding/binding.go", "package binding\n\nimport \"strings\"\n\n"+
		"func EchoUpper(s string) string { return strings.ToUpper(s) + \"!\" }\n")
	if rerun("replace target body") {
		t.Fatal("cache HIT after a locally replaced module's Go body changed: the " +
			"wrapper built from the OLD body survived and would be re-executed, so " +
			"`nomi run` reports the previous answer for the user's own binding")
	}
	if !rerun("replace target body settles") {
		t.Fatal("cache did not settle after the binding edit: the new identity was " +
			"not recorded, so every subsequent run misses")
	}

	// The MAIN module's own Go. writeAbsolutizedGoMod requires and replaces the
	// project back to itself precisely so wrapper code can import it, so this
	// tree is linked too.
	edit("app.go", "package main\n\nfunc helper() int { return 2 }\n")
	if rerun("project-owned Go") {
		t.Fatal("cache HIT after the project's own Go changed: the main module is " +
			"required and replaced into the wrapper, so its source is a link input")
	}
	if !rerun("project-owned Go settles") {
		t.Fatal("cache did not settle after the project Go edit")
	}

	// A test file is not a link input. Go never links `*_test.go` into a
	// non-test binary, so selecting it would be over-inclusion with no payoff.
	edit("app_test.go", "package main\n\nfunc unusedProbe() int { return 99 }\n")
	if !rerun("test file") {
		t.Fatal("cache MISSED for a *_test.go edit: Go cannot link a test file " +
			"into the wrapper, so it must not be a key input")
	}

	// An EMBEDDED asset is a link input: `//go:embed` puts its bytes in the
	// binary, so a change to one changes the artifact with nothing else moving.
	edit("assets/asset.txt", "a different embedded asset\n")
	if rerun("embedded asset") {
		t.Fatal("cache HIT after an embedded asset changed: `//go:embed` copies " +
			"those bytes into the wrapper, so the old asset would be re-executed")
	}
	if !rerun("embedded asset settles") {
		t.Fatal("cache did not settle after the asset edit")
	}

	// And the fast path the whole key is built around. This `.nomi` sits INSIDE
	// the embedding package, so it is a selection candidate and the row
	// discriminates — a project scope that kept `.nomi` fails here. A first
	// draft put the file at the project root, where nothing could ever select
	// it, and the row passed while defending nothing.
	edit("assets/query.nomi", "fn q() { io.print(\"edited\") }\n")
	if !rerun("nomi source beside an embed directive") {
		t.Fatal("cache MISSED for a `.nomi` edit next to a `//go:embed`: the " +
			"wrapper calls rt.LoadFile(entry), so a source edit must propagate " +
			"without a Go rebuild. Sweeping project `.nomi` into the key trades " +
			"one stale-answer bug for an 18 MB relink on every source edit.")
	}
	edit("main.nomi", "fn main() {\n  io.print(\"edited\")\n}\n")
	if !rerun("nomi source at the project root") {
		t.Fatal("cache MISSED for a plain `.nomi` edit")
	}
}

// TestLocalIdentity_RootsMirrorTheStagedGoMod pins the ROOT SET, which is the
// half of the identity a content hash cannot defend: hashing the wrong trees
// perfectly is still the wrong answer. The set has to mirror
// writeAbsolutizedGoMod, which is what writes the `replace` lines the wrapper
// is actually built against.
func TestLocalIdentity_RootsMirrorTheStagedGoMod(t *testing.T) {
	projectRoot := stageLocalIdentityProject(t)
	roots, err := localIdentityRoots(projectRoot)
	if err != nil {
		t.Fatalf("localIdentityRoots: %v", err)
	}
	want := []string{
		filepath.Clean(projectRoot),
		filepath.Clean(filepath.Join(projectRoot, "..", "binding")),
	}
	slices.Sort(want)
	if !slices.Equal(roots, want) {
		t.Fatalf("root set mismatch\n got: %v\nwant: %v", roots, want)
	}

	// The compiler roots are the Compiler field's job. Hashing them here too
	// would walk them twice for an identical answer.
	nomiRoot, err := nomiModuleRoot()
	if err != nil {
		t.Fatalf("nomiModuleRoot: %v", err)
	}
	inTree := t.TempDir()
	goMod := "module intree\n\ngo 1.26.3\n\nrequire github.com/nomi-language/nomi v0.0.0\n\nreplace github.com/nomi-language/nomi => " +
		filepath.ToSlash(nomiRoot) + "\n"
	if err := os.WriteFile(filepath.Join(inTree, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	roots, err = localIdentityRoots(inTree)
	if err != nil {
		t.Fatalf("localIdentityRoots (in-tree): %v", err)
	}
	if slices.Contains(roots, filepath.Clean(nomiRoot)) {
		t.Errorf("the compiler root is hashed twice: %v", roots)
	}
	if !slices.Contains(roots, filepath.Clean(inTree)) {
		t.Errorf("the project root itself was dropped: %v", roots)
	}

	// A module-to-module replace carries a version and names no directory;
	// go.sum speaks for it. Reordering replace lines cannot change the
	// artifact, so it must not change the key either.
	reordered := t.TempDir()
	goMod = "module ordering\n\ngo 1.26.3\n\n" +
		"replace example.com/b => ./b\n" +
		"replace example.com/a => ./a\n" +
		"replace example.com/v => example.com/w v1.2.3\n"
	if err := os.WriteFile(filepath.Join(reordered, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	roots, err = localIdentityRoots(reordered)
	if err != nil {
		t.Fatalf("localIdentityRoots (ordering): %v", err)
	}
	wantOrdered := []string{
		filepath.Clean(reordered),
		filepath.Join(reordered, "a"),
		filepath.Join(reordered, "b"),
	}
	slices.Sort(wantOrdered)
	if !slices.Equal(roots, wantOrdered) {
		t.Fatalf("replace-order or version-replace handling wrong\n got: %v\nwant: %v",
			roots, wantOrdered)
	}
}

// TestLocalIdentity_NomiScopeSplit is the discriminating test for the one place
// the two scopes differ. The compiler's `std/**.nomi` is embedded INTO the
// binary — changing `Int.max_value` in `std/int.nomi` must not leave a warm
// cache printing the old number — so the
// compiler scope must select it. A project's `.nomi` is read at RUN time and
// must not be selected, or a neighbouring `//go:embed` in the user's Go would
// silently make every source edit cost an 18 MB relink.
//
// Both scopes still select the embedded ASSET, so this is not "the project
// scope ignores embeds".
func TestLocalIdentity_NomiScopeSplit(t *testing.T) {
	root := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	write("go.mod", "module scopesplit\n\ngo 1.26.3\n")
	write("pkg/embedder.go", "package pkg\n\nimport \"embed\"\n\n//go:embed *\nvar fs embed.FS\n")
	write("pkg/asset.txt", "an embedded asset\n")
	write("pkg/program.nomi", "fn main() {}\n")

	compilerSel, err := identityFilesScoped(root, compilerTreeScope)
	if err != nil {
		t.Fatalf("identityFilesScoped(compiler): %v", err)
	}
	projectSel, err := identityFilesScoped(root, projectTreeScope)
	if err != nil {
		t.Fatalf("identityFilesScoped(project): %v", err)
	}

	if !slices.Contains(compilerSel, "pkg/program.nomi") {
		t.Errorf("the compiler scope dropped an embedded `.nomi`: %v\n"+
			"that is `std/**.nomi`, which is linked into the binary and was "+
			"measured changing a warm cache's answer", compilerSel)
	}
	if slices.Contains(projectSel, "pkg/program.nomi") {
		t.Errorf("the project scope swept in a `.nomi`: %v\n"+
			"project sources are excluded from the key on purpose", projectSel)
	}
	for _, scope := range []struct {
		name  string
		files []string
	}{{"compiler", compilerSel}, {"project", projectSel}} {
		if !slices.Contains(scope.files, "pkg/asset.txt") {
			t.Errorf("%s scope dropped the embedded asset: %v", scope.name, scope.files)
		}
		if !slices.Contains(scope.files, "pkg/embedder.go") {
			t.Errorf("%s scope dropped the embedding Go file: %v", scope.name, scope.files)
		}
	}
}

// TestLocalIdentity_KeyFieldMovesAlone checks the ATTRIBUTION. The mechanism
// test above would pass if a user Go edit moved some other field by accident —
// a cache miss for the wrong reason still reads as a pass. This asserts that a
// Go body edit moves `Locals` and nothing else, so the field means what its
// doc says.
func TestLocalIdentity_KeyFieldMovesAlone(t *testing.T) {
	withCompilerIdentity(t, "sha256:compiler-pinned")
	projectRoot := stageLocalIdentityProject(t)
	discovered := []DiscoveredPackage{{ImportPath: "example.com/binding", Alias: "binding"}}
	requires := []string{"example.com/binding"}

	before, err := computeHashes(projectRoot, discovered, requires)
	if err != nil {
		t.Fatalf("computeHashes (before): %v", err)
	}
	if before.Locals == "" {
		t.Fatal("the Locals field is empty, so the project half of the key is absent")
	}

	binding := filepath.Join(projectRoot, "..", "binding", "binding.go")
	if err := os.WriteFile(binding, []byte("package binding\n\nimport \"strings\"\n\n"+
		"func EchoUpper(s string) string { return strings.ToUpper(s) + \"!\" }\n"), 0o644); err != nil {
		t.Fatalf("edit binding: %v", err)
	}
	after, err := computeHashes(projectRoot, discovered, requires)
	if err != nil {
		t.Fatalf("computeHashes (after): %v", err)
	}

	if after.Locals == before.Locals {
		t.Error("Locals did not move for a change to a locally replaced module's " +
			"Go body — the input this field exists to cover")
	}
	if after.GoMod != before.GoMod || after.GoSum != before.GoSum ||
		after.Template != before.Template || after.Discovered != before.Discovered ||
		after.Compiler != before.Compiler {
		t.Errorf("a field other than Locals moved for a Go body edit, so the miss "+
			"would be attributed to the wrong input\nbefore: %+v\nafter:  %+v",
			before, after)
	}
}

// TestLocalIdentity_AbsentReplaceTargetIsNotFatal covers the regression the
// first draft of this field caused, which the existing suite caught:
// `TestCache_AbsolutizesReplaces` stages a go.mod naming `./local-binding`
// that does not exist, and hashing that root failed `ensureWrapper`, so `nomi
// run` refused to run at all.
//
// A `replace` naming a directory the user has not created yet is an ordinary
// state, and the right answer is `go build`'s complaint, never a refusal from
// the cache key. identity.go already states the rule for a file that vanishes
// mid-walk — "the honest outcome is a DIFFERENT identity (one extra rebuild),
// never a refusal to run" — and a root is the same rule one level up.
//
// The second half is what makes the tolerance a marker rather than a skip:
// creating the directory has to MOVE the identity, or the wrapper built while
// it was missing would be reused once it appeared.
func TestLocalIdentity_AbsentReplaceTargetIsNotFatal(t *testing.T) {
	root := t.TempDir()
	goMod := "module absent\n\ngo 1.26.3\n\nrequire example.com/ghost v0.0.0\n\n" +
		"replace example.com/ghost => ./ghost\n"
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	missing, err := projectIdentity(root)
	if err != nil {
		t.Fatalf("projectIdentity refused a go.mod naming a directory that does "+
			"not exist, so `nomi run` cannot reach the `go build` that would "+
			"explain it: %v", err)
	}

	// The compiler scope must NOT tolerate this: a missing compiler root would
	// select nothing and produce a stable, meaningless identity.
	if _, err := hashCompilerTrees([]string{filepath.Join(root, "ghost")}); err == nil {
		t.Error("the compiler scope accepted a missing root, which would hash to " +
			"a stable value no matter what the tree later contained")
	}

	if err := os.MkdirAll(filepath.Join(root, "ghost"), 0o755); err != nil {
		t.Fatalf("mkdir ghost: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "ghost", "ghost.go"),
		[]byte("package ghost\n\nfunc Haunt() int { return 1 }\n"), 0o644); err != nil {
		t.Fatalf("write ghost.go: %v", err)
	}
	appeared, err := projectIdentity(root)
	if err != nil {
		t.Fatalf("projectIdentity after the directory appeared: %v", err)
	}
	if appeared == missing {
		t.Error("the identity did not move when the replace target appeared, so " +
			"the wrapper built while it was missing would be re-executed against " +
			"a module that now exists")
	}
}
