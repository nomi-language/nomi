package vmhost_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nomi-language/nomi/vmhost"
)

// fsWatchApp is the app the watcher programs below share, after their own
// imports: a supervisor the watchers run under, whose shutdown waits a
// minute for them, and a wait of five seconds for a watcher's next event.
const fsWatchApp = `
struct App
    context:  Context
    watchers: Supervisor
end

fn boot() -> Result<App, String>
    Ok(App{
        context:  Context.root(),
        watchers: Supervisor.new(max_running: 4, shutdown_timeout: Duration.minutes(1)),
    })
end

fn next(w: Watcher) -> Result<Option<Event>, TimedOut>
    Receiver.receive_within(Watcher.events(w), Duration.seconds(5))
end
`

// fsWatchImports are the imports every watcher program below uses.
const fsWatchImports = `    std::channels.{Receiver, TimedOut}
    std::duration.Duration
    std::fs.{self, Event, Watcher}
    std::io
    std::supervisors.Supervisor
`

// A watcher under the virtual clock: a supervised `fs.watch` over a sandbox
// reports each change one interval after it, a write that keeps the size
// included, since the sandbox stamps a write with the clock in force; stop
// ends its task, so the supervisor flushes; a consumer that falls more than
// the channel's 1,024 events behind finds one Rescan queued and nothing
// else; and `fs.watch_each` runs its handler in the calling task until the
// task is cancelled.
func TestFsWatch_VirtualClock(t *testing.T) {
	t.Setenv("NOMI_COLOR", "never")
	src := "import\n" + fsWatchImports + `    std::channels.{Channel, Sender}
    std::fs::testing.{self, Sandbox}
    std::supervisors.{FlushOutcome, Wait}
    std::tasks.{Outcome, Task}
    std::testing.Clock
    std::timer
end
` + fsWatchApp + `
fn main()
    io.print("watchers")
end

// What is queued on the watcher now, without waiting.
fn queued(w: Watcher) -> List<Event>
    Iter.loop([], fn got ->
        case Receiver.receive_within(Watcher.events(w), Duration.nanoseconds(0))
            Ok(Some(e)) -> [e, ..got]
            _           -> break Iter.reverse(got)
        end
    end)
end

group "watching under the virtual clock"
    clock Clock.Virtual

    boot boot()

    test "a supervised watcher reports each change and stops"
        ran = testing.run(Sandbox{dirs: ["d"]}, fn ->
            w = fs.watch("d", App.watchers, options: {interval: Duration.seconds(1)})!
            _ = fs.write_text("d/a.txt", "a")
            created = next(w)
            _ = fs.write_text("d/a.txt", "b")
            modified = next(w)
            _ = fs.rename("d/a.txt", "d/b.txt")
            renamed = [next(w), next(w)]
            _ = fs.remove("d/b.txt")
            removed = next(w)
            Watcher.stop(w)
            ended = next(w)
            flushed = Supervisor.flush(App.watchers, Wait.UpTo(Duration.seconds(10)))
            (created, modified, renamed, removed, ended, flushed)
        end)

        (created, modified, renamed, removed, ended, flushed) = ran.value
        assert created == Ok(Some(Event.Created{path: "d/a.txt"}))
        assert modified == Ok(Some(Event.Modified{path: "d/a.txt"}))
        assert renamed == [Ok(Some(Event.Removed{path: "d/a.txt"})), Ok(Some(Event.Created{path: "d/b.txt"}))]
        assert removed == Ok(Some(Event.Removed{path: "d/b.txt"}))
        assert ended == Ok(None)
        assert flushed == FlushOutcome.Flushed
    end

    test "a consumer that falls behind finds one Rescan"
        ran = testing.run(Sandbox{dirs: ["d"]}, fn ->
            w = fs.watch("d", App.watchers, options: {interval: Duration.seconds(1)})!

            Iter.each(1..=1100, fn n ->
                _ = fs.write_text("d/f${n}.txt", "x")
            end)

            timer.sleep(Duration.milliseconds(3500))
            backlog = queued(w)
            _ = fs.write_text("d/late.txt", "x")
            late = next(w)
            Watcher.stop(w)
            (backlog, late)
        end)

        assert ran.value == ([Event.Rescan], Ok(Some(Event.Created{path: "d/late.txt"})))
    end

    test "watch_each runs its handler in the calling task until it is cancelled"
        ran = testing.run(Sandbox{dirs: ["d"]}, fn ->
            seen: Channel<Event> = Channel.buffered(8)

            concurrent
                task = Task.spawn(fn ->
                    fs.watch_each("d", options: {interval: Duration.seconds(1)}, fn e ->
                        _ = Sender.send(seen.sender, e)
                    end)
                end)

                timer.sleep(Duration.milliseconds(1))
                _ = fs.write_text("d/a.txt", "a")
                got = Receiver.receive_within(seen.receiver, Duration.seconds(5))
                Task.cancel(task)
                (got, Task.outcome(task) is Outcome.Cancelled)
            end
        end)

        assert ran.value == (Ok(Some(Event.Created{path: "d/a.txt"})), True)
    end
end
`
	const name = "watch_test.nomi"
	p, err := vmhost.LoadSource(name, src)
	if err != nil {
		t.Fatalf("the front end rejects the watcher tests: %v", err)
	}
	var buf bytes.Buffer
	rep := vmhost.NewTestReport(&buf)
	p.Test(&buf, rep, name, vmhost.TestOptions{}, func(n string) string { return vmhost.TestName(name, n) })
	rep.Summary()
	out := buf.String()
	if !strings.Contains(out, "test result: ok. 3 passed, 0 failed") {
		t.Fatalf("the watcher tests did not all pass:\n%s", out)
	}
}

