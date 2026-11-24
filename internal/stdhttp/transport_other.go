//go:build !js

package stdhttp

import "net/http"

// browserTransport changes nothing outside js/wasm.
func browserTransport(*http.Transport) {}

// fetchNamesTheAgent is false outside js/wasm: Go's transport writes the
// request, so Send gives it the default User-Agent (withUserAgent).
const fetchNamesTheAgent = false
