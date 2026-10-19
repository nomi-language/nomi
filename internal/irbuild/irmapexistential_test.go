package irbuild

import "testing"

// `Map.put` into a map the checker solved as `Map<K, Display>` runs when
// the map was built at a concrete value type and the new value is another
// concrete type: the map is the same value at the interface's kind.
func TestIRMapPutIntoExistentialValues_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `import std/io

fn main() {
    m: Map<String, Display> = Map.put(Map.put(%{}, "a", 1), "b", True)
    io.print(Map.values(m) |> Iter.map(Display.to_string) |> String.join(","))
}
`
	irRunSource(t, src, "1,True\n")
}
