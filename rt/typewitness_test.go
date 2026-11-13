package rt

import "testing"

// Two witnesses are equal, and hash alike, when they name the same type, at
// the top and inside a container; one inspects as `Type<Name>`.
func TestTypeWitness_EqualHashAndDebug(t *testing.T) {
	tid, other := &TypeID{Nomi: "ids.TraceId"}, &TypeID{Nomi: "ids.Other"}
	a, b, c := Type[any]{TID: tid}, Type[any]{TID: tid}, Type[any]{TID: other}
	if !Equal(a, b) || Equal(a, c) || Equal(Type[any]{}, Type[any]{}) {
		t.Errorf("Equal: same %v, different %v, zero %v", Equal(a, b), Equal(a, c), Equal(Type[any]{}, Type[any]{}))
	}
	if Hash(a) != Hash(b) {
		t.Error("two witnesses of one type hash differently")
	}
	la := ConsCell[any, List[any]](a, nil)
	lb := ConsCell[any, List[any]](b, nil)
	if !Equal(la, lb) || Hash(la) != Hash(lb) {
		t.Error("lists of witnesses of one type differ")
	}
	got, err := DebugText(la, nil)
	if err != nil || got != "[Type<TraceId>]" {
		t.Errorf("Debug %q, %v; want [Type<TraceId>]", got, err)
	}
}
