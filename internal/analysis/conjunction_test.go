package analysis

import (
	"strings"
	"testing"
)

// An interface conjunction `A and B` is a value type (spec §12 *Interface
// conjunctions*). These tests use interfaceBoundSetup: User implements
// Showable and Tagged, ShowOnly only Showable.

func TestConjunction_AcceptedAsValueType(t *testing.T) {
	src := interfaceBoundSetup + `struct Box
    item: Showable and Tagged
end
fn label(x: Showable and Tagged) -> String
    Showable.show(x) + Tagged.tag(x)
end
fn make(u: User) -> Showable and Tagged
    u
end
fn demo() -> String
    items: List<Showable and Tagged> = [User{name: "a"}, User{name: "b"}]
    b = Box{item: User{name: "c"}}
    _ = items
    label(b.item) + label(make(User{name: "d"}))
end`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestConjunction_MissingImplNamesTheMember(t *testing.T) {
	src := interfaceBoundSetup + `fn demo() -> Int
    _x: Showable and Tagged = ShowOnly{name: "a"}
    0
end`
	_, errs := checkSource(src)
	expectError(t, errs, "expected Showable and Tagged, got ShowOnly")
	found := false
	for _, e := range errs {
		for _, h := range e.Hints {
			if strings.Contains(h, "ShowOnly does not implement Tagged") {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("the help does not name the missing interface: %v", errs)
	}
}

func TestConjunction_WidensToAMemberAndNotBack(t *testing.T) {
	src := interfaceBoundSetup + `fn demo(x: Showable and Tagged) -> String
    s: Showable = x
    Showable.show(s)
end`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)

	src = interfaceBoundSetup + `fn demo(s: Showable) -> String
    x: Showable and Tagged = s
    Tagged.tag(x)
end`
	_, errs = checkSource(src)
	expectError(t, errs, "expected Showable and Tagged, got Showable")
}

func TestConjunction_OrderAndDuplicatesDoNotMatter(t *testing.T) {
	src := interfaceBoundSetup + `fn take(x: Tagged and Showable) -> String
    Tagged.tag(x)
end
fn demo(x: Showable and Tagged and Showable) -> String
    take(x)
end`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
	a := NewConjunction([]*InterfaceType{{Name: "B"}, {Name: "A"}})
	b := NewConjunction([]*InterfaceType{{Name: "A"}, {Name: "B"}, {Name: "A"}})
	if !TypesEqual(a, b) || a.String() != "A and B" {
		t.Fatalf("B and A is %s, and A and B and A is %s: want one type", a, b)
	}
}

func TestConjunction_AliasIsAValueType(t *testing.T) {
	src := interfaceBoundSetup + `typealias ShowAndTag Showable and Tagged
struct Box
    field: ShowAndTag
end
fn announce(x: ShowAndTag) -> String
    Showable.show(x)
end
fn make(x: User) -> ShowAndTag
    x
end
fn demo() -> String
    announce(Box{field: make(User{name: "a"})}.field)
end`
	_, errs := checkSource(src)
	expectNoErrors(t, errs)
}

func TestConjunction_DemandOutsideItsMembers(t *testing.T) {
	src := interfaceBoundSetup + `interface Priced
    fn price(item: self) -> Int
end
fn demo(x: Showable and Tagged) -> Int
    Priced.price(x)
end`
	_, errs := checkSource(src)
	expectError(t, errs, "`x` is a `Showable and Tagged`, and Showable and Tagged does not require Priced of its implementors")
}

func TestConjunction_HoldsOnlyInterfaces(t *testing.T) {
	src := interfaceBoundSetup + `fn demo(_x: Showable and Int) -> Int
    0
end`
	_, errs := checkSource(src)
	expectError(t, errs, "`and` joins interfaces, and Int is not an interface")
}
