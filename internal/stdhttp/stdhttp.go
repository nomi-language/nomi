// Package stdhttp backs std/http: the client over Go's net/http, and the
// inventory that holds std/http to the whole of Go's HTTP packages
// (inventory.txt, docs/design/http.md).
package stdhttp

// LandedPhase is the last phase of docs/design/http.md's order of work that
// std holds. TestGoInventoryIsComplete requires every covered row of a phase
// up to it to name a declaration std has.
const LandedPhase = 6
