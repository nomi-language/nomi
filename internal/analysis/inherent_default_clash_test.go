package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// defaultClashMarker is in every diagnostic for a type function named as a
// default its interface gives it.
const defaultClashMarker = "shadows the default"

func inherentDefaultClashes(fa *analysis.FileAnalysis) []analysis.TypeError {
	var out []analysis.TypeError
	for _, e := range entryErrors(fa) {
		if strings.Contains(e.Message, defaultClashMarker) {
			out = append(out, e)
		}
	}
	return out
}

// ord2 declares an open default `least` and a final default `most`.
const ord2 = `interface Ord2 {
    fn cmp(a: self, b: self) -> Int

    open fn least(a: self, b: self) -> self {
        if cmp(a, b) <= 0 { a } else { b }
    }

    fn most(a: self, b: self) -> self {
        if cmp(a, b) >= 0 { a } else { b }
    }
}

struct Odd {
    n: Int
}
`

// The reproducer: `Odd.least(a, b)` checked clean and was refused when it
// ran ("this call to `Odd.least` is not supported yet"), since the call named
// both the type's function and the default Ord2 gives Odd. The open default
// is overridden by writing it in the impl; the final one cannot be.
func TestInherentDefaultClash_OpenAndFinalDefaults(t *testing.T) {
	fa := buildInherentFixture(t, map[string]string{
		"main.nomi": ord2 + `
impl Ord2 for Odd {
    fn cmp(a: Odd, b: Odd) -> Int {
        a.n - b.n
    }
}

impl Odd {
    pub fn least(_a: Odd, b: Odd) -> Odd {
        b
    }

    pub fn most(a: Odd, _b: Odd) -> Odd {
        a
    }
}

fn main() {
    dbg Odd.least(Odd{n: 1}, Odd{n: 2})
}
`,
	})
	got := inherentDefaultClashes(fa)
	want := []struct {
		line, col int
		msg       string
	}{
		{24, 12, "`Odd.least` shadows the default `least` that `Ord2` gives `Odd`, and Nomi does not overload, so a call `Odd.least(...)` would name both; to override the default, write `fn least` in `impl Ord2 for Odd` instead"},
		{28, 12, "`Odd.most` shadows the default `most` that `Ord2` gives `Odd`, and Nomi does not overload, so a call `Odd.most(...)` would name both; `Ord2`'s `most` is final and cannot be overridden, so give this function another name"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d default-clash diagnostics, want %d: %v (all errors: %v)", len(got), len(want), got, entryErrors(fa))
	}
	for i, w := range want {
		if got[i].Message != w.msg {
			t.Errorf("diagnostic %d message = %q\nwant %q", i, got[i].Message, w.msg)
		}
		if got[i].Line != w.line || got[i].Col != w.col {
			t.Errorf("diagnostic %d at %d:%d, want %d:%d", i, got[i].Line, got[i].Col, w.line, w.col)
		}
	}
}

// The impl in another file than the type: the error is still at the type's
// own function, in the type's file.
func TestInherentDefaultClash_ImplInAnotherFile(t *testing.T) {
	fa := buildInherentFixture(t, map[string]string{
		"shapes.nomi": `pub struct Odd {
    n: Int
}

impl Odd {
    pub fn least(_a: Odd, b: Odd) -> Odd {
        b
    }
}
`,
		"main.nomi": `import shapes.Odd

interface Ord2 {
    fn cmp(a: self, b: self) -> Int

    open fn least(a: self, b: self) -> self {
        if cmp(a, b) <= 0 { a } else { b }
    }
}

impl Ord2 for Odd {
    fn cmp(a: Odd, b: Odd) -> Int {
        a.n - b.n
    }
}

fn main() {
    Unit
}
`,
	})
	wantOneClash := func(got []analysis.TypeError) {
		t.Helper()
		if len(got) != 1 {
			t.Fatalf("got %d default-clash diagnostics, want 1: %v (all errors: %v)", len(got), got, entryErrors(fa))
		}
		e := got[0]
		if e.Line != 6 || e.Col != 12 || !strings.HasSuffix(e.File, "shapes.nomi") {
			t.Errorf("at %s %d:%d, want shapes.nomi 6:12", e.File, e.Line, e.Col)
		}
		if !strings.HasPrefix(e.Message, "`Odd.least` shadows the default `least` that `Ord2` gives `Odd`") {
			t.Errorf("message = %q", e.Message)
		}
	}
	wantOneClash(inherentDefaultClashes(fa))
}

// No clash: an impl that writes the default overrides it, which leaves the
// type no function of its own by that name to clash with; a type function
// of another name, and another type's function of the same name, are not
// the default.
func TestInherentDefaultClash_OverridesAndOtherNamesAreNoClash(t *testing.T) {
	fa := buildInherentFixture(t, map[string]string{
		"main.nomi": ord2 + `
struct Even {
    n: Int
}

impl Ord2 for Odd {
    fn cmp(a: Odd, b: Odd) -> Int {
        a.n - b.n
    }

    fn least(_a: Odd, b: Odd) -> Odd {
        b
    }
}

impl Odd {
    pub fn lesser(a: Odd, _b: Odd) -> Odd {
        a
    }
}

impl Even {
    pub fn least(a: Even, _b: Even) -> Even {
        a
    }
}

fn main() {
    assert Odd.least(Odd{n: 1}, Odd{n: 2}).n == 2
    assert Odd.most(Odd{n: 1}, Odd{n: 2}).n == 2
    assert Odd.lesser(Odd{n: 1}, Odd{n: 2}).n == 1
    assert Even.least(Even{n: 1}, Even{n: 2}).n == 1
}
`,
	})
	if got := inherentDefaultClashes(fa); len(got) != 0 {
		t.Fatalf("got default-clash diagnostics where none is: %v", got)
	}
	if errs := entryErrors(fa); len(errs) != 0 {
		t.Fatalf("the fixture does not check clean: %v", errs)
	}
}
