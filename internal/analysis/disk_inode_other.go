//go:build !unix

package analysis

import "os"

// inodeOf is 0: the platform's stat has no inode number.
func inodeOf(os.FileInfo) uint64 {
	return 0
}
