package analysis

import "testing"

// An unannotated `pub once` exposes its value's type as an annotated one
// does, so an internal type or a type declared inside a body in it is the
// visibility error the annotation would get.
func TestCheck_PublicOnceInferredTypeVisibility(t *testing.T) {
	_, errs := checkSource(`
struct Hidden
    x: Int
end
pub struct Shown
    x: Int
end
pub once hidden = [Hidden{x: 1}]
pub once shown = Shown{x: 1}
once private_hidden = Hidden{x: 2}
pub once boxes = begin
    struct Box
        inner: Int
    end

    [Box{inner: 1}]
end
`)
	expectErrorCount(t, errs, "public once binding exposes internal type Hidden", 1)
	expectErrorCount(t, errs, "public once binding exposes private type Box, which is declared inside a body", 1)
	expectErrorCount(t, errs, " exposes ", 2)
}

// Each interface of a conjunction the inferred type holds is checked, as a
// single interface is, at any depth, and a type argument is reported before
// the type it is an argument of.
func TestCheck_PublicOnceExposesAConjunctionsInterfaces(t *testing.T) {
	_, errs := checkSource(`
interface Hidden
    fn hid(s: self) -> Int
end
interface Named
    fn name(s: self) -> String
end
pub interface Shown
    fn show(s: self) -> Int
end
struct Thing
    n: Int
end
struct Box<T>
    v: T
end
impl Hidden for Thing
    fn hid(t: Thing) -> Int
        t.n
    end
end
impl Named for Thing
    fn name(_t: Thing) -> String
        "thing"
    end
end
impl Shown for Thing
    fn show(t: Thing) -> Int
        t.n
    end
end
fn one() -> Hidden
    Thing{n: 1}
end
fn both() -> Hidden and Named
    Thing{n: 1}
end
fn open() -> Hidden and Shown
    Thing{n: 1}
end
pub once single = one()
pub once pair = both()
pub once pairs = [both()]
pub once half = open()
pub once boxed = Box{v: one()}
`)
	var got []string
	for _, e := range errs {
		got = append(got, e.Message)
	}
	want := []string{
		"public once binding exposes internal type Hidden",
		"public once binding exposes internal type Hidden",
		"public once binding exposes internal type Named",
		"public once binding exposes internal type Hidden",
		"public once binding exposes internal type Named",
		"public once binding exposes internal type Hidden",
		"public once binding exposes internal type Hidden",
		"public once binding exposes internal type Box",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d errors, want %d:\n%v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("error %d is %q, want %q", i, got[i], want[i])
		}
	}
}
