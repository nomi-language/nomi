package stdhttp

import (
	"net"
	"os"
	"slices"
	"syscall"
	"testing"

	"github.com/nomi-language/nomi/rt"
)

// netErrCase is a system error a socket call returns, wrapped as Go's net
// package wraps it, and the net.Error variant's name it is sorted to.
type netErrCase struct {
	call  string
	errno syscall.Errno
	want  string
}

func checkNetErrs(t *testing.T, system string, cases []netErrCase) {
	t.Helper()
	for _, c := range cases {
		err := &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError(c.call, c.errno)}
		got := strsOf(netErr("dial", "127.0.0.1:1", err))
		want := []string{c.want, "127.0.0.1:1"}
		if !slices.Equal(got, want) {
			t.Errorf("%s errno %d (%s): got %q, want %q", system, uint64(c.errno), c.errno, got, want)
		}
	}
}

// TestNetErr_UnixErrnosNameTheirVariant holds each errno a Unix socket call
// returns to the net.Error variant that names its failure.
func TestNetErr_UnixErrnosNameTheirVariant(t *testing.T) {
	checkNetErrs(t, "Unix", []netErrCase{
		{"connect", syscall.ECONNREFUSED, "Refused"},
		{"connect", syscall.ENETUNREACH, "Unreachable"},
		{"connect", syscall.EHOSTUNREACH, "Unreachable"},
		{"read", syscall.ECONNRESET, "Reset"},
		{"write", syscall.ECONNABORTED, "Reset"},
		{"write", syscall.EPIPE, "Reset"},
		{"bind", syscall.EADDRINUSE, "AddressInUse"},
		{"connect", syscall.ETIMEDOUT, "Timeout"},
	})
}

// TestNetErr_WindowsCodesNameTheUnixVariant holds each Winsock code a
// Windows socket call returns, built by number so it runs on any system, to
// the variant its Unix errno names. Go's syscall.ECONNREFUSED on Windows is
// a number of Go's own, so a comparison with it never matches the
// WSAECONNREFUSED a refused dial carries.
func TestNetErr_WindowsCodesNameTheUnixVariant(t *testing.T) {
	defer rt.ReadWindowsCodesForTest()()
	checkNetErrs(t, "Windows", []netErrCase{
		{"connectex", 10061, "Refused"},     // WSAECONNREFUSED
		{"connectex", 10051, "Unreachable"}, // WSAENETUNREACH
		{"connectex", 10065, "Unreachable"}, // WSAEHOSTUNREACH
		{"wsarecv", 10054, "Reset"},         // WSAECONNRESET
		{"wsasend", 10053, "Reset"},         // WSAECONNABORTED
		{"wsasend", 10058, "Reset"},         // WSAESHUTDOWN
		{"bind", 10048, "AddressInUse"},     // WSAEADDRINUSE
		{"connectex", 10060, "Timeout"},     // WSAETIMEDOUT
	})
	// A receive reads WSAEMSGSIZE, a datagram longer than its buffer, as
	// the errno a Unix system would give, which it names Truncated.
	if got := rt.Errno(os.NewSyscallError("wsarecvfrom", syscall.Errno(10040))); got != syscall.EMSGSIZE {
		t.Errorf("WSAEMSGSIZE reads as errno %d, want EMSGSIZE", uint64(got))
	}
}

// TestNetErr_UnsupportedNamesWhatIsMissing holds a failure for want of a
// socket option or a kind of socket, however Go wraps it, to the
// Unsupported variant with the operation and the reason.
func TestNetErr_UnsupportedNamesWhatIsMissing(t *testing.T) {
	err := &net.OpError{Op: "listen", Net: "tcp", Err: unsupported("windows has no SO_REUSEPORT")}
	got := strsOf(netErr("listen", "127.0.0.1:1", err))
	want := []string{"Unsupported", "listen", "windows has no SO_REUSEPORT"}
	if !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}
