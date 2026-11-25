//go:build unix

package rt

import (
	"context"
	"os"
	"syscall"
	"testing"
	"time"
)

// These tests send signals to the test process itself, only while a feed
// catches them: a SIGUSR1 nothing catches would end the run.

// feed subscribes a fresh unbuffered channel to sig on a frame of ctx and
// answers its receiving half.
func feed(t *testing.T, ctx context.Context, sig os.Signal, value string) (*Frame, Receiver[string]) {
	t.Helper()
	fr := NewFrame(ctx)
	ch := ChannelUnbuffered[string]()
	SubscribeSignals(fr, ch.Sender, map[os.Signal]func() string{sig: func() string { return value }})
	return fr, ch.Receiver
}

func raise(t *testing.T, sig syscall.Signal) {
	t.Helper()
	if err := syscall.Kill(os.Getpid(), sig); err != nil {
		t.Fatal(err)
	}
}

// within is what a receive on r answers within wait: the value, "closed"
// for a closed channel, or "" when nothing came.
func within(fr *Frame, r Receiver[string], wait time.Duration) string {
	got := ReceiverReceiveWithin(fr, Site{}, r, Duration(wait))
	switch {
	case got.Tag != TagOk:
		return ""
	case got.Ok.Tag == TagSome:
		return got.Ok.Some
	default:
		return "closed"
	}
}

func caught(sig os.Signal) bool {
	signalRegistry.mu.Lock()
	defer signalRegistry.mu.Unlock()
	return signalRegistry.watches[sig] != nil
}

// Each arrival is one value, and the process keeps running.
func TestSignals_EachArrivalIsAValue(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fr, r := feed(t, ctx, syscall.SIGUSR1, "usr1")
	for i := range 3 {
		raise(t, syscall.SIGUSR1)
		if got := within(fr, r, 10*time.Second); got != "usr1" {
			t.Fatalf("arrival %d answered %q", i, got)
		}
	}
}

// A burst that arrives while a value waits unreceived is that one value.
func TestSignals_ABurstWhileAValueWaitsIsOneValue(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fr, r := feed(t, ctx, syscall.SIGUSR1, "usr1")
	raise(t, syscall.SIGUSR1)
	// The rest of the burst arrives once the first arrival's value waits.
	deadline := time.Now().Add(10 * time.Second)
	for {
		signalRegistry.mu.Lock()
		w := signalRegistry.watches[syscall.SIGUSR1]
		pending := false
		for p := range w.ports {
			pending = p.pending.Load()
		}
		signalRegistry.mu.Unlock()
		if pending {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the first arrival never became a waiting value")
		}
		time.Sleep(time.Millisecond)
	}
	for range 4 {
		raise(t, syscall.SIGUSR1)
	}
	time.Sleep(200 * time.Millisecond)
	if got := within(fr, r, 10*time.Second); got != "usr1" {
		t.Fatalf("the burst answered %q", got)
	}
	if got := within(fr, r, 200*time.Millisecond); got != "" {
		t.Fatalf("a burst of five answered a second value %q", got)
	}
}

// Two feeds that want one signal each get each arrival, and one closing
// leaves the other fed; the last one closing stops catching the signal.
func TestSignals_TwoFeedsEachGetEachArrival(t *testing.T) {
	ctxA, cancelA := context.WithCancel(context.Background())
	defer cancelA()
	ctxB, cancelB := context.WithCancel(context.Background())
	defer cancelB()
	frA, a := feed(t, ctxA, syscall.SIGUSR2, "a")
	frB, b := feed(t, ctxB, syscall.SIGUSR2, "b")
	raise(t, syscall.SIGUSR2)
	if got := within(frA, a, 10*time.Second); got != "a" {
		t.Fatalf("the first feed answered %q", got)
	}
	if got := within(frB, b, 10*time.Second); got != "b" {
		t.Fatalf("the second feed answered %q", got)
	}

	cancelB()
	if got := within(frA, b, 10*time.Second); got != "closed" {
		t.Fatalf("the feed whose owner ended answered %q, not a closed channel", got)
	}
	raise(t, syscall.SIGUSR2)
	if got := within(frA, a, 10*time.Second); got != "a" {
		t.Fatalf("the remaining feed answered %q", got)
	}

	cancelA()
	if got := within(NewFrame(context.Background()), a, 10*time.Second); got != "closed" {
		t.Fatalf("the last feed answered %q, not a closed channel", got)
	}
	if caught(syscall.SIGUSR2) {
		t.Fatal("SIGUSR2 is still caught after its last feed closed; it should have its default action back")
	}
}

