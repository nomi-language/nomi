package analysis_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// clashSuffix ends the diagnostic for a type function named as one of its
// impl functions.
const clashSuffix = "would name both; give this function another name"

func inherentImplClashes(fa *analysis.FileAnalysis) []analysis.TypeError {
	var out []analysis.TypeError
	for _, e := range entryErrors(fa) {
		if strings.Contains(e.Message, clashSuffix) {
			out = append(out, e)
		}
	}
	return out
}

// wantOneClash requires exactly one clash diagnostic, with this text, at
// this line and column of the file named base ("" for the entry, which this
// fixture loads with no path).
func wantOneClash(t *testing.T, fa *analysis.FileAnalysis, base string, line, col int, msg string) {
	t.Helper()
	got := inherentImplClashes(fa)
	if len(got) != 1 {
		t.Fatalf("got %d clash diagnostics, want 1: %v (all errors: %v)", len(got), got, entryErrors(fa))
	}
	e := got[0]
	if e.Message != msg {
		t.Errorf("message = %q\nwant      %q", e.Message, msg)
	}
	if e.Line != line || e.Col != col {
		t.Errorf("position = %d:%d, want %d:%d", e.Line, e.Col, line, col)
	}
	if base == "" && e.File != "" || base != "" && filepath.Base(e.File) != base {
		t.Errorf("file = %q, want one named %s", e.File, base)
	}
}

const boxClashMessage = "`Box.multiply` is already `Multiply<Int, Box>`'s `multiply` for `Box`, and Nomi does not overload, so a call `Box.multiply(...)` would name both; give this function another name"

// The reproducer: `Box.multiply(b, 1.5)` reached the impl's
// `multiply(lhs: Box, rhs: Int)` and failed with "argument 2: expected Int,
// got Float", so the inherent function could never be called by its owner.
func TestInherentImplClash_InherentFnNamedAsAnImplFn(t *testing.T) {
	fa := buildInherentFixture(t, map[string]string{
		"main.nomi": `struct Box {
    n: Int
}

impl Multiply<Int, Box> for Box {
    fn multiply(lhs: Box, rhs: Int) -> Box {
        #Box{n: lhs.n * rhs}
    }
}

impl Box {
    pub fn multiply(lhs: Box, _factor: Float) -> Box {
        lhs
    }
}

fn main() {
    Unit
}
`,
	})
	wantOneClash(t, fa, "", 12, 12, boxClashMessage)
}

// The impl written after the inherent function: the same one error, at the
// inherent function.
func TestInherentImplClash_ImplDeclaredAfterTheInherentFn(t *testing.T) {
	fa := buildInherentFixture(t, map[string]string{
		"main.nomi": `struct Box {
    n: Int
}

impl Box {
    pub fn multiply(lhs: Box, _factor: Float) -> Box {
        lhs
    }
}

impl Multiply<Int, Box> for Box {
    fn multiply(lhs: Box, rhs: Int) -> Box {
        #Box{n: lhs.n * rhs}
    }
}

fn main() {
    Unit
}
`,
	})
	wantOneClash(t, fa, "", 6, 12, boxClashMessage)
}

// The impl in another file of the module than the type and its inherent
// function: one type, so one clash, reported in the type's file.
func TestInherentImplClash_ImplInAnotherFile(t *testing.T) {
	fa := buildInherentFixture(t, map[string]string{
		"shapes.nomi": `pub struct Box {
    n: Int
}

impl Box {
    pub fn multiply(lhs: Box, _factor: Float) -> Box {
        lhs
    }
}
`,
		"main.nomi": `import shapes.Box

impl Multiply<Int, Box> for Box {
    fn multiply(lhs: Box, rhs: Int) -> Box {
        #Box{n: lhs.n * rhs}
    }
}

fn main() {
    Unit
}
`,
	})
	wantOneClash(t, fa, "shapes.nomi", 6, 12, boxClashMessage)
}

// A generic receiver: `impl Wrap<T>` and `impl Display for Wrap<T>` name one
// type.
func TestInherentImplClash_GenericReceiver(t *testing.T) {
	fa := buildInherentFixture(t, map[string]string{
		"main.nomi": `struct Wrap<T> {
    value: T
}

impl Display for Wrap<T> {
    fn to_string(_self: Wrap<T>) -> String {
        "wrap"
    }
}

impl Wrap<T> {
    pub fn to_string(_w: Wrap<T>, _width: Int) -> String {
        "wide wrap"
    }
}

fn main() {
    Unit
}
`,
	})
	wantOneClash(t, fa, "", 12, 12,
		"`Wrap.to_string` is already `Display`'s `to_string` for `Wrap`, and Nomi does not overload, so a call `Wrap.to_string(...)` would name both; give this function another name")
}

// Two types that share a name are two types: a `Box` of another file with a
// `multiply` impl does not clash with this file's `Box.multiply`, and an
// inherent function whose name no impl function has is no clash either.
func TestInherentImplClash_OtherNamesAndOtherTypesAreNoClash(t *testing.T) {
	fa := buildInherentFixture(t, map[string]string{
		"other.nomi": `pub struct Box {
    n: Int
}

impl Multiply<Int, Box> for Box {
    fn multiply(lhs: Box, rhs: Int) -> Box {
        #Box{n: lhs.n * rhs}
    }
}
`,
		"main.nomi": `import other

struct Box {
    n: Int
}

impl Multiply<Int, Box> for Box {
    fn multiply(lhs: Box, rhs: Int) -> Box {
        #Box{n: lhs.n * rhs}
    }
}

impl Box {
    pub fn multiply_by(lhs: Box, _factor: Float) -> Box {
        lhs
    }
}

fn main() {
    a = Box.multiply_by(#Box{n: 1}, 1.5)
    b = other.Box.multiply(#other.Box{n: 1}, 2)
    assert a.n == 1
    assert b.n == 2
}
`,
	})
	if got := inherentImplClashes(fa); len(got) != 0 {
		t.Fatalf("got clash diagnostics where none is: %v", got)
	}
}
