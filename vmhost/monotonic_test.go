package vmhost_test

import (
	"bytes"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// monotonicProgram measures sleeps with the monotonic clock, under the
// virtual clock and the real one.
const monotonicProgram = `import
    std::duration.Duration
    std::instant.Monotonic
    std::tasks.Task
    std::testing.Clock
    std::timer
end

group "virtual"
    clock Clock.Virtual

    test "sleeps add up exactly"
        start = Monotonic.now()
        timer.sleep(Duration.seconds(5))
        assert Monotonic.elapsed(start) == Duration.seconds(5)

        timer.sleep(Duration.minutes(2))
        assert Monotonic.elapsed(start) == Duration.seconds(125)
    end

    test "a task's sleep counts on the same clock"
        start = Monotonic.now()

        concurrent
            a = Task.spawn(fn -> timer.sleep(Duration.seconds(30)))
            b = Task.spawn(fn -> timer.sleep(Duration.seconds(10)))
            Task.await(a)
            Task.await(b)
        end

        assert Monotonic.elapsed(start) == Duration.seconds(30)
        assert Monotonic.now() - start == Duration.seconds(30)
    end
end

test "readings on the real clock are ordered"
    first = Monotonic.now()
    timer.sleep(Duration.milliseconds(2))
    second = Monotonic.now()

    assert first < second
    assert Monotonic.between(first, second) >= Duration.milliseconds(2)
    assert Monotonic.elapsed(first) >= Monotonic.between(first, second)
end
`

// Under `clock Clock.Virtual`, Monotonic.now reads the test's virtual
// clock, so the sleeps a test takes add up to exactly what it slept; on the
// real clock two readings are ordered and an elapsed time is at least the
// sleep between them.
func TestMonotonic_SleepsAddUpExactlyUnderTheVirtualClock(t *testing.T) {
	p, err := vmhost.Load(writeProgram(t, monotonicProgram))
	if err != nil {
		t.Fatalf("the front end rejects this, so it cannot test the clock: %v", err)
	}
	results := p.Cases(&bytes.Buffer{}, vmhost.TestOptions{})
	if len(results) != 3 {
		t.Fatalf("%d cases, want 3", len(results))
	}
	for _, r := range results {
		if r.Blocked != nil {
			t.Fatalf("%s: blocked: %v", r.Name, r.Blocked)
		}
		if r.Err != nil {
			t.Errorf("%s: %v", r.Name, r.Err)
		}
	}
}
