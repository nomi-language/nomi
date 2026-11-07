package mf2_test

import (
	"github.com/nomi-language/nomi/internal/mf2"
	"strings"
	"testing"
)

var tokenKindNames = map[mf2.TokenKind]string{
	mf2.TokenKeyword: "keyword", mf2.TokenVariable: "variable", mf2.TokenNamespace: "namespace", mf2.TokenFunction: "function",
	mf2.TokenOption: "option", mf2.TokenAttribute: "attribute", mf2.TokenMarkup: "markup", mf2.TokenName: "name",
	mf2.TokenNumber: "number", mf2.TokenQuoted: "quoted", mf2.TokenKey: "key", mf2.TokenCatchAll: "catchall",
	mf2.TokenQuote: "quote", mf2.TokenBrace: "brace", mf2.TokenOperator: "operator",
}

func tokenList(text string) string {
	var out []string
	for _, t := range mf2.Tokens(text) {
		out = append(out, tokenKindNames[t.Kind]+" "+text[t.Span.Start:t.Span.End])
	}
	return strings.Join(out, "\n")
}

func TestTokens(t *testing.T) {
	for _, c := range []struct{ name, text, want string }{
		{"plain text has none", "Hello, world", ""},
		{"a placeholder", "Hi {$name}!", "brace {\nvariable $name\nbrace }"},
		{"a match", ".input {$count :integer}\n.match $count\none {{{$count} item}}\n* {{items}}",
			"keyword .input\nbrace {\nvariable $count\nfunction :integer\nbrace }\nkeyword .match\nvariable $count\n" +
				"key one\nquote {{\nbrace {\nvariable $count\nbrace }\nquote }}\ncatchall *\nquote {{\nquote }}"},
		{"a namespace and options", "Your {$product :nomi:noun case=genitive count=$n}",
			"brace {\nvariable $product\nnamespace :nomi\nfunction :noun\noption case\noperator =\nname genitive\noption count\noperator =\nvariable $n\nbrace }"},
		{"literals", ".local $x = {|a b| :number minimumFractionDigits=2} {{{$x} {#b/}}}",
			"keyword .local\nvariable $x\noperator =\nbrace {\nquoted |a b|\nfunction :number\noption minimumFractionDigits\noperator =\nnumber 2\nbrace }\n" +
				"quote {{\nbrace {\nvariable $x\nbrace }\nbrace {\nmarkup #b\noperator /\nbrace }\nquote }}"},
		{"a syntax error keeps what parsed", ".match $count\none {{x}}\n* {{oops {$count",
			"keyword .match\nvariable $count\nkey one\nquote {{\nquote }}\ncatchall *\nquote {{\nbrace {\nvariable $count"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := tokenList(c.text); got != c.want {
				t.Errorf("mf2.Tokens(%q) =\n%s\nwant\n%s", c.text, got, c.want)
			}
		})
	}
}

// mf2.Tokens never panics, whatever it reads, and its tokens are within the
// text and in order.
func FuzzTokens(f *testing.F) {
	for _, s := range []string{"", "{", "{{", ".input {$x :integer}", ".match $x one {{a}} * {{b}}", "{$a :nomi:noun case=x}", "{#b a=1 @c=|d|/}", "|", "\\"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, text string) {
		prev := 0
		for _, tok := range mf2.Tokens(text) {
			if tok.Span.Start < prev || tok.Span.End > len(text) || tok.Span.End <= tok.Span.Start {
				t.Fatalf("token %+v out of order or range in %q", tok, text)
			}
			prev = tok.Span.End
		}
	})
}

func TestAddPluralForms(t *testing.T) {
	en := locale("en", "English", false)
	pl := locale("pl", "Polish", false)
	sig := mf2.Signature{Name: "cart", Params: []mf2.Param{{Name: "count", Kind: mf2.KindInt, Localize: true}, {Name: "who", Kind: mf2.KindString, Localize: true}}}
	for _, c := range []struct {
		name, text string
		loc        mf2.Locale
		want       string
	}{
		{"english", "{$count} items", en, ".input {$count :integer}\n.match $count\none {{{$count} items}}\n* {{{$count} items}}"},
		{"polish", "{$count} rzeczy", pl, ".input {$count :integer}\n.match $count\none {{{$count} rzeczy}}\nfew {{{$count} rzeczy}}\nmany {{{$count} rzeczy}}\n* {{{$count} rzeczy}}"},
		{"keeps the input", ".input {$count :integer} .local $w = {$who :string} {{{$w}: {$count}}}", en,
			".input {$count :integer}\n.local $w = {$who :string}\n.match $count\none {{{$w}: {$count}}}\n* {{{$w}: {$count}}}"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, ok := mf2.AddPluralForms(c.text, "count", sig, c.loc)
			if !ok || got != c.want {
				t.Fatalf("mf2.AddPluralForms = %q, %v; want %q", got, ok, c.want)
			}
			m, errs := mf2.Parse(got)
			if len(errs) > 0 {
				t.Fatalf("the result does not parse: %v", errs)
			}
			if ds := mf2.Validate(m, sig, c.loc); len(ds) > 0 {
				t.Fatalf("the result does not validate: %v", ds)
			}
		})
	}
	for _, text := range []string{".input {$count :integer} .match $count * {{x}}", "{$count", ".input {$count :string} {{x}}"} {
		if got, ok := mf2.AddPluralForms(text, "count", sig, en); ok {
			t.Errorf("mf2.AddPluralForms(%q) = %q, want none", text, got)
		}
	}
	if got := mf2.PluralCandidates("{$count} by {$who}", sig); len(got) != 1 || got[0] != "count" {
		t.Errorf("mf2.PluralCandidates = %v, want [count]", got)
	}
}
