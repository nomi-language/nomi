//go:build js

package stdcalendar

import (
	"errors"
	"fmt"
	"syscall/js"
)

// LocalZone is `DateTime.local_zone`: in a browser or Node, the zone the
// host's Intl reads its clocks in,
// `Intl.DateTimeFormat().resolvedOptions().timeZone`, which a browser takes
// from the system and Node from TZ or the system.
func LocalZone() (string, error) {
	intl := js.Global().Get("Intl")
	if intl.Type() != js.TypeObject || intl.Get("DateTimeFormat").Type() != js.TypeFunction {
		return "", errors.New("the host has no Intl.DateTimeFormat to name its zone")
	}
	zone := intl.Get("DateTimeFormat").Invoke().Call("resolvedOptions").Get("timeZone")
	if zone.Type() != js.TypeString {
		return "", errors.New("the host's Intl names no zone")
	}
	name := zone.String()
	if !isZoneName(name) {
		return "", fmt.Errorf("the host's zone is %q, which is no zone in the database", name)
	}
	return name, nil
}
