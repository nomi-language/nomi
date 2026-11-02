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

// armValue is a `Channel.select` arm: its erased case and its handler.
type armValue struct {
	c    rt.SelectCase
	then *functionValue
	// kind says how the handler is called: with the received Maybe, with the
	// send's Result, or with nothing.
	kind armKind
}

type armKind uint8

const (
	armReceive armKind = iota
	armSend
	armAfter
)

func (*armValue) OpaqueText() string { return rt.ArmInspectText() }

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
			case "Sender.close_on_drain":
				s, ok := oneArg[*senderValue](args)
				if !ok {
					bad = fmt.Errorf("vm: %s: expected one Sender", name)
					return
				}
				rt.SenderCloseOnDrain(s.s)
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
			case "Receiver.receive_within":
				if len(args) != 2 {
					bad = fmt.Errorf("vm: %s: expected a Receiver and a Duration", name)
					return
				}
				r, ok := args[0].(*receiverValue)
				wait, isDuration := durationOperand(args[1])
				if !ok || !isDuration {
					bad = fmt.Errorf("vm: %s: expected a Receiver and a Duration", name)
					return
				}
				got := rt.ReceiverReceiveWithin(m.hostFrame, r.r, wait)
				switch {
				case got.Tag != rt.TagOk:
					out = errValue(markerValue("channels.TimedOut"))
				case got.Ok.Tag == rt.TagSome:
					out = okValue(some(got.Ok.Some))
				default:
					out = okValue(noneValue)
				}
			case "Arm.receive":
				if len(args) != 2 {
					bad = fmt.Errorf("vm: %s: expected a Receiver and a handler", name)
					return
				}
				r, ok := args[0].(*receiverValue)
				f, isFn := args[1].(*functionValue)
				if !ok || !isFn || f.arity != 1 {
					bad = fmt.Errorf("vm: %s: expected a Receiver and a unary handler", name)
					return
				}
				out = &armValue{c: rt.SelectReceive(r.r), then: f, kind: armReceive}
			case "Arm.send":
				if len(args) != 3 {
					bad = fmt.Errorf("vm: %s: expected a Sender, a value and a handler", name)
					return
				}
				s, ok := args[0].(*senderValue)
				f, isFn := args[2].(*functionValue)
				if !ok || !isFn || f.arity != 1 {
					bad = fmt.Errorf("vm: %s: expected a Sender, a value and a unary handler", name)
					return
				}
				out = &armValue{c: rt.SelectSend(s.s, args[1]), then: f, kind: armSend}
			case "Arm.after":
				if len(args) != 2 {
					bad = fmt.Errorf("vm: %s: expected a Duration and a handler", name)
					return
				}
				wait, isDuration := durationOperand(args[0])
				f, isFn := args[1].(*functionValue)
				if !isDuration || !isFn || f.arity != 0 {
					bad = fmt.Errorf("vm: %s: expected a Duration and a nullary handler", name)
					return
				}
				out = &armValue{c: rt.SelectAfter(wait), then: f, kind: armAfter}
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

// durationOperand reads a `Duration`, std/duration's record over its
// nanoseconds, saturating one past int64's range.
func durationOperand(v any) (rt.Duration, bool) {
	r, ok := v.(*rt.Record)
	if !ok || r.NumFields() != 1 {
		return 0, false
	}
	n := r.Field(0)
	if !rt.IsInt(n) {
		return 0, false
	}
	return rt.Duration(rt.IntSaturated(n)), true
}

// channelSelectHost is `Channel.select(arms)`: rt waits for the first arm
// that can finish, and the winner's handler runs on the caller's frame with
// its outcome.
func channelSelectHost(m *Machine, _ ir.Pos, args []any) (any, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("vm: Channel.select: expected one list of arms, got %d operands", len(args))
	}
	xs, ok := args[0].(*list)
	if !ok && args[0] != nil {
		return nil, fmt.Errorf("vm: Channel.select: the operand is %T, not a list of arms", args[0])
	}
	items := listSlice(xs)
	arms := make([]*armValue, len(items))
	cases := make([]rt.SelectCase, len(items))
	for i, item := range items {
		a, isArm := item.(*armValue)
		if !isArm {
			return nil, fmt.Errorf("vm: Channel.select: arm %d is %T, not an arm", i, item)
		}
		arms[i], cases[i] = a, a.c
	}
	var got rt.Selected
	if err := rtFault(func() { got = rt.Select(m.hostFrame, cases) }); err != nil {
		return nil, err
	}
	won := arms[got.Index]
	var operand []any
	switch won.kind {
	case armReceive:
		if got.OK {
			operand = []any{some(got.Value)}
		} else {
			operand = []any{noneValue}
		}
	case armSend:
		if got.OK {
			operand = []any{okValue(rt.Unit{})}
		} else {
			operand = []any{errValue(markerValue("channels.ChannelClosed"))}
		}
	}
	return m.apply(won.then, operand, m.hostFrame)
}
