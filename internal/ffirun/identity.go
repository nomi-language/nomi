package ffirun

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
)

// identityVersion prefixes the compiler-identity hash so a change to the
// SCHEME below (what is selected, in what order) is itself a change of
// identity. Without it, a bug fix to the selector would leave every cache dir
// on disk claiming an identity computed by the old selector.
const identityVersion = "ffirun-compiler-identity/1"

// identitySkipDirs are directories that hold no input to a wrapper build. They
// are named rather than pattern-matched because each is a specific thing: the
// two git directories this repository uses.
var identitySkipDirs = map[string]bool{
	".git":  true,
	".bare": true,
}

// compilerIdentity is the COMPILER half of the FFI-run cache key: a content
// hash of the Go module a generated wrapper LINKS from source.
//
// Why the key needs it at all. The cached artifact in a build directory is not
// just the generated `main.go` — it is `nomi-ffi-wrapper`, an ~18 MB
// EXECUTABLE that statically links `nomi/analysis` and the VM. The rest of
// the key (the project's go.mod / go.sum, the wrapper template, the discovered
// binding set) names nothing about the compiler, so without this a warm cache
// would re-execute a binary built by a DIFFERENT compiler and report that
// compiler's answer.
//
// Why the SOURCE TREE and not the running binary. A development build's
// wrapper go.mod says `replace github.com/nomi-language/nomi => <tree>` (see
// prepareWrapperModule), so what gets compiled into the artifact is the SOURCE
// at that root. The running `nomi` binary is
// not that input and can be arbitrarily stale relative to it — editing the
// analyzer and running the old `nomi` still produces a wrapper containing the
// EDIT. Hashing the running executable would therefore key on the wrong thing
// and would also cost an 18 MB read per invocation.
//
// It is memoized for the process. The identity of a compiler cannot change
// while that compiler runs; `internal/irbuild` expresses the same fact as a
// compile-time `generatorTag` constant. See hashCompilerTrees for what this
// does NOT protect against.
//
// Declared as a var so a test can substitute a known identity and drive the
// hit/miss mechanism without mutating the repository it is running from.
var compilerIdentity = defaultCompilerIdentity

// defaultCompilerIdentity is compilerIdentity's production value: the memoized
// content hash of the compiler module's tree, or for a versioned `nomi` the
// module version it links (versionedCompilerIdentity).
var defaultCompilerIdentity = sync.OnceValues(func() (string, error) {
	if p := currentPlan(); p.versioned() {
		return versionedCompilerIdentity(p.Version), nil
	}
	roots, err := compilerIdentityRoots()
	if err != nil {
		return "", err
	}
	return hashCompilerTrees(roots)
})

// compilerIdentityRoots is the module root a wrapper links under a local
// `replace`. It fails exactly when the wrapper path was going to fail anyway:
// a staged go.mod naming a tree that is not there cannot be built.
func compilerIdentityRoots() ([]string, error) {
	src, err := resolveCompilerSource()
	if err != nil {
		return nil, err
	}
	return []string{src.Dir}, nil
}

