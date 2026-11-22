package runners

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
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
const identityVersion = "runner-compiler-identity/1"

// identitySkipDirs are directories that hold no input to a runner build. They
// are named rather than pattern-matched because each is a specific thing: the
// two git directories this repository uses.
var identitySkipDirs = map[string]bool{
	".git":  true,
	".bare": true,
}

// CompilerIdentity is the COMPILER half of every runner's cache key: a
// content hash of the Go module a runner LINKS from source.
//
// Why the key needs it at all. The cached artifact in a build directory is a
// nomi-vm EXECUTABLE that statically links the VM. The rest of the key (the
// target and the variant) names nothing about the compiler, so without this
// a warm cache would hand out a runner built by a DIFFERENT compiler.
//
// Why the SOURCE TREE and not the running binary. A development build's
// runner is built from the source at the compiler's root. The running `nomi`
// binary is not that input and can be arbitrarily stale relative to it:
// editing the VM and running the old `nomi` still produces a runner
// containing the EDIT. Hashing the running executable would therefore key on
// the wrong thing and would also cost a large read per invocation.
//
// It is memoized for the process. The identity of a compiler cannot change
// while that compiler runs; `internal/irbuild` expresses the same fact as a
// compile-time `generatorTag` constant. See hashCompilerTrees for what this
// does NOT protect against.
//
// It is the content hash of the compiler module's tree, or for a versioned
// `nomi` the module version it links (versionedCompilerIdentity).
func CompilerIdentity() (string, error) { return compilerIdentity() }

var compilerIdentity = sync.OnceValues(func() (string, error) {
	if p := currentPlan(); p.versioned() {
		return versionedCompilerIdentity(p.Version), nil
	}
	roots, err := compilerIdentityRoots()
	if err != nil {
		return "", err
	}
	return hashCompilerTrees(roots)
})

// compilerIdentityRoots is the module root a runner is built from. It fails
// exactly when the build was going to fail anyway: a source that is not
// there cannot be built.
func compilerIdentityRoots() ([]string, error) {
	src, err := ResolveCompilerSource()
	if err != nil {
		return nil, err
	}
	return []string{src.Dir}, nil
}

// versionedCompilerIdentity is the identity of a runner built from the
// compiler module at version. A published version's content is fixed (the
// checksum database holds every proxy to it), so the version stands for the
// tree. Leaving the module's hash out keeps a warm run from invoking `go` at
// all, and lets it run after the module cache is cleaned.
func versionedCompilerIdentity(version string) string {
	sum := sha256.Sum256([]byte(identityVersion + "\x00module\x00" + CompilerModulePath + "@" + version +
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
//     covers `std/**`, which reaches a runner as embedded bytes and
//     not as Go source at all.
//
// The roots are included because each runner's cache directory is keyed by
// and records the tree it was built from: two worktrees with byte-identical
// compiler content keep a runner each, and a sweep removes one whose tree is
// gone (evict.go).
//
// WHAT THIS DOES NOT PROTECT AGAINST — read this before trusting it:
//
//   - The Go BUILD CACHE and the module cache. A dependency resolved from
//     `GOMODCACHE` or a `replace` pointing outside this root is not
//     hashed; only the compiler's go.sum speaks for it.
//   - Editing the compiler tree WHILE a run is in flight. The identity is
//     computed once per process, so a mid-run edit is not observed. That is a
//     torn build in any case, and it is the same guarantee irbuild's constant
//     `generatorTag` gives.
//   - Anything the runner reads at RUN time rather than link time: the
//     program's image, which `nomi build` appends to it.
//   - Build INPUTS that are not files: `GOFLAGS`, `GOEXPERIMENT`, `CGO_ENABLED`,
//     `-tags`. `runtime.Version()` names the toolchain and nothing more.
//   - A change that produces byte-identical source in the root. By
//     construction that change cannot alter the artifact.
//
// The one selection heuristic is the embed detector: it is a line-anchored
// substring scan, not a parse, so a `//go:embed` line appearing inside a STRING
// LITERAL reads as a directive, and that package's directory subtree is
// hashed too. Over-inclusion costs a rebuild; under-inclusion costs a wrong
// answer, so the scan is deliberately the loose one.
//
// The hash is the version, the Go toolchain, then every selected file under
// every root.
func hashCompilerTrees(roots []string) (string, error) {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00", identityVersion, runtime.Version())
	for _, root := range roots {
		fmt.Fprintf(h, "root\x00%s\x00", filepath.ToSlash(root))
		rels, err := identityFiles(root)
		if err != nil {
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
		return fmt.Errorf("hashing compiler input %s: %w", path, err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("hashing compiler input %s: %w", path, err)
	}
	fmt.Fprintf(h, "file\x00%s\x00%d\x00", rel, info.Size())
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("hashing compiler input %s: %w", path, err)
	}
	return nil
}

// identityFiles returns the slash-separated paths, relative to root and sorted,
// of every file the identity selects. Sorting is what makes the hash
// independent of directory-walk order across filesystems.
func identityFiles(root string) ([]string, error) {
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
		return nil, fmt.Errorf("scanning tree %s: %w", root, err)
	}
	for _, dir := range embedDirs {
		if err := addEmbedSubtree(root, dir, selected); err != nil {
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
// including patterns added later, which is the point. The compiler's
// `std/**.nomi` is embedded INTO the binary and was measured changing a warm
// cache's answer, so `.nomi` files are selected too.
func addEmbedSubtree(root, dir string, selected map[string]bool) error {
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
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		selected[filepath.ToSlash(rel)] = true
		return nil
	})
	if err != nil {
		return fmt.Errorf("scanning embedded assets under %s: %w", dir, err)
	}
	return nil
}

// fileHasEmbedDirective reports whether path contains a line beginning
// `//go:embed`. See hashCompilerTrees for why this scan is deliberately loose.
func fileHasEmbedDirective(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("reading %s: %w", path, err)
	}
	const directive = "//go:embed"
	if strings.HasPrefix(string(data), directive) {
		return true, nil
	}
	return strings.Contains(string(data), "\n"+directive), nil
}
