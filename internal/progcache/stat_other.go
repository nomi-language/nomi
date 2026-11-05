//go:build !(darwin || freebsd || netbsd || openbsd || linux)

package progcache

import "os"

// changeTime answers nothing where the platform's stat has no change time
// Go exposes (Windows, js/wasm, wasip1): an entry is then trusted on its
// size and modification time alone, outside the racy window.
func changeTime(os.FileInfo) (int64, uint64) { return 0, 0 }
