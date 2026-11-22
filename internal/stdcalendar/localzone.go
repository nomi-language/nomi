package stdcalendar

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"
	"syscall"
)

// The machine's own zone, `DateTime.local_zone`, as an IANA name the
// embedded database holds, so every zone operation takes it as it takes
// any other name. LocalZone (localzone_os.go, localzone_js.go) reads the
// machine; the rules here turn what it read into a name, and take the
// link reader as an argument so a test gives them any file system.
//
// Go's time.Local names no zone: it is "Local" for /etc/localtime and on
// Windows, and it falls back to UTC for a TZ it cannot load. So the name
// is recovered from what Go reads, by the same rules:
//
//   - TZ unset: /etc/localtime, which a system links into its zone
//     database (`/usr/share/zoneinfo/Europe/Paris`,
//     `/var/db/timezone/zoneinfo/Europe/Paris`). The name is the path
//     after the last `zoneinfo/`, without a `posix/` or `right/` subtree.
//     Each link on the way is tried, so `/etc/localtime` linking to a
//     link still names its zone. A system with no /etc/localtime keeps
//     its clocks in UTC, as Go and the C library read it, and so does one
//     whose /etc/localtime the program cannot see (a wasip1 module).
//   - TZ empty: UTC. TZ with a leading colon: the rest.
//   - TZ an absolute path: that file, named as /etc/localtime is.
//   - TZ anything else: a zone name, which the database must hold.
//
// A copied zone file (an /etc/localtime that is a file, not a link) holds
// rules and no name, so it has none to recover; neither has a TZ that
// names no zone in the database, such as a POSIX rule string
// (`EST5EDT,M3.2.0,M11.1.0`). Each is an error that says so, rather than
// UTC, which would read every local time wrong.

// zoneFromTZ is the zone TZ's value names.
func zoneFromTZ(tz string, readlink func(string) (string, error)) (string, error) {
	name := strings.TrimPrefix(tz, ":")
	switch {
	case name == "":
		return "UTC", nil
	case strings.HasPrefix(name, "/"):
		return zoneFromFile(name, false, readlink)
	case isZoneName(name):
		return name, nil
	}
	return "", fmt.Errorf("TZ is %q, which is no zone in the database", tz)
}

// zoneFromFile is the zone a zone file's path, or a link on the way to it,
// names. A missing file is UTC when missingIsUTC (/etc/localtime). The
// paths are a Unix system's, slash-separated on every system.
func zoneFromFile(file string, missingIsUTC bool, readlink func(string) (string, error)) (string, error) {
	at := file
	for range 16 {
		if name := zoneNameInPath(at); name != "" {
			return name, nil
		}
		target, err := readlink(at)
		switch {
		case err == nil:
		case errors.Is(err, syscall.EINVAL):
			// A file, not a link.
			return "", fmt.Errorf("%s is a zone file with no name: it links to no zone in a zoneinfo directory", file)
		case at == file && missingIsUTC:
			// No /etc/localtime the program can see: a system with none
			// keeps UTC, and so does a wasip1 host that shows the module
			// no /etc.
			return "UTC", nil
		case errors.Is(err, fs.ErrNotExist):
			return "", fmt.Errorf("%s is not there", at)
		default:
			return "", fmt.Errorf("%s cannot be read: %v", at, err)
		}
		if !path.IsAbs(target) {
			target = path.Join(path.Dir(at), target)
		}
		at = target
	}
	return "", fmt.Errorf("%s is a chain of more than 16 links", file)
}

// zoneNameInPath is the zone a path into a zoneinfo directory names, or ""
// for a path that is in none or names no zone the database holds.
func zoneNameInPath(file string) string {
	i := strings.LastIndex(file, "/zoneinfo/")
	if i < 0 {
		return ""
	}
	name := file[i+len("/zoneinfo/"):]
	name = strings.TrimPrefix(strings.TrimPrefix(name, "posix/"), "right/")
	if !isZoneName(name) {
		return ""
	}
	return name
}

// isZoneName says whether the database holds a zone of this name. "Local"
// is Go's own name for the machine's zone, which names no zone itself.
func isZoneName(name string) bool {
	if name == "" || name == "Local" {
		return false
	}
	_, _, ok := LoadZone(name)
	return ok
}
