// Package zed carries this directory's Zed extension, but for its compiled
// Rust, into `nomi`, which writes it into Zed's extensions directory with
// `nomi editor install zed` (internal/editors). It is a package of its own,
// outside internal/, only because `//go:embed` reads no file above the
// embedding package's directory; nothing outside this module imports it.
package zed

import "embed"

// Files are extension.toml, languages/ and debug_adapter_schemas/, at the
// paths they have here. The extension's WebAssembly, built from src/ with
// cargo, is carried only by a release `nomi` (internal/editors,
// zedwasm_embed.go), and the grammar's by package grammar.
//
//go:embed extension.toml languages debug_adapter_schemas
var Files embed.FS
