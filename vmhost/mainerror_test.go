package vmhost_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// Run answers a main that returns `Err` as a *MainError carrying the payload's
// text, which WriteFailure prints as `nomi run` does. Call answers the same
// Err as main's value, since a called function's Result is data.
func TestRun_MainErrIsAMainError(t *testing.T) {
	for _, tc := range []struct {
		name, src, text string
	}{
		{"String", "fn main(): Result<Unit, String> {\n    Err(\"boom\")\n}\n", "boom"},
		{"Display", `struct Oops {
    code: Int
}

impl Display for Oops {
    fn to_string(o: Oops): String {
        "oops ${o.code}"
    }
}

fn main(): Result<Unit, Oops> {
    Err(Oops{code: 7})
}
`, "oops 7"},
		{"no Display", `struct Oops {
    zeta: String
    alpha: Int
}

fn main(): Result<Unit, Oops> {
    Err(Oops{zeta: "z", alpha: 1})
}
`, `Oops{zeta: "z", alpha: 1}`},
		{"Int", "fn main(): Result<Unit, Int> {\n    Err(42)\n}\n", "42"},
		{"List of String", "fn main(): Result<Unit, List<String>> {\n    Err([\"a\", \"b\"])\n}\n", "[a, b]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := vmhost.LoadSource("main.nomi", tc.src)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			var out bytes.Buffer
			err = p.Run(context.Background(), &out, nil, false)
			var mainErr *vmhost.MainError
			if !errors.As(err, &mainErr) {
				t.Fatalf("Run answered %v (%T); want a *vmhost.MainError", err, err)
			}
			if mainErr.Text != tc.text {
				t.Fatalf("MainError.Text = %q; want %q", mainErr.Text, tc.text)
			}
			var printed bytes.Buffer
			vmhost.WriteFailure(&printed, err)
			if want := "error: " + tc.text + "\n"; printed.String() != want {
				t.Fatalf("WriteFailure printed %q; want %q", printed.String(), want)
			}
			if got := vmhost.FormatFailure(err); got != "error: "+tc.text {
				t.Fatalf("FormatFailure = %q; want %q", got, "error: "+tc.text)
			}
			if _, err := p.Call(context.Background(), "main"); err != nil {
				t.Fatalf("Call(main) answered an error %v; a called function's Err is its value", err)
			}
		})
	}
}

func TestRun_MainOkAndNonResultMainsSucceed(t *testing.T) {
	for _, src := range []string{
		"fn main(): Result<Unit, String> {\n    Ok(Unit)\n}\n",
		"fn main(): Maybe<Int> {\n    None\n}\n",
		"fn main(): Int {\n    1\n}\n",
	} {
		p, err := vmhost.LoadSource("main.nomi", src)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if err := p.Run(context.Background(), &bytes.Buffer{}, nil, false); err != nil {
			t.Fatalf("Run answered %v for\n%s", err, src)
		}
	}
}
