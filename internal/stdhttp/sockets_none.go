//go:build js || wasip1

package stdhttp

// hasSockets is false on js/wasm and wasip1. Each listen, dial and server
// path tests it first, and so does each crossing that only a socket can
// reach (a datagram's receive, send and address, a multicast group, and
// rawOf under every socket option), since no socket exists here for them
// to act on. It is a constant, so the compiler drops what follows, and the
// linker leaves net/http's server, crypto/tls's server handshake, and
// net's listeners, dialers and datagram paths out of these builds.
const hasSockets = false
