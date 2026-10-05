package rt

import "testing"

// The prelude enums are called by one implementation, so a test that compares
// two computations over them compares two paths that both end here. Every
// assertion below therefore spells out the expected answer.

func TestMaybeZeroValueIsDetectablyInvalid(t *testing.T) {
	var m Maybe[int64]
	if m.Tag != 0 {
		t.Fatalf("zero Maybe has tag %d, want 0", m.Tag)
	}
	if m.Tag == TagSome || m.Tag == TagNone {
		t.Fatal("an unconstructed Maybe is indistinguishable from a variant")
	}
	// The reason for the reservation: a slice of them is n never-constructed
	// values, not n copies of the first variant.
	for i, v := range make([]Maybe[int64], 3) {
		if v.Tag != 0 {
			t.Fatalf("make() element %d looks constructed: tag %d", i, v.Tag)
		}
	}
	var r Result[int64, string]
	if r.Tag != 0 {
		t.Fatalf("zero Result has tag %d, want 0", r.Tag)
	}
	if r.Tag == TagOk || r.Tag == TagErr {
		t.Fatal("an unconstructed Result is indistinguishable from a variant")
	}
}

func TestPreludeTagsAreDeclarationOrder(t *testing.T) {
	// std/maybe.nomi declares `Some T` then `None`; std/results.nomi declares
	// `Ok T` then `Err E`. Tags are 1-based in that order.
	// internal/irbuild's TestPreludeShapeMatchesStdSource is what holds this
	// against the .nomi files; these are the absolute values it holds them to.
	if got := Some[int64](7).Tag; got != 1 {
		t.Fatalf("Some tag = %d, want 1", got)
	}
	if got := None[int64]().Tag; got != 2 {
		t.Fatalf("None tag = %d, want 2", got)
	}
	if got := Ok[int64, string](7).Tag; got != 1 {
		t.Fatalf("Ok tag = %d, want 1", got)
	}
	if got := Err[int64, string]("bad").Tag; got != 2 {
		t.Fatalf("Err tag = %d, want 2", got)
	}
	if TagSome == TagNone {
		t.Fatal("Some and None share a tag")
	}
	if TagOk == TagErr {
		t.Fatal("Ok and Err share a tag")
	}
}

func TestPreludePayloadsRoundTrip(t *testing.T) {
	if got := Some("hi").Some; got != "hi" {
		t.Fatalf("Some payload = %q, want %q", got, "hi")
	}
	// None's payload slot is the T zero value and is never read by lowered
	// code; pinned so a future layout change that aliased it onto something
	// live would fail here rather than in a corpus program.
	if got := None[string]().Some; got != "" {
		t.Fatalf("None payload = %q, want the zero value", got)
	}
	ok := Ok[int64, string](42)
	if ok.Ok != 42 || ok.Err != "" {
		t.Fatalf("Ok = %+v, want {Tag:1 Ok:42 Err:\"\"}", ok)
	}
	e := Err[int64, string]("nope")
	if e.Err != "nope" || e.Ok != 0 {
		t.Fatalf("Err = %+v, want {Tag:2 Ok:0 Err:\"nope\"}", e)
	}
}

func TestPreludeValuesAreComparableAndCopied(t *testing.T) {
	// A Maybe is a value, not a pointer: assigning one copies it, which is
	// what makes `case` binding a payload a copy rather than an alias.
	a := Some[int64](1)
	b := a
	b.Some = 2
	if a.Some != 1 {
		t.Fatalf("assignment aliased: a.Some = %d, want 1", a.Some)
	}
	if a == b {
		t.Fatal("distinct payloads compared equal")
	}
	if a != Some[int64](1) {
		t.Fatal("two Some(1) are not equal")
	}
}

// MaybeToResult, pinned absolutely.
//
// A never-constructed receiver is caught only here, because no Nomi program
// can produce one and no golden-file fixture can therefore reach it.
func TestPreludeMethodHelpers(t *testing.T) {
	if got := MaybeToResult(Some[int64](3), "missing"); got != Ok[int64, string](3) {
		t.Fatalf("MaybeToResult(Some(3)) = %+v, want Ok(3)", got)
	}
	if got := MaybeToResult(None[int64](), "missing"); got != Err[int64, string]("missing") {
		t.Fatalf("MaybeToResult(None) = %+v, want Err(\"missing\")", got)
	}

	// A never-constructed receiver — Tag 0, which is neither variant. No Nomi
	// program can produce one, so a program fixture cannot reach this
	// and only an absolute assertion can. Each helper must take the branch that
	// does not invent a payload out of a Go zero value.
	var zeroMaybe Maybe[int64]
	if got := MaybeToResult(zeroMaybe, "e"); got != Err[int64, string]("e") {
		t.Fatalf("MaybeToResult(never-constructed) = %+v, want Err", got)
	}
}
