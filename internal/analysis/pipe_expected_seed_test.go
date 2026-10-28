package analysis_test

import "testing"

// A pipe stage's arguments take the pipe's expected type as the direct
// call's do: an anonymous struct literal in the stage's lambda builds the
// struct the result names, through the pipe as written out.
func TestPipeStage_TakesTheExpectedTypeAsTheDirectCallDoes(t *testing.T) {
	const decls = `struct P {
    x: Int
}

`
	for name, body := range map[string]string{
		"direct": `fn f() -> Maybe<P> {
    Maybe.map(Some(1), fn x -> #{x})
}
`,
		"pipe": `fn f() -> Maybe<P> {
    Some(1) |> Maybe.map(fn x -> #{x})
}
`,
		"pipe into an annotated binding": `fn f() -> Maybe<P> {
    p: Maybe<P> = Some(1) |> Maybe.map(fn x -> #{x})
    p
}
`,
	} {
		t.Run(name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(decls + body)
			expectNoErrorsT(t, errs)
		})
	}
}

// A literal that does not fit the struct is reported against the struct,
// through the pipe as in the direct call.
func TestPipeStage_ExpectedTypeStillRejectsALiteralThatDoesNotFit(t *testing.T) {
	const decls = `struct P {
    x: Int
}

`
	for name, body := range map[string]string{
		"direct": `fn f() -> Maybe<P> {
    Maybe.map(Some(1), fn x -> #{y: x})
}
`,
		"pipe": `fn f() -> Maybe<P> {
    Some(1) |> Maybe.map(fn x -> #{y: x})
}
`,
	} {
		t.Run(name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(decls + body)
			expectErrorT(t, errs, "P has no field 'y'")
		})
	}
}
