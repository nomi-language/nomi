package format

import "testing"

// An application-field read is an ordinary field access, and a `with` is one
// statement per field among the block's other statements.
func TestFormat_WithStatement(t *testing.T) {
	src := `fn main() {
    io.print(App.tag)
    with App.tag = "audit"
    with App.context = Context.with_timeout(App.context, Duration.seconds(1))
    run()
}
`
	formatWithTwice(t, src, src)
}

// Extra spaces collapse, and comments above and after a `with` line stay.
func TestFormat_WithKeepsComments(t *testing.T) {
	src := `fn main() {
    // why
    with   App.logger =   Silent // inline
    run_import()
}
`
	want := `fn main() {
    // why
    with App.logger = Silent // inline
    run_import()
}
`
	formatWithTwice(t, src, want)
}

// A struct-literal value needs no parentheses, so the formatter drops any
// written around one.
func TestFormat_WithDropsStructLiteralParentheses(t *testing.T) {
	src := `fn main() {
    with App.clock = (FakeClock{at: 4242})
    tick()
}
`
	want := `fn main() {
    with App.clock = FakeClock{at: 4242}
    tick()
}
`
	formatWithTwice(t, src, want)
}

// Parentheses around anything other than a struct literal stay.
func TestFormat_WithKeepsOtherParentheses(t *testing.T) {
	src := `fn main() {
    with App.n = (1 + 2)
    tick()
}
`
	formatWithTwice(t, src, src)
}

// A long value breaks the way a binding's does.
func TestFormat_WithLongValue(t *testing.T) {
	src := "fn main() { with App.config = make_config_with_a_long_argument_name(really_long_argument_name, second_really_long_argument)\n run() }\n"
	want := `fn main() {
    with App.config = make_config_with_a_long_argument_name(
        really_long_argument_name,
        second_really_long_argument,
    )

    run()
}
`
	formatWithTwice(t, src, want)
}

// A `with` line in a group's setup formats as it does in any block.
func TestFormat_WithInSetup(t *testing.T) {
	src := `tests "g" {
    setup {
        with App.store = (FakeStore{})
        Fixture{a: 1}
    }

    test "t", {a} {
        assert a == 1
    }
}
`
	want := `tests "g" {
    setup {
        with App.store = FakeStore{}
        Fixture{a: 1}
    }

    test "t", {a} {
        assert a == 1
    }
}
`
	formatWithTwice(t, src, want)
}

// formatWithTwice formats src, requires want, and requires a second pass to
// change nothing.
func formatWithTwice(t *testing.T, src, want string) {
	t.Helper()
	got, err := Format(src)
	if err != nil {
		t.Fatalf("Format(%q): %v", src, err)
	}
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	again, err := Format(got)
	if err != nil {
		t.Fatalf("second Format: %v", err)
	}
	if again != got {
		t.Errorf("not idempotent:\n%s\nthen:\n%s", got, again)
	}
}
