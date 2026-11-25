package vmhost_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nomi-language/nomi/rt"
	"github.com/nomi-language/nomi/vmhost"
)

const deadlockProgram = `import std::channels.{Channel, Receiver}

pub fn wait_forever() -> Option<Int>
  ch = Channel.unbuffered<Int>()
  Receiver.receive(ch.receiver)
end

pub fn add(a: Int, b: Int) -> Int
  a + b
end
`

// A call whose every task waits answers the deadlock's report as its
// error, and the host's process, and the program, carry on: the next call
// runs.
func TestCall_ADeadlockedCallAnswersTheReport(t *testing.T) {
	p, err := vmhost.LoadSource("main", deadlockProgram)
	if err != nil {
		t.Fatal(err)
	}
	ended := make(chan error, 1)
	go func() {
		_, err := p.Call(context.Background(), "wait_forever")
		ended <- err
	}()
	select {
	case err = <-ended:
	case <-time.After(30 * time.Second):
		t.Fatal("the call still waits: the deadlock was not reported")
	}
	if err == nil {
		t.Fatal("a deadlocked call answered no error")
	}
	want := "line 5: " + rt.DeadlockText + "\n  wait_forever waits to receive on a channel at "
	if !strings.HasPrefix(err.Error(), want) || !strings.HasSuffix(err.Error(), ":5") {
		t.Errorf("the call answered %q, want the report beginning %q", err, want)
	}
	got, err := p.Call(context.Background(), "add", 1, 2)
	if err != nil || got != int64(3) {
		t.Errorf("the next call answered %v, %v, want 3", got, err)
	}
}
