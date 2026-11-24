// Package nvim carries this directory's Neovim plugin into `nomi`, which
// writes it into Neovim's package directory with `nomi editor install nvim`
// (internal/editors). It is a package of its own, outside internal/, only
// because `//go:embed` reads no file above the embedding package's
// directory; nothing outside this module imports it.
package nvim

import "embed"

// Files are the plugin, at the paths they have here: what Neovim loads
// (ftdetect/, ftplugin/, lsp/, lua/, plugin/ and the generated queries/)
// and the README. The tests and the parser a checkout compiles into
// parser/ are left out.
//
//go:embed README.md ftdetect ftplugin lsp lua plugin queries
var Files embed.FS
