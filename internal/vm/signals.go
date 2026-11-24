package vm

import (
	"fmt"
	"os"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
)

// signalSubscribeHost is std/signals' `Signal.subscribe(sender, wanted,
// names)`: the channel std made is fed from rt's one signal registry
// (rt/signals.go), with `wanted[i]` sent for each arrival of the signal
// `names[i]` names, and owned by the calling frame. The values are the
// program's own `Signal`s, handed back as they came, so nothing here knows
// how std spells a variant. It answers `Some` of why when this system
// delivers no signals, whatever is wanted.
func signalSubscribeHost(m *Machine, _ ir.Pos, args []any) (any, error) {
	const usage = "vm: Signal.subscribe: expected a Sender, a list of values and a list of names"
	if len(args) != 3 {
		return nil, fmt.Errorf("%s, got %d operands", usage, len(args))
	}
	s, isSender := args[0].(*senderValue)
	values, isList := args[1].(*list)
	names, isNames := args[2].(*list)
	if !isSender || (!isList && args[1] != nil) || (!isNames && args[2] != nil) {
		return nil, fmt.Errorf("%s", usage)
	}
	if why := rt.SignalsUnsupported(); why != "" {
		return some(why), nil
	}
	vs, ns := listSlice(values), listSlice(names)
	if len(vs) != len(ns) {
		return nil, fmt.Errorf("vm: Signal.subscribe: %d values and %d names", len(vs), len(ns))
	}
	wanted := make(map[os.Signal]func() any, len(vs))
	for i, v := range vs {
		name, isText := ns[i].(string)
		if !isText {
			return nil, fmt.Errorf("vm: Signal.subscribe: name %d is %T, not a String", i, ns[i])
		}
		sig, why := rt.SignalNamed(name)
		if why != "" {
			return some(why), nil
		}
		wanted[sig] = func() any { return v }
	}
	rt.SubscribeSignals(m.hostFrame, s.s, wanted)
	return noneValue, nil
}