// versionedCompilerIdentity is the identity of a wrapper that requires the
// compiler module at version. A published version's content is fixed (the
// checksum database holds every proxy to it), so the version stands for the
// tree. Leaving the module's hash out keeps a warm run from invoking `go` at
// all, and lets it run after the module cache is cleaned.
func versionedCompilerIdentity(version string) string {
	sum := sha256.Sum256([]byte(identityVersion + "\x00module\x00" + compilerModulePath + "@" + version +
		"\x00" + runtime.Version()))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// hashCompilerTrees hashes every build input under roots, in a deterministic
// order, together with the Go toolchain version and the roots themselves.
//
// Selection is by LANGUAGE RULE rather than by a curated file list, so it
// cannot go stale the way a hand-maintained list does:
//
//   - `go.mod` / `go.sum` at any depth — module graph and checksums.
//   - `*.go` excluding `*_test.go` — Go never links a test file into a
//     non-test binary, so excluding them is a rule, not a guess.
//   - every file under the directory of any Go file that carries a
//     `//go:embed` directive. `//go:embed` patterns cannot escape their own
//     directory, so the directory subtree is a sound superset. This is what
//     covers `std/**`, which reaches a wrapper as embedded bytes and
//     not as Go source at all.
//
// The roots are included because a staged go.mod names them literally: two
// worktrees with byte-identical compiler content still produce wrappers whose
// `replace` lines point at different trees, and reusing one for the other would
// leave a cache dir depending on a directory that may be deleted.
//
// WHAT THIS DOES NOT PROTECT AGAINST — read this before trusting it:
//
//   - The Go BUILD CACHE and the module cache. A dependency resolved from
//     `GOMODCACHE` or a `replace` pointing outside this root is not
//     hashed; only the project's own go.sum speaks for it.
//   - Editing the compiler tree WHILE a run is in flight. The identity is
//     computed once per process, so a mid-run edit is not observed. That is a
//     torn build in any case, and it is the same guarantee irbuild's constant
//     `generatorTag` gives.
//   - Anything the wrapper reads at RUN time rather than link time. The
//     project's `.nomi` sources are deliberately excluded from the whole key
//     because the wrapper calls `rt.LoadFile(entry)`; that exclusion is
//     unchanged and still correct.
//   - Build INPUTS that are not files: `GOFLAGS`, `GOEXPERIMENT`, `CGO_ENABLED`,
//     `-tags`. `runtime.Version()` names the toolchain and nothing more.
//   - A change that produces byte-identical source in the root. By
//     construction that change cannot alter the artifact.
//
// The one selection heuristic is the embed detector: it is a line-anchored
// substring scan, not a parse, so a `//go:embed` line appearing inside a STRING
// LITERAL reads as a directive. `codegen.go`'s wrapper template contains
// exactly that, and the consequence is that `internal/ffirun`'s own directory
// subtree is hashed too. Over-inclusion costs a rebuild; under-inclusion costs
// a wrong answer, so the scan is deliberately the loose one.
func hashCompilerTrees(roots []string) (string, error) {
	return hashTrees(identityVersion, roots, compilerTreeScope)
}

// identityScope is the per-tree policy. Its two fields travel together — both
// differ between the compiler's trees and a project's, and both are about the
// same question, which files of a tree are LINKED — so they are one value
// rather than two booleans threaded separately through three functions.
type identityScope struct {
	// excludeNomiSources drops `.nomi` files from the embed-subtree rule. The
	// compiler's `std/**.nomi` is embedded INTO the binary and was measured
	// changing a warm cache's answer, so the compiler keeps them. A project's
	// `.nomi` is read at RUN time by rt.LoadFile and is absent from the whole
	// key on purpose, so a project drops them.
	excludeNomiSources bool
	// missingRootIsAbsent folds a root that does not exist in as ABSENT instead
	// of failing. A missing COMPILER root means the wrapper build was going to
	// fail anyway, and silently selecting nothing there would produce a stable,
	// meaningless identity — the one failure a derived key must never have. A
	// missing PROJECT root is ordinary: a `replace` may name a directory the
	// user has not created yet, and `TestCache_AbsolutizesReplaces` stages
	// exactly that. `nomi run` must reach `go build` and let it say so, never
	// refuse because a key input is unavailable.
	missingRootIsAbsent bool
}

var (
	compilerTreeScope = identityScope{}
	projectTreeScope  = identityScope{excludeNomiSources: true, missingRootIsAbsent: true}
)

// hashTrees is the engine both halves of the key share: version, Go toolchain,
// then every selected file under every root. It is split out so the compiler
// half and the project half cannot drift into two selection rules that
// disagree about what a build input is.
func hashTrees(version string, roots []string, scope identityScope) (string, error) {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00", version, runtime.Version())
	for _, root := range roots {
		fmt.Fprintf(h, "root\x00%s\x00", filepath.ToSlash(root))
		rels, err := identityFilesScoped(root, scope)
		if err != nil {
			if scope.missingRootIsAbsent && errors.Is(err, fs.ErrNotExist) {
				// A distinct marker, not a skip: the day the directory appears,
				// its content has to move the identity and force the relink.
				fmt.Fprint(h, "rootgone\x00")
				continue
			}
			return "", err
		}
		for _, rel := range rels {
			if err := hashOneFile(h, filepath.Join(root, filepath.FromSlash(rel)), rel); err != nil {
				return "", err
			}
		}
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// hashOneFile folds one file's path, length and content into h. The length is
// written before the bytes so two files cannot be confused for one longer file
// with the same concatenation.
//
// A file that VANISHED between selection and hashing is folded in as absent
// rather than reported as an error. Something is always writing transient files
// into a live source tree — an editor's swap file, a `go build` scratch
// directory, `stdcompilerrun`'s own size probe, which is a real `main.go`
// created and removed inside this module while other packages' tests run. The
// first draft of this function errored instead, and `nomi run` failed outright
// for the duration of that probe. A transient file makes the tree's content
// genuinely ambiguous, so the honest outcome is a DIFFERENT identity (one extra
// rebuild), never a refusal to run.
func hashOneFile(h io.Writer, path, rel string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintf(h, "gone\x00%s\x00", rel)
			return nil
		}
		return fmt.Errorf("ffirun: hashing compiler input %s: %w", path, err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("ffirun: hashing compiler input %s: %w", path, err)
	}
	fmt.Fprintf(h, "file\x00%s\x00%d\x00", rel, info.Size())
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("ffirun: hashing compiler input %s: %w", path, err)
	}
	return nil
}

// identityFiles returns the slash-separated paths, relative to root and sorted,
// of every file the COMPILER scope selects. Sorting is what makes the hash
// independent of directory-walk order across filesystems.
func identityFiles(root string) ([]string, error) {
	return identityFilesScoped(root, compilerTreeScope)
}

// identityFilesScoped is identityFiles under an explicit scope. See
// identityScope for what the two differ about.
func identityFilesScoped(root string, scope identityScope) ([]string, error) {
	selected := map[string]bool{}
	var embedDirs []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			// Same race as hashOneFile's: something removed mid-walk is skipped,
			// not fatal. The ROOT itself is exempt — a missing root would
			// silently select nothing and produce a stable, meaningless
			// identity, which is the one failure a derived key must never have.
			if os.IsNotExist(err) && path != root {
				return nil
			}
			return err
		}
		if d.IsDir() {
			if path != root && identitySkipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		name := d.Name()
		switch {
		case name == "go.mod" || name == "go.sum":
		case strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go"):
			if embeds, err := fileHasEmbedDirective(path); err != nil {
				if os.IsNotExist(err) {
					return nil
				}
				return err
			} else if embeds {
				embedDirs = append(embedDirs, filepath.Dir(path))
			}
		default:
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		selected[filepath.ToSlash(rel)] = true
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("ffirun: scanning tree %s: %w", root, err)
	}
	for _, dir := range embedDirs {
		if err := addEmbedSubtree(root, dir, selected, scope); err != nil {
			return nil, err
		}
	}
	out := make([]string, 0, len(selected))
	for rel := range selected {
		out = append(out, rel)
	}
	sort.Strings(out)
	return out, nil
}

// addEmbedSubtree selects every regular file under dir. A `//go:embed` pattern
// is resolved relative to the containing package's directory and may not
// contain `..`, so the subtree is a superset of whatever the directive names —
// including patterns added later, which is the point.
//
// The scope's excludeNomiSources drops `.nomi` files. Under a PROJECT root this
// rule is the only way a `.nomi` file could enter the key at all, and project
// `.nomi` is excluded on purpose so editing one does not force an 18 MB relink.
func addEmbedSubtree(root, dir string, selected map[string]bool, scope identityScope) error {
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
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
		if scope.excludeNomiSources && strings.HasSuffix(d.Name(), ".nomi") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		selected[filepath.ToSlash(rel)] = true
		return nil
	})
	if err != nil {
		return fmt.Errorf("ffirun: scanning embedded assets under %s: %w", dir, err)
	}
	return nil
}

// fileHasEmbedDirective reports whether path contains a line beginning
// `//go:embed`. See hashCompilerTrees for why this scan is deliberately loose.
func fileHasEmbedDirective(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("ffirun: reading %s: %w", path, err)
	}
	const directive = "//go:embed"
	if strings.HasPrefix(string(data), directive) {
		return true, nil
	}
	return strings.Contains(string(data), "\n"+directive), nil
}
