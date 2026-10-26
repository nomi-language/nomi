//go:build js && wasm

package stdio

import "sync"

// A js/wasm build (the browser, Go's Node loader, the tour) has no disk Go
// can reach: wasm_exec.js's fs shim answers ENOSYS. Its file system is in
// memory, empty until the host seeds it (vmrunner from `run`'s `files`
// option, the tour from a block's data files).

var processMemory = sync.OnceValue(func() *Memory { return NewMemory("/work") })

// defaultBackend is a js/wasm build's file system: one in-memory file system
// for the process.
func defaultBackend() Backend { return processMemory() }

// ProcessMemory is the in-memory file system a js/wasm build's programs
// reach unless their host gives them another.
func ProcessMemory() *Memory { return processMemory() }
