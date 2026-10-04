package irbuild

import (
	"testing"
)

// TestGenericEnumInstancesAreDistinctDefs: two instantiations of one enum
// template are two defs.
//
// A collapsed identity makes two instantiations one type, and the second
// instantiation's payload slot is then the first's type. For an enum the slot
// is written through a tag switch, so the wrong type can reach a `case` arm
// before anything rejects it.
//
// It reads the DEFS, which say which instance and which slot.
func TestGenericEnumInstancesAreDistinctDefs(t *testing.T) {
	src := "enum Wrapper<T> {\n  Wrapped T\n  Empty\n}\n\n" +
		"fn a(_w: Wrapper<Int>): Int {\n  0\n}\n\n" +
		"fn b(_w: Wrapper<String>): Int {\n  0\n}\n"
	g := lowerableGen(t, src)
	var enums []*typeDef
	for _, d := range g.genericInstOrder {
		if d.isEnum {
			enums = append(enums, d)
		}
	}
	if len(enums) != 2 {
		t.Fatalf("built %d enum instances, want 2 (`Wrapper<Int>` and `Wrapper<String>`); "+
			"with fewer this guard has nothing to compare", len(enums))
	}
	if enums[0] == enums[1] || enums[0].variants[0].payloads[0].k == enums[1].variants[0].payloads[0].k {
		t.Errorf("both instantiations of %s carry one payload kind, so the second "+
			"instantiation's payload slot is the first's type", enums[0].nomi)
	}
	for _, d := range enums {
		if len(d.slots) != 1 {
			t.Errorf("%s has %d slots, want 1: `Wrapped` carries one payload and `Empty` "+
				"carries none", d.nomi, len(d.slots))
			continue
		}
		if d.slots[0].k != d.variants[0].payloads[0].k {
			t.Errorf("%s's slot holds %s while its `Wrapped` payload is %s — assignSlots "+
				"did not run for this instance, so the payload reads back as the zero value",
				d.nomi, d.slots[0].k.nomi(), d.variants[0].payloads[0].k.nomi())
		}
	}
	if enums[0].slots[0].k == enums[1].slots[0].k {
		t.Errorf("both instantiations laid out the same slot type %s, so the substitution "+
			"frame did not reach the payload", enums[0].slots[0].k.nomi())
	}
}

// TestGenericEnum_RecursivePayloadIsBoxed: a recursive generic enum's payload
// is boxed.
//
// `genericInstance` must call `assignSlots`, or an enum payload has no slot and
// reads back as the zero value. An enum's payload boxing is decided inside
// `assignSlots`, on `slotDef.boxed`, through the same `appendInlineDefs` walk
// `buildTypes` applies to a struct field; the storage belongs to the slot and
// a payload only indexes one, so there is no `payload.boxed`.
//
// The test asserts the outcome rather than the mechanism, so a change that
// moves the boxing decision off `slotDef.boxed`, or drops `genericInstance`'s
// `d.assignSlots()` call, fails here. Without boxing the slot's type contains
// itself. This asserts the def, which says which slot.
func TestGenericEnum_RecursivePayloadIsBoxed(t *testing.T) {
	src := "enum Chain<T> {\n  Link Chain<T>\n  Tip T\n}\n\n" +
		"fn depth(_c: Chain<Int>): Int {\n  0\n}\n"
	g := lowerableGen(t, src)
	var inst *typeDef
	for _, d := range g.genericInstOrder {
		if d.isEnum && d.nomi == "Chain" {
			inst = d
			break
		}
	}
	if inst == nil {
		t.Fatal("no `Chain<Int>` instance was built, so this guard ran over an empty population")
	}
	link := inst.variant("Link")
	if link == nil || len(link.payloads) != 1 {
		t.Fatalf("`Chain<Int>.Link` did not survive resolution with its payload; variants are %d", len(inst.variants))
	}
	if link.payloads[0].slot < 0 {
		t.Fatal("`Link`'s payload got no slot, so assignSlots did not run and the payload " +
			"reads back as the zero value")
	}
	if !inst.slots[link.payloads[0].slot].boxed {
		t.Error("`Chain<Int>.Link`'s slot is UNBOXED, so its type contains itself. " +
			"An enum's payload boxing is assignSlots' own, on slotDef.boxed")
	}
}

// TestGenericEnumTemplateDeclarationFabricatesNoRefusal: the template
// declaration records no refusal of its own.
//
// Variants are resolved per instantiation under a substitution, so walking the
// template's payloads would report a refusal naming a type the program never
// wrote (`non-scalar variant payload | Wrapper.Wrapped carries T`).
// `resolveEnum` returns early for a template, as `resolveStruct` does, and this
// guards that early return.
func TestGenericEnumTemplateDeclarationFabricatesNoRefusal(t *testing.T) {
	src := "enum Wrapper<T> {\n  Wrapped T\n  Empty\n}\n\n" +
		"fn seen(_w: Wrapper<Int>): Int {\n  0\n}\n"
	g := lowerableGen(t, src)
	d := g.types["Wrapper"]
	if d == nil {
		t.Fatal("no `Wrapper` declaration def")
	}
	for _, r := range d.refusals {
		if r.construct == "generic type" {
			// declModifiers' own, and it is what a BARE `Wrapper` mention
			// reports. Expected, and deliberately not removed.
			continue
		}
		t.Errorf("the template declaration carries a fabricated refusal %s {%s}. Its variants "+
			"are resolved per INSTANTIATION under a substitution, so a refusal recorded from "+
			"the declaration walk names a payload type the program never wrote",
			r.construct, r.detail)
	}
}
