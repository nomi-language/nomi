package format

import "testing"

// Map update by spread: `%{..base, key => value}`. The spread leads, always,
// and the literal lays out as any map literal does: flat when it fits, one
// element per line when it does not or when it holds a comment.

func TestFormat_MapSpreadInline(t *testing.T) {
	roundTrip(t, `fn main() {
    m = %{"a" => 1}
    a = %{..m, "b" => 2}
    b = %{..m, "b" => 2, "c" => 3}
    c = %{..make(), "b" => 2}
    d = %{..cfg.prefs, "theme" => "dark"}
}
`)
}

func TestFormat_MapSpreadSpacingAndLines(t *testing.T) {
	migrates(t, `fn main() {
    a = %{  ..m ,"b"=>2 }
    b = %{..m
        "b" => 2
    }
    c = %{
        ..m, "b" => 2}
}
`, `fn main() {
    a = %{..m, "b" => 2}
    b = %{..m, "b" => 2}

    c = %{
        ..m,
        "b" => 2,
    }
}
`)
}

func TestFormat_MapSpreadBreaksByWidth(t *testing.T) {
	migrates(t, `fn main() {
    c = %{..m, "a long key number one" => 1000000, "a long key number two" => 2000000, "a long key number three" => 3}
}
`, `fn main() {
    c = %{
        ..m,
        "a long key number one" => 1000000,
        "a long key number two" => 2000000,
        "a long key number three" => 3,
    }
}
`)
}

func TestFormat_MapSpreadKeepsComments(t *testing.T) {
	roundTrip(t, `fn main() {
    d = %{
        // before the base
        ..m, // after the base
        // before b
        "b" => 2, // after b
        // at the end
    }
}
`)
}

func TestFormat_MapSpreadCommentBreaksAFlatLiteral(t *testing.T) {
	migrates(t, `fn main() {
    d = %{..m, // after the base
      "b" => 2}
}
`, `fn main() {
    d = %{
        ..m, // after the base
        "b" => 2,
    }
}
`)
}

// A map three levels deep breaks as the depth rule breaks any map, the
// spread counted as an element; the depth-2 map inside it fits on one line.
func TestFormat_MapSpreadNestedDepth(t *testing.T) {
	migrates(t, `fn main() {
    e = %{..m, "x" => %{..m, "y" => %{..m, "z" => 1}}}
}
`, `fn main() {
    e = %{
        ..m,
        "x" => %{..m, "y" => %{..m, "z" => 1}},
    }
}
`)
}

// A comment between `{` and `..` stays above the spread in the struct form
// too; it used to move below it, to the first field.
func TestFormat_StructSpreadCommentBeforeTheBase(t *testing.T) {
	roundTrip(t, `fn main() {
    d = {
        // before the base
        ..p, // after the base
        // before x
        x: 2, // after x
    }
}
`)
}
