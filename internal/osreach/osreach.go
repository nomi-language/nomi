// Package osreach counts, per capability class, the host function calls
// that went on to the operating system: a process started, a socket
// listened on or dialed, a name looked up. A classed crossing a fake
// answers (std::process::testing's, installed on the frame) never gets this far,
// so the count tells a call that needed the class from one that only could
// have. cmd/nomi-docgen reads it to keep a stdlib reference example live
// in the browser when its run reaches no such call (promptClassifier).
//
// The class names are internal/stdlibbindings' (`connect`, `listen`,
// `processes`), which cannot be imported here: that package imports the
// ones that call Note.
package osreach

import (
	"sync"
	"sync/atomic"
)

var counts sync.Map // class -> *atomic.Int64

func counter(class string) *atomic.Int64 {
	if c, ok := counts.Load(class); ok {
		return c.(*atomic.Int64)
	}
	c, _ := counts.LoadOrStore(class, new(atomic.Int64))
	return c.(*atomic.Int64)
}

// Note records one call of class that reached the operating system.
func Note(class string) { counter(class).Add(1) }

// Count is how many calls of class have reached the operating system in
// this process.
func Count(class string) int64 { return counter(class).Load() }
