# Nomi

[![test](https://github.com/nomi-language/nomi/actions/workflows/test.yml/badge.svg?branch=main)](https://github.com/nomi-language/nomi/actions/workflows/test.yml)

Nomi is a statically typed language where every value is immutable. It has
no null and no exceptions, and a `case` must cover every shape its value can
take, so the compiler catches those mistakes before the program runs.

**[Take the tour at nomi-lang.org](https://nomi-lang.org)** to read and run Nomi
in your browser.

- **Statically typed**, with local inference: named functions spell out their
  parameters and return types; bindings and lambdas infer.
- **Bodies end in `end`**: a function, an `if`, a `case` or a type is a
  header line, its body below and `end`. Braces hold data only.
- **Fully immutable**: bindings, structs and collections are persistent;
  "modifying" returns a new value.
- **Functional-first**: first-class functions, algebraic data types,
  exhaustive `case` destructuring, and tail calls in constant stack space.
- **Explicit interfaces**: `impl Interface for Type` blocks, `derive` for
  generated implementations, and qualified calls (`Display.to_string(x)`,
  `String.length(s)`) instead of value methods.
- **Pipe-oriented**: `x |> f()` keeps data flow reading top to bottom.
- **Structured concurrency**: `concurrent` blocks, tasks, channels,
  supervisors and context-style cancellation.
- **Built-in tests**: `test` / `group` blocks and `//!` attached examples are
  ordinary Nomi code, run by `nomi test`.

Nomi compiles programs to bytecode and runs them on its own virtual machine.
The same VM runs `nomi run`, `nomi test`, the REPL, executables written by
`nomi build`, the examples in the browser tour, and applications that embed
Nomi. Nomi is implemented in Go. A Go program, or a program in any language
with a WebAssembly runtime, can host Nomi and supply the `host fn`s it
declares.

## Status

Nomi is very early and pre-release. The language changes without
compatibility guarantees. The core (expressions, types, pattern matching,
imports, interfaces, derives, tests, collections and control flow) is the most
exercised; concurrency, embedding and package management are younger.

## Learn

- **[The tour](https://nomi-lang.org/introduction/)**: the language a chapter
  at a time, with examples that run in the browser.
  [First Steps](https://nomi-lang.org/learn/values/) is for someone new to
  programming, and [Coming from…](https://nomi-lang.org/coming-from/) has a
  guide for each of several languages.
- **[The cheat sheet](docs/cheatsheet.md)**: the rules most often written
  wrong, with checked examples.
- **[The language spec](docs/spec.md)**: canonical for what the language
  does.
- **[The tooling reference](docs/tooling.md)**: the `nomi` command,
  `nomi.toml`, dependencies, the test runner and builds.
- **[The style guide](docs/style.md)**: how to write Nomi well.

## Getting started

`nomi` is one file: the compiler, the test runner, the formatter, the
language server (`nomi lsp`) and the debugger (`nomi dap`). On macOS and
Linux:

```sh
curl -fsSL https://nomi-lang.org/install.sh | sh
```

[Installing Nomi](docs/install.md) covers the rest: Homebrew, Windows,
putting `nomi` on your `PATH`, and building from source.

Write a first program in a directory of your own, outside the checkout. Save
this as `~/hello-nomi/hello.nomi`:

```nomi
import std::io

fn main()
    io.print("Hello, Nomi!")
end
```
```sh
cd ~/hello-nomi
nomi run hello.nomi        # prints Hello, Nomi!
nomi                       # or try expressions in the REPL
```

Then set up your editor with `nomi editor install helix` (or `nvim`,
`vscode`, `zed`), as [Editor support](#editor-support) below says.

## Usage

```sh
nomi new hello                             # start a project in hello/: nomi.toml and main.nomi
nomi run app.nomi                          # run a program
nomi run hello                             # run a project's one entry point
./app.nomi a b                             # run a file whose first line is #!/usr/bin/env nomi
nomi test                                  # run every test under the current directory
nomi                                       # an interactive REPL
nomi build app.nomi                        # write a standalone executable
nomi build app.nomi --target linux/amd64   # ... for another platform
nomi build app.nomi --target wasip1/wasm   # ... as a WebAssembly module
nomi build app.nomi --web                  # write app-web/, a page that runs it in a browser,
                                           # with the files of web/ beside it copied in
nomi run --web app.nomi                    # serve that page and web/ on localhost; reload it on every edit
nomi build app.nomi --no-vm                # write app.nomic, the compiled program alone, for any platform's nomi-vm
nomi build-vm                              # write nomi-vm, which runs a compiled program it is given:
nomi-vm app.nomic a b                      #   by path, or
cat app.nomic input | nomi-vm              #   framed on stdin, the rest being the program's input
nomi check app.nomi                        # type-check without running
nomi fmt -w .                              # format
nomi help run                              # a command's usage, as nomi run --help prints it
```

## Editor support

`nomi editor install <editor>` sets up Helix, Neovim, VS Code or Zed: the
language server (`nomi lsp`), highlighting, run and test lenses and the
debugger (`nomi dap`). [Installing Nomi](docs/install.md#set-up-your-editor)
lists what each editor also needs, and the tooling reference's
[Editors and debugger](docs/tooling.md#editors-and-debugger) says what each
install writes and what the language server and debugger do. Each editor's
directory has its README:

| Editor | Directory |
|---|---|
| Helix | [`editors/helix/`](editors/helix/) |
| Neovim 0.11+ | [`editors/nvim/`](editors/nvim/) |
| VS Code | [`editors/vscode/`](editors/vscode/) |
| Zed | [`editors/zed/`](editors/zed/) |
| bat (and syntect users) | [`editors/bat/`](editors/bat/) |

Helix, Neovim and Zed parse with the tree-sitter grammar in
[`tree-sitter-nomi/`](tree-sitter-nomi/), published read-only at
[nomi-language/tree-sitter-nomi](https://github.com/nomi-language/tree-sitter-nomi).
To run each editor against a checkout while working on Nomi, see
[`editors/README.md`](editors/README.md).

## Contributing

Nomi is not accepting pull requests yet. The language and compiler still
change too quickly for an outside change to land without being reworked, and
that isn't a fair use of a contributor's time. Bug reports, questions and
feedback are welcome as [issues](https://github.com/nomi-language/nomi/issues).

To build and test from source, see [`CONTRIBUTING.md`](CONTRIBUTING.md) and
[`AGENTS.md`](AGENTS.md). What is not built yet is in
[feature status](docs/feature-status.md), and the larger work ahead in the
[roadmap](docs/roadmap.md).

## License

Nomi is distributed under the terms of both the MIT license and the Apache
License (Version 2.0), at your option.

See [LICENSE-APACHE](LICENSE-APACHE) and [LICENSE-MIT](LICENSE-MIT) for
details.

Unless you explicitly state otherwise, any contribution intentionally
submitted for inclusion in the work by you, as defined in the Apache-2.0
license, shall be dual licensed as above, without any additional terms or
conditions.
