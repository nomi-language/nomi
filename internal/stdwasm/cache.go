//go:build !js

package stdwasm

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"time"

	"github.com/nomi-language/nomi/internal/artifactcache"
	"github.com/tetratelabs/wazero"
)

// cacheDir is the directory under the artifact cache that holds compiled
// modules, one directory each, named by the SHA-256 of the module's bytes.
const cacheDir = "wasm"

// pruneEvery is how often a process prunes the compiled modules nothing
// has used for artifactcache.Unused.
const pruneEvery = 24 * time.Hour

// compilationCache is the wazero compilation cache of the module whose
// bytes hash to sum: its own directory under the artifact cache, which
// wazero fills with the machine code it compiles and reads back in a later
// process. It is nil when there is no artifact cache (NOMI_ARTIFACT_CACHE
// is off, or the target has no file system), and the module compiles in
// every process.
//
// One directory a module makes the cache prunable by the artifact cache's
// one rule: using the module touches its directory, and a prune removes
// the directories nothing has used for artifactcache.Unused.
func compilationCache(sum [sha256.Size]byte) wazero.CompilationCache {
	root := artifactcache.Root()
	if root == "" {
		return nil
	}
	parent := filepath.Join(root, cacheDir)
	dir := filepath.Join(parent, hex.EncodeToString(sum[:]))
	cache, err := wazero.NewCompilationCacheWithDir(dir)
	if err != nil {
		return nil
	}
	if info, err := os.Stat(dir); err == nil {
		artifactcache.Touch(dir, info)
	}
	prune(parent, filepath.Base(dir), false)
	return cache
}

// Prune removes now the compiled modules under root, the artifact cache,
// that nothing has used for artifactcache.Unused, and answers what it
// removed.
func Prune(root string) artifactcache.Pruned {
	return prune(filepath.Join(root, cacheDir), "", true)
}

// prune removes the modules' directories of parent other than keep that
// nothing has used for artifactcache.Unused, at most once per pruneEvery
// (artifactcache.Due) or now when force is set.
func prune(parent, keep string, force bool) artifactcache.Pruned {
	if !artifactcache.Due(parent, pruneEvery, force) {
		return artifactcache.Pruned{}
	}
	return artifactcache.Prune(parent, keep, artifactcache.Unused)
}
