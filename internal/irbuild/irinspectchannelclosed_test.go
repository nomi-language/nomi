package irbuild

import "testing"

// std's ChannelClosed marker is inspected alone and inside a Maybe or a
// Result, as `Sender.send`'s answer is.
func TestIRInspectChannelClosed_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std/io
import std/channels.{ChannelClosed}

fn main() {
    io.inspect(ChannelClosed)
    m: Maybe<ChannelClosed> = Some(ChannelClosed)
    io.inspect(m)
    r: Result<Unit, ChannelClosed> = Ok(Unit)
    io.inspect(r)
    e: Result<Unit, ChannelClosed> = Err(ChannelClosed)
    io.inspect(e)
    io.print(Debug.inspect(e))
}
`
	irRunSource(t, src, "ChannelClosed\nSome(ChannelClosed)\nOk(Unit)\nErr(ChannelClosed)\nErr(ChannelClosed)\n")
}
