package fileuri

import "testing"

func TestUnixURIsArePrefixedPaths(t *testing.T) {
	for _, p := range []string{"/home/me/a.nomi", "/a b/c%41.nomi", "rel/x.nomi"} {
		u := fromPath(p, false)
		if u != "file://"+p {
			t.Errorf("fromPath(%q) = %q", p, u)
		}
		if got := toPath(u, false); got != p {
			t.Errorf("toPath(%q) = %q, want %q", u, got, p)
		}
		if got := canonical(u, false); got != u {
			t.Errorf("canonical(%q) = %q", u, got)
		}
	}
	if got := toPath("untitled:Untitled-1", false); got != "untitled:Untitled-1" {
		t.Errorf("a URI of another scheme is unchanged, got %q", got)
	}
}

func TestWindowsPathsAndTheirURIs(t *testing.T) {
	cases := []struct{ path, uri string }{
		{`C:\Users\me\a.nomi`, "file:///C:/Users/me/a.nomi"},
		{`C:\My Docs\a.nomi`, "file:///C:/My%20Docs/a.nomi"},
		{`\\server\share\a.nomi`, "file://server/share/a.nomi"},
		{`\tmp\a.nomi`, "file:///tmp/a.nomi"},
	}
	for _, c := range cases {
		if got := fromPath(c.path, true); got != c.uri {
			t.Errorf("fromPath(%q) = %q, want %q", c.path, got, c.uri)
		}
		if got := toPath(c.uri, true); got != c.path {
			t.Errorf("toPath(%q) = %q, want %q", c.uri, got, c.path)
		}
	}
}

func TestWindowsClientSpellingsAreOneDocument(t *testing.T) {
	want := "file:///C:/Users/me/a.nomi"
	for _, u := range []string{
		"file:///c%3A/Users/me/a.nomi", // VS Code
		"file:///c:/Users/me/a.nomi",
		"file:///C:/Users/me/a.nomi",
		`file://C:\Users\me\a.nomi`, // "file://" + a Windows path
		"file://localhost/C:/Users/me/a.nomi",
	} {
		if got := canonical(u, true); got != want {
			t.Errorf("canonical(%q) = %q, want %q", u, got, want)
		}
	}
	if got := fromPath(`c:\Users\me\a.nomi`, true); got != want {
		t.Errorf("a lower-case drive is upper-cased: %q", got)
	}
}

func TestCanonicalJSONRewritesOnlyURIFields(t *testing.T) {
	raw := []byte(`{"textDocument":{"uri":"file:///c%3A/a.nomi","text":"file:///c%3A/kept"},"rootUri":"file:///c%3A/","n":1.50}`)
	got := string(canonicalJSON(raw, true))
	want := `{"n":1.50,"rootUri":"file:///C:/","textDocument":{"text":"file:///c%3A/kept","uri":"file:///C:/a.nomi"}}`
	if got != want {
		t.Errorf("canonicalJSON = %s\nwant %s", got, want)
	}
	if got := canonicalJSON(raw, false); string(got) != string(raw) {
		t.Errorf("off Windows the message is unchanged, got %s", got)
	}
	plain := []byte(`{"textDocument":{"uri":"file:///C:/a.nomi"}}`)
	if got := canonicalJSON(plain, true); &got[0] != &plain[0] {
		t.Errorf("a message already canonical is answered as it came")
	}
}
