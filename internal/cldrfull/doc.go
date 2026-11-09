// Package cldrfull compiles every locale's CLDR data into a binary:
// root.cldr, locales.cldr and collation-cjk.cldr, which internal/cldrgen
// writes (`go generate ./internal/cldrfull`; cldrgen's
// TestGeneratedDataIsCurrent fails while any is stale).
//
// `nomi`, the language server and the tour link it, through vmhost: a
// program run in process gets its locales' data from here, cut to what its
// nomi.toml lists (Bundle), the same bundle `nomi build` writes into an
// image. A runner (nomi-vm, the wasm runners) must not link it: it reads the
// data its image carries, and TestRuntimeArtifactLinksNoFrontEnd
// (internal/vm) holds it to that.
//
// Importing the package makes Full the data a machine given none reads
// (cldrdata.SetDefault).
package cldrfull
