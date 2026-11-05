//go:build nomi_stdbundle

package std

import _ "embed"

// embeddedStdInterfaces is the stdlib's interfaces and facts
// (shared.go), which internal/irbuild/stdbundlegen wrote just before the
// build. No stdiface.bin is in the source tree: the build supplies it with
// `go build -overlay`, so a build with the tag and no fresh artifact fails
// to compile rather than embedding a stale one.
//
//go:embed stdiface.bin
var embeddedStdInterfaces []byte
