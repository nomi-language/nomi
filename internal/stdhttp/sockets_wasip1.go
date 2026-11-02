//go:build wasip1

package stdhttp

// noSocketsMessage is what every listen and dial answers on wasip1, which
// gives a module no sockets.
const noSocketsMessage = "a WASI program has no sockets: it cannot listen for or open connections"
