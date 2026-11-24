//go:build !darwin && !(js && wasm)

package stdio

import "os"

// linkName makes name a hard link to target: os.Link, which follows no
// symlink at target here.
func linkName(target, name string) error { return os.Link(target, name) }
