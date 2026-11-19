package vmrunner_test

import (
	"bytes"
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	"github.com/nomi-language/nomi/vmrunner"
)

const endlessSource = `import std::duration.Duration
import std::tasks.Task
import std::timer

once counted = count_up()

fn count_up() -> Int
    spin(0)
end

fn spin(n: Int) -> Int
    if n < 0 then
        n
    else
        spin(n + 1)
    end
end

export fn forever() -> Int
    spin(0)
end

export fn sleepy() -> Int
    timer.sleep(Duration.milliseconds(3600000))
    1
end

export fn busy() -> Int
    concurrent
        a = Task.spawn(fn -> spin(0))
        b = Task.spawn(fn -> spin(0))
        Task.await(a) + Task.await(b)
    end
end

export fn forced() -> Int
    counted
end

export fn add(a: Int, b: Int) -> Int
    a + b
end
`

// within answers f's error, failing the test when f has not answered in a
// generous bound: the stop is asserted by its answer, never by its timing.
func within(t *testing.T, f func() error) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- f() }()
	select {
	case err := <-done:
		return err
	case <-time.After(60 * time.Second):
		t.Fatal("the bounded call did not stop within 60 seconds")
		return nil
	}
}

// A call that computes forever, one that waits for an hour, one whose
// tasks compute forever and one forcing a `once` that never finishes are
// each stopped when their context ends, answer its error, and leave the
// program serving calls: the `once` is forced again the next time.
func TestInstance_ACallItsContextStops(t *testing.T) {
	in, _ := start(t, endlessSource)
	baseline := runtime.NumGoroutine()
	for _, name := range []string{"forever", "sleepy", "busy", "forced", "forced"} {
		err := within(t, func() error {
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			_, err := in.Call(ctx, name, []byte("[]"))
			return err
		})
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("%s answered %v, want context.DeadlineExceeded", name, err)
		}
		if got := call(t, in, "add", "[2, 3]"); got != "5" {
			t.Fatalf("after %s was stopped, add = %s", name, got)
		}
		// The tasks the call started stopped with it.
		for deadline := time.Now().Add(30 * time.Second); runtime.NumGoroutine() > baseline; time.Sleep(10 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("after %s was stopped, %d goroutines run, %d before", name, runtime.NumGoroutine(), baseline)
			}
		}
	}
	// A context cancelled outright stops a call the same way.
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	if err := within(t, func() error { _, err := in.Call(ctx, "forever", []byte("[]")); return err }); !errors.Is(err, context.Canceled) {
		t.Fatalf("forever under a cancelled context answered %v", err)
	}
}

// WithStop bounds the boot: a boot that never finishes is stopped, and
// nothing is started. A boot that finishes in time leaves the program's
// later calls unbounded by it.
func TestWithStop_BoundsTheBoot(t *testing.T) {
	src := `struct App
    n: Int
end

fn spin(n: Int) -> Int
    if n < 0 then n else spin(n + 1)
end

fn boot() -> Result<App, String>
    Ok(App{n: spin(0)})
end

fn main()
    _ = App.n
end
`
	image := webImage(t, src)
	err := within(t, func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		var out bytes.Buffer
		in, err := vmrunner.Boot(image, &out, &out, nil, nil, vmrunner.WithStop(ctx))
		if err == nil {
			in.Close()
		}
		return err
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("the endless boot answered %v, want context.DeadlineExceeded", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var out bytes.Buffer
	in, err := vmrunner.Boot(webImage(t, endlessSource), &out, &out, nil, nil, vmrunner.WithStop(ctx))
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	cancel()
	if got := call(t, in, "add", "[1, 1]"); got != "2" {
		t.Fatalf("after the boot's context ended, add = %s", got)
	}
}

// Main's context bounds it as a call's does.
func TestInstance_MainItsContextStops(t *testing.T) {
	src := "fn spin(n: Int) -> Int\n    if n < 0 then n else spin(n + 1)\nend\n\nfn main()\n    _ = spin(0)\nend\n\nexport fn add(a: Int, b: Int) -> Int\n    a + b\nend\n"
	var out bytes.Buffer
	in, err := vmrunner.Boot(webImage(t, src), &out, &out, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	err = within(t, func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		return in.Main(ctx)
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("main answered %v, want context.DeadlineExceeded", err)
	}
	if got := call(t, in, "add", "[2, 2]"); got != "4" {
		t.Fatalf("after main was stopped, add = %s", got)
	}
}
