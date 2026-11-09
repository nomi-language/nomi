// Package gotoolchain answers one question: which Go toolchain the command in
// front of the user compiles with, and what the failure says when there is
// none.
//
// `nomi run`, `nomi test` and `nomi check` on a project with Go FFI build a
// wrapper with `go build` (internal/ffirun), and `nomi build` builds a
// project runner the same way. A user who installed a prebuilt `nomi` may
// have no Go at all. The toolchain is, in order:
//
//  1. NOMI_GO, the path of a `go` command (or of a GOROOT holding bin/go);
//  2. the `go` on PATH, so a Go developer downloads nothing;
//  3. the managed toolchain (managed.go): the Go release this `nomi` was built
//     with, downloaded from go.dev once, verified against a SHA-256 compiled
//     into this binary, and kept in the cache. Only a command the user ran
//     downloads it (AllowDownload); the language server uses it only once it
//     is there.
//
// When none is found the failure says what needed Go, where it looked, and
// why the managed toolchain was not used.
//
// # The managed toolchain
//
// The archive comes from go.dev, or from NOMI_GO_DOWNLOAD_URL, and is
// checked against the checksums in pinned_gen.go. It is installed under
// ToolchainRoot: NOMI_TOOLCHAIN_ROOT, else <user cache>/nomi/go/<version>,
// beside the FFI build cache. Under `go test` the real directory is
// refused, so a test sets NOMI_TOOLCHAIN_ROOT.
//
// After a Go version bump, pin the new release's archives:
//
//	go generate ./internal/gotoolchain
//
// scripts/release.sh refuses to build with a Go that pinned_gen.go does not
// pin, since that release's `nomi` could not verify its own toolchain.
package gotoolchain
