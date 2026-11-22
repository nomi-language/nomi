package irbuild

import (
	"fmt"
	"testing"
)

// siblingNamesLib declares a generic interface whose signature names its
// own `Item`, a default function whose parameter default calls the file's
// private `base`, and a function whose default does too.
const siblingNamesLib = `pub struct Item
    n: Int
end

fn base() -> Int
    40
end

pub fn bump(x: Int, by: Int = base() + 2) -> Int
    x + by
end

pub interface Maker<T>
    fn make(value: self, x: T) -> Item

    fn size(value: self, x: T, extra: Int = base()) -> Int
        Maker.make(value, x).n + extra
    end
end
`

const siblingNamesMain = `import
    lib
end

%s
fn base() -> Int
    1
end

struct Foo
    k: Int
end

impl lib.Maker<Int> for Foo
    fn make(foo: Foo, x: Int) -> lib.Item
        lib.Item{n: foo.k + x}
    end
end

fn main()
    foo = Foo{k: 3}
    dbg base()
    dbg lib.bump(1)
    dbg lib.Maker.make(foo, 4).n
    dbg lib.Maker.size(foo, 5)
    dbg lib.Maker.size(foo, 5, extra: 100)
end
`

// A sibling's generic interface is instantiated here from its declaring
// file's records, and its defaults mean what they mean there, whatever
// this file binds the names they mention to: its own `Item`, no `Item` at
// all, and its own `base`.
func TestSiblingNames_AnInterfaceAndItsDefaultsMeanTheirFilesNames(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const want = "dbg line %d: base() = 1\n" +
		"dbg line %d: lib.bump(1) = 43\n" +
		"dbg line %d: lib.Maker.make(foo, 4).n = 7\n" +
		"dbg line %d: lib.Maker.size(foo, 5) = 48\n" +
		"dbg line %d: lib.Maker.size(foo, 5, extra: 100) = 108\n"
	for _, tc := range []struct {
		name, own string
		first     int
	}{
		{"its own Item", "struct Item\n    s: String\nend\n", 25},
		{"no Item", "", 22},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := tc.first
			typeCycleRun(t, map[string]string{
				"lib.nomi":  siblingNamesLib,
				"main.nomi": fmt.Sprintf(siblingNamesMain, tc.own),
			}, fmt.Sprintf(want, l, l+1, l+2, l+3, l+4))
		})
	}
}
