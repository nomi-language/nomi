//go:build !js

package stdcalendar

import (
	"errors"
	"os"
	"runtime"
)

// LocalZone is `DateTime.local_zone`: the IANA name of the zone the machine
// is set to, read when it is called (localzone.go has the rules). TZ comes
// first on every system. Without it a Unix system names its zone by where
// /etc/localtime links. A wasip1 module sees the TZ its host passes it,
// and without one it is UTC, since a host shows it no /etc/localtime to
// read (wasmtime does not, even with /etc mapped in). Windows names its
// zones by names of its own, which Go does not map to IANA names, so there
// a zone comes from TZ alone.
func LocalZone() (string, error) {
	if tz, ok := os.LookupEnv("TZ"); ok {
		return zoneFromTZ(tz, os.Readlink)
	}
	if runtime.GOOS == "windows" {
		return "", errors.New("Windows names its zone by a name of its own, not an IANA one; set TZ to the IANA name")
	}
	return zoneFromFile("/etc/localtime", true, os.Readlink)
}
