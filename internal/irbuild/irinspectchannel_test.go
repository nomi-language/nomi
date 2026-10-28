package irbuild

import "testing"

// TestIRInspectChannel_Runs renders a channel's Debug: a half reads
// `<channel>` (rt.ChannelInspectText) through Debug.inspect and dbg, and a
// `Channel<T>` reads as the struct std declares, its two halves in
// declaration order (rt's debugChannel), at the top, in a struct through its
// derived Debug, and inside a List, a Vector, a tuple, a Map value, a Maybe
// and a Result payload, an enum variant's payload and a list of structs.
// Counter's derived Debug is what message_loop's `Config.inspect` calls;
// before, the builder declined it and the retained `Config.inspect` called a
// body no module retained. A task reads `<task>`, through the same arm as a
// half.
func TestIRInspectChannel_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std/channels.Channel
import std/io
import std/tasks.Task

struct Counter {
    inbox: Channel<Int>
}

struct Config {
    counter: Counter
}

enum Slot {
    Empty
    Full Channel<Int>
}

fn main() {
    c = Channel.buffered<Int>(1)
    io.print(Debug.inspect(c.sender))
    dbg c.receiver
    io.print(Debug.inspect(c))
    io.inspect(#Config{counter: #Counter{inbox: c}})
    io.print(Debug.inspect([c]))
    io.print(Debug.inspect(#[c]))
    io.print(Debug.inspect(#(c, 1)))
    io.print(Debug.inspect(%{"a" => c}))
    m: Maybe<Channel<Int>> = Some(c)
    io.print(Debug.inspect(m))
    r: Result<Channel<Int>, String> = Ok(c)
    dbg r
    io.inspect(Slot.Full(c))
    io.inspect([#Counter{inbox: c}])
    io.inspect([c.sender])
    concurrent {
        t = Task.spawn(fn -> 1)
        io.print(Debug.inspect(t))
        _ = Task.await(t)
    }
}
`
	const ch = "#Channel{sender: <channel>, receiver: <channel>}"
	irRunSource(t, src, "<channel>\n"+
		"dbg line 21: c.receiver = <channel>\n"+
		ch+"\n"+
		"#Config{counter: #Counter{inbox: "+ch+"}}\n"+
		"["+ch+"]\n"+
		"#["+ch+"]\n"+
		"#("+ch+", 1)\n"+
		"%{\"a\" => "+ch+"}\n"+
		"Some("+ch+")\n"+
		"dbg line 31: r = Ok("+ch+")\n"+
		"Full("+ch+")\n"+
		"[#Counter{inbox: "+ch+"}]\n"+
		"[<channel>]\n"+
		"<task>\n")
}
