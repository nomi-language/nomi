# tree-sitter-nomi

The [tree-sitter](https://tree-sitter.github.io/) grammar for the
[Nomi](https://github.com/nomi-language/nomi) programming language.

This repository is a read-only mirror. The grammar is developed in the
`tree-sitter-nomi/` directory of
[nomi-language/nomi](https://github.com/nomi-language/nomi), and
`scripts/publish-grammar.sh` there publishes it here with `git subtree split`.
Send issues and pull requests to that repository.

The generated parser (`src/parser.c`, `src/scanner.c`) is committed, so editors
can compile it without running `tree-sitter generate`. `queries/` holds the
reference highlight and injection queries; each editor's own queries live
under `editors/` in the main repository.

## License

Licensed under either of the Apache License, Version 2.0
([LICENSE-APACHE](LICENSE-APACHE)) or the MIT license
([LICENSE-MIT](LICENSE-MIT)), at your option. `src/tree_sitter/parser.h` is
tree-sitter's own header, under its MIT license
([src/tree_sitter/LICENSE](src/tree_sitter/LICENSE)).
