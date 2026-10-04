# bat syntax for Nomi

A Sublime Text syntax definition for the [Nomi](https://github.com/nomi-language/nomi) language. Usable by any tool that embeds [syntect](https://github.com/trishume/syntect), including:

- [`bat`](https://github.com/sharkdp/bat) — syntax-highlighted `cat`
- [`yazi`](https://github.com/sxyazi/yazi) — terminal file manager (indirectly, via `bat`)
- [`delta`](https://github.com/dandavison/delta) — git diff viewer
- anywhere else syntect is used

## Install for `bat`

From the repository root:

```
mkdir -p "$(bat --config-dir)/syntaxes"
cp editors/bat/nomi.sublime-syntax "$(bat --config-dir)/syntaxes/"
bat cache --build
```

Verify:

```
bat --list-languages | grep Nomi
# Nomi:nomi
```

## Enable in `yazi`

Yazi uses its own bundled syntect and won't automatically see `bat`'s custom syntaxes. To route `.nomi` files through `bat` for previews, install the [piper](https://github.com/yazi-rs/plugins/tree/main/piper.yazi) plugin and add a previewer override.

```
ya pkg add yazi-rs/plugins:piper
```

Add to `~/.config/yazi/yazi.toml`:

```toml
[[plugin.prepend_previewers]]
url = "*.nomi"
run = 'piper -- bat --color=always --paging=never --style=plain --terminal-width=$w "$1"'
```

Requires yazi ≥ 26.1.22 (piper's `job.file.path` API).

## Scopes

The syntax emits standard TextMate scopes:

| Element | Scope |
|---------|-------|
| Declaration keywords (`pub`, `fn`, `struct`, `impl`, …) | `keyword.declaration.nomi` |
| Control keywords (`if`, `else`, `case`, `return`, …) | `keyword.control.nomi` |
| Word operators (`and`, `or`) | `keyword.operator.word.nomi` |
| `true` / `false` | `constant.language.nomi` |
| `it` | `variable.language.nomi` |
| PascalCase identifiers | `storage.type.nomi` |
| Function calls (`name(...)`) | `variable.function.nomi` |
| Other snake_case identifiers | `variable.other.nomi` |
| Doc comments (`///`) | `comment.block.documentation.nomi` |
| Line comments (`//`) | `comment.line.double-slash.nomi` |
| Strings, escapes, interpolation | `string.quoted.*.nomi`, `constant.character.escape.nomi`, `punctuation.section.interpolation.*.nomi` |
| Numbers (int/hex/bin/float) | `constant.numeric.*.nomi` |

## Embedded grammar in tagged literals — not supported

Tagged-literal bodies (`sql"..."`, `html"..."`, `regex"..."`, etc.) render as plain string content. Adding embedded grammar highlighting would require bundling each target grammar into the syntect pack and emitting `embed:` directives per tag — out of scope today; tree-sitter-driven editors (e.g. Zed via `editors/zed`) handle this via injection queries instead.

## Maintenance

This definition is regex-based and maintained independently of the tree-sitter grammar. When language syntax changes, update this file alongside the `highlights.scm` files in `tree-sitter-nomi/` and `editors/zed/`, and alongside `editors/vscode/syntaxes/nomi.tmLanguage.json`, the VS Code TextMate grammar derived from this file.
