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
