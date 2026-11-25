// Package buildversion answers which build a binary is: the commit it was
// built from (Token) and the release it is, or none (Release). Every
// reader of either asks here. It imports only the standard library, so a
// runner may link it.
//
// # The commit
//
// Go stamps the VCS revision and a dirtiness flag into every binary it
// builds from a checkout, and the main module's version into every binary.
// Token reads them. Name is the release and the token together, as a
// message names a binary: `nomi build --no-vm` writes it into a .nomic
// file's header, nomi-vm names itself by it in `--version` and when it
// refuses a .nomic file another version wrote, and `nomi lsp`, `nomi mcp`
// and `nomi editor` report it. `nomi --version` prints the same two with
// the platform (cmd/nomi's versionLineFor).
//
// # The release
//
// Release is the release this binary is, such as v0.0.2, or "" for a build
// from source. It is the one answer to that question: `nomi --version`,
// std/http's default `User-Agent`, `system.nomi_release()` (and so
// sqlkit's release stamp), the version `nomi new` and `nomi init` write
// as [package].nomi, the floor a package's [package].nomi is checked
// against and the version the first-party lib/ packages are taken at
// (internal/pkgcache's NomiNeeded and Pinned), and the release a `nomi`
// downloads another platform's runners from (internal/runners) all read
// it.
//
// Two sources can name a release, and a binary is one when either names a
// release version (IsRelease):
//
//   - The stamp. scripts/release.sh builds `nomi` with
//     `-ldflags -X github.com/nomi-language/nomi/internal/buildversion.stamp=<tag>`,
//     and no other build sets it. When it names a release version it wins.
//   - The main module's version. Go stamps it from the version `go install
//     ...@vX.Y.Z` fetched, and from the tag at the commit a clean checkout
//     builds. A modified checkout's version carries `+dirty`, an untagged
//     commit's is a pseudo-version, and a build with no VCS information
//     has `(devel)`; none of those is a release.
//
// What each kind of build answers:
//
//	scripts/release.sh at the tag vX.Y.Z    vX.Y.Z (both sources; the script refuses a module version that is not the tag)
//	scripts/release.sh v0.0.0-dryrun        v0.0.0-dryrun (the stamp)
//	go install .../cmd/nomi@vX.Y.Z          vX.Y.Z (the module version)
//	go build in a clean checkout at vX.Y.Z  vX.Y.Z (the module version)
//	go build anywhere else, a test binary   none
//	nomi-vm, nomi-vm-reactor and a built executable, the js/wasm and
//	wasip1 builds                           their module version's answer: a release's runners are built at its tag
//	a runner a `go install`ed nomi builds   none: it is built from the module cache's copy, which has no VCS information
//
// BuiltByReleaseScript answers another question: whether scripts/release.sh
// built this binary for a release's archives, so that it carries its
// platform's runners and its release publishes every other one. A `go
// install ...@vX.Y.Z` is a release and was not built that way; it builds
// its runners from that version's source (internal/runners).
package buildversion
