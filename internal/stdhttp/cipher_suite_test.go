package stdhttp

import (
	"testing"

	"github.com/nomi-language/nomi/rt"
)

// A suite ID is 16 bits. One past them names no suite: it has no name, and a
// configuration that offers it traps naming it, where truncating 0x11301 to
// 16 bits once named and offered TLS_AES_128_GCM_SHA256.
func TestCipherSuiteIDsPast16Bits(t *testing.T) {
	if got := CipherSuiteName(rt.SmallInt(0x11301)); got != "0x11301" {
		t.Errorf("CipherSuiteName(0x11301) = %q, want 0x11301", got)
	}
	if got := CipherSuiteName(rt.SmallInt(-1)); got != "-0x1" {
		t.Errorf("CipherSuiteName(-1) = %q, want -0x1", got)
	}
	if got := CipherSuiteName(rt.SmallInt(0x1301)); got != "TLS_AES_128_GCM_SHA256" {
		t.Errorf("CipherSuiteName(0x1301) = %q", got)
	}
	msg := func() (msg string) {
		defer func() {
			if p := recover(); p != nil {
				msg = p.(*rt.Error).Msg
			}
		}()
		cipherSuiteIDs(rt.Cons(rt.SmallInt(0xC02F), rt.Cons(rt.SmallInt(0x1C02F), nil)))
		return ""
	}()
	if want := "tls: cipher suite 0x1C02F is past a suite ID's 16 bits, 0x0000 to 0xFFFF"; msg != want {
		t.Errorf("trapped %q, want %q", msg, want)
	}
	if got := cipherSuiteIDs(rt.Cons(rt.SmallInt(0xC02F), nil)); len(got) != 1 || got[0] != 0xC02F {
		t.Errorf("cipherSuiteIDs([0xC02F]) = %v", got)
	}
}
