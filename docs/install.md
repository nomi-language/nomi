# Installing Nomi

Nomi is pre-release and changes from day to day, so most people should build
it from a clone: that is where editor support is installed from, and `git
pull` keeps it current. `go install github.com/nomi-language/nomi/cmd/nomi@latest`
builds the newest commit on `main` without a clone. No versions are tagged
and no prebuilt binaries are published yet. Set up an editor from a clone
either way (see the [README](../README.md#editor-support)).

## From source

You need git and Go 1.21 or newer. If your Go is older than the version Nomi
builds with, the first build downloads that version for you, unless you have
set `GOTOOLCHAIN=local`.

```sh
git clone https://github.com/nomi-language/nomi.git
go install -C nomi ./cmd/nomi ./cmd/nomi-lsp
nomi --version
```

`go install` writes `nomi` and `nomi-lsp` to `$(go env GOPATH)/bin`, usually
`~/go/bin`, which must be on `PATH`. Editors find the language server as
`nomi-lsp` and the debugger as `nomi dap`, so these two binaries are all an
editor needs from the install (Neovim also needs the nvim-dap plugin to
debug). A source build's `nomi --version` prints
the commit, with `-dirty` appended when the checkout had uncommitted changes:

```
nomi 0123456789ab (go1.27.0 darwin/arm64)
```

Keep the checkout where it is. A `nomi` built from a clone is a development
build: `nomi build` and `nomi build-vm` build nomi-vm from the checkout, and a project with Go
bindings compiles against it. To point a development build at another
checkout, for instance after moving it, set `NOMI_COMPILER_SOURCE` to that
checkout's root. The variable also makes a `go install`ed `nomi` build Go
bindings against a checkout instead of its own version, which is how to try
compiler changes on a project without reinstalling.

## With `go install`

With Go 1.21 or newer, install without cloning:

```sh
go install github.com/nomi-language/nomi/cmd/nomi@latest github.com/nomi-language/nomi/cmd/nomi-lsp@latest
```

With no tagged versions, `@latest` is the newest commit on `main`; `@<commit>`
names another one. `nomi --version` prints the module's pseudo-version.

This `nomi` needs no checkout. A project with Go bindings compiles against the
compiler module at `nomi`'s own version, which the Go toolchain fetches into
the module cache like any other dependency, through your `GOPROXY` and
checksum database. `go install` installs no `nomi-vm`, so `nomi build` and
`nomi build-vm` build nomi-vm with Go from that module, for `--target` platforms too, and
caches it. The executable `nomi build` writes needs nothing installed.

## What Nomi needs besides itself

A release archive's `nomi` (once releases are published) and a `go
install`ed one need nothing else for a project without Go bindings, its
dependencies included: `nomi` fetches the modules `go.mod` requires through
the Go module proxy itself (`nomi add`, `nomi fetch`). `nomi new` starts a
project (`nomi init` makes an existing directory one), and `nomi` writes its
`go.mod` and `go.sum` from then on.

A project with Go bindings, its own or a dependency's, is built with Go. With
a `go` on `PATH`, `nomi` uses it. With none, `nomi` downloads the Go release
it was built with from go.dev the first time it needs one (about 70 MB),
checks it against a checksum compiled into `nomi`, and keeps it in
`~/Library/Caches/nomi/go` on macOS or `~/.cache/nomi/go` on Linux. Set
`NOMI_GO` to a `go` command, or a Go installation's root, to choose one
yourself. The editor's language server never downloads modules or Go: run
`nomi fetch` in the project once, which downloads everything the project
needs, so the editor finds every module and `--offline` commands need no
network. Go bindings
build without cgo, so a Go package that calls C cannot be bound.

## Windows

Nomi builds for Windows (amd64) and has not yet been run there. Every
package cross-compiles, and CI has Windows jobs that run the whole test
suite, which have not reported yet; treat Windows as untested. The commands
above are the same in PowerShell, and install `nomi.exe` and
`nomi-lsp.exe` to `%USERPROFILE%\go\bin`. Set Git's `core.autocrlf` to
`false` before cloning if you will run the tests, since their recorded
output has LF line ends; Nomi itself reads source with either.

Where Windows differs:

- `nomi build` with no `-o` writes `<entry>.exe`.
- The managed Go toolchain is kept in `%LocalAppData%\nomi\go`.
- Paths in a Nomi program use `/`, and std/fs also takes `\`; a drive is
  `C:/Users`.
- The debugger's integrated terminal needs Windows 10 version 1803 or later
  (it connects over a Unix socket).
