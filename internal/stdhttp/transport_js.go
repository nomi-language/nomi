//go:build js

package stdhttp

import "net/http"

// browserTransport keeps Go's js/wasm transport on the Fetch API, which a
// custom dial function turns off (net/http's roundtrip_js.go).
func browserTransport(tr *http.Transport) { tr.DialContext = nil }

// noSocketsMessage is what every listen and dial answers on js/wasm, where
// the program runs in a browser or under Node: Go's net package there is
// an in-process stand-in that nothing outside the program can reach.
const noSocketsMessage = "a WebAssembly program in a browser has no sockets: it cannot listen for or open connections (std/http's client works, through fetch)"
