package irbuild

import "testing"

// A struct field, a lambda parameter and a distinct type may hold a channel
// handle whose messages hold channels in turn, as long as that chain of
// channels ends: here `Request` carries a reply channel of results, and a
// `Box` field, a lambda parameter of type `Box` and `type Inbox
// Sender<Request>` carry the requests.
func TestIRChannelOfMessagesWithChannels_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import
    std::channels.{Channel, Receiver, Sender}
    std::io
end

enum Request
    Ask {n: Int, reply: Channel<Result<Int, String>>}
end

struct Box
    inbox: Channel<Request>
end

type Inbox Sender<Request>

fn ask(inbox: Inbox, n: Int) -> Unit
    Inbox(sender) = inbox
    reply = Channel.buffered<Result<Int, String>>(1)
    _ = Sender.send(sender, Request.Ask{n:, reply:})
    Unit
end

fn answer(box: Box) -> Int
    case Receiver.receive(box.inbox.receiver)
        Some(Request.Ask{n:, reply:}) ->
            _ = Sender.send(reply.sender, Ok(n * 2))
            n
        None -> 0
    end
end

fn main()
    box = Box{inbox: Channel.buffered<Request>(2)}
    ask(Inbox(box.inbox.sender), 21)
    serve = fn b: Box -> answer(b)
    io.print(Int.to_string(serve(box)))
end
`
	irRunSource(t, src, "21\n")
}

// A struct field, an enum variant's field and a task handle in a field may
// hold a channel or task of a structural composite (`Map<String, Int>`,
// `List<Int>`, `(Int, List<String>)`): the composite's parts end, so the
// field's check does too. The handle reads back through the field and
// sends, receives, selects and awaits through it.
func TestIRHandleOfCompositeInAField_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import
    std::channels.{Arm, Channel, Receiver, Sender}
    std::io
    std::tasks.Task
end

pub struct M
    state: Channel<Map<String, Int>>
end

struct Halves
    out: Sender<List<Int>>
    inbound: Receiver<List<Int>>
end

struct Pair
    ch: Channel<(Int, List<String>)>
end

struct Job
    task: Task<List<Int>>
end

enum Msg
    Put {rows: Channel<List<Int>>}
end

fn size(m: M) -> Int
    Channel.select([
        Arm.receive(m.state.receiver, fn s -> Maybe.with_default(s, {}) |> Map.size()),
    ])
end

fn main()
    m = M{state: Channel.buffered(1)}
    _ = Sender.send(m.state.sender, {"a" => 1})
    got = Receiver.receive(m.state.receiver)
    io.print("received ${got == Some({"a" => 1})}")
    _ = Sender.send(m.state.sender, {"b" => 2, "c" => 3})
    io.print("selected ${size(m)}")
    ch = Channel.buffered<List<Int>>(1)
    h = Halves{out: ch.sender, inbound: ch.receiver}
    _ = Sender.send(h.out, [1, 2, 3])
    io.print("halves ${Receiver.receive(h.inbound) == Some([1, 2, 3])}")
    p = Pair{ch: Channel.buffered(1)}
    _ = Sender.send(p.ch.sender, (7, ["x"]))
    io.print("pair ${Receiver.receive(p.ch.receiver) == Some((7, ["x"]))}")
    rows = Channel.buffered<List<Int>>(1)
    case (Msg.Put{rows:})
        Msg.Put{rows: r} ->
            _ = Sender.send(r.sender, [4])
            io.print("msg ${Receiver.receive(rows.receiver) == Some([4])}")
    end
    total = concurrent
        j = Job{task: Task.spawn(fn -> [1, 2, 3])}
        Task.await(j.task) == [1, 2, 3]
    end
    io.print("job ${total}")
end
`
	irRunSource(t, src, "received True\nselected 2\nhalves True\npair True\nmsg True\njob True\n")
}
