package irbuild

import "testing"

// `Maybe.with_default` and `Result.with_default` over a Unit payload run:
// both outcomes are the one Unit value, so the call answers Unit after its
// operands have run, side effects included, rather than declining at the
// rt crossing, which takes no Unit payload.
func TestIRWithDefaultOverUnit_Runs(t *testing.T) {
	irRunSource(t, `import std::io

fn act(ok: Bool) -> Result<Unit, String>
    io.print("act ${ok}")
    if ok then Ok(Unit) else Err("no")
end

fn main()
    act(False) |> Result.with_default(Unit)
    done: Unit = Result.with_default(act(True), Unit)
    io.inspect(done)
    io.inspect(Maybe.with_default(Some(Unit), Unit))
    io.inspect(Result.with_default(Ok(3), 0))
end
`, "act False\nact True\nUnit\nUnit\n3\n")
}
