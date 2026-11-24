package analysis

import (
	"strings"
	"testing"
)

// Every RecordingKind has its own words in a missing-impl report
// (requiredClause), so a kind added without them fails here instead of
// reaching a reader as "via an unknown site".
func TestEveryRecordingKindSaysWhatRequiredIt(t *testing.T) {
	unknown := requiredClause(Recording{Kind: recordingKinds})
	seen := map[string]RecordingKind{}
	for k := RecordingKind(0); k < recordingKinds; k++ {
		got := requiredClause(Recording{Kind: k})
		if got == unknown || !strings.HasPrefix(got, "required ") {
			t.Errorf("kind %d renders %q", k, got)
		}
		if prev, dup := seen[got]; dup {
			t.Errorf("kinds %d and %d both render %q", prev, k, got)
		}
		seen[got] = k
	}
}

// What an argument's, a typed literal slot's and an impl's bound demand
// name is what the program wrote.
func TestRequiredClause_NamesWhatTheProgramWrote(t *testing.T) {
	for _, c := range []struct {
		rec  Recording
		want string
	}{
		{Recording{Kind: RecordingKindArgument, Callee: "log.any", Param: "value", Arg: 2}, "required by parameter `value` of `log.any`"},
		{Recording{Kind: RecordingKindArgument, Callee: "f", Arg: 1}, "required by argument 1 of `f`"},
		{Recording{Kind: RecordingKindTypedLiteralSlot, Tag: "Sql"}, "required by this `Sql` slot"},
		{Recording{Kind: RecordingKindCallSite, Callee: "b.Show.show"}, "required by the call `b.Show.show`"},
		{Recording{Kind: RecordingKindCallSite, Tag: "Url"}, "required by this `Url` literal"},
		{Recording{Kind: RecordingKindInterpolation}, "required to interpolate it"},
	} {
		if got := requiredClause(c.rec); got != c.want {
			t.Errorf("%+v: got %q, want %q", c.rec, got, c.want)
		}
	}
	note := implBoundNote(Recording{Bound: ImplBound{Receiver: "Measure<P>", Iface: "Display", Param: "P", Bound: "Unit2"}})
	if want := "`Measure<P>`'s `Display` impl requires `P: Unit2`"; note != want {
		t.Errorf("got %q, want %q", note, want)
	}
	if note := implBoundNote(Recording{}); note != "" {
		t.Errorf("a demand the site made itself has the note %q", note)
	}
}
