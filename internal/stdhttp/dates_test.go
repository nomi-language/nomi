package stdhttp

import (
	"strings"
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/nomi-language/nomi/rt"
)

// processZones are time zones the process may run in. An HTTP date means
// the same moment in each: America/New_York names EST and EDT, which Go's
// time.Parse would otherwise read against it.
var processZones = []string{"UTC", "America/New_York", "Asia/Tokyo"}

// sameInEveryZone runs f with time.Local set to each of processZones and
// returns its answer, failing the test where two zones answer differently.
func sameInEveryZone(t *testing.T, what string, f func() string) string {
	t.Helper()
	saved := time.Local
	defer func() { time.Local = saved }()
	var first string
	for i, name := range processZones {
		loc, err := time.LoadLocation(name)
		if err != nil {
			t.Fatal(err)
		}
		time.Local = loc
		got := f()
		if i == 0 {
			first = got
		} else if got != first {
			t.Errorf("%s: %q under %s, %q under %s", what, first, processZones[0], got, name)
		}
	}
	return first
}

// sunday is the moment RFC 9110's examples name, in nanoseconds.
var sunday = time.Date(1994, 11, 6, 8, 49, 37, 0, time.UTC).UnixNano()

// Each of the three HTTP-date forms is GMT whatever zone abbreviation it
// carries and whatever zone the process runs in (RFC 9110 section 5.6.7: a
// recipient MUST read an obsolete form as GMT).
func TestParseTime_IsGMTInEveryZone(t *testing.T) {
	for _, text := range []string{
		"Sun, 06 Nov 1994 08:49:37 GMT",  // IMF-fixdate
		"Sunday, 06-Nov-94 08:49:37 GMT", // RFC 850
		"Sunday, 06-Nov-94 08:49:37 EST", // RFC 850 naming another zone
		"Sunday, 06-Nov-94 08:49:37 EDT",
		"Sun Nov  6 08:49:37 1994", // asctime
	} {
		got := sameInEveryZone(t, text, func() string {
			r := ParseTime(text)
			if r.Tag != rt.TagOk {
				return "Err " + r.Err
			}
			return r.Ok.String()
		})
		if want := rt.SmallInt(sunday).String(); got != want {
			t.Errorf("ParseTime(%q) = %s, want %s", text, got, want)
		}
	}
}

// A cookie's Expires is UTC whatever zone token it names (RFC 6265
// section 5.1.1), in either form Go reads.
func TestParseSetCookie_ExpiresIsUTCInEveryZone(t *testing.T) {
	for _, text := range []string{
		"id=7; Expires=Sun, 06 Nov 1994 08:49:37 GMT",
		"id=7; Expires=Sun, 06 Nov 1994 08:49:37 EST",
		"id=7; Expires=Sun, 06-Nov-1994 08:49:37 EST",
	} {
		got := sameInEveryZone(t, text, func() string {
			r := ParseSetCookie(text)
			if r.Tag != rt.TagOk {
				return "Err " + r.Err
			}
			return strs(r.Ok)[5]
		})
		if want := rt.SmallInt(sunday).String(); got != want {
			t.Errorf("expires of %q = %s, want %s", text, got, want)
		}
	}
	// An expiry neither form reads crosses as raw_expires alone.
	r := ParseSetCookie("id=7; Expires=whenever")
	if parts := strs(r.Ok); r.Tag != rt.TagOk || parts[5] != "" || parts[6] != "whenever" {
		t.Errorf("unreadable expiry crosses as %q", parts)
	}
}

// ServeContent's conditional headers are GMT too: an If-Modified-Since in
// RFC 850 form naming EST, two hours before the content changed, is a 200
// in every zone, where Go's ParseTime would make it a 304 under New York.
func TestAnswerContent_ConditionalDatesAreGMTInEveryZone(t *testing.T) {
	modified := rt.SmallInt(sunday + int64(2*time.Hour))
	for _, h := range []string{"If-Modified-Since", "If-Unmodified-Since"} {
		headers := listOf([]string{h, "Sunday, 06-Nov-94 08:49:37 EST"})
		got := sameInEveryZone(t, h, func() string {
			return strs(AnswerContent("GET", "/f", headers, "f.txt", modified, nil, rt.Bytes("hello")))[1]
		})
		want := map[string]string{"If-Modified-Since": "200", "If-Unmodified-Since": "412"}[h]
		if got != want {
			t.Errorf("%s: status %s, want %s", h, got, want)
		}
	}
	// If-Range naming the moment of the change sends the range asked for.
	headers := listOf([]string{"Range", "bytes=1-2", "If-Range", "Sunday, 06-Nov-94 10:49:37 EST"})
	got := sameInEveryZone(t, "If-Range", func() string {
		return strings.Join(strs(AnswerContent("GET", "/f", headers, "f.txt", modified, nil, rt.Bytes("hello")))[1:3], " ")
	})
	if got != "206 el" {
		t.Errorf("If-Range: %q, want 206 el", got)
	}
}
