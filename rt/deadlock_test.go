package rt

import (
	"context"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// The task registry's deadlock report (package doc, "Deadlock"): each shape
// of deadlock is reported to its main line with every task's wait, each wait
// that something outside the tasks could end is not reported while it could,
// and tasks handing off at speed are never reported.

// at is a Site in a test's program, p.nomi.
func at(line int) Site { return Site{File: "p.nomi", Line: line} }

// mainRun is a main line running body on a goroutine of its own.
type mainRun struct {
	fr    *Frame
	ended chan *Error
}

// settleRegistry waits for the tasks earlier tests left to end: a record
// with no main line, or one still running, stops every report.
func settleRegistry(t *testing.T) {
	t.Helper()
	for range 10000 {
		tasks.mu.Lock()
		empty := tasks.first == nil
		tasks.mu.Unlock()
		if empty {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("tasks of an earlier test are still recorded")
}

// startMainLine runs body as a main line named main.
func startMainLine(t *testing.T, body func(fr *Frame)) *mainRun {
	t.Helper()
	settleRegistry(t)
	fr := NewFrame(context.Background())
	end := MainLine(fr, "main")
	r := &mainRun{fr: fr, ended: make(chan *Error, 1)}
	go func() {
		defer end()
		r.ended <- recoverFault(func() { body(fr) })
	}()
	return r
}

// fault is what the main line ended with. A main line still waiting long
// after it should have ended is a report that never came, so the guard
// fails the test rather than hanging it.
func (r *mainRun) fault(t *testing.T) *Error {
	t.Helper()
	select {
	case err := <-r.ended:
		return err
	case <-time.After(30 * time.Second):
		t.Fatal("the main line still waits: no deadlock was reported")
		return nil
	}
}

// waitsNow waits until the main line is blocked with no record running,
// and answers whether it is still waiting, unreported.
func (r *mainRun) waitsNow(t *testing.T) bool {
	t.Helper()
	for range 10000 {
		tasks.mu.Lock()
		still := r.fr.task.blocked && tasks.running == 0
		tasks.mu.Unlock()
		if still {
			return r.fr.task.deadlock == ""
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("the main line never blocked")
	return false
}

func reportOf(t *testing.T, err *Error) string {
	t.Helper()
	if err == nil {
		t.Fatal("the main line ended with no fault")
	}
	return err.Msg
}

func wantReport(t *testing.T, got string, lines ...string) {
	t.Helper()
	want := DeadlockText + "\n  " + strings.Join(lines, "\n  ")
	if got != want {
		t.Errorf("the report is\n%s\nwant\n%s", got, want)
	}
}

// A main line alone on a channel nobody sends on.
func TestDeadlock_AReceiveNobodySendsOnIsReported(t *testing.T) {
	r := startMainLine(t, func(fr *Frame) {
		ch := ChannelUnbuffered[int64]()
		ReceiverReceive(fr, at(9), ch.Receiver)
	})
	wantReport(t, reportOf(t, r.fault(t)), "main waits to receive on a channel at p.nomi:9")
}

// A send nobody receives.
func TestDeadlock_ASendNobodyReceivesIsReported(t *testing.T) {
	r := startMainLine(t, func(fr *Frame) {
		ch := ChannelBuffered[int64](1)
		SenderSend(fr, at(3), ch.Sender, 1)
		SenderSend(fr, at(4), ch.Sender, 2)
	})
	wantReport(t, reportOf(t, r.fault(t)), "main waits to send on a channel at p.nomi:4")
}

// A select over channels nobody feeds.
func TestDeadlock_ASelectNothingCanFinishIsReported(t *testing.T) {
	r := startMainLine(t, func(fr *Frame) {
		a, b := ChannelUnbuffered[int64](), ChannelBuffered[int64](1)
		SenderSend(fr, at(2), b.Sender, 1)
		Select(fr, at(5), []SelectCase{SelectReceive(a.Receiver), SelectSend(b.Sender, 2)})
	})
	wantReport(t, reportOf(t, r.fault(t)), "main waits in Channel.select at p.nomi:5")
}

// The pipeline whose last channel nobody closes: the consumer waits for a
// value, and the main line waits for the consumer.
func TestDeadlock_AConsumerOfAChannelNobodyClosesIsReported(t *testing.T) {
	r := startMainLine(t, func(fr *Frame) {
		scope := EnterScope(fr)
		defer ScopeExit(scope, at(20))
		cooked := ChannelBuffered[int64](4)
		producer := TaskSpawn(scope, at(21), func(tf *Frame) Unit {
			for i := range int64(3) {
				SenderSend(tf, at(11), cooked.Sender, i)
			}
			return Unit{}
		})
		consumer := TaskSpawn(scope, at(22), func(tf *Frame) int64 {
			sum := int64(0)
			for {
				got := ReceiverReceive(tf, at(15), cooked.Receiver)
				if got.Tag != TagSome {
					return sum
				}
				sum += got.Some
			}
		})
		TaskAwait(scope, at(23), producer)
		TaskAwait(scope, at(24), consumer)
	})
	wantReport(t, reportOf(t, r.fault(t)),
		"main waits for a task to end at p.nomi:24",
		"the task spawned at p.nomi:22 waits to receive on a channel at p.nomi:15")
}

// A task traps before it reports, and its collector waits for the report.
func TestDeadlock_ACollectorOfATrappedTaskIsReported(t *testing.T) {
	r := startMainLine(t, func(fr *Frame) {
		scope := EnterScope(fr)
		defer ScopeExit(scope, at(30))
		done := ChannelBuffered[int64](4)
		_ = TaskSpawnAll(scope, at(31), ListSeq(listFromSlice([]int64{1, 2, 3})), func(tf *Frame, n int64) Unit {
			if n == 2 {
				Trap("line 12: found nothing")
			}
			SenderSend(tf, at(40), done.Sender, n)
			return Unit{}
		}, 3)
		for line := 32; line <= 34; line++ {
			ReceiverReceive(scope, at(line), done.Receiver)
		}
	})
	wantReport(t, reportOf(t, r.fault(t)), "main waits to receive on a channel at p.nomi:34")
}

// A task waiting for a `max_running` slot its sibling never gives back.
func TestDeadlock_ATaskWaitingForASlotIsReported(t *testing.T) {
	r := startMainLine(t, func(fr *Frame) {
		scope := EnterScope(fr)
		defer ScopeExit(scope, at(50))
		never := ChannelUnbuffered[int64]()
		batch := TaskSpawnAll(scope, at(51), ListSeq(listFromSlice([]int64{1, 2})), func(tf *Frame, n int64) Unit {
			ReceiverReceive(tf, at(60), never.Receiver)
			return Unit{}
		}, 1)
		TaskAwaitAll(scope, at(52), batch)
	})
	err := reportOf(t, r.fault(t))
	for _, line := range []string{
		"main waits for a task to end at p.nomi:52",
		"the task spawned at p.nomi:51 waits to receive on a channel at p.nomi:60",
		"the task spawned at p.nomi:51 waits for a max_running slot at p.nomi:51",
	} {
		if !strings.Contains(err, "\n  "+line) {
			t.Errorf("the report has no line %q:\n%s", line, err)
		}
	}
}

// A task waiting for a `once` another task forces, while that force waits
// on a channel nobody sends on.
func TestDeadlock_ATaskWaitingForAOnceIsReported(t *testing.T) {
	r := startMainLine(t, func(fr *Frame) {
		scope := EnterScope(fr)
		defer ScopeExit(scope, at(70))
		never := ChannelUnbuffered[int64]()
		cell := NewOnceCell[int64]("config")
		forcing := make(chan struct{})
		rhs := func(rf *Frame) int64 {
			close(forcing)
			return ReceiverReceive(rf, at(80), never.Receiver).Some
		}
		first := TaskSpawn(scope, at(71), func(tf *Frame) int64 { return cell.Get(tf, at(81), rhs) })
		<-forcing
		second := TaskSpawn(scope, at(72), func(tf *Frame) int64 { return cell.Get(tf, at(82), rhs) })
		TaskAwait(scope, at(73), first)
		TaskAwait(scope, at(74), second)
	})
	wantReport(t, reportOf(t, r.fault(t)),
		"main waits for a task to end at p.nomi:73",
		"the task spawned at p.nomi:71 waits to receive on a channel at p.nomi:80",
		"the task spawned at p.nomi:72 waits for a once value another task is computing at p.nomi:82")
}

// Every kind of wait reads in a report: waitKind is the closed set, and a
// kind with no text would leave its line blank.
func TestDeadlock_EveryWaitKindReads(t *testing.T) {
	seen := map[string]waitKind{}
	for k := waitKind(1); k < waitKinds; k++ {
		text := k.text()
		if text == "" {
			t.Errorf("wait kind %d has no text", k)
		}
		if other, dup := seen[text]; dup {
			t.Errorf("wait kinds %d and %d read the same: %q", other, k, text)
		}
		seen[text] = k
	}
}

// Under a virtual clock the registry reports the deadlock before the bubble
// finds every goroutine blocked, with the same waits.
func TestDeadlock_UnderAVirtualClockIsReported(t *testing.T) {
	settleRegistry(t)
	synctest.Test(t, func(t *testing.T) {
		fr := NewFrame(context.Background())
		end := MainLine(fr, "the test")
		defer end()
		err := recoverFault(func() {
			ch := ChannelUnbuffered[int64]()
			ReceiverReceive(fr, at(5), ch.Receiver)
		})
		wantReport(t, reportOf(t, err), "the test waits to receive on a channel at p.nomi:5")
	})
}

// A select with a timer could end: nothing is reported, and the timer's
// case wins when its time comes.
func TestDeadlock_ASelectWithATimerIsNotReported(t *testing.T) {
	settleRegistry(t)
	synctest.Test(t, func(t *testing.T) {
		fr := NewFrame(context.Background())
		end := MainLine(fr, "the test")
		defer end()
		ch := ChannelUnbuffered[int64]()
		var got Selected
		if err := recoverFault(func() {
			got = Select(fr, at(5), []SelectCase{SelectReceive(ch.Receiver), SelectAfter(Duration(time.Hour))})
		}); err != nil {
			t.Fatalf("a select with a timer faulted: %s", err.Msg)
		}
		if got.Index != 1 {
			t.Errorf("the select answered case %d, want the timer's, 1", got.Index)
		}
	})
}

// A wait under a deadline could end: nothing is reported, and the deadline
// stops the wait when it passes.
func TestDeadlock_AWaitUnderADeadlineIsNotReported(t *testing.T) {
	settleRegistry(t)
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
		defer cancel()
		fr := NewFrame(ctx)
		end := MainLine(fr, "the test")
		defer end()
		err := recoverFault(func() {
			scope := EnterScope(fr)
			defer ScopeExit(scope, at(4))
			ch := ChannelUnbuffered[int64]()
			h := TaskSpawn(scope, at(5), func(tf *Frame) Option[int64] { return ReceiverReceive(tf, at(6), ch.Receiver) })
			TaskOutcomeOf(scope, at(7), h)
		})
		if err == nil || strings.HasPrefix(err.Msg, DeadlockText) {
			t.Fatalf("a wait under a deadline ended with %v, want the deadline's cancellation", err)
		}
	})
}

// A channel a close_on_drain hook feeds could close: nothing is reported
// while the hook is armed, and the drain's close ends the wait.
func TestDeadlock_AChannelTheDrainClosesIsNotReported(t *testing.T) {
	ResetSupervisors()
	defer ResetSupervisors()
	ch := ChannelBuffered[int64](1)
	SenderCloseOnDrain(ch.Sender)
	var got Option[int64]
	r := startMainLine(t, func(fr *Frame) {
		got = ReceiverReceive(fr, at(9), ch.Receiver)
	})
	if !r.waitsNow(t) {
		t.Fatal("a wait on a channel the drain closes was reported")
	}
	DrainSupervisors()
	if err := r.fault(t); err != nil {
		t.Fatalf("the main line faulted: %s", err.Msg)
	}
	if got.Tag != TagNone {
		t.Errorf("the drained channel answered %v, want None", got)
	}
}

// A task in a call into Go runs: nothing is reported while it is there, and
// the value it sends once it returns ends the main line's wait.
func TestDeadlock_ATaskInACallIntoGoIsNotReported(t *testing.T) {
	gate := make(chan struct{})
	var got Option[int64]
	r := startMainLine(t, func(fr *Frame) {
		scope := EnterScope(fr)
		defer ScopeExit(scope, at(2))
		ch := ChannelUnbuffered[int64]()
		h := TaskSpawn(scope, at(3), func(tf *Frame) Unit {
			<-gate // a host function waiting on the outside world
			SenderSend(tf, at(5), ch.Sender, 7)
			return Unit{}
		})
		got = ReceiverReceive(scope, at(6), ch.Receiver)
		TaskAwait(scope, at(7), h)
	})
	for range 10000 {
		tasks.mu.Lock()
		blocked := r.fr.task.blocked
		running := tasks.running
		tasks.mu.Unlock()
		if blocked {
			if running != 1 {
				t.Fatalf("with the task in Go, %d records run, want 1", running)
			}
			break
		}
		time.Sleep(time.Millisecond)
	}
	close(gate)
	if err := r.fault(t); err != nil {
		t.Fatalf("the main line faulted: %s", err.Msg)
	}
	if got.Tag != TagSome || got.Some != 7 {
		t.Errorf("the main line received %v, want Some(7)", got)
	}
}

// While a wake source outside the tasks is armed (a drain) nothing is
// reported, and once it is disarmed the deadlock is.
func TestDeadlock_NothingIsReportedWhileASourceIsArmed(t *testing.T) {
	tasks.arm()
	disarmed := false
	defer func() {
		if !disarmed {
			tasks.disarm()
		}
	}()
	r := startMainLine(t, func(fr *Frame) {
		ch := ChannelUnbuffered[int64]()
		ReceiverReceive(fr, at(3), ch.Receiver)
	})
	if !r.waitsNow(t) {
		t.Fatal("a deadlock was reported while a drain ran")
	}
	disarmed = true
	tasks.disarm()
	wantReport(t, reportOf(t, r.fault(t)), "main waits to receive on a channel at p.nomi:3")
}

// The false-report guard: senders and receivers hand off over an
// unbuffered channel, a buffered one and selects, many times over, each
// side blocking and waking the other, and the registry never reports.
func TestDeadlock_HandOffsAtSpeedAreNeverReported(t *testing.T) {
	const pairs, each = 8, 2000
	var mu sync.Mutex
	sum := int64(0)
	r := startMainLine(t, func(fr *Frame) {
		scope := EnterScope(fr)
		defer ScopeExit(scope, at(1))
		rendezvous := ChannelUnbuffered[int64]()
		buffered := ChannelBuffered[int64](2)
		var senders, receivers []Task[Unit]
		for range pairs {
			senders = append(senders, TaskSpawn(scope, at(2), func(tf *Frame) Unit {
				for i := range int64(each) {
					if i%2 == 0 {
						SenderSend(tf, at(3), rendezvous.Sender, 1)
					} else {
						Select(tf, at(4), []SelectCase{SelectSend(buffered.Sender, 1), SelectSend(rendezvous.Sender, 1)})
					}
				}
				return Unit{}
			}))
			receivers = append(receivers, TaskSpawn(scope, at(5), func(tf *Frame) Unit {
				for range each {
					got := Select(tf, at(6), []SelectCase{SelectReceive(rendezvous.Receiver), SelectReceive(buffered.Receiver)})
					mu.Lock()
					sum += got.Value.(int64)
					mu.Unlock()
				}
				return Unit{}
			}))
		}
		TaskAwaitAll(scope, at(7), listFromSlice(senders))
		TaskAwaitAll(scope, at(8), listFromSlice(receivers))
	})
	if err := r.fault(t); err != nil {
		t.Fatalf("hand-offs were reported: %s", err.Msg)
	}
	if want := int64(pairs * each); sum != want {
		t.Errorf("the receivers took %d values, want %d", sum, want)
	}
}

// A record two goroutines wait on at once leaves the registry unsure, and
// it reports nothing again.
func TestDeadlock_ARecordWaitedOnTwiceStopsReports(t *testing.T) {
	settleRegistry(t)
	defer func() {
		tasks.mu.Lock()
		tasks.unsure = false
		tasks.mu.Unlock()
	}()
	fr := NewFrame(context.Background())
	end := MainLine(fr, "main")
	defer end()
	fr.task.busy.Store(true)
	p := parkerOf(fr)
	if p.rec != nil {
		t.Fatal("a second wait on a busy record was recorded")
	}
	tasks.mu.Lock()
	unsure := tasks.unsure
	tasks.mu.Unlock()
	if !unsure {
		t.Error("the registry still trusts a record two goroutines wait on")
	}
	fr.task.busy.Store(false)
}
