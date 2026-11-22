package stdcalendar

import (
	"io/fs"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/nomi-language/nomi/rt"
)

// links is a file system of symbolic links for zoneFromFile: a path maps
// to its target, a path in files is a regular file, and any other path is
// not there.
type links struct {
	targets map[string]string
	files   map[string]bool
}

func (l links) readlink(path string) (string, error) {
	if t, ok := l.targets[path]; ok {
		return t, nil
	}
	if l.files[path] {
		return "", &fs.PathError{Op: "readlink", Path: path, Err: syscall.EINVAL}
	}
	return "", &fs.PathError{Op: "readlink", Path: path, Err: fs.ErrNotExist}
}

func TestLocalZone_FromTZ(t *testing.T) {
	sys := links{
		targets: map[string]string{
			"/etc/localtime": "/var/db/timezone/zoneinfo/America/Indiana/Indianapolis",
			"/opt/tz":        "../usr/share/zoneinfo/Europe/Paris",
		},
		files: map[string]bool{"/etc/copied": true},
	}
	for _, c := range []struct {
		tz, zone, err string
	}{
		{tz: "America/New_York", zone: "America/New_York"},
		{tz: ":Asia/Kolkata", zone: "Asia/Kolkata"},
		{tz: "", zone: "UTC"},
		{tz: ":", zone: "UTC"},
		{tz: "UTC", zone: "UTC"},
		{tz: "/usr/share/zoneinfo/Asia/Tokyo", zone: "Asia/Tokyo"},
		{tz: ":/usr/share/zoneinfo/posix/Europe/Berlin", zone: "Europe/Berlin"},
		{tz: "/etc/localtime", zone: "America/Indiana/Indianapolis"},
		{tz: "/opt/tz", zone: "Europe/Paris"},
		{tz: "Mars/Olympus", err: `TZ is "Mars/Olympus", which is no zone in the database`},
		{tz: "Local", err: `TZ is "Local", which is no zone in the database`},
		{tz: "EST5EDT,M3.2.0,M11.1.0", err: "which is no zone in the database"},
		{tz: "/etc/copied", err: "/etc/copied is a zone file with no name"},
		{tz: "/nowhere", err: "/nowhere is not there"},
	} {
		zone, err := zoneFromTZ(c.tz, sys.readlink)
		switch {
		case c.err == "" && (err != nil || zone != c.zone):
			t.Errorf("TZ=%q: %q, %v; want %q", c.tz, zone, err, c.zone)
		case c.err != "" && (err == nil || !strings.Contains(err.Error(), c.err)):
			t.Errorf("TZ=%q: %q, %v; want an error holding %q", c.tz, zone, err, c.err)
		}
	}
}

func TestLocalZone_FromLocaltime(t *testing.T) {
	for _, c := range []struct {
		name      string
		sys       links
		zone, err string
	}{
		{name: "a Linux link", zone: "Europe/Warsaw",
			sys: links{targets: map[string]string{"/etc/localtime": "/usr/share/zoneinfo/Europe/Warsaw"}}},
		{name: "a relative link", zone: "Africa/Lagos",
			sys: links{targets: map[string]string{"/etc/localtime": "../usr/share/zoneinfo/Africa/Lagos"}}},
		{name: "a leap-second tree", zone: "America/Chicago",
			sys: links{targets: map[string]string{"/etc/localtime": "/usr/share/zoneinfo/right/America/Chicago"}}},
		{name: "a link to a link", zone: "Australia/Sydney", sys: links{targets: map[string]string{
			"/etc/localtime":              "/etc/alternatives/localtime",
			"/etc/alternatives/localtime": "/usr/share/zoneinfo/Australia/Sydney",
		}}},
		{name: "no file", zone: "UTC", sys: links{}},
		{name: "a copied file", err: "/etc/localtime is a zone file with no name",
			sys: links{files: map[string]bool{"/etc/localtime": true}}},
		{name: "a link to no zone", err: "/usr/share/zoneinfo/Mars/Olympus is not there",
			sys: links{targets: map[string]string{"/etc/localtime": "/usr/share/zoneinfo/Mars/Olympus"}}},
		{name: "a loop", err: "a chain of more than 16 links", sys: links{targets: map[string]string{
			"/etc/localtime": "/etc/a", "/etc/a": "/etc/localtime",
		}}},
	} {
		zone, err := zoneFromFile("/etc/localtime", true, c.sys.readlink)
		switch {
		case c.err == "" && (err != nil || zone != c.zone):
			t.Errorf("%s: %q, %v; want %q", c.name, zone, err, c.zone)
		case c.err != "" && (err == nil || !strings.Contains(err.Error(), c.err)):
			t.Errorf("%s: %q, %v; want an error holding %q", c.name, zone, err, c.err)
		}
	}
}

// LocalZone reads TZ when it is called, on every system, so a test sets it.
func TestLocalZone_ReadsTZ(t *testing.T) {
	if runtime.GOOS == "js" {
		t.Skip("js/wasm reads the host's Intl, not TZ")
	}
	t.Setenv("TZ", "Pacific/Auckland")
	if zone, err := LocalZone(); err != nil || zone != "Pacific/Auckland" {
		t.Fatalf("LocalZone() under TZ=Pacific/Auckland = %q, %v", zone, err)
	}
	t.Setenv("TZ", "Mars/Olympus")
	if zone, err := LocalZone(); err == nil {
		t.Fatalf("LocalZone() under TZ=Mars/Olympus = %q, want an error", zone)
	}
}

// TestLoadZone_LocalAndEmptyAreNoZones: Go reads "Local" as the machine's
// zone and "" as UTC; neither is an IANA name, and a program reads the
// machine's zone through LocalZone, which a list names.
func TestLoadZone_LocalAndEmptyAreNoZones(t *testing.T) {
	for _, zone := range []string{"Local", ""} {
		if loc, cerr, ok := LoadZone(zone); ok || loc != nil {
			t.Errorf("LoadZone(%q) loaded %v; it must be an unknown zone", zone, loc)
		} else if cerr.Tag != rt.TagUnknownZone {
			t.Errorf("LoadZone(%q) answered tag %v; want UnknownZone", zone, cerr.Tag)
		}
	}
	if _, _, ok := LoadZone("UTC"); !ok {
		t.Error(`LoadZone("UTC") failed`)
	}
}
