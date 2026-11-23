package irbuild

import "testing"

// A Context renders as `<context>` (spec §12 *Display and Debug*) by every
// spelling of its Debug: `Debug.inspect`, `Context.inspect`, which names
// std's `impl Debug for Context` by its type, `io.inspect`, `dbg`, a
// generic function's `Debug.inspect`, and as a part of a struct, a list, an
// Option, a Map, a tuple and a record.
func TestContextInspect_EverySpellingPrintsTheSpecsText(t *testing.T) {
	irRunSource(t, `import std::io

struct S
    c: Context
end

fn show<T>(x: T) -> String
    Debug.inspect(x)
end

fn main()
    c = Context.root()
    io.print(Debug.inspect(c))
    io.print(Context.inspect(c))
    io.inspect(c)
    dbg c
    io.print(show(c))
    io.print(Debug.inspect(S{c: c}))
    io.print(Debug.inspect([c]))
    io.print(Debug.inspect(Some(c)))
    io.print(Debug.inspect({1 => c}))
    io.print(Debug.inspect((c, 1)))
    io.print(Debug.inspect({f: c}))
end
`, `<context>
<context>
<context>
dbg line 16: c = <context>
<context>
S{c: <context>}
[<context>]
Some(<context>)
{1 => <context>}
(<context>, 1)
{f: <context>}
`)
}
