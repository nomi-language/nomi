package stdio

import (
	"os"

	"golang.org/x/sys/unix"
)

// linkName makes name a hard link to target. macOS's link(2), which
// os.Link calls, follows a symlink at target; linkat with no flags does
// not, as Linux's link(2) and an *os.Root's Link do not, so a symlink is
// linked as itself everywhere.
func linkName(target, name string) error {
	if err := unix.Linkat(unix.AT_FDCWD, target, unix.AT_FDCWD, name, 0); err != nil {
		return &os.LinkError{Op: "link", Old: target, New: name, Err: err}
	}
	return nil
}
