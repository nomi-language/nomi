# Nomi

[![test](https://github.com/nomi-language/nomi/actions/workflows/test.yml/badge.svg?branch=main)](https://github.com/nomi-language/nomi/actions/workflows/test.yml)

Nomi is a statically typed, immutable language for writing expression-shaped
programs that stay explicit as they grow.

**[Take the tour at nomi-lang.org](https://nomi-lang.org)** to read and run Nomi
in your browser.

- **Statically typed**, with local inference: named functions spell out their
  parameters and return types; bindings and lambdas infer.
- **Fully immutable**: bindings, structs and collections are persistent;
  "modifying" returns a new value.
- **Functional-first**: first-class functions, algebraic data types,
  exhaustive `case` destructuring, and tail calls in constant stack space.
- **Explicit interfaces**: `impl Interface for Type { ... }`, `derive` for
  generated implementations, and qualified calls (`Display.to_string(x)`,
  `String.length(s)`) instead of value methods.
- **Pipe-oriented**: `x |> f()` keeps data flow reading top to bottom.
- **Structured concurrency**: `concurrent { ... }` blocks, tasks, channels,
  supervisors and context-style cancellation.
- **Built-in tests**: `test` / `tests` blocks and `//!` attached examples are
  ordinary Nomi code, run by `nomi test`.

Nomi compiles programs to bytecode and runs them on its own virtual machine.
The same VM runs `nomi run`, `nomi test`, the REPL, executables written by
`nomi build`, the examples in the browser tour, and Go applications that embed
Nomi. Nomi is implemented in Go, and can call Go code through a typed FFI.

## Status

Nomi is very early and pre-release. The language changes without
compatibility guarantees. The core (expressions, types, pattern matching,
imports, interfaces, derives, tests, collections and control flow) is the most
exercised; concurrency, FFI and module management are younger.

## Learn

- **[The language spec](docs/spec.md)**: canonical
  for what the language does.
- **[Style guide](docs/style.md)**, **[feature status](docs/feature-status.md)**,
  **[roadmap](docs/roadmap.md)**.

## Getting started

You need git and Go 1.21 or newer. If your Go is older than the version Nomi
builds with, the first build downloads that version for you.

Clone the repository and install `nomi` and the language server `nomi-lsp`:

```sh
git clone https://github.com/nomi-language/nomi.git
cd nomi
go install ./cmd/nomi ./cmd/nomi-lsp
```

`go install` puts both in `$(go env GOPATH)/bin` (usually `~/go/bin`); make
sure that directory is on your `PATH`. Keep the checkout: editor support is
installed from it, and a `nomi` built from it builds projects with Go bindings
against it. [`docs/install.md`](docs/install.md) also covers
`go install ...@<version>` without a clone, and prebuilt binaries of tagged
versions, which need no Go but are snapshots that fall behind `main`. Either of
those builds Go bindings against its own released version, which Go fetches.

Write a first program in a directory of your own, outside the checkout. Save
this as `~/hello-nomi/hello.nomi`:

```nomi
import std/io

fn main() {
    io.print("Hello, Nomi!")
}
```

```sh
cd ~/hello-nomi
nomi run hello.nomi        # prints Hello, Nomi!
nomi                       # or try expressions in the REPL
```

Then set up your editor from [Editor support](#editor-support) below.

## Usage

```sh
nomi run app.nomi                          # run a program
./app.nomi a b                             # run a file whose first line is #!/usr/bin/env nomi
nomi test                                  # run every test under the current directory
nomi                                       # an interactive REPL
nomi build app.nomi                        # write a standalone executable
nomi build app.nomi --target linux/amd64   # ... for another platform
nomi check app.nomi                        # type-check without running
nomi fmt -w .                              # format
```

## Editor support

Every editor launches `nomi-lsp` from `PATH`. Helix, Neovim and Zed parse with
the tree-sitter grammar in [`tree-sitter-nomi/`](tree-sitter-nomi/); VS Code
and bat highlight with regex grammars instead (VS Code's is derived from
bat's). The tree-sitter grammar is also published read-only at
[nomi-language/tree-sitter-nomi](https://github.com/nomi-language/tree-sitter-nomi).
Run each command from the repository root, and again after `git pull`. The
Helix and Neovim setups compile the grammar, so they need `make`, `python3`
and a C compiler (on macOS, the Xcode Command Line Tools:
`xcode-select --install`).

| Editor | Directory | Setup |
|---|---|---|
| Helix | [`editors/helix/`](editors/helix/) | `make install-helix`. It ends with `hx --health nomi`, which should find the parser, the queries, `nomi-lsp` and the `nomi` formatter. Its README has the manual route. |
| Neovim 0.11+ | [`editors/nvim/`](editors/nvim/) | `make install-nvim`. Its README covers lazy.nvim. |
| VS Code | [`editors/vscode/`](editors/vscode/) | `make install-vscode`, which needs Node 22+ and npm. It builds `editors/vscode/nomi.vsix` and installs it with `code`; its README has the details. |
| Zed | [`editors/zed/`](editors/zed/) | `scripts/sync-zed-grammar.sh`, then run "zed: install dev extension" in Zed and select `editors/zed/`. Zed compiles the extension, which needs Rust installed through rustup. |
| bat (and syntect users) | [`editors/bat/`](editors/bat/) | See its README. |

Helix, Neovim, VS Code and Zed draw attached tests (`//!` lines) dimmed, so they do
not compete with the code they test; their diagnostics are drawn at full
strength. Neovim and VS Code show the attached test under the cursor normally
(their READMEs have the settings). In Zed, if you turn semantic tokens on, the
extension's `semantic_token_rules.json` keeps their identifiers dimmed too.

To work on Nomi itself, run `make dev-editors` instead. It installs `nomi` and
`nomi-lsp` from the checkout and makes each installed editor read the
checkout: Helix's queries are linked rather than copied, Neovim loads
`editors/nvim` through lazy.nvim with a parser compiled beside it, and it says
when Zed's dev extension needs reinstalling.

## Contributing

Nomi is not accepting pull requests yet. The language and compiler still
change too quickly for an outside change to land without being reworked, and
that isn't a fair use of a contributor's time. Bug reports, questions and
feedback are welcome as [issues](https://github.com/nomi-language/nomi/issues).

To build and test from source, see [`CONTRIBUTING.md`](CONTRIBUTING.md) and
[`AGENTS.md`](AGENTS.md).

## License

Nomi is distributed under the terms of both the MIT license and the Apache
License (Version 2.0), at your option.

See [LICENSE-APACHE](LICENSE-APACHE) and [LICENSE-MIT](LICENSE-MIT) for
details.

Unless you explicitly state otherwise, any contribution intentionally
submitted for inclusion in the work by you, as defined in the Apache-2.0
license, shall be dual licensed as above, without any additional terms or
conditions.
