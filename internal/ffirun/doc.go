// Package ffirun builds and runs the Go FFI wrapper: the cached Go program
// that `nomi run`, `nomi check`, `nomi test` and `nomi build` use for a
// program with Go bindings. It is internal to the nomi CLI and not part of
// the public embedding API.
//
// The wrapper imports the bound Go packages and holds a host adapter per
// bound declaration. `run` and `test` run the program on the VM inside the
// wrapper; `check` stops after load and analysis; `nomi build` has the
// wrapper lower the image it appends to a runner built for the project
// (ProjectRunner). A pure-Nomi program runs in process with no wrapper.
//
// The path has three stages:
//
//  1. Discovery (discovery.go): walk up to the controlling go.mod, then
//     scan Nomi source for top-level `gopkg "import/path" as alias`
//     handles and `go alias.Symbol` type and function bindings, and
//     validate each binding against the Go package (validate.go).
//  2. Codegen (codegen.go): emit a single-file main package holding a VM
//     host table of adapters generated for each discovered binding, which
//     dispatches to vmhost's Check, Load and Run, Test, or an image build
//     on the entry path passed through the private --nomi-wrapper-mode
//     flag.
//  3. Cache (cache.go): keep the generated wrapper and its binary under
//     os.UserCacheDir()/nomi/builds/<hash>/ with a hash record, and
//     regenerate when go.mod, go.sum, the discovered set, the wrapper
//     template or the compiler (identity.go) changes. The compiler is in
//     the key because the cached binary statically links the front end,
//     the IR builder and the VM, not just the generated main.go.
//
// Prepare runs the three stages and returns a Result the CLI acts on.
// CommandVM is the command that runs the cached wrapper binary.
//
// # Go standard library packages
//
// A bound Go standard library package is read from the source of the
// toolchain `go env GOROOT` names, with its build constraints for this
// platform (gostd.go), so `strings.ToUpper` is validated and adapted like a
// project package. It needs no go.mod: a project with none gets a wrapper
// that is its own module. The toolchain's version joins the cache key
// whenever a binding names a standard library package.
//
// # Maps
//
// A Go map crosses to Nomi in ascending key order, since a Nomi Map keeps
// insertion order and Go iterates randomly. A string, integer or float key
// (or a named type over one) sorts by cmp.Compare, NaN first; a bool key
// false first; a struct key by its exported fields in declaration order by
// the same rules; a time.Time key or field by Time.Compare (internal/hostgen,
// emit.go). Any other key type (pointer, interface, channel, array) is
// refused at generation, since its only order would differ between runs. A
// Nomi Map passed to Go needs no order.
//
// # Streams
//
// A Go io.Reader or io.ReadCloser result becomes a Reader handle over the Go
// value, and an io.Writer or io.WriteCloser result a Writer that hands it
// each write as made, leaving buffering to Go; either closes the Go value
// through its Close if it has one. A Reader or Writer passed to such a
// parameter reaches Go as an io.ReadCloser or io.WriteCloser calling the
// handle's own operations (hostadapt/streams.go), so Go reads the handle's
// buffer and writes in order with the program, under the handle's lock, on
// the frame of the binding's call (later, if Go keeps the stream). The
// match is by declared Go type, so a function returning a concrete type
// (`*gzip.Reader`) needs a one-line project Go function returning the
// interface.
//
// # Unexported fields
//
// The generator reads exported fields only. An unexported field's type is
// an unread placeholder (fieldType in gotypes.go, and gotypesimport.go for a
// type-checked package), since no value crosses through it. So a bound
// handle's internals may name types the reader cannot read, such as
// pgxpool's `*puddle.Pool[*connResource]`.
//
// # Context
//
// A leading context.Context parameter is left out of the match
// (withoutTaskContext, validate.go) and of the adapter's arity, and the
// adapter passes fr.Context() (internal/hostgen's isGoContext, beside the
// *rt.Frame parameter std's hosts take): the ambient deadline
// rt.EnterDeadline derived at the last `with App.context` rebind, ending
// when the task is cancelled. A context.Context anywhere else is refused, by
// validation and by the generator. tests/18-ffi-and-dynamic/context_ffi_app
// holds a Go call to the deadline.
//
// # The wrapper's module and its cache
//
// .nomi edits need no rebuild of the wrapper; Go changes change the key. The
// cache is safe to delete. Under `go test` the real one is refused, so tests
// set NOMI_FFIRUN_CACHE_ROOT.
//
// The wrapper's go.mod names the compiler module one of two ways
// (compilersource.go):
//
//   - A versioned `nomi` (main module version a tag, or a pseudo-version
//     from `go install ...@version`, no +dirty) requires
//     github.com/nomi-language/nomi at that version, and Go fetches it. The
//     cache key uses the version instead of hashing a tree.
//   - Any other build, including a checkout at an untagged commit, replaces
//     the module with the tree it was built from.
//
// NOMI_COMPILER_SOURCE=<checkout> forces the replace with that checkout.
//
// A project's go.mod holds only its own module and dependencies. The
// wrapper and the FFI runner `nomi build` makes add the compiler's require
// and replace, overriding any the project wrote, and merge the compiler's
// go.sum into theirs (gomod.go, stageGoSum;
// TestFFI_AProjectOutsideTheRepositoryRuns). A versioned `nomi` fetches the
// compiler at its version through GOPROXY, so every release tag must be
// pushed. TestReleaseArchive_RunsAGoBindingFromAFetchedModule (cmd/nomi)
// builds `nomi` as scripts/release.sh does and runs a Go binding with no
// checkout and no `go` on PATH. A bound package of a required module is
// read from the Go module cache. One that needs cgo is an error at its
// `gopkg`, since wrappers build with CGO_ENABLED=0.
//
// The `go` every build runs is internal/gotoolchain's: NOMI_GO, else the one
// on PATH, else the managed toolchain.
//
// # Prebuilt runners
//
// `nomi build` of a pure-Nomi program finds its runner without building one
// when it can: beside `nomi`, in a source checkout, in the download cache,
// or from the release (Runner; prebuilt.go's opening comment gives the
// order and the same-commit check).
//
// # Prebuilt lib runners
//
// A release runs, tests and builds a project whose Go bindings all belong
// to first-party lib/ modules without Go (libprebuilt.go says how). `go run
// ./internal/ffirun/libgen` (GenerateLibMains, over libModules: image,
// markdown, password_hashers, sql/postgres, sql/sqlite, sql/sqlkit) writes
// the wrapper and runner a project of all of them would build into
// librunners/nomi-lib and librunners/nomi-vm-lib. librunners/ is a nested
// module requiring the compiler and those modules by directory, with its
// generated sources committed. They name each declaring file by module path
// plus path in it (github.com/nomi-language/nomi/lib/sql/sqlite/sqlite.nomi),
// never the generating machine's, and the wrapper's entry-scoped keys match
// a module cache target by that suffix once each @version is dropped (the
// template's Relocated blocks, nomiWithoutVersions). TestLibModules
// (cmd/nomi) fails while they are stale: rerun libgen after changing a lib/
// module's `go` bindings or the wrapper template.
//
// scripts/release.sh builds both from librunners/ (nomi-lib with the std
// bundle) for the five native platforms, and nomi-vm-lib alone for js/wasm,
// into nomi_<version>_<goos>_<goarch>_lib.tar.gz, and stamps `nomi` with
// LibVersion, the pseudo-version of the release's commit. Prepare serves a
// program from nomi-lib instead of a generated wrapper when every binding's
// declaring file is in a libModules module whose cache directory is at
// LibVersion (libBindingsMiss), skipping validation, which ran at
// generation. nomi-lib is looked up beside `nomi`, then in the release
// download cache, then downloaded from the lib archive (findLibWrapper,
// gated by AllowLibDownload, which only the CLI calls). ProjectRunner finds
// nomi-vm-lib the same way for a native or js/wasm target (Variant.Lib).
// Each must carry `nomi`'s VCS stamp, as nomi-vm must (prebuilt.go).
// Anything else takes the generated-wrapper path, where a missing toolchain
// or compiler source names the first binding that could not be served
// (needsGoError, from the libMiss Prepare recorded): a project's own
// binding, a lib/ module at another version (with `nomi update`), one
// replaced with a directory, or a target or variant no lib runner ships
// for.
//
// The decisions:
//
//   - A separate archive, since the lib runners would double every
//     download for users who need neither (the sizes are in
//     libprebuilt.go). A pure-Nomi program still gets the plain nomi-vm.
//   - No nomi_nonet lib runner and no wasip1 lib runner or reactor, for the
//     reasons libprebuilt.go gives. A wasip1 build of a lib/ project falls
//     back to a project runner built with Go. No std/compiler variant
//     either, as for the plain runner.
//   - A source build generates its own wrapper: LibVersion is empty for
//     every build release.sh does not make.
//   - A release `nomi` keeps lib/ modules at LibVersion: `nomi add` of a
//     lib/ module with no version takes it, and `nomi update` moves every
//     lib/ module to it (modfetch.Options.LibVersion); @latest takes the
//     latest commit.
//
// TestLibRelease_* (cmd/nomi/lib_release_test.go) builds `nomi` with both
// stamps and nomi-lib and nomi-vm-lib once per test process, serves the
// lib/ modules from a file proxy at two versions, runs, tests, checks and
// builds a project of sqlite, sqlkit, migrate, markdown and image with no
// `go` on PATH and an empty toolchain cache, downloads the lib archive from
// a local release, and checks the refusals of a project binding its own
// package and of one at the other version.
//
// # Programs of a dependency's tool
//
// A dependency's tool (`nomi tool sqlkit generate`) runs in a wrapper built
// in the project's Go module, cached apart from the project's own
// (prepareModule), and reads the project's module graph through
// NOMI_MAIN_MODULE (internal/modcache package doc, "The main module").
package ffirun