// The drain closes a feed and keeps its signal caught, so an arrival during
// shutdown is dropped; the reset after the program's cleanup gives the
// signal its default action back.
func TestSignals_TheDrainClosesAFeedAndDropsArrivals(t *testing.T) {
	defer ResetSupervisors()
	fr, r := feed(t, context.Background(), syscall.SIGUSR1, "usr1")
	DrainSupervisors()
	if got := within(fr, r, 10*time.Second); got != "closed" {
		t.Fatalf("a feed the drain closed answered %q", got)
	}
	if !caught(syscall.SIGUSR1) {
		t.Fatal("the drain stopped catching SIGUSR1, so one arriving during shutdown would end the process")
	}
	raise(t, syscall.SIGUSR1)
	time.Sleep(100 * time.Millisecond)
	ResetSupervisors()
	if caught(syscall.SIGUSR1) {
		t.Fatal("SIGUSR1 is still caught after the reset")
	}
}

// A feed made after the drain started is closed at once.
func TestSignals_AFeedMadeDuringTheDrainIsClosed(t *testing.T) {
	defer ResetSupervisors()
	DrainSupervisors()
	fr, r := feed(t, context.Background(), syscall.SIGUSR1, "usr1")
	if got := within(fr, r, 10*time.Second); got != "closed" {
		t.Fatalf("a feed made after the drain answered %q", got)
	}
	ResetSupervisors()
	if caught(syscall.SIGUSR1) {
		t.Fatal("SIGUSR1 is still caught after the reset")
	}
}

// A feed a virtual-clock case makes after its cleanup is closed at once and
// starts no watch. A watch it started would hold a channel of the case's
// bubble, which the next case's ReleaseSignals would close from outside it.
func TestSignals_AFeedMadeAfterACaseEndedStartsNoWatch(t *testing.T) {
	ResetSupervisors()
	defer ResetSupervisors()

	var got string
	err := RunBubbled(bubbleThen(func() {
		fr, r := feed(t, context.Background(), syscall.SIGUSR1, "usr1")
		got = within(fr, r, 0)
	}), func() error { return nil }, nil)
	if err != nil {
		t.Fatalf("the case: %v", err)
	}
	if got != "closed" {
		t.Fatalf("a feed made after the case's cleanup answered %q", got)
	}
	if caught(syscall.SIGUSR1) {
		t.Fatal("a feed made after the case's cleanup left SIGUSR1 caught")
	}
}

// A system with these signals names each, and refuses a name it has not.
func TestSignals_NamesAreTheSystems(t *testing.T) {
	for name, want := range map[string]os.Signal{"SIGHUP": syscall.SIGHUP, "SIGUSR1": syscall.SIGUSR1, "SIGUSR2": syscall.SIGUSR2, "SIGWINCH": syscall.SIGWINCH} {
		if got, why := SignalNamed(name); got != want || why != "" {
			t.Errorf("%s is %v (%q)", name, got, why)
		}
	}
	if _, why := SignalNamed("SIGTERM"); why != "no signal is named SIGTERM" {
		t.Errorf("SIGTERM, which is shutdown's, answered %q", why)
	}
}

// A feed that wants no signal is closed at once.
func TestSignals_AFeedOfNoSignalIsClosed(t *testing.T) {
	fr := NewFrame(context.Background())
	ch := ChannelUnbuffered[string]()
	SubscribeSignals(fr, ch.Sender, map[os.Signal]func() string{})
	if got := within(fr, ch.Receiver, 10*time.Second); got != "closed" {
		t.Fatalf("a feed of no signal answered %q", got)
	}
}

// A terminal resize, SIGWINCH by its name, is a value on a feed as the other
// signals are.
func TestSignals_AWindowChangeIsAValue(t *testing.T) {
	sig, why := SignalNamed("SIGWINCH")
	if why != "" {
		t.Fatalf("SIGWINCH is not a signal a program receives: %s", why)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fr, r := feed(t, ctx, sig, "winch")
	raise(t, syscall.SIGWINCH)
	if got := within(fr, r, 10*time.Second); got != "winch" {
		t.Fatalf("a SIGWINCH answered %q", got)
	}
}
