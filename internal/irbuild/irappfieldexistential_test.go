package irbuild

import "testing"

// A `with` writes an application field typed as an interface, or as a list
// of one, from concrete values: the value is lowered against the field's
// type, so a list of mixed values enters `List<Display>`.
func TestIRAppFieldExistentialWrite_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std::io

struct App
    context: Context
    out: Display
    many: List<Display>
end

fn boot(_startup: Startup) -> Result<App, String>
    Ok(App{context: Context.root(), out: 1, many: [1]})
end

fn main()
    io.print(Display.to_string(App.out))
    show()
end

fn show()
    with App.out = "x"
    with App.many = [2, "y"]
    io.print(Display.to_string(App.out))
    io.print(App.many |> Iter.map(Display.to_string) |> String.join(","))
end
`
	irRunSource(t, src, "1\nx\n2,y\n")
}

// A `with` sets a Map application field from `{}`, whose element
// types are the field's.
func TestIRAppFieldEmptyMapWrite_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std::io

struct App
    cache: Map<String, Int>
end

fn boot(_startup: Startup) -> Result<App, String>
    Ok(App{cache: {"a" => 1}})
end

fn size() -> Int
    Map.size(App.cache)
end

fn main()
    io.print(size())
    with App.cache = {}
    io.print(size())
end
`
	irRunSource(t, src, "1\n0\n")
}
