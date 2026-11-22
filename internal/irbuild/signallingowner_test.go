package irbuild

import "testing"

// Whether a qualified call drives its callback as a signalling one is read
// from what the call's record says it is qualified with (recordedQualRoute,
// irSignallingCallback), never from how the owner is spelled. A std type
// imported under the name `Iter` keeps its own two-operand `map`, and
// std's `Iter.map` and `Map.map_values`, named under other names, keep
// their signalling callbacks.
func TestSignallingCallback_ReadsTheOwnerTheRecordNames(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	irRunSource(t, `import std::io
import std::random.{Generator as Iter, Seed}

fn main()
    gen = Iter.map(Iter.constant(3), fn x -> x * 10)
    (value, _) = Iter.step(gen, Seed.from_int(1))
    io.print("${value}")
end
`, "30\n")
	irRunSource(t, `import std::io
import std::iter.{Iter as It}
import std::maps.{Map as M}

fn main()
    xs =
        It.map([1, 2, 3, 4], fn x ->
            if x > 2 then break
            x * 10
        end)
        |> It.to_list()
    m =
        M.map_values({"a" => 1, "b" => 5}, fn v ->
            if v > 2 then continue
            v * 10
        end)
    io.print("${xs} ${m}")
end
`, "[10, 20] {a => 10}\n")
}
