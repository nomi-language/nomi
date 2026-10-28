package rt

import "testing"

// testHalf is a channel half as the engine holds one: an Opaque value whose
// placeholder is ChannelInspectText.
type testHalf struct{}

func (testHalf) OpaqueText() string { return ChannelInspectText() }

// TestDebugText_RendersAChannelAndItsHalves holds DebugText to std's
// universal Debug of a `Channel<T>`, its halves in declaration order, at the
// top and inside a list and a tuple, and of a half alone.
func TestDebugText_RendersAChannelAndItsHalves(t *testing.T) {
	ch := NewStructDesc(ChannelTypeName, []FieldSpec{
		{Name: "sender", Type: SlotRef}, {Name: "receiver", Type: SlotRef},
	}).Make(testHalf{}, testHalf{})
	const want = "#Channel{sender: <channel>, receiver: <channel>}"
	cases := []struct {
		name string
		v    any
		want string
	}{
		{"a half", testHalf{}, "<channel>"},
		{"a channel", ch, want},
		{"a list of channels", ConsCell[any, List[any]](ch, nil), "[" + want + "]"},
		{"a tuple holding a channel", TupleDesc(SlotRef, SlotInt).Make(ch, int64(1)), "#(" + want + ", 1)"},
	}
	for _, c := range cases {
		got, err := DebugText(c.v, nil)
		if err != nil || got != c.want {
			t.Errorf("%s: DebugText answered %q, %v; want %q", c.name, got, err, c.want)
		}
	}
}
