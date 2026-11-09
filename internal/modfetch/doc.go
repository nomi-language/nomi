// Package modfetch puts the Go modules a Nomi project needs into the Go
// module cache, through the module proxy protocol and the checksum database,
// with golang.org/x/mod and without the go command (docs/tooling.md,
// *Modules and dependencies*). A pure-Nomi dependency therefore needs no Go
// toolchain. internal/modcache decides which modules a project needs and
// reads them; this package downloads, verifies and extracts the ones the
// cache lacks, and keeps go.mod and go.sum, the lock file, up to date for
// `nomi add`, `nomi fetch`, `nomi remove` and `nomi update` (fetch.go,
// edit.go). `nomi new` and `nomi init` write the first go.mod
// (cmd/nomi/init_cmd.go).
//
// Every downloaded module zip is checked against go.sum. A module go.sum does
// not list is checked against the checksum database (GOSUMDB, unless
// GONOSUMDB, GOPRIVATE or GOSUMDB=off exempts it) and, when the command may
// update go.sum (`nomi add`, `nomi fetch`, GOFLAGS=-mod=mod), written there;
// otherwise it is an error that says to run `nomi fetch`, as the go command's
// -mod=readonly default refuses a missing go.sum line. A module GOPROXY sends
// `direct` (GOPRIVATE, GONOPROXY, or a proxy that does not have it) is
// downloaded by the go command, which reads version control.
//
// A release `nomi` keeps the first-party lib/ modules at the version its
// prebuilt lib runners were cut at (Options.LibVersion; internal/ffirun
// package doc, "Prebuilt lib runners").
//
// # Tests
//
// Under `go test` a download refuses unless GOMODCACHE is set, so no test
// writes the real module cache. Tests serve modules from a file:// GOPROXY
// and a test checksum database (fetch_test.go) or GOSUMDB=off, never the
// network.
package modfetch
