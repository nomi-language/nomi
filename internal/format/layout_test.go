package format

import "testing"

func TestRender_Flat(t *testing.T) {
	d := Group(Concat(Text("a"), Line(), Text("b"), Line(), Text("c")))
	got := Render(d, 80)
	if got != "a b c" {
		t.Errorf("got %q, want %q", got, "a b c")
	}
}

func TestRender_BreaksWhenOverWidth(t *testing.T) {
	d := Group(Nest(2, Concat(
		Text("x"),
		Line(), Text("|> very_long_function_name_that_forces_break()"),
		Line(), Text("|> another()"),
	)))
	got := Render(d, 20)
	want := "x\n  |> very_long_function_name_that_forces_break()\n  |> another()"
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestRender_HardLineForcesBreak(t *testing.T) {
	d := Group(Concat(Text("a"), HardLine(), Text("b")))
	got := Render(d, 80)
	if got != "a\nb" {
		t.Errorf("got %q, want %q", got, "a\nb")
	}
}

func TestRender_NestedGroups_InnerFlat_OuterBroken(t *testing.T) {
	inner := Group(Concat(Text("("), Text("x"), Text(","), Text(" "), Text("y"), Text(")")))
	outer := Group(Nest(2, Concat(
		Text("foo"),
		Line(), Text("long_enough_to_force_outer_break"),
		Line(), inner,
	)))
	got := Render(outer, 30)
	want := "foo\n  long_enough_to_force_outer_break\n  (x, y)"
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestRender_NilEmits_Nothing(t *testing.T) {
	if Render(Nil(), 80) != "" {
		t.Error("Nil should render to empty string")
	}
}

func TestRender_LineOrEmpty_Flat_EmitsNothing(t *testing.T) {
	d := Group(Concat(Text("("), LineOrEmpty(), Text("x"), LineOrEmpty(), Text(")")))
	got := Render(d, 80)
	if got != "(x)" {
		t.Errorf("got %q, want %q", got, "(x)")
	}
}

func TestRender_LineOrEmpty_Broken_BecomesNewline(t *testing.T) {
	d := Group(Concat(
		Text("("),
		Nest(2, Concat(LineOrEmpty(), Text("very_long_arg_that_forces_break"))),
		LineOrEmpty(),
		Text(")"),
	))
	got := Render(d, 10)
	want := "(\n  very_long_arg_that_forces_break\n)"
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestRender_IfBroken_FlatSelectsFlat(t *testing.T) {
	// Enclosing Group is flat (fits in width) — IfBroken emits the `flat` branch.
	d := Group(Concat(Text("a"), IfBroken(Text(" "), Text(",")), Text("b")))
	got := Render(d, 80)
	if got != "a b" {
		t.Errorf("got %q, want %q", got, "a b")
	}
}

func TestRender_IfBroken_BrokenSelectsBroken(t *testing.T) {
	// Enclosing Group is broken because it exceeds width — IfBroken emits
	// the `broken` branch. Here the `broken` branch is a trailing comma that
	// appears only in the multi-line rendering of an arg list.
	d := Group(Concat(
		Text("("),
		Nest(2, Concat(LineOrEmpty(), Text("very_long_arg_that_forces_break"))),
		IfBroken(Nil(), Text(",")),
		LineOrEmpty(),
		Text(")"),
	))
	got := Render(d, 10)
	want := "(\n  very_long_arg_that_forces_break,\n)"
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestRender_IfBroken_FlatBranchCountedForFit(t *testing.T) {
	// The `flat` branch width must participate in the `fits` decision.
	// At width 4, "a<flat=123>b" = 5 cols doesn't fit flat, so the Group
	// breaks — and only the broken branch ("") is emitted.
	d := Group(Concat(Text("a"), IfBroken(Text("123"), Text("")), Text("b")))
	got := Render(d, 4)
	if got != "ab" {
		t.Errorf("got %q, want %q", got, "ab")
	}
	// At width 5 it stays flat.
	got2 := Render(d, 5)
	if got2 != "a123b" {
		t.Errorf("got %q, want %q", got2, "a123b")
	}
}
