//go:build js || wasip1

package stdhttp

// hasSockets is false on js/wasm and wasip1. Each listen, dial and server
// path tests it first, and since it is a constant the compiler drops what
// follows, so the linker leaves net/http's server, crypto/tls's server
// handshake and net's listeners and dialers out of these builds
// (TestBrowserVMLinksNoServerCode).
const hasSockets = false
