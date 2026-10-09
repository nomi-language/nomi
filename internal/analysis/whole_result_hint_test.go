package analysis_test

import (
	"strings"
	"testing"
)

// A name holding the whole `Result<Int, String>` given where a
// `Result<Tx, String>` is expected was usually bound by an arm that matched
// the error, and the author meant to pass the error on. The mismatch says
// so. Where the error types differ too, or the value is not a name, there is
// no such hint.
func TestWholeResultHint(t *testing.T) {
	const decls = `struct Tx {
    amount: Int
}

fn parse_amount(input: String): Result<Int, String> {
    Err(input)
}

fn parse_flag(input: String): Result<Int, Int> {
    Err(0)
}

fn lookup(input: String): Maybe<Int> {
    None
}

fn take(r: Result<Tx, String>): Int {
    0
}

`
	const resultHelp = "`err` is the whole `Result<Int, String>`, so its `Ok` type is still `Int`; " +
		"to pass the error on, write `Err(e) -> Err(e)`, or unwrap with `try`"
	for _, tc := range []struct {
		name, fn, message, help string
	}{
		{
			"catch-all arm",
			`fn withdrawal(input: String): Result<Tx, String> {
    case parse_amount(input) {
        Ok(amount) -> Ok(Tx{amount: amount})
        err -> err
    }
}
`,
			"case branch type mismatch: expected Result<Tx, String>, got Result<Int, String>",
			resultHelp,
		},
		{
			"as name over Err",
			`fn withdrawal(input: String): Result<Tx, String> {
    case parse_amount(input) {
        Ok(amount) -> Ok(Tx{amount: amount})
        Err(_) as err -> err
    }
}
`,
			"case branch type mismatch: expected Result<Tx, String>, got Result<Int, String>",
			resultHelp,
		},
		{
			"return",
			`fn withdrawal(input: String): Result<Tx, String> {
    case parse_amount(input) {
        Ok(amount) -> Ok(Tx{amount: amount})
        err -> {
            return err
        }
    }
}
`,
			"return type mismatch: expected Result<Tx, String>, got Result<Int, String>",
			resultHelp,
		},
		{
			"argument",
			`fn withdrawal(input: String): Int {
    err = parse_amount(input)
    take(err)
}
`,
			"argument 1: expected Result<Tx, String>, got Result<Int, String>",
			resultHelp,
		},
		{
			"maybe catch-all arm",
			`fn find(input: String): Maybe<Tx> {
    case lookup(input) {
        Some(amount) -> Some(Tx{amount: amount})
        none -> none
    }
}
`,
			"case branch type mismatch: expected Maybe<Tx>, got Maybe<Int>",
			"`none` is the whole `Maybe<Int>`, so its `Some` type is still `Int`; " +
				"to pass the absence on, write `None -> None`, or unwrap with `try`",
		},
		{
			"error types differ",
			`fn withdrawal(input: String): Result<Tx, String> {
    case parse_flag(input) {
        Ok(amount) -> Ok(Tx{amount: amount})
        err -> err
    }
}
`,
			"case branch type mismatch: expected Result<Tx, String>, got Result<Int, Int>",
			"",
		},
		{
			"not a name",
			`fn withdrawal(input: String): Result<Tx, String> {
    case parse_amount(input) {
        Ok(amount) -> Ok(Tx{amount: amount})
        Err(_) -> parse_amount(input)
    }
}
`,
			"case branch type mismatch: expected Result<Tx, String>, got Result<Int, String>",
			"",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			errs := checkWithStdlib(decls + tc.fn)
			for _, e := range errs {
				if e.Message != tc.message {
					continue
				}
				if tc.help == "" {
					for _, h := range e.Hints {
						if strings.Contains(h, "is the whole") {
							t.Fatalf("hint %q, want none", h)
						}
					}
					return
				}
				if len(e.Hints) != 1 || e.Hints[0] != tc.help {
					t.Fatalf("hints %q, want [%q]", e.Hints, tc.help)
				}
				return
			}
			t.Fatalf("the front end accepts this, or reports something else: no %q error; got %v", tc.message, errs)
		})
	}
}
