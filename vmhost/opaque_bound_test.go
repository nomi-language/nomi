package vmhost

import "testing"

// Inside an opaque type's file, the `TryFrom` impl the compiler provides
// for it meets a bound (spec §4 *Opaque distinct types*), a generic
// function's that another file declares included, and the program runs:
// the generic body, lowered in its own file, reaches the provided impl at
// the type the defining file instantiated it at.
func TestRun_ProvidedTryFromMeetsABoundInsideItsFile(t *testing.T) {
	got := runProject(t, map[string]string{
		"gen.nomi": `pub fn make<T>(n: Int) -> Option<T> where T: TryFrom<Int>
    T.try_from(n)
end
`,
		"pct.nomi": `import gen

pub opaque type Pct Int when fn n -> n >= 0 and n <= 100

pub fn parse(n: Int) -> Option<Pct>
    gen.make(n)
end

pub fn value(Pct(n)) -> Int
    n
end
`,
		"main.nomi": `import
    std::io
    pct
end

fn main()
    io.inspect(pct.parse(5) |> Option.map(pct.value))
    io.inspect(pct.parse(500) |> Option.map(pct.value))
end
`,
	})
	if want := "Some(5)\nNone\n"; got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}
