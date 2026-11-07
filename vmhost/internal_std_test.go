package vmhost_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// What a `phrase` declaration's function calls (std::phrase's private
// constructors) and the Go-backed halves of std::phrase and std::money
// (std::phrase_format, std::money_format) are std's own. A program that
// names one is rejected with the name and the module it is internal to,
// and the expansion that calls them still runs (tests/19-locales).
func TestInternalStd_AProgramCannotNameIt(t *testing.T) {
	constructors := []string{
		`compiled("x", "t", [])`, `integer(1)`, `float(1.0)`, `decimal(1d)`,
		`text("a")`, `variant("A")`, `localized_variant("A", 1)`,
		`compiled_noun("x", "t")`, `noun(1)`, `list(["a"])`, `date(1)`,
		`time(1)`, `moment(1)`, `money(1)`, `nested(1)`, `localized(1)`,
	}
	type rejected struct{ name, src, want string }
	var cases []rejected
	for _, call := range constructors {
		fn := call[:strings.IndexByte(call, '(')]
		cases = append(cases, rejected{
			name: "phrase." + fn,
			src:  "import std::phrase\n\nfn main() {\n    _ = phrase." + call + "\n}\n",
			want: "`phrase." + fn + "` is internal to std::phrase",
		})
	}
	cases = append(cases,
		rejected{"selective", "import std::phrase.compiled\n\nfn main() {\n    _ = compiled(\"x\", \"t\", [])\n}\n",
			"`compiled` is internal to std::phrase"},
		rejected{"grouped selective", "import {\n    std::phrase.integer\n}\n\nfn main() {\n    _ = integer(1)\n}\n",
			"`integer` is internal to std::phrase"},
		rejected{"private type", "import std::phrase\n\nfn f(a: phrase.Argument) -> Int {\n    1\n}\n\nfn main() {\n}\n",
			"`phrase.Argument` is internal to std::phrase"},
		rejected{"money_format", "import std::money_format\n\nfn main() {\n}\n",
			"`std::money_format` is internal to std::money"},
		rejected{"phrase_format", "import {\n    std::phrase_format.variant_name\n}\n\nfn main() {\n}\n",
			"`std::phrase_format` is internal to std::phrase"},
		// Removed: `Currency.parse` reads an ISO code, and a language's
		// genders and cases are the compiler's and the language server's.
		rejected{"Currency.iso", "import std::currency.Currency\n\nfn main() {\n    _ = Currency.iso(\"EUR\")\n}\n",
			"type 'Currency' has no member 'iso'"},
		rejected{"Locale.genders", "fn main() {\n    _ = Locale.genders(Locale`pl`)\n}\n",
			"type 'Locale' has no member 'genders'"},
		rejected{"Locale.cases", "fn main() {\n    _ = Locale.cases(Locale`pl`)\n}\n",
			"type 'Locale' has no member 'cases'"},
	)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := vmhost.LoadSource("main.nomi", c.src)
			if err == nil {
				t.Fatalf("the front end admits a program that names std's internals:\n%s", c.src)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("the error does not say %q:\n%s", c.want, err)
			}
		})
	}
}
