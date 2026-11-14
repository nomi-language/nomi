package lsp

import (
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// A hover on a typed literal's prefix, quotes or text ends with what the
// literal is, what builds it and what each `${}` takes, read from the
// checker's record of its handler (hoverdoc.TypedLiteralNote); a hover on
// a slot's expression keeps its own hover and ends with what that slot
// takes.
func TestHover_TypedLiteral(t *testing.T) {
	const decls = `import
    std::io
    std::html.Html
    std::calendar.Date
    std::literals.{Fragment, Literal}
end

struct Box
    contents: String
end

impl Literal for Box
    fn from_fragments(fragments: List<Fragment<String>>) -> Box
        body = Iter.reduce(fragments, "", fn acc, frag ->
            case frag
                .Static(s) -> acc + s
                .Dynamic(v) -> acc + v
            end
        end)

        Box{contents: body}
    end
end

fn imports_used(d: Date) -> Html
    _ = d
    Html"<i>x</i>"
end

fn main()
    name = "world"
`
	cases := []struct {
		name, body, want string
	}{
		{"an Html literal's text", `    page = Html"<p>hello ▮${name}</p>"
    io.print(Html.to_string(page))`,
			"*typed literal*: an `Html`, not a `String`, read by the compiler as an HTML template from 2 text parts and 1 slot; each `${}` takes a `ToHtml`"},
		{"an Html literal's prefix", `    page = Ht▮ml"<p>${name}</p>"
    io.print(Html.to_string(page))`,
			"*typed literal*: an `Html`, not a `String`, read by the compiler as an HTML template from 2 text parts and 1 slot; each `${}` takes a `ToHtml`"},
		{"an Html literal with slots in two places", `    page = Html"<a title=\"${name}\">▮${name}</a>"
    io.print(Html.to_string(page))`,
			"*typed literal*: an `Html`, not a `String`, read by the compiler as an HTML template from 3 text parts and 2 slots; each `${}` takes, by where it stands, a `Display` or a `ToHtml`"},
		{"an Html literal with no slots", `    io.print(name)
    page = Html"<b>hi▮</b>"
    io.print(Html.to_string(page))`,
			"*typed literal*: an `Html`, not a `String`, read by the compiler as an HTML template from its text"},
		{"a Date literal", `    day = Date"2026-▮05-04"
    io.print(day)
    io.print(name)`,
			"*typed literal*: a `Date`, not a `String`, built by `Date`'s `Literal` impl from its text"},
		{"a Date literal with a slot", `    day = Date"2026-▮${name}-04"
    io.print(day)`,
			"*typed literal*: a `Result<Date, Error>`, not a `String`, built by `Date`'s `Literal` impl from 2 text parts and 1 slot; each `${}` takes a `String`"},
		{"a slot of an Html literal", `    page = Html"<p>${na▮me}</p>"
    io.print(Html.to_string(page))`,
			"a slot of the `Html` literal: takes a `ToHtml`"},
		{"a program's own Literal impl", `    b = Box"hello ${name}▮!"
    io.print(b.contents)`,
			"*typed literal*: a `Box`, not a `String`, built by `Box`'s `Literal` impl from 2 text parts and 1 slot; each `${}` takes a `String`"},
		{"a slot of a program's own literal", `    b = Box"hello ${▮name}!"
    io.print(b.contents)`,
			"a slot of the `Box` literal: takes a `String`"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src, pos := hoverMarkerPosition(t, decls+tc.body+"\nend\n")
			doc := NewServer().docs.Open("file:///typed_literal.nomi", src)
			if doc == nil || doc.Analysis == nil || len(doc.Errors) > 0 || len(doc.Analysis.TypeErrors) > 0 {
				t.Fatalf("the program does not check clean: %v %v", doc.Errors, doc.Analysis.TypeErrors)
			}
			got, _ := hoverAt(t, src, protocol.Position{Line: uint32(pos.Line - 1), Character: uint32(pos.Col - 1)})
			if got == "" {
				t.Fatal("no hover")
			}
			last := got
			if i := strings.LastIndex(got, "\n\n"); i >= 0 {
				last = got[i+2:]
			}
			if last != tc.want {
				t.Errorf("hover ends %q, want %q\nwhole hover: %q", last, tc.want, got)
			}
			if strings.HasPrefix(tc.want, "a slot") && !strings.Contains(got, "name") {
				t.Errorf("a slot's hover lost the expression's own hover: %q", got)
			}
		})
	}
}
