package irbuild

import (
	"sync"
	"sync/atomic"

	"github.com/nomi-language/nomi/std"
)

// perStd is sync.OnceValue for a value worked out from the shared stdlib
// (std.Shared), which grows as programs reach its modules: f runs again
// once the stdlib has grown since it last ran, so an answer about a module
// built later (a spec row's declaration, a listed module's nodes) is not
// the one given before it was built. A program reaches only modules the
// stdlib has built, so the answers it reads are the ones a stdlib built
// whole gives.
func perStd[T any](f func() T) func() T {
	type answer struct {
		lib *std.StdLib
		v   T
	}
	var cur atomic.Pointer[answer]
	var mu sync.Mutex
	return func() T {
		lib := std.Shared()
		if a := cur.Load(); a != nil && a.lib == lib {
			return a.v
		}
		mu.Lock()
		defer mu.Unlock()
		if a := cur.Load(); a != nil && a.lib == lib {
			return a.v
		}
		a := &answer{lib: lib, v: f()}
		cur.Store(a)
		return a.v
	}
}
