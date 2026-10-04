Fixture: a Nomi-only crate shape. The directory has no .go files;
the discovery test arranges it as a required Go module with a
`replace` directive pointing here. `go/packages` reports "no Go
files"; Discover must silently skip the module.
