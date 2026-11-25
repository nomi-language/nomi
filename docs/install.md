# Installing Nomi

Installing Nomi is three steps: put `nomi` on your `PATH`, set up your
editor with it, and run a program. `nomi` is one file. It is the compiler,
the test runner, the formatter, the language server (`nomi lsp`) and the
debugger (`nomi dap`).

Nomi is pre-release: every `0.0.x` release may break the one before.

## Install `nomi`

### From a release

On macOS and Linux, on amd64 or arm64:

```console
$ curl -fsSL https://nomi-lang.org/install.sh | sh
```

The script downloads the release archive for your machine, checks its
SHA-256 against the release's checksums, and puts `nomi` in `~/.nomi/bin`.
It changes no shell profile: when that directory is not on your `PATH`, it
prints the line to add. `NOMI_VERSION=v0.0.1` installs that release in
place of the latest, and `NOMI_INSTALL_DIR` names another directory. Run it
again to update.

With Homebrew, on macOS or Linux:

```console
$ brew install nomi-language/nomi/nomi
```

`brew upgrade nomi` updates it.

To install by hand, download the archive for your platform from the
[release page](https://github.com/nomi-language/nomi/releases). A release
is one archive per platform, holding `nomi` alone:
`nomi_<version>_<os>_<arch>.tar.gz`, or `.zip` for Windows, with
`nomi_<version>_checksums.txt` beside them. Check the archive against its
line in the checksums file, unpack it and move `nomi` to a directory on
your `PATH`:

```console
$ shasum -a 256 nomi_v0.0.1_darwin_arm64.tar.gz
$ tar -xzf nomi_v0.0.1_darwin_arm64.tar.gz
$ mv nomi ~/.local/bin/
$ nomi --version
nomi v0.0.1 (0123456789ab, darwin/arm64)
```

The binaries are not signed. On macOS, Gatekeeper quarantines a file
downloaded with a browser and refuses to run the unsigned `nomi` from it;
`xattr -d com.apple.quarantine nomi` clears it. The script and Homebrew
download with tools that set no quarantine.

On Windows, download `nomi_<version>_windows_amd64.zip` from the release
page and unzip `nomi.exe` into a directory on your `PATH`. Windows is
exercised by automated tests only ("Windows" below).

A release `nomi` needs nothing else installed.

### From source

You need git and Go 1.21 or newer. If your Go is older than the version Nomi
builds with, the first build downloads that version for you, unless you have
set `GOTOOLCHAIN=local`.

```console
$ git clone https://github.com/nomi-language/nomi.git
$ go install -C nomi ./cmd/nomi
$ nomi --version
```

`go install` writes `nomi` to `$(go env GOPATH)/bin`, usually `~/go/bin`,
which must be on `PATH`. `git pull` and the same `go install` keep it
current. A source build's `nomi --version` prints the commit, with `-dirty`
appended when the checkout had uncommitted changes:

```console
nomi 0123456789ab (darwin/arm64)
```

Keep the checkout where it is: `nomi build` builds the program's runner
from it ("What a source build needs" below).

### With `go install`, without a clone

```console
$ go install github.com/nomi-language/nomi/cmd/nomi@latest
```

`@latest` is the newest release, `@v0.0.1` names one, and `@<commit>` a
commit of `main`. `nomi --version` prints the release it was built at:

```console
nomi v0.0.1 (darwin/arm64)
```

## Set up your editor

`nomi` carries each editor's files and writes them where the editor reads
them, so they match the compiler's version:

```console
$ nomi editor install helix    # or nvim, vscode, zed
```

| Editor | Also needs |
|---|---|
| Helix | A C compiler, to build the grammar. |
| Neovim 0.11+ | A C compiler, to build the grammar. nvim-dap to debug. |
| VS Code | `code` on `PATH`. |
| Zed | Nothing. Restart Zed afterwards. |

On macOS the C compiler is the Xcode Command Line Tools
(`xcode-select --install`). Run the command again after updating `nomi`.
`nomi editor` lists what is installed and from which version, and
`nomi editor uninstall <editor>` removes it. The tooling reference's
"Setting up an editor" says what each install writes.

A release `nomi` carries the VS Code extension and the Zed extension's
compiled code. A `nomi` built from source carries neither, since they are
built with Node and Rust: from a clone, `make install-vscode` (Node 22+)
and `make install-zed` (Rust through rustup, with the `wasm32-wasip2`
target) build and install them.

## Run a program

```console
$ nomi new hello
$ nomi run hello/main.nomi
$ nomi build hello/main.nomi -o hello/hello
$ hello/hello
```

`nomi new` starts a project (`nomi init` makes an existing directory one).
`nomi` with no arguments starts the REPL. The executable `nomi build`
writes needs nothing installed where it runs.

## What `nomi` needs besides itself

To run, check, test and format a project, nothing, whichever way `nomi` was
installed. That includes the project's dependencies: `nomi` fetches the
packages `nomi.toml` names from their repositories' hosts itself (`nomi
add`, `nomi fetch`), and writes the project's `nomi.toml` dependencies and
`nomi.lock`. A package in a private repository, or on a host other than
GitHub, GitLab, Codeberg, Bitbucket and sourcehut, needs `git` on `PATH`
(the tooling reference's "Private packages").

The editor's language server never downloads packages. Run `nomi fetch` in
the project once, which downloads every package the project needs, so the
editor finds them and `--offline` commands need no network.

`nomi build` is where a release and a source build differ.

### What a release carries

`nomi build` appends your program to a runner, `nomi-vm`. A release `nomi`
carries the runners for its own platform inside it, so building an
executable needs nothing else and no network. For another platform
(`--target`), for WebAssembly, and for a program importing `std::compiler`,
`nomi build` downloads the runner it needs from the release and checks it
against the release's checksums.

A release is also one git tag of the compiler's repository (`v0.0.1`),
which versions the `lib/` packages in it too. A release `nomi` takes the
`lib/` packages at its own version, so `nomi add` of a library fails and
names the tag when the repository lacks its release's tag.

### What a source build needs

A `nomi` built from source builds each runner with Go: the one `NOMI_GO`
names (a `go` command or a Go installation's root), else the `go` on
`PATH`.

A `nomi` built from a clone builds the runner from that checkout. To point
it at another checkout, for instance after moving it, set
`NOMI_COMPILER_SOURCE` to that checkout's root. The variable also makes a
`go install`ed `nomi` build the runner from a checkout instead of its own
version, which is how to try compiler changes on a project without
reinstalling.

A `go install`ed `nomi` needs no checkout. It builds the runner from the
compiler module at its own version, which the Go toolchain fetches into the
module cache like any other dependency, through your `GOPROXY` and checksum
database, for `--target` platforms too, and caches it.

On a Mac with Xcode's command line tools, `nomi` starts about 1 ms faster
linked by the system linker, which `make build` and `make dev-editors` do:

```console
$ CGO_ENABLED=1 go install -C nomi -ldflags=-linkmode=external ./cmd/nomi
```

## Windows

Nomi builds for Windows (amd64), and CI runs the compiler's Go tests there
on every push to `main`; the js/wasm, tour and editor checks run on Linux
only. That is all the testing Windows gets. A release installs from its zip
("From a release" above), as the install script and Homebrew are for macOS
and Linux. The source commands above are the same in PowerShell, and
`go install` writes `nomi.exe` to `%USERPROFILE%\go\bin`. Set Git's `core.autocrlf` to `false`
before cloning if you will run the tests, since their recorded output has
LF line ends; Nomi itself reads source with either.

Where Windows differs:

- `nomi build` with no `-o` writes `<entry>.exe`.
- Paths in a Nomi program use `/`, and std::fs also takes `\`; a drive is
  `C:/Users`.
- Through a std::fs `Dir` on the disk, a `..` after a symlink is taken off
  the path's text before the link is followed. With `up` a link,
  `sub/up/../a.txt` is `sub/a.txt`, which is `NotFound` when missing. On
  macOS, Linux and in memory the link is followed first, and the path is
  `InvalidPath` when its `..` then leads outside. Neither answer leaves the
  directory.
- The debugger's integrated terminal needs Windows 10 version 1803 or later
  (it connects over a Unix socket).
