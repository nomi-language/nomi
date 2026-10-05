package format

import "testing"

// Struct update by spread — `{..base, field: value}`.
//
// The formatter emitted `Fields` and nothing else before this, so `{..p, x: 9}`
// came back as `{x: 9}` and `{..p}` as `{}`. That is a rewrite of a working
// program into a different one, which is the same class of defect the
// single-field punning rule above `structLitFieldsPunnable` records.
//
// Two layouts, inline and stacked, and the choice is the AUTHOR's:
// `structLitUserMultiLine` now counts the `..` token's line as an element,
// because a spread plus one field is two elements on two lines and a
// fields-only rule read it as single-line and flattened it.

func TestFormat_StructSpreadInline(t *testing.T) {
	roundTrip(t, `struct Point {
    x: Int
    y: Int
}

fn main() {
    p = Point{x: 1, y: 2}
    a = {..p, x: 9}
    b = {..p}
    c = {..p, x: 1, y: 2}
    d = {..make(), x: 3}
    e = {..p.inner, x: 4}
}
`)
}

func TestFormat_StructSpreadStacked(t *testing.T) {
	roundTrip(t, `fn main() {
    p = Point{x: 1, y: 2}

    c = {
        ..p,
        x: 11,
        y: 12,
    }
}
`)
}

// A newline-separated stacked literal lands on the comma-separated canonical
// form, which is what an ordinary stacked anonymous literal does — the
// spread is carried along, not given a style of its own.
func TestFormat_StructSpreadStackedMigratesToCommas(t *testing.T) {
	migrates(t, `fn main() {
    c = {
        ..p
        x: 11
        y: 12
    }
}
`, `fn main() {
    c = {
        ..p,
        x: 11,
        y: 12,
    }
}
`)
}

// A spread plus ONE field, stacked. The regression this guards is specific:
// with a fields-only multi-line rule there is one field, so `len < 2` returned
// false and the literal was flattened onto one line.
func TestFormat_StructSpreadOneFieldStaysStacked(t *testing.T) {
	roundTrip(t, `fn main() {
    c = {
        ..p,
        x: 11,
    }
}
`)
}

// Punning is allowed after a spread at any arity. `{..p, name}` cannot be read
// as a block, so the rule that forces `{name: name}` for a lone anonymous
// field does not reach here.
func TestFormat_StructSpreadPunsASingleField(t *testing.T) {
	migrates(t, `fn main() {
    c = {..p, name: name}
}
`, `fn main() {
    c = {..p, name}
}
`)
}

// Without a spread, a single anonymous field still may NOT pun — the rule
// structLitFieldsPunnable states. Kept beside the case above so the two cannot
// drift into one.
func TestFormat_SingleAnonFieldStillDoesNotPun(t *testing.T) {
	roundTrip(t, `fn main() {
    c = {name: name}
}
`)
}
