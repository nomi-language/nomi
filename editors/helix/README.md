# Helix support for Nomi

This directory provides:

- `languages.toml` for `.nomi` file detection (and an extensionless script whose `#!` line names `nomi`), `nomi-lsp`, `nomi fmt`, and the external tree-sitter grammar
- tree-sitter highlighting and typed-literal injection queries mirrored from `tree-sitter-nomi/queries/`
- Helix-specific indentation, textobject, locals, and fold queries

## Install

You need `hx`, `make`, `python3` and a C compiler (on macOS, the Xcode Command
Line Tools). Helix does not install language servers: it starts the commands
named in `languages.toml`, so `nomi` and `nomi-lsp` must be on `PATH`. From a
clone of the repository:

```sh
git clone https://github.com/nomi-language/nomi.git
cd nomi
go install ./cmd/nomi ./cmd/nomi-lsp
make install-helix
```

`make install-helix` does three things:

- copies the queries into `~/.config/helix/runtime/queries/nomi/` (or under
  `$XDG_CONFIG_HOME` when it is set);
- writes the contents of [`languages.toml`](languages.toml) into your
  `languages.toml` between `# BEGIN NOMI HELIX SUPPORT` and
  `# END NOMI HELIX SUPPORT`, with the grammar source set to this checkout's
  `tree-sitter-nomi/` directory, and replaces that block on later runs;
- builds only Nomi's grammar into `runtime/grammars/nomi.so` and runs
  `hx --health nomi`, which should show the parser, the queries, `nomi-lsp`
  and the formatter all found.

Keep the checkout where it is, since the grammar is built from it. Run
`make install-helix` again after `git pull` so the grammar and the queries
stay in step.

### Working on Nomi

`make dev-helix` does the same as `make install-helix`, except that
`runtime/queries/nomi/` becomes a link to this checkout's
`editors/helix/runtime/queries/nomi/`, so query changes reach Helix without a
reinstall. Run it again after a grammar change, or enable the repository's
post-merge hook (`make enable-hooks`). Running
`make install-helix` later replaces the link with a copy.

### Manual install

The alternative builds the grammar from the published mirror,
[nomi-language/tree-sitter-nomi](https://github.com/nomi-language/tree-sitter-nomi),
at the rev pinned in `languages.toml`. From the repository root, with `nomi`
and `nomi-lsp` installed as above:

```sh
mkdir -p ~/.config/helix/runtime/queries/nomi
cat editors/helix/languages.toml >> ~/.config/helix/languages.toml
cp editors/helix/runtime/queries/nomi/*.scm ~/.config/helix/runtime/queries/nomi/
hx --grammar fetch
hx --grammar build
hx --health nomi
```

If your `languages.toml` already has a Nomi block (from an earlier install or
`make install-helix`), remove it before appending.

`hx --grammar fetch` and `hx --grammar build` process every grammar Helix
knows (about 250), which takes a minute or so. A failure that names another
grammar is Helix's own and does not affect Nomi; `hx --health nomi` says
whether Nomi's parser was built. Rebuild after a grammar update.

The query files are needed because Nomi is not part of Helix's bundled
runtime: the `[[grammar]]` entry builds the parser, but highlighting,
indentation, textobjects and folds come from `runtime/queries/nomi/`.

## Local Development

`injections.scm` is a byte-for-byte mirror of:

- `tree-sitter-nomi/queries/injections.scm`

`highlights.scm` is generated from Zed's last-wins query order, with captures
translated to Helix's themed scopes where needed (`variable.other.member` for
fields, `constant.numeric` for numbers, `namespace` for module paths). Helix
and Zed both let later overlapping captures win in practice; keeping catch-alls
first prevents generic `identifier` captures from erasing
member/function/constructor colors.

Attached tests (`//!` lines) are drawn as comments: the block between
`; BEGIN attached-test dim` and `; END attached-test dim` captures every node
inside an `attached_test_prompt` as `@comment`, one pattern per depth down to
16, and the `//!` markers stay `@comment.documentation`. Helix draws the
innermost capture over an outer one and has no ancestor predicate, so one
capture on the whole test would not do it. Diagnostics are drawn separately
and stay at full strength. A node under a tree-sitter `ERROR` node is not
reached by these patterns and keeps its ordinary color. The theme's
`comment` style sets the color; a modifier such as bold that the theme puts on
a keyword can still show through.

`locals.scm` is Helix-specific. It marks lexical scopes, local definitions, and
local references so Helix can color parameter and binding uses such as `n` in a
function body. Do not expect LSP semantic tokens to cover that path in Helix.

After changing the source grammar queries or Zed highlight query, run:

```sh
make build-helix
```

`make build-helix` runs `./scripts/sync-helix-runtime.sh`, which refreshes the
checked-in Helix query mirrors. `make install-helix` runs the same sync before
installing, so it always installs queries that match this checkout.

The Helix-specific `indents.scm`, `textobjects.scm`, `locals.scm` and
`folds.scm` files are maintained here.

`scripts/publish-grammar.sh` publishes the grammar and writes the new rev into `languages.toml`.
