package rt

import "testing"

func TestHTMLEscape(t *testing.T) {
	cases := map[string]string{
		"plain":                     "plain",
		"<script>alert(1)</script>": "&lt;script&gt;alert(1)&lt;/script&gt;",
		`Bo & "Co"`:                 "Bo &amp; &#34;Co&#34;",
		"it's":                      "it&#39;s",
		"a\x00b":                    "a�b",
	}
	for in, want := range cases {
		if got := HTMLEscape(in); got != want {
			t.Errorf("HTMLEscape(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHTMLFilterURL(t *testing.T) {
	kept := []string{"https://example.com/a", "HTTP://x", "mailto:a@b.c", "tel:+15551234",
		"/posts/1", "posts/1?x=a:b", "#top", "?q=a:b", "//cdn.example.com/a.js", ""}
	for _, u := range kept {
		if got := HTMLFilterURL(u); got != u {
			t.Errorf("HTMLFilterURL(%q) = %q, want it kept", u, got)
		}
	}
	refused := []string{"javascript:alert(1)", "JavaScript:x", "data:text/html,x", "vbscript:x",
		" javascript:x", "java\tscript:x", "java\nscript:x"}
	for _, u := range refused {
		if got := HTMLFilterURL(u); got != HTMLUnsafeURL {
			t.Errorf("HTMLFilterURL(%q) = %q, want %q", u, got, HTMLUnsafeURL)
		}
	}
}

func TestHTMLEscapeURLPath(t *testing.T) {
	cases := map[string]string{
		"42":              "42",
		"a b/c?d":         "a%20b%2Fc%3Fd",
		"script:alert(1)": "script%3Aalert%281%29",
		"tea & cake":      "tea%20&%20cake",
		"café":            "caf%C3%A9",
		`"><script>`:      "%22%3E%3Cscript%3E",
	}
	for in, want := range cases {
		if got := HTMLEscapeURLPath(in); got != want {
			t.Errorf("HTMLEscapeURLPath(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestHTMLUnescape holds the decoding to HTML5's in element content: the
// references Go's table holds, the two it leaves out (widerReferences),
// and a reference Go would read past a cut around them.
func TestHTMLUnescape(t *testing.T) {
	cases := map[string]string{
		"plain":                             "plain",
		"a &lt; b &amp;&amp; c":             "a < b && c",
		"&amp &copy2 &notit;":               "& ©2 ¬it;",
		"&#65;&#x42;&#67 &#0;":              "ABC �",
		"&#x80;":                            "€",
		"&fjlig;&semi;":                     "fj;",
		"&nLt;&nGt;":                        "\u226A\u20D2\u226B\u20D2",
		"&amp;nLt; &nLt &nLtv;":             "&nLt; &nLt \u226A\u0338",
		"x&nLt;&lt;&n&nGt;":                 "x\u226A\u20D2<&n\u226B\u20D2",
		"&zzz; & &; &#; &#x;":               "&zzz; & &; &#; &#x;",
		"&CounterClockwiseContourIntegral;": "∳",
	}
	for in, want := range cases {
		if got := HTMLUnescape(in); got != want {
			t.Errorf("HTMLUnescape(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestHTMLEntity holds the lookup of one name to whole names only: a
// legacy name's prefix, a name with its `;` or `&`, a numeric reference
// and a name the table lacks are None.
func TestHTMLEntity(t *testing.T) {
	found := map[string]string{"amp": "&", "semi": ";", "fjlig": "fj", "nLt": "\u226A\u20D2", "nGt": "\u226B\u20D2",
		"NotGreaterGreater": "\u226B\u0338", "copy": "©"}
	for name, want := range found {
		if got := HTMLEntity(name); got != Some(want) {
			t.Errorf("HTMLEntity(%q) = %v, want Some(%q)", name, got, want)
		}
	}
	for _, name := range []string{"", "notit", "copy2", "amp;", "&amp", "#65", "x41", "Amp", "zzz", "n"} {
		if got := HTMLEntity(name); got.Tag == TagSome {
			t.Errorf("HTMLEntity(%q) = %v, want None", name, got)
		}
	}
}
