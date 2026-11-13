package analysis

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	goruntime "runtime"
	"strings"

	"github.com/nomi-language/nomi/internal/stdcache"
)

// StdlibPath returns the absolute filesystem path of the bundled
// stdlib directory — the source of `std/...` imports. Stdlib is virtually injected into Project.ModuleIndex
// under the short-name "std" using this path so the regular
// discovery/buildModule pipeline walks stdlib files just like any
// other cross-module dependency.
//
// Resolution chain (first existing path wins):
//
//  1. NOMI_STD_PATH env var — explicit test/embedder override. Set
//     unconditionally in test setup so go test's temp binary location
//     never has to be searched against.
//
//  2. os.Executable() + "/../std" — installed layout: binary lives
//     somewhere like <prefix>/bin/nomi with stdlib at
//     <prefix>/share/nomi/std, or a sibling-of-binary layout where
//     the launcher script is one level above the std/ dir.
//
//  3. os.Executable() + "/std" — sibling-of-binary layout: matches
//     <install>/libexec/nomi/{nomi,std/}.
//
//  4. runtime.Caller compile-time source location + "/../std" — last
//     resort for `go test` and `go run` contexts where the binary
//     lives in a temp dir with no sibling std/. Works because the
//     compiled Go binary embeds the source file path; tests can
//     always reach the in-tree stdlib without an env-var dance.
//
// Returns an error rather than panicking so callers (BuildProjectWithCache)
// can attach the failure as a TypeError on the entry FA instead of taking
// the whole process down — the LSP serves diagnostics from a long-lived
// process where a stdlib-misconfiguration panic would silently kill
// completion/hover for the rest of the session.
func StdlibPath() (string, error) {
	tried := []string{}

	if p := os.Getenv("NOMI_STD_PATH"); p != "" {
		tried = append(tried, p+" (NOMI_STD_PATH)")
		if isDir(p) {
			return p, nil
		}
	}

	if exe, err := os.Executable(); err == nil {
		// Resolve symlinks once so /usr/local/bin/nomi -> Cellar/...
		// (Homebrew, Nix-style profiles) still finds its sibling std/.
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
		dir := filepath.Dir(exe)
		// <bindir>/../std — installed layout with std/ as a sibling of
		// the bin/ dir (e.g. /usr/local/{bin,share/nomi/std}).
		cand := filepath.Join(dir, "..", "..", "std")
		tried = append(tried, cand+" (exe/../std)")
		if isDir(cand) {
			abs, _ := filepath.Abs(cand)
			return abs, nil
		}
		// <bindir>/std — sibling-of-binary layout
		// (<install>/libexec/nomi/{nomi,std/}).
		cand = filepath.Join(dir, "std")
		tried = append(tried, cand+" (exe/std)")
		if isDir(cand) {
			abs, _ := filepath.Abs(cand)
			return abs, nil
		}
	}

	// In-tree fallback — works for `go test`, `go run`, and any other
	// context where os.Executable() points at a temp binary with no
	// sibling std/. runtime.Caller(0) is this file's source path
	// (...internal/analysis/stdlib_path.go); ../../std reaches the
	// bundled stdlib directory in the repo layout.
	if _, file, _, ok := goruntime.Caller(0); ok {
		cand := filepath.Join(filepath.Dir(file), "..", "..", "std")
		tried = append(tried, cand+" (runtime.Caller)")
		if isDir(cand) {
			abs, _ := filepath.Abs(cand)
			return abs, nil
		}
	}

	return "", fmt.Errorf("could not locate stdlib directory; tried: %v", tried)
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// StdlibLoadOrder is the canonical dependency order for root stdlib modules.
// Nested source paths are discovered from the embedded tree by std.Load and
// from the project discovery walk; project_build.go places discovered nested
// files before their root facade while retaining this order for roots.
//
// Two call sites consume it:
//
//  1. std.Load() — uses this order for embedded root modules, then appends
//     recursively discovered nested source names (it prepends nothing).
//  2. project_build.go — orders the Sweep A/B/C build of stdlib files that
//     flowed in through discovery, prepending "std/" to each root name and
//     inserting discovered nested files before their root.
//
// Keeping the root list here (rather than in std.go) lets both sites import
// it without an import cycle (std already imports analysis; the inverse
// would close one). "prelude" is intentionally NOT included — std.Load()
// loads it separately as the last step (see Load()'s tail), and the
// project-build path explicitly handles it after iterating this slice.
var StdlibLoadOrder = []string{
	"display", "debug", "equatable", "hashable", "comparable",
	"add", "subtract", "multiply", "divide", "remainder",
	"unit", "bool", "maybe", "results", "resources",
	"literals", "assertions", "testing",
	"discrete", "steppable",
	"int", "float", "decimal", "code_points",
	"iter", "lists", "vectors", "bytes", "strings", "regex", "maps", "sets", "ranges",
	"io", "duration", "instant", "timer", "context", "structs", "type",
	"compiler", "startup", "random", "toml",
	"calendar", "locale", "plural", "localize", "phrase/format",
	"dynamic",
	"json", "uuid", "reverse", "presence", "phrase",
	"channels", "tasks", "supervisors",
}

// StdlibLogicalModuleName maps a stdlib source path, relative to the stdlib
// root and with or without its `.nomi` suffix, onto the module name a program
// imports it under: `json.nomi` becomes `json`, and `storage/testing.nomi`
// becomes `storage/testing` (std::storage::testing).
//
// The stdlib is ONE Nomi module and every module's name is its path, so
// this is the suffix and nothing else. It stays a shared
// function because `std` and `internal/frontend` both derive the module key
// from a physical path and the two disagreeing is not a cosmetic defect: two
// derivations that disagree load ONE file under TWO module keys, so every
// type in it has two declarations and fails with a mismatch naming the one
// file under both keys (`expected std/calendar.Date, got <other key>.Date`).
// One caller cannot drift from the other
// while there is one rule here.
func StdlibLogicalModuleName(rel string) string {
	return strings.TrimSuffix(filepath.ToSlash(rel), ".nomi")
}

// stdlibModuleForPath answers "is this .nomi file a stdlib source, and which
// logical module is it the source of" — one question, asked at three sites that
// had three copies of the answer: DocumentManager.isStdlibFile (which branch
// the LSP analyzes an open document on), originForStandalonePath (the Origin
// stamped on the declarations of a single-file analysis) and isStdlibRecording
// (whether a missing-impl diagnostic is suppressed). Each used to carry a
// comment telling the next editor to update the other two.
//
// The rule is that the file's directory is the stdlib's root: `std`, or a std
// version directory (internal/stdcache.IsVersion) whose parent is `std`; or
// that its directory is a parent module's, `<parent>/`, not `_`-prefixed,
// directly under such a root. Every public module is a `std/<name>.nomi` or
// a module nested under its parent, `std/<parent>/<name>.nomi`, whose module
// name is its path (`storage/testing`). It holds for all three physical
// shapes that reach here — the in-repo source (`std/x.nomi`), the jump-to-def
// materialization (`~/.cache/nomi/std/<version>/x.nomi`), and the key-shaped
// paths callers synthesize (`std/x.nomi`) — and it answers NO for
// `std/_fixtures/nested/deeper/module.nomi`, which std.Load does not carry
// either.
//
// A module's name must be its path under std. It was not once, and the cost
// was visible: while `std/calendar` shipped its facade at
// `std/calendar/calendar.nomi` under the name `calendar`, the open
// document's own `Date` was stamped OriginEntry while the copy loaded as its
// own import carried `std/calendar`, so the file reported 46 diagnostics
// against itself (`equality type mismatch: Date vs Date`).
func stdlibModuleForPath(filePath string) (string, bool) {
	if filePath == "" {
		return "", false
	}
	slashed := path.Clean(filepath.ToSlash(filePath))
	dir, base := path.Split(slashed)
	// A directory handed in by mistake must not read as a module called "" —
	// the file's own name is never a candidate for the `std` component, which
	// is why this splits before comparing rather than scanning components.
	parent := path.Clean(dir)
	name := StdlibLogicalModuleName(base)
	if name == "" || name == base {
		return "", false
	}
	if isStdlibRootDir(parent) {
		return name, true
	}
	if p := path.Base(parent); nestedStdlibParent(parent, p, name) {
		return p + "/" + name, true
	}
	return "", false
}

// nestedStdlibParent reports whether dir, named p, holds nested stdlib
// modules such as name: it sits directly under a stdlib root, is not
// `_`-prefixed (`_fixtures`), is not a facade's directory (a module named
// as its directory, `calendar/calendar.nomi`), and, for a path on disk,
// has its parent module's file beside it (`storage.nomi` beside
// `storage/`), so a project under a directory called `std` is not read as
// the stdlib. A key-shaped relative path (`std/storage/testing.nomi`)
// names no file and is taken as written.
func nestedStdlibParent(dir, p, name string) bool {
	if p == "" || p == "." || p == "/" || strings.HasPrefix(p, "_") || p == name || !isStdlibRootDir(path.Dir(dir)) {
		return false
	}
	if !path.IsAbs(dir) && !filepath.IsAbs(filepath.FromSlash(dir)) {
		return true
	}
	info, err := os.Stat(filepath.FromSlash(dir + ".nomi"))
	return err == nil && !info.IsDir()
}

// isStdlibRootDir reports whether dir is a stdlib root: a directory named
// `std`, or a std version directory under one, as the jump-to-def
// materialization keeps them (std/<version>/, internal/stdcache).
func isStdlibRootDir(dir string) bool {
	if path.Base(dir) == "std" {
		return true
	}
	return stdcache.IsVersion(path.Base(dir)) && path.Base(path.Dir(dir)) == "std"
}
