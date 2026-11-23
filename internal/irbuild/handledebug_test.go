package irbuild

import "testing"

// A channel half renders as `<channel>`, an arm as `<arm>` and a task as
// `<task>` (spec §12 *Display and Debug*), and a `Channel<T>` as the struct
// around its two halves, by every spelling of their Debug: the type's own
// `T.inspect`, which names std's impl by its type, `Debug.inspect`,
// `io.inspect`, `dbg`, a generic function's `Debug.inspect`, and as a part
// of a list and an Option. `Sender.inspect`, `Receiver.inspect`,
// `Channel.inspect` and `Task.inspect` were declined by the builder, and
// `Arm.inspect` printed `Arm`.
func TestHandleInspect_EverySpellingPrintsTheSpecsText(t *testing.T) {
	irRunSource(t, `import
    std::channels.{Arm, Channel, Receiver, Sender}
    std::duration.Duration
    std::io
    std::tasks.Task
end

fn show<T>(x: T) -> String
    Debug.inspect(x)
end

fn main()
    ch = Channel.buffered<Int>(1)
    arm = Arm.after(Duration.seconds(0), fn -> 1)
    io.print(Sender.inspect(ch.sender))
    io.print(Receiver.inspect(ch.receiver))
    io.print(Channel.inspect(ch))
    io.print(Arm.inspect(arm))
    io.print(Debug.inspect(ch.sender))
    io.print(Debug.inspect(ch))
    io.print(Debug.inspect(arm))
    io.inspect(ch.receiver)
    dbg ch.sender
    io.print(show(ch.receiver))
    io.print(show(arm))
    io.print(Debug.inspect([ch.sender]))
    io.print(Debug.inspect(Some(ch)))
    texts = concurrent
        task = Task.spawn(fn -> 1)
        shown = [Task.inspect(task), Debug.inspect(task), show(task), Debug.inspect([task])]
        _ = Task.await(task)
        shown
    end
    io.print(Debug.inspect(texts))
end
`, `<channel>
<channel>
Channel{sender: <channel>, receiver: <channel>}
<arm>
<channel>
Channel{sender: <channel>, receiver: <channel>}
<arm>
<channel>
dbg line 23: ch.sender = <channel>
<channel>
<arm>
[<channel>]
Some(Channel{sender: <channel>, receiver: <channel>})
["<task>", "<task>", "<task>", "[<task>]"]
`)
}
