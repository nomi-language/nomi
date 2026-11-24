//go:build !darwin && !linux && !(js && wasm)

package stdio

import (
	"os"
	"time"
)

func accessTime(os.FileInfo) (time.Time, bool) { return time.Time{}, false }
func linkCount(os.FileInfo) (uint64, bool)     { return 0, false }
