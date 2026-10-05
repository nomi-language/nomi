package irbuild

// The pipe operator.
//
// `x |> f()` is `f(x)` and `xs |> Iter.map(g)` is `Iter.map(xs, g)`: the left
// operand becomes the FIRST argument of the right operand's call. So the
// lowering is a desugaring — a shallow copy of the right-hand call with the
// left operand spliced in at index 0, handed to the ordinary call path. Nothing
// about a pipe reaches the built IR.
//
// # Why a desugaring and not its own lowering
//
// A pipe's decisions — which function the stage names, whether a placeholder
// claims the piped slot, whether a trailing lambda routes past a defaulted
// parameter — are all properties of the call, and gen.call already makes all
// of them for a call that was written directly. Reimplementing them here would
// be a second call lowering whose only distinguishing feature is that it can
// disagree.
//
// # Evaluation order, which is the part that can go wrong silently
//
// A pipe evaluates the LEFT operand first, before the callee is resolved and
// before any other argument is evaluated. Putting the left operand at argument
// index 0 gives that order, because directCall evaluates its arguments left to
// right and forces every non-final one into a temporary (see operand). It has
// to be checked rather than assumed: a builder that appended instead would
// produce `f(g(), x)` for `x |> f(g())`, which is a well-typed program that
// runs g() before x. TestPipeEvaluationOrder and testdata/pipes.nomi both
// observe the order from inside the operands.
//
// # What a pipe does NOT do
//
// A piped call records no assertion rows for its arguments, while a direct
// call does. So inside an assertion subject, `assert f(x) == y` reports a row
// for `x` and `assert x |> f() == y` reports none. Both pass or fail
// identically; only the FAILURE REPORT differs. See gen.pipedCall, and
// testdata/pipe_assert_report.nomi, whose assertion FAILS so the report text
// itself is compared.
