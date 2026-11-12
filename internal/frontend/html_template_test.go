package frontend_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/frontend"
)

// An `Html` literal is read as an HTML template when the program is checked
// (spec §18 *HTML templates*): its markup errors are reported at their
// character in the literal, and a slot value with no rendering where it
// stands is the argument check's error, with the context's help.

func htmlDiagnostics(t *testing.T, body string) frontend.Diagnostics {
	t.Helper()
	src := "import std::html.Html\n\nfn page(name: String, n: Int, h: Html) -> Html\n" + body + "\nend\n"
	err := checkSource(t, src)
	if err == nil {
		return nil
	}
	var all frontend.Diagnostics
	if !errors.As(err, &all) {
		t.Fatalf("not diagnostics: %v", err)
	}
	// The page's parameters are each read by some cases only.
	var ds frontend.Diagnostics
	for _, d := range all {
		if !strings.Contains(d.Message, "is never read") {
			ds = append(ds, d)
		}
	}
	return ds
}

func TestHtmlTemplate_Accepted(t *testing.T) {
	for _, body := range []string{
		`    Html"<p class='${name}'>${n}</p>"`,
		`    Html"<p>${n > 0}</p>"`,
		`    Html"<a href='/u/${name}?q=${n}#${name}'>${h}</a>"`,
		`    Html"<input checked=${n > 0} title=${Some(name)}/>"`,
		`    Html"<div style='width: ${n}%'>${[h, h]}</div>"`,
		"    Html\"\"\"\n        <!DOCTYPE html>\n        <html><title>${name}</title></html>\n        \"\"\"",
		"    Html`<p>${name}</p>`",
		`    Html"<p class='a ${if n > 0 then name} ${Some(n)}'><title>${if n > 0 then name}</title></p>"`,
		`    Html"<a href='/u/${Some(name)}?q=${if n > 0 then n}#${Some(n)}'>x</a>"`,
		`    Html"<b style='width: ${Some(n)}%'></b>"`,
	} {
		if ds := htmlDiagnostics(t, body); len(ds) > 0 {
			t.Errorf("the front end rejects %s: %v", body, ds)
		}
	}
}

func TestHtmlTemplate_MarkupErrorsAreAtTheirCharacter(t *testing.T) {
	cases := []struct {
		body      string
		msg       string
		line, col int
	}{
		// Line 4 is the body's first line; the literal's opening quote is at
		// column 9 on a one-line literal. A slot's error is at its
		// expression.
		{`    Html"<p>${name}</div>"`, "`</div>` closes `<p>`, opened at line 4", 4, 20},
		{`    Html"<div>${name}"`, "`<div>` is not closed in this literal", 4, 10},
		{`    Html"<dvi></dvi>"`, "unknown element `<dvi>`", 4, 10},
		{`    Html"<script>${name}</script>"`, "a slot in a `<script>` element would be JavaScript", 4, 20},
		{`    Html"<a href='https://${name}/'>x</a>"`, "a slot in a URL's scheme or host cannot be escaped", 4, 29},
		{`    Html"<b class=x${name}></b>"`, "quote the attribute's value to put a slot in it", 4, 22},
		{"    Html\"\"\"\n        <div>\n          <p>${name}\n        </div>\n        \"\"\"", "`</div>` closes `<p>`, opened at line 6", 7, 9},
	}
	for _, tc := range cases {
		ds := htmlDiagnostics(t, tc.body)
		found := false
		for _, d := range ds {
			if strings.Contains(d.Message, tc.msg) {
				found = true
				if d.Line != tc.line || d.Col != tc.col {
					t.Errorf("%s: %q at %d:%d, want %d:%d", tc.body, d.Message, d.Line, d.Col, tc.line, tc.col)
				}
			}
		}
		if !found {
			t.Errorf("%s: want %q, got %v", tc.body, tc.msg, ds)
		}
	}
}

func TestHtmlTemplate_SlotTypeErrorsSayWhatTheContextTakes(t *testing.T) {
	cases := []struct{ body, msg, help string }{
		{`    Html"<title>${h}</title>"`, "this slot takes a `Display`, and an `Html` is not one", "`<title>` and `<textarea>` hold text, not markup"},
		{`    Html"<a href='${name}'>x</a>"`, "this slot takes a `UrlValue`, and a `String` is not one", "a URL attribute's value starts with a `Url`"},
		{`    Html"<b style='${name}'></b>"`, "this slot takes a `CssValue`, and a `String` is not one", "a `style` value takes a number, or a `Css`"},
		{`    Html"<input checked=${name}>"`, "this slot takes an `OptionalAttribute`, and a `String` is not one", "pass a `Bool` or a `Maybe<String>`"},
		{`    Html"<p>${Some(n > 0)}</p>"`, "element content does not show a `Maybe<Bool>`", "unwrap it; a `Maybe` of a `ToHtml` value shows its value"},
		{`    Html"<p>${[name]}</p>"`, "element content does not show a `List<String>`", "render its elements with `Html.each`"},
		{`    Html"<p>${(n, n)}</p>"`, "element content does not show a `(Int, Int)`", "put each part in a slot of its own"},
		{`    Html"<p class='${Some(Some(n))}'></p>"`, "this slot does not show a `Maybe<Maybe<Int>>`", "flatten it with `Maybe.flatten`"},
		{`    Html"<p class='${Some(h)}'></p>"`, "this slot takes a `Display`, and a `Maybe<Html>` is not one", "or a `Maybe` of one, which writes nothing for `None`"},
		{`    Html"<p>${fn x: Int -> x}</p>"`, "this slot takes a `ToHtml` or a `Display`, and a `(Int) -> Int` is neither", "or any other `Display` value, escaped as text"},
	}
	for _, tc := range cases {
		ds := htmlDiagnostics(t, tc.body)
		found := false
		for _, d := range ds {
			if strings.Contains(d.Message, tc.msg) {
				found = true
				if len(d.Hints) != 1 || !strings.Contains(d.Hints[0], tc.help) {
					t.Errorf("%s: help %q, want %q", tc.body, d.Hints, tc.help)
				}
			}
		}
		if !found {
			t.Errorf("%s: want %q, got %s", tc.body, tc.msg, fmt.Sprint(ds))
		}
	}
}

// A type of the program's own named Html is an ordinary typed-literal
// prefix: its literal goes to its own handler, not to the template reader.
func TestHtmlTemplate_AProgramsOwnHtmlIsAnOrdinaryPrefix(t *testing.T) {
	src := `import std::literals.{Fragment, Literal}

type Html String

impl Literal<String, Html> for Html
    fn from_fragments(fragments: List<Fragment<String>>) -> Html
        Html("${Iter.count(fragments)}")
    end
end

fn main()
    _ = Html"<p>${"x"}"
end
`
	if err := checkSource(t, src); err != nil {
		t.Fatalf("the front end rejects a program's own Html prefix: %v", err)
	}
}
