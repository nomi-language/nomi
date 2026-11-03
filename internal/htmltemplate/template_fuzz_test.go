package htmltemplate

import (
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/rt"
)

// render writes tmpl's slots with value, each escaped for its context as
// std/html's context functions escape it (std/html.nomi), with the same rt
// functions.
func render(tmpl *Template, value string) string {
	var b strings.Builder
	for i, s := range tmpl.Statics {
		b.WriteString(s)
		if i == len(tmpl.Slots) {
			break
		}
		slot := tmpl.Slots[i]
		switch slot.Context {
		case ContextText, ContextRCDATA, ContextAttr, ContextCSS:
			b.WriteString(rt.HTMLEscape(value))
		case ContextURLStart:
			b.WriteString(rt.HTMLEscape(rt.HTMLFilterURL(value)))
		case ContextURLPath:
			b.WriteString(rt.HTMLEscape(rt.HTMLEscapeURLPath(value)))
		case ContextURLQuery:
			b.WriteString(rt.HTMLEscape(url.QueryEscape(value)))
		case ContextURLFragment:
			b.WriteString(rt.HTMLEscape(rt.URLEscapeFragment(value)))
		case ContextOptionalAttribute:
			b.WriteString(" " + slot.Attr + `="` + rt.HTMLEscape(value) + `"`)
		}
	}
	return b.String()
}

// events parses text, a whole rendered page, and answers its tags in order.
func events(text string) ([]string, []Error) {
	p := &parser{parts: []string{text}}
	p.run()
	return p.events, p.errs
}

// escapingHolds says how a rendering of tmpl with value differs from one
// with a harmless value: "" when it parses and its tags are the same.
func escapingHolds(tmpl *Template, value string) string {
	want, errs := events(render(tmpl, "x"))
	if len(errs) > 0 {
		return ""
	}
	out := render(tmpl, value)
	got, errs := events(out)
	if len(errs) > 0 {
		return "the rendering does not parse: " + errs[0].Message + "\n" + out
	}
	if !reflect.DeepEqual(got, want) {
		return "the value changed the tags: " + strings.Join(got, " ") + "\n" + out
	}
	return ""
}

// A slot value cannot add, remove or end markup, in either quote.
func TestEscapingKeepsTheMarkup(t *testing.T) {
	values := []string{`"><script>alert(1)</script>`, `' onclick='x`, `it's`, `</p><p>`, `<!--`, "a\x00b", `javascript:alert(1)`}
	for _, tmpl := range []string{
		`<p>@</p>`, `<title>@</title>`, `<textarea>@</textarea>`,
		`<div class="@"></div>`, `<div class='@'></div>`,
		`<a href="@">x</a>`, `<a href='@'>x</a>`, `<a href="/p/@">x</a>`, `<a href='/p/@'>x</a>`,
		`<a href="/s?q=@">x</a>`, `<a href='/s?q=@'>x</a>`, `<a href='/s#@'>x</a>`,
		`<div style='width: @'></div>`, `<input title=@>`,
	} {
		parsed, errs := Parse(split(tmpl), Options{})
		if len(errs) > 0 {
			t.Fatalf("%s refused: %v", tmpl, errs)
		}
		for _, v := range values {
			if why := escapingHolds(parsed, v); why != "" {
				t.Errorf("%s with %q: %s", tmpl, v, why)
			}
		}
	}
}

// A `'` in a value written into a single-quoted attribute is a character
// reference, so it cannot end the value.
func TestAQuoteInASingleQuotedAttribute(t *testing.T) {
	parsed, errs := Parse(split(`<span class='a @'>x</span>`), Options{})
	if len(errs) > 0 {
		t.Fatalf("refused: %v", errs)
	}
	if got, want := render(parsed, `b' onclick='go()`), `<span class='a b&#39; onclick=&#39;go()'>x</span>`; got != want {
		t.Fatalf("rendered %s, want %s", got, want)
	}
}

// A URL slot whose value has a scheme other than http, https, mailto or tel
// writes the unsafe-URL marker, in either quote.
func TestAJavascriptURLIsReplaced(t *testing.T) {
	for _, tmpl := range []string{`<a href="@">x</a>`, `<a href='@'>x</a>`} {
		parsed, _ := Parse(split(tmpl), Options{})
		if got := render(parsed, "javascript:alert(1)"); !strings.Contains(got, rt.HTMLUnsafeURL) {
			t.Errorf("%s rendered %s", tmpl, got)
		}
	}
}

func FuzzTemplateEscaping(f *testing.F) {
	for _, tmpl := range []string{`<p>@</p>`, `<a href='/p/@?q=@#@'>@</a>`, `<div class="@" style='@'>@</div>`, `<input checked=@ title=@>`} {
		f.Add(tmpl, `"'<>&`)
	}
	f.Fuzz(func(t *testing.T, tmpl, value string) {
		parsed, errs := Parse(split(tmpl), Options{})
		if len(errs) > 0 {
			return
		}
		if why := escapingHolds(parsed, value); why != "" {
			t.Fatalf("template %q, value %q: %s", tmpl, value, why)
		}
	})
}
