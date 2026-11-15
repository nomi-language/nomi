package stdhttp

import (
	"crypto/x509"
	"math/big"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/nomi-language/nomi/rt"
)

// forever is a date servers send as "never", past the years Go's UnixNano
// counts (2262).
var forever = time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)

// An HTTP date of any year crosses as its exact nanoseconds, never wrapped
// by UnixNano into a moment in 1816; text Go cannot parse is refused with
// Go's reason.
func TestParseTime_AnyYearIsExact(t *testing.T) {
	text := forever.Format(http.TimeFormat)
	if got := ParseTime(text); got.Tag != rt.TagOk || got.Ok.String() != "253402300799000000000" {
		t.Errorf("ParseTime(%q) = %+v", text, got)
	}
	in := "Sun, 06 Nov 1994 08:49:37 GMT"
	if got := ParseTime(in); got.Tag != rt.TagOk || got.Ok != rt.SmallInt(time.Date(1994, 11, 6, 8, 49, 37, 0, time.UTC).UnixNano()) {
		t.Errorf("ParseTime(%q) = %+v", in, got)
	}
	if got := ParseTime("yesterday"); got.Tag == rt.TagOk || !strings.Contains(got.Err, "cannot parse") {
		t.Errorf("ParseTime(%q) = %+v", "yesterday", got)
	}
}

// A cookie expiring in 9999 crosses with its expiry's nanoseconds and goes
// back to Go with that expiry, so a Set-Cookie written from it keeps it.
func TestCookie_ExpiryOfAnyYearCrosses(t *testing.T) {
	c, err := http.ParseSetCookie("id=7; Expires=" + forever.Format(time.RFC1123))
	if err != nil {
		t.Fatal(err)
	}
	parts := cookieParts(c)
	if parts[5] != "253402300799000000000" || parts[6] != forever.Format(time.RFC1123) {
		t.Fatalf("expires %q, raw_expires %q", parts[5], parts[6])
	}
	back := cookieOf(parts)
	if !back.Expires.Equal(forever) {
		t.Errorf("back to Go: Expires %v, want %v", back.Expires, forever)
	}
	if s := back.String(); !strings.Contains(s, "Expires=Fri, 31 Dec 9999 23:59:59 GMT") {
		t.Errorf("Set-Cookie %q", s)
	}
	// An expiry Go cannot parse crosses as raw_expires alone, as Go keeps it.
	c, err = http.ParseSetCookie("id=7; Expires=whenever")
	if err != nil {
		t.Fatal(err)
	}
	parts = cookieParts(c)
	if parts[5] != "" || parts[6] != "whenever" {
		t.Fatalf("expires %q, raw_expires %q", parts[5], parts[6])
	}
	if back := cookieOf(parts); !back.Expires.IsZero() {
		t.Errorf("back to Go: Expires %v, want none", back.Expires)
	}
}

// A certificate's validity crosses as the instants' nanoseconds, so one
// valid until 9999-12-31, which X.509 calls "no well-defined expiration",
// reads as that moment rather than a wrapped one.
func TestCertInfo_ValidityOfAnyYearIsExact(t *testing.T) {
	one := big.NewInt(1)
	got := certInfo(&x509.Certificate{NotBefore: time.Date(1600, 1, 1, 0, 0, 0, 0, time.UTC), NotAfter: forever, SerialNumber: one})
	if got[3] != "-11676096000000000000" || got[4] != "253402300799000000000" {
		t.Errorf("validity %q .. %q", got[3], got[4])
	}
	zone := time.FixedZone("", 5*3600)
	got = certInfo(&x509.Certificate{NotBefore: time.Date(1970, 1, 1, 5, 0, 0, 0, zone), NotAfter: forever, SerialNumber: one})
	if got[3] != "0" {
		t.Errorf("not_before %q, want the epoch", got[3])
	}
}
