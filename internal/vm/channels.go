package vm

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
)

// Channels run on rt's channel, so a blocked send or receive observes the
// VM's runtime frame. A `Channel<T>`
// is std's `channels.Channel` struct as a record; its two halves are this
// package's handles over rt's.

type senderValue struct{ s rt.Sender[any] }

func (*senderValue) OpaqueText() string { return rt.ChannelInspectText() }

type receiverValue struct{ r rt.Receiver[any] }

func (*receiverValue) OpaqueText() string { return rt.ChannelInspectText() }

func channelValue(ch rt.Channel[any]) any {
	return channelDesc.Make(&senderValue{s: ch.Sender}, &receiverValue{r: ch.Receiver})
}

// ClosedChannel is a `Channel<T>` whose sender is closed and whose buffer is
// empty: a send answers Err(ChannelClosed) and a receive answers None, and
// neither blocks. A host exercising retained functions offers its halves as
// arguments.
func ClosedChannel() any {
	ch := rt.ChannelBuffered[any](1)
	rt.SenderClose(ch.Sender)
	return channelValue(ch)
}

// channelHost adapts the std/channels operations.
func channelHost(name string) hostFn {
	return func(m *Machine, _ ir.Pos, args []any) (any, error) {
		var out any
		var bad error
		err := rtFault(func() {
			switch name {
			case "Channel.buffered":
				n, ok := oneArg[int64](args)
				if !ok {
					bad = fmt.Errorf("vm: %s: expected one Int operand", name)
					return
				}
				out = channelValue(rt.ChannelBuffered[any](n))
			case "Channel.unbuffered":
				if len(args) != 0 {
					bad = fmt.Errorf("vm: %s: expected no operands", name)
					return
				}
				out = channelValue(rt.ChannelUnbuffered[any]())
			case "Sender.send":
				if len(args) != 2 {
					bad = fmt.Errorf("vm: %s: expected a Sender and a value", name)
					return
				}
				s, ok := args[0].(*senderValue)
				if !ok {
					bad = fmt.Errorf("vm: %s: expected a Sender and a value", name)
					return
				}
				if rt.SenderSend(m.hostFrame, s.s, args[1]).Tag == rt.TagOk {
					out = okValue(rt.Unit{})
				} else {
					out = errValue(markerValue("channels.ChannelClosed"))
				}
			case "Sender.close":
				s, ok := oneArg[*senderValue](args)
				if !ok {
					bad = fmt.Errorf("vm: %s: expected one Sender", name)
					return
				}
				rt.SenderClose(s.s)
				out = rt.Unit{}
			case "Receiver.receive":
				r, ok := oneArg[*receiverValue](args)
				if !ok {
					bad = fmt.Errorf("vm: %s: expected one Receiver", name)
					return
				}
				if got := rt.ReceiverReceive(m.hostFrame, r.r); got.Tag == rt.TagSome {
					out = some(got.Some)
				} else {
					out = noneValue
				}
			default:
				bad = fmt.Errorf("vm: %s is not a channel operation", name)
			}
		})
		if err != nil {
			return nil, err
		}
		if bad != nil {
			return nil, bad
		}
		return out, nil
	}
}

func oneArg[T any](args []any) (T, bool) {
	var zero T
	if len(args) != 1 {
		return zero, false
	}
	v, ok := args[0].(T)
	return v, ok
}
