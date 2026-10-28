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
	const src = `import {
    std/channels.{Channel, Receiver, Sender}
    std/io
}

enum Request {
    Ask {n: Int, reply: Channel<Result<Int, String>>}
}

struct Box {
    inbox: Channel<Request>
}

type Inbox Sender<Request>

fn ask(inbox: Inbox, n: Int) -> Unit {
    Inbox(sender) = inbox
    reply = Channel.buffered<Result<Int, String>>(1)
    _ = Sender.send(sender, #Request.Ask{n, reply})
    Unit
}

fn answer(box: Box) -> Int {
    case Receiver.receive(box.inbox.receiver) {
        Some(#Request.Ask{n, reply}) -> {
            _ = Sender.send(reply.sender, Ok(n * 2))
            n
        }
        None -> 0
    }
}

fn main() {
    box = #Box{inbox: Channel.buffered<Request>(2)}
    ask(Inbox(box.inbox.sender), 21)
    serve = fn b: Box -> answer(b)
    io.print(Int.to_string(serve(box)))
}
`
	irRunSource(t, src, "21\n")
}
