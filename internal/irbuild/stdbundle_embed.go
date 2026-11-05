//go:build nomi_stdbundle

package irbuild

import _ "embed"

// embeddedStdBundle is the std bundle internal/irbuild/stdbundlegen wrote just
// before the build (stdbundle.go). No stdbundle.bin is in the source tree:
// the build supplies it with `go build -overlay`, so a build with the tag and
// no fresh bundle fails to compile rather than embedding a stale one.
//
//go:embed stdbundle.bin
var embeddedStdBundle []byte
