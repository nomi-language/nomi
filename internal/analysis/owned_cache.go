package analysis

import (
	"runtime"
	"sync"
	"weak"
)

// ownedCache is a process-wide cache of an answer computed from one object,
// an entry of which lives as long as the object it is keyed by and no
// longer. A process checks many programs and may build the stdlib more than
// once, and an entry keyed by the plain pointer would keep its key, and all
// the key reaches, for the life of the process.
//
// The key is held weakly and a cleanup on it deletes the entry once it is
// collected. The value is held strongly, so it must not reach its own key,
// or the entry would keep the key alive and never be deleted.
type ownedCache[K, V any] struct {
	m sync.Map // weak.Pointer[K] -> V
}

func (c *ownedCache[K, V]) load(k *K) (V, bool) {
	v, ok := c.m.Load(weak.Make(k))
	if !ok {
		var zero V
		return zero, false
	}
	return v.(V), true
}

// set records v for k, replacing any answer there.
func (c *ownedCache[K, V]) set(k *K, v V) {
	w := weak.Make(k)
	if _, loaded := c.m.Swap(w, v); !loaded {
		runtime.AddCleanup(k, func(w weak.Pointer[K]) { c.m.Delete(w) }, w)
	}
}

// store records v for k unless an answer is there already, and answers the
// one the cache holds.
func (c *ownedCache[K, V]) store(k *K, v V) V {
	w := weak.Make(k)
	got, loaded := c.m.LoadOrStore(w, v)
	if !loaded {
		runtime.AddCleanup(k, func(w weak.Pointer[K]) { c.m.Delete(w) }, w)
	}
	return got.(V)
}