// A program whose main returns while a watcher waits out a long interval
// ends at once: shutdown closes the watcher's stop channel, so the drain
// does not wait out the supervisor's minute.
func TestFsWatch_ShutdownEndsTheWatcher(t *testing.T) {
	p, err := vmhost.Load(writeProgram(t, "import\n"+fsWatchImports+"end\n"+fsWatchApp+`
fn main() -> Result<Unit, io.Error>
    dir = fs.make_temp_dir()?
    defer fs.remove_all_quietly(dir)
    _ = fs.watch(dir, App.watchers, options: {interval: Duration.hours(1)})?
    io.print("watching")
    Ok(Unit)
end
`))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var out bytes.Buffer
	if err := p.Run(ctx, &out, nil, false); err != nil {
		t.Fatalf("the program failed: %v\n%s", err, out.String())
	}
	if ctx.Err() != nil {
		t.Fatalf("the program ran until the test's deadline: shutdown waited for the watcher")
	}
	if out.String() != "watching\n" {
		t.Errorf("output %q, want %q", out.String(), "watching\n")
	}
}

// A watcher of a directory on the disk, under the real clock, reports a
// creation, a write that keeps the size, a rename and a removal, each
// within five seconds. The settle window coalesces a write a poll catches
// half done, and a temporary file an atomic write makes and renames away.
// It waits for each event rather than for a fixed time; run it with
// -count=20 to look for flakes.
func TestFsWatch_OnTheDisk(t *testing.T) {
	p, err := vmhost.Load(writeProgram(t, "import\n"+fsWatchImports+"end\n"+fsWatchApp+`
fn expect(w: Watcher, want: Option<Event>) -> Result<Unit, String>
    got = next(w)
    if got == Ok(want) then Ok(Unit) else Err("got ${Debug.inspect(got)}, want ${Debug.inspect(want)}")
end

fn main() -> Result<Unit, String>
    dir = fs.make_temp_dir() |> Result.map_err(fn e -> "${e}")?
    defer fs.remove_all_quietly(dir)

    options = fs.WatchOptions{interval: Duration.milliseconds(10), settle: Duration.milliseconds(30)}
    w = fs.watch(dir, App.watchers, options:) |> Result.map_err(fn e -> "${e}")?
    defer Watcher.stop(w)

    a = "${dir}/a.txt"
    b = "${dir}/b.txt"

    _ = fs.write_text(a, "one")
    expect(w, Some(Event.Created{path: a}))?
    _ = fs.write_text(a, "two")
    expect(w, Some(Event.Modified{path: a}))?
    _ = fs.write_text(a, "three", mode: .ReplaceAtomically)
    expect(w, Some(Event.Modified{path: a}))?
    _ = fs.rename(a, b)
    expect(w, Some(Event.Removed{path: a}))?
    expect(w, Some(Event.Created{path: b}))?
    _ = fs.remove(b)
    expect(w, Some(Event.Removed{path: b}))?
    Watcher.stop(w)
    expect(w, None)?
    io.print("ok")
    Ok(Unit)
end
`))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatalf("the watcher on the disk: %v\n%s", err, out.String())
	}
	if out.String() != "ok\n" {
		t.Errorf("output %q, want %q", out.String(), "ok\n")
	}
}
