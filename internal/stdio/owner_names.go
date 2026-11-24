//go:build !windows && !wasip1 && !(js && wasm)

package stdio

import (
	"os/user"
	"strconv"
)

// ownerName is os/user's lookup of a user or group by number. On macOS it
// asks the system's directory services through libc; on Linux built
// without cgo, as releases are, it reads /etc/passwd and /etc/group, so a
// user only NSS knows (LDAP, systemd-homed) has no name.
func ownerName(id uint64, group bool) (string, bool) {
	key := strconv.FormatUint(id, 10)
	if group {
		g, err := user.LookupGroupId(key)
		if err != nil {
			return "", false
		}
		return g.Name, true
	}
	u, err := user.LookupId(key)
	if err != nil {
		return "", false
	}
	return u.Username, true
}
