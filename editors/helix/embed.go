// Package helix carries this directory's Helix support into `nomi`, which
// writes it into Helix's configuration with `nomi editor install helix`
// (internal/editors). It is a package of its own, outside internal/, only
// because `//go:embed` reads no file above the embedding package's
// directory; nothing outside this module imports it.
package helix

import "embed"

// Files are languages.toml and the queries under runtime/queries/ (nomi/
// and nomi-catalog/), at the paths they have here.
//
//go:embed languages.toml runtime/queries
var Files embed.FS
