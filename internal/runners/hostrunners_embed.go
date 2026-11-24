//go:build nomi_hostrunners

package runners

import _ "embed"

// The host's two runners, gzipped, which internal/runners/hostrunnersgen
// wrote just before the build. No .gz file is in the source tree: the build
// supplies them with `go build -overlay`, so a build with the tag and no
// fresh runners fails to compile rather than embedding stale ones.
var (
	//go:embed nomi-vm.gz
	embeddedFullRunner []byte
	//go:embed nomi-vm-nonet.gz
	embeddedLeanRunner []byte
)

// embeddedRunners are a release nomi's runners for its own platform, gzipped,
// by Variant.BinaryName: the full tier and the lean one (embeddedRunner).
var embeddedRunners = map[string][]byte{
	Variant{}.BinaryName():                embeddedFullRunner,
	Variant{NoNetwork: true}.BinaryName(): embeddedLeanRunner,
}
