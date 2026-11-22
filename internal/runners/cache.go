package runners

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// CacheRootEnv lets tests override the cache root so we don't write
// into the user's ~/.cache/nomi during `go test`. Empty string means
// "use the user-cache default".
const CacheRootEnv = "NOMI_RUNNER_CACHE_ROOT"

// CacheDirForKey is the single funnel every runner cache directory is
// created through. It hashes
// key for the directory name, records compilerRoot and kind inside the
// directory, and sweeps the root.
//
// The order is load-bearing. The marker is written BEFORE the sweep,
// so this process's own entry carries a stamp of now and is
// protected by the grace window as well as by name. And the marker
// is written on every call including a warm hit, which is what makes
// its mtime a last-USE reading rather than a last-regeneration one —
// a warm hit otherwise writes nothing at all.
//
// A sweep failure is discarded deliberately: cache hygiene must
// never fail a build. The same goes for the marker — an entry
// without one is judged by the bound instead of by its root, which
// is how scanCacheEntries reads any unmarked entry.
func CacheDirForKey(key, compilerRoot, kind string) (string, error) {
	root, err := cacheRoot()
	if err != nil {
		return "", err
	}
	hash := cacheKeyHash(key)
	dir := filepath.Join(root, hash)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("creating cache dir %s: %w", dir, err)
	}
	_ = recordOrigin(dir, compilerRoot, kind)
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
// honoring the NOMI_RUNNER_CACHE_ROOT override.
//
// An in-process test that has NOT set the override is refused rather
// than served the user's real cache. A test that
// reached a build without it would key directories on a t.TempDir()
// the test deletes on the way out, and since a cache directory can be
// DELETED, the same omission would evict a developer's working set
// mid-test, so the class is closed here instead of being fixed one
// test at a time. `testing.Testing()` is false in a subprocess, so
// tests that exec the real `nomi` binary are unaffected (they pass
// the override in the child's environment anyway).
func cacheRoot() (string, error) {
	if override := os.Getenv(CacheRootEnv); override != "" {
		return override, nil
	}
	if testing.Testing() {
		return "", fmt.Errorf("%s is unset under `go test`; "+
			"set it to a t.TempDir() — a test must never read or evict the real build cache",
			CacheRootEnv)
	}
	uc, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("locating user cache dir: %w", err)
	}
	return filepath.Join(uc, "nomi", "builds"), nil
}
