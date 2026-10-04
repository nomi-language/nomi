//go:build js && wasm

package rt

// maxCallDepth is MaxCallDepth in the browser build, the one the language
// tour runs visitors' programs on.
//
// Under js/wasm every Go call is a WebAssembly call, so recursion spends the
// JS engine's stack (about 1 MB in V8), not Go's 1 GB goroutine stack, and it
// runs out long before 100,000 activations. When it does, the engine throws
// RangeError through the Go runtime, which is left mid-function: under Node
// 24, a few such throws in one instance end in "fatal error: schedule:
// holding locks". So the limit must fault first.
//
// Under Node 24, one fresh instance per reading, plain
// non-tail recursion completed 6,000 nested calls and overflowed at 7,000, and
// recursion through an `Iter.map` callback completed 1,000 levels (2,000
// activations) and overflowed at 1,500. A Chrome 152 dedicated worker (the
// tour's) has less: plain recursion overflowed at 2,000 once and at 3,000 once
// while completing 3,000 on other runs, because the depth reached varies with
// which functions V8 has tiered up. 1,000 is under every overflow seen, so
// plain recursion faults with Nomi's own message. The heavier callback path can
// still exhaust the engine stack first; the tour's run worker treats any
// exception out of the Go runtime as fatal to that instance and replaces it.
const maxCallDepth = 1_000
