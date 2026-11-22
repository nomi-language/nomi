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

```console
$ git clone https://github.com/nomi-language/nomi.git
$ go install -C nomi ./cmd/nomi ./cmd/nomi-lsp
$ nomi --version
```

On a Mac with Xcode's command line tools, `nomi` starts about 1 ms faster
linked by the system linker, which `make build` and `make dev-editors` do:

```console
$ CGO_ENABLED=1 go install -C nomi -ldflags=-linkmode=external ./cmd/nomi ./cmd/nomi-lsp
```

`go install` writes `nomi` and `nomi-lsp` to `$(go env GOPATH)/bin`, usually
`~/go/bin`, which must be on `PATH`. Editors find the language server as
`nomi-lsp` and the debugger as `nomi dap`, so these two binaries are all an
editor needs from the install (Neovim also needs the nvim-dap plugin to
debug). A source build's `nomi --version` prints
the commit, with `-dirty` appended when the checkout had uncommitted changes:

```console
nomi 0123456789ab (go1.27.0 darwin/arm64)
```

Keep the checkout where it is. A `nomi` built from a clone is a development
build: `nomi build` and `nomi build-vm` build nomi-vm from the checkout with
Go. To point a development build at another checkout, for instance after
moving it, set `NOMI_COMPILER_SOURCE` to that checkout's root. The variable
also makes a `go install`ed `nomi` build nomi-vm from a checkout instead of
its own version, which is how to try compiler changes on a project without
reinstalling.

## With `go install`

With Go 1.21 or newer, install without cloning:

```console
$ go install github.com/nomi-language/nomi/cmd/nomi@latest github.com/nomi-language/nomi/cmd/nomi-lsp@latest
```

With no tagged versions, `@latest` is the newest commit on `main`; `@<commit>`
names another one. `nomi --version` prints the module's pseudo-version.

This `nomi` needs no checkout. `go install` installs no `nomi-vm`, so
`nomi build` and `nomi build-vm` build nomi-vm with Go from the compiler
module at `nomi`'s own version, which the Go toolchain fetches into the
module cache like any other dependency, through your `GOPROXY` and checksum
database, for `--target` platforms too, and caches it. The executable `nomi build` writes needs nothing installed.

## What Nomi needs besides itself

A release archive's `nomi` (once releases are published) and a `go
install`ed one need nothing else to run, check and test a project, its
dependencies included: `nomi` fetches the modules `nomi.toml` names through
the Go module proxy itself (`nomi add`, `nomi fetch`). `nomi new` starts a
project (`nomi init` makes an existing directory one), and `nomi` writes its
`nomi.toml` dependencies and `nomi.lock` from then on.

Nomi source binds no Go code. The first-party `lib/` modules, the SQLite
driver and image among them, run their C engines as WebAssembly, so a
project of them needs no Go either.

A release `nomi` needs no Go, except to download a private module its
proxies send `direct`, which the `go` on `PATH` downloads (the tooling
reference's "Private modules"). Its release ships every
`nomi-vm` `nomi build` appends a program to, for every target, and a
program importing `std::compiler` gets one from a second archive per
target. `nomi build` downloads a `nomi-vm` that is not installed beside
`nomi` from the release and checks it against the release's checksums.

A `nomi` built from source, from a clone or with `go install`, builds each
`nomi-vm` with Go: the one `NOMI_GO` names (a `go` command or a Go
installation's root), else the `go` on `PATH`. The editor's language server never
downloads modules: run `nomi fetch` in the project once, which downloads
every module the project needs, so the editor finds them and `--offline`
commands need no network.

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
- Paths in a Nomi program use `/`, and std::fs also takes `\`; a drive is
  `C:/Users`.
- The debugger's integrated terminal needs Windows 10 version 1803 or later
  (it connects over a Unix socket).
