package vmhost_test

import "testing"

// A `Type<T>` witness inspects as the type it names, `Type<TraceId>` (spec
// §4 *Type witnesses*), by every spelling of its Debug: the type's own
// `Type.inspect`, which names std's impl by its type, the function as a
// value, `Debug.inspect`, `io.inspect`, `dbg`, a generic function's
// `Debug.inspect`, and as a part of a list and a Maybe. `Type.inspect` and
// `dbg` printed `Type`, the bare name the universal Debug of a host type
// writes.
func TestTypeWitnessInspect_EverySpellingPrintsTheTypeItNames(t *testing.T) {
	out, err := runVM(t, `import std::io

type TraceId String

fn show<T>(x: T) -> String
    Debug.inspect(x)
end

fn main()
    w: Type<TraceId> = TraceId
    f: (Type<TraceId>) -> String = Type.inspect
    io.print(Type.inspect(w))
    io.print(f(w))
    io.print(Debug.inspect(w))
    io.inspect(w)
    dbg w
    io.print(show(w))
    io.print(Debug.inspect([w]))
    io.print(Debug.inspect(Some(w)))
end
`)
	if err != nil {
		t.Fatalf("run: %v\noutput so far:\n%s", err, out)
	}
	want := `Type<TraceId>
Type<TraceId>
Type<TraceId>
Type<TraceId>
dbg line 16: w = Type<TraceId>
Type<TraceId>
[Type<TraceId>]
Some(Type<TraceId>)
`
	if out != want {
		t.Errorf("output:\n%s\nwant:\n%s", out, want)
	}
}
