//go:build windows || wasip1

package stdio

// ownerName is nothing: Windows names owners by SID and WASI has none.
func ownerName(uint64, bool) (string, bool) { return "", false }
