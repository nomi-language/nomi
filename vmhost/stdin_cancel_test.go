package vmhost_test

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"

	"github.com/nomi-language/nomi/vmhost"
)

// runOnPipe runs src's main with the read end of a pipe as its standard
// input, and answers its output once it ends or fails the test after a
// limit. Nothing is written to the pipe before write runs, so a read that
// waits for it waits until then.
func runOnPipe(t *testing.T, src string, write func(w *os.File)) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close(); w.Close() })
	p, err := vmhost.LoadSource("main.nomi", src, vmhost.WithInput(r))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	var out bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- p.Run(context.Background(), &out, nil, false) }()
	if write != nil {
		write(w)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run: %v\noutput:\n%s", err, out.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the program is still waiting on a read of its standard input")
	}
	return out.String()
}

// A task blocked reading standard input is cancelled as one blocked on a
// channel is: Task.cancel stops it at once, though no input comes.
func TestStdinCancel_ACancelledTaskStopsWaitingForInput(t *testing.T) {
	for name, read := range map[string]string{
		"read_line":        "io.read_line()",
		"Reader.read_line": "io.Reader.read_line(io.stdin)",
		"Reader.read":      "io.Reader.read(io.stdin)",
	} {
		t.Run(name, func(t *testing.T) {
			src := `import {
    std/io
    std/tasks.Task
}

fn main() {
    outcome = concurrent {
        reader = Task.spawn(fn -> {
            _ = ` + read + `
            1
        })
        Task.cancel(reader)
        Task.outcome(reader)
    }
    io.inspect(outcome)
}
`
			if got := runOnPipe(t, src, nil); got != "Cancelled\n" {
				t.Fatalf("output %q, want the task's cancellation", got)
			}
		})
	}
}

// What arrives after a read gave up is not lost: the next read answers it.
// The first task waits for a line, is cancelled, and then the line is
// written; main reads it.
func TestStdinCancel_InputThatArrivesAfterACancelledReadIsReadNext(t *testing.T) {
	src := `import {
    std/duration.Duration
    std/io
    std/tasks.Task
    std/timer
}

fn main() {
    outcome = concurrent {
        reader = Task.spawn(fn -> io.read_line())
        timer.sleep(Duration.milliseconds(50))
        Task.cancel(reader)
        Task.outcome(reader)
    }
    io.inspect(outcome)
    io.print("ready")
    io.print(io.read_line())
    io.print(io.read_line())
}
`
	got := runOnPipe(t, src, func(w *os.File) {
		time.Sleep(300 * time.Millisecond)
		_, _ = w.WriteString("late line\n")
		w.Close()
	})
	if want := "Cancelled\nready\nOk(Some(late line))\nOk(None)\n"; got != want {
		t.Fatalf("output:\n%s\nwant:\n%s", got, want)
	}
}

// A `concurrent` block whose body fails tears down its tasks, a task
// waiting on standard input among them, and the program ends.
func TestStdinCancel_AScopesTeardownStopsATaskWaitingForInput(t *testing.T) {
	src := `import {
    std/io
    std/tasks.Task
}

fn scope() -> Result<Int, String> {
    stop: Result<Int, String> = Err("stop")
    concurrent {
        waiting = Task.spawn(fn -> io.read_line())
        n = stop?
        _ = Task.await(waiting)
        Ok(n)
    }
}

fn main() {
    io.print(scope())
}
`
	if got := runOnPipe(t, src, nil); got != "Err(stop)\n" {
		t.Fatalf("output %q", got)
	}
}
