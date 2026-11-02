//go:build !js

package stdhttp

import "net/http"

// browserTransport changes nothing outside js/wasm.
func browserTransport(*http.Transport) {}
