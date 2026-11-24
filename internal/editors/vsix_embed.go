//go:build nomi_vsix

package editors

import _ "embed"

// vsix is the VS Code extension, editors/vscode/nomi.vsix, which
// scripts/release.sh builds and internal/runners/hostrunnersgen -vsix
// stages just before the build. No nomi.vsix is in the source tree: the
// build supplies it with `go build -overlay`, so a build with the tag and
// no extension fails to compile rather than embedding a stale one.
//
//go:embed nomi.vsix
var vsix []byte
