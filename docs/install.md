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
`~/go/bin`, which must be on `PATH`. A source build's `nomi --version` prints
the commit, with `-dirty` appended when the checkout had uncommitted changes:

```
nomi 0123456789ab (go1.27.0 darwin/arm64)
```

Keep the checkout where it is. A `nomi` built from a clone is a development
build: `nomi build` builds its runner from the checkout, and a project with Go
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
checksum database. `go install` installs no `nomi-runner`, so `nomi build`
builds the runner with Go from that module, for `--target` platforms too, and
caches it. The executable `nomi build` writes needs nothing installed.
