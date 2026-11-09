# tree-sitter-nomi

The [tree-sitter](https://tree-sitter.github.io/) grammar for the
[Nomi](https://github.com/nomi-language/nomi) programming language.

This repository is a read-only mirror. The grammar is developed in the
`tree-sitter-nomi/` directory of
[nomi-language/nomi](https://github.com/nomi-language/nomi), and
`scripts/publish-grammar.sh` there publishes it here, as one commit on this
repository's `main` whose tree is that directory.
Send issues and pull requests to that repository.

The generated parser (`src/parser.c`, `src/scanner.c`) is committed, so editors
can compile it without running `tree-sitter generate`. It is generated with
tree-sitter-cli 0.20.8 (language ABI 14), the version `package.json` and the
main repository's `scripts/tree-sitter.sh` pin; a newer CLI rewrites it at
ABI 15. `queries/` holds the reference highlight and injection queries; each
editor's own queries live under `editors/` in the main repository.

## Checks in the main repository

These run in nomi-language/nomi, whose `AGENTS.md` ("Grammar and editor
extensions") holds the checklist after a grammar change. This section is the
rules behind its checks.

`go test ./internal/parser -run TestTreeSitterParsesWhatTheParserAccepts`
parses with tree-sitter every tracked `.nomi` file and every tour and spec
block the Go parser accepts, plus the line-break cases in `lineBreakInputs`.
It fails on each one with an ERROR or MISSING node, or with a statement list
(top level, block, `//!` prompt, case or else arms) whose statement count
differs from the Go parser's, unless `treeSitterParityGaps` pins it with a
reason. It also parses each malformed source in `rejectedInputs`, which the
Go parser must reject, and fails on one tree-sitter reads with no ERROR or
MISSING node. It runs the CLI through `scripts/tree-sitter.sh` and skips
when `npx` or a cached copy of the pinned CLI is missing.

`queries/injections.scm` hands a tagged literal's body to the tag's language
as one document with its `${...}` slots left out. Each pattern matches the
literal's children as one repetition, so one match captures every body
chunk, and Zed, Helix and Neovim parse the content nodes of one match
together; Zed also groups by `@injection.host`. Neither
`injection.combined` (every literal in the file as one document) nor a
whole-node capture without `injection.include-children` (Zed injects the
whole node; Neovim cuts out only named children) works across the editors.
`TestTreeSitterInjectsASlottedLiteralAsOneDocument` (`internal/parser`)
holds the one-match shape.

Only the std tags with an embedded language inject, each by a static rule
that sets the grammar's lowercase name: `Sql` as `sql`, `Html` as `html`,
`Toml` as `toml`, `Regex` as `regex`. No rule passes the tag itself as the
language. Zed would match it case-insensitively and Neovim lowercases it,
but Helix compares it to its language ids exactly and then searches its
`injection-regex`es inside it, so `Sql` found nothing and `Url` found R.
`TestTreeSitterInjectsTypedLiteralsByGrammarName` checks each tag's
language in every literal form, and that `Url`, `Cron`, `Locale` and
`Route` inject nothing.

An HTML node that spans a slot, such as `href="/posts/${id}/edit"`'s value,
keeps the slot's characters inside its range, and Neovim reads a node's text
for a predicate from the buffer over that range. nvim-treesitter's html
injections inject JavaScript into any attribute value whose text matches
`%${` (lit-html), which would make every slotted attribute value JavaScript.
So the Neovim ftplugin creates a Nomi buffer's parser with an html injection
query that leaves those patterns out (`editors/nvim/lua/nomi/injections.lua`);
HTML files keep theirs. `make test-nvim` holds it when the machine has an
html parser.

`scripts/tree-sitter.sh test` checks the `// ^ capture` assertions in
`test/highlight/*.nomi` against `queries/highlights.scm`. `make test-nvim`
checks which of `discards.nomi`'s tokens read as comments, and which of
`delimiters.nomi`'s read as `@type`, against the Neovim query generated from
Zed's. Those fixtures are tracked `.nomi` files, so they must parse, be
formatted and type-check
(`TestWorkspaceFixtureDiagnosticsAreQuietWhenExpectationsMatch` in
`internal/lsp` analyzes them). A caret can only point at a column three or
more past the comment's indentation, `// <-` points at the indentation
itself, and an assertion separated from its line by a blank line checks the
blank line. `nomi fmt` indents a comment after a line ending in `{` into the
block.

## License

Licensed under either of the Apache License, Version 2.0
([LICENSE-APACHE](LICENSE-APACHE)) or the MIT license
([LICENSE-MIT](LICENSE-MIT)), at your option. `src/tree_sitter/parser.h` is
tree-sitter's own header, under its MIT license
([src/tree_sitter/LICENSE](src/tree_sitter/LICENSE)).
