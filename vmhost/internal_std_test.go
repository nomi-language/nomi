package vmhost_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// What a `phrase` declaration's function calls (std::phrase's internal
// constructors) and the Go-backed halves of std::phrase and std::money
// (std::phrase::format, std::money::format) and every `host` module
// (std::fs::host) declare nothing public: they are internal to std, as any
// module's unmarked declarations are to it. A program that names one gets
// the ordinary error, which says the package it is internal to, and the
// expansion that calls them through its hidden import still runs
// (tests/19-locales).
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
			src:  "import std::phrase\n\nfn main()\n    _ = phrase." + call + "\nend\n",
			want: "`" + fn + "` is internal to package `std`",
		})
	}
	cases = append(cases,
		rejected{"selective", "import std::phrase.compiled\n\nfn main()\n    _ = compiled(\"x\", \"t\", [])\nend\n",
			"`compiled` is internal to package `std`"},
		rejected{"grouped selective", "import\n    std::phrase.integer\nend\n\nfn main()\n    _ = integer(1)\nend\n",
			"`integer` is internal to package `std`"},
		rejected{"internal type", "import std::phrase\n\nfn f(a: phrase.Argument) -> Int\n    1\nend\n\nfn main() end\n",
			"`Argument` is internal to package `std`"},
		rejected{"money/format", "import std::money::format\n\nfn main()\n    _ = format.label\nend\n",
			"`label` is internal to package `std`"},
		rejected{"phrase/format", "import\n    std::phrase::format.variant_name\nend\n\nfn main()\n    _ = variant_name(1)\nend\n",
			"`variant_name` is internal to package `std`"},
		rejected{"fs/host", "import std::fs::host\n\nfn main()\n    _ = host.here()\nend\n",
			"`here` is internal to package `std`"},
		rejected{"http/host through std", "import std\n\nfn main()\n    _ = std::http::host.default_client()\nend\n",
			"`default_client` is internal to package `std`"},
		// Removed: `Currency.parse` reads an ISO code, and a language's
		// genders and cases are the compiler's and the language server's.
		rejected{"Currency.iso", "import std::currency.Currency\n\nfn main()\n    _ = Currency.iso(\"EUR\")\nend\n",
			"type 'Currency' has no member 'iso'"},
		rejected{"Locale.genders", "fn main()\n    _ = Locale.genders(Locale\"pl\")\nend\n",
			"type 'Locale' has no member 'genders'"},
		rejected{"Locale.cases", "fn main()\n    _ = Locale.cases(Locale\"pl\")\nend\n",
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

// A program cannot edit std, so the help on naming one of std's internal or
// private declarations says the name is not part of std's public surface,
// never to write `pub` on it. When one public std module declares a type of
// the name, the help points at that module's reference page.
func TestInternalStd_TheHelpSaysItIsNotPublic(t *testing.T) {
	for _, c := range []struct{ name, src, help string }{
		{"a function", "import std::http::host\n\nfn main()\n    _ = host.default_client()\nend\n",
			"help: `default_client` is not part of std's public surface"},
		{"a type with a public counterpart", "import std::http::host\n\nfn f(_c: host.Client) -> Int\n    1\nend\n\nfn main()\n    _ = f\nend\n",
			"help: `Client` is not part of std's public surface; the public `Client` is in `std::http`, https://nomi-lang.org/reference/http/"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := vmhost.LoadSource("main.nomi", c.src)
			if err == nil {
				t.Fatalf("the front end admits a program that names std's internals:\n%s", c.src)
			}
			if !strings.Contains(err.Error()+"\n", c.help+"\n") || strings.Contains(err.Error(), "write `pub`") {
				t.Errorf("the help is not %q:\n%s", c.help, err)
			}
		})
	}
}
