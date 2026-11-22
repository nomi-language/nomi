package stdio

import (
	"net"
	"testing"
	"time"

	"github.com/nomi-language/nomi/rt"
)

// A read past the deadline a stream set, such as a connection's
// set_read_deadline, is io.Error's TimedOut, which a read that waits in
// steps (lib/sql/postgres's) tells from a failure.
func TestClassify_APassedDeadlineIsTimedOut(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	if err := a.SetReadDeadline(time.Now().Add(10 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	_, err := a.Read(make([]byte, 1))
	if err == nil {
		t.Fatal("a read with nothing to read returned before its deadline")
	}
	if got := Classify("read", "<connection>", err); got.Tag != rt.TagIOTimedOut {
		t.Fatalf("Classify(%v) = %+v, want TimedOut", err, got)
	}
}
