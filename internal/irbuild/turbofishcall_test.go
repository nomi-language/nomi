package irbuild

import "testing"

// A turbofish on any call but `Iface.method<T>` runs as the same call
// without it, at the signature the checker instantiated: a std owner's
// generic (`Maybe.collect<Int>`, the checker's own suggested fix for an
// ambiguous element type), two type arguments, a file-qualified generic, a
// local and a nested generic, and `Context.value<T>`.
func TestTurbofishCall_RunsAtTheCheckedSignature(t *testing.T) {
	verifyLambdaProgram(t, `import std/io

type TraceId String

fn ident<T>(x: T): T {
    x
}

fn main() {
    fn twice<T>(x: T): List<T> {
        [x, x]
    }
    c = io.capture<Int>("", || 1)
    io.inspect(c.value)
    io.inspect(Maybe.collect<Int>([None, None]))
    io.inspect(Maybe.collect<Int>([Some(1)]))
    io.inspect(Maybe.map<Int, String>(Some(1), |x| "${x}"))
    ctx = Context.with_value(Context.root(), TraceId("t"))
    io.inspect(Context.value<TraceId>(ctx, TraceId))
    io.inspect(ident<Int>(3))
    io.inspect(ident<Maybe<Int>>(None))
    io.inspect(twice<String>("a"))
}
`, "1\nNone\nSome([1])\nSome(\"1\")\nSome(TraceId(\"t\"))\n3\nNone\n[\"a\", \"a\"]\n")
}
