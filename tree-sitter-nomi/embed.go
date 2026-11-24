// Package grammar carries the grammar's generated C and its WebAssembly
// build into `nomi`, which writes them where Helix, Neovim and Zed read a
// parser from with `nomi editor install` (internal/editors). It is a
// package of its own, outside internal/, only because `//go:embed` reads no
// file above the embedding package's directory; nothing outside this module
// imports it.
//
// This file belongs to the Nomi repository, not to the grammar:
// scripts/publish-grammar.sh leaves it out of the published repository and
// scripts/sync-zed-grammar.sh out of Zed's grammar checkout.
package grammar

import "embed"

// Files are src/parser.c, src/scanner.c, src/tree_sitter/ (parser.h and its
// LICENSE) and tree-sitter-nomi.wasm, at the paths they have here.
//
//go:embed src/parser.c src/scanner.c src/tree_sitter tree-sitter-nomi.wasm
var Files embed.FS
