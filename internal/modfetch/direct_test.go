package modfetch

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// requirePrivate publishes example.com/private v1.0.0 to p and names it in
// app's nomi.toml.
func requirePrivate(t *testing.T, p *testProxy, app string) {
	t.Helper()
	p.publish(t, "example.com/private", "v1.0.0", map[string]string{
		"nomi.toml":    "[module]\nname = \"private\"\npath = \"example.com/private\"\n",
		"private.nomi": "pub fn secret() -> Int\n    42\nend\n",
	})
	writeFile(t, filepath.Join(app, "nomi.toml"), "[module]\nname = \"app\"\n\n[dependencies]\n"+
		"private = { module = \"example.com/private\", version = \"v1.0.0\" }\n")
}

// A module GOPROXY sends `direct` with no `go` on PATH is an error that names
// the module, says why it went `direct`, and says what to set to serve it
// from a proxy; setting that serves it.
func TestFetch_DirectWithNoGoOnPathSaysWhatToSet(t *testing.T) {
	p := newTestProxy(t)
	app, _ := setup(t, p)
	requirePrivate(t, p, app)
	t.Setenv("PATH", t.TempDir())

	for _, c := range []struct {
		name, goproxy, goprivate string
		want                     []string
	}{
		{"GOPRIVATE", p.url(), "example.com/private", []string{
			"GONOPROXY, or GOPRIVATE when GONOPROXY is unset, matches it",
			"set GOPROXY to a module proxy that serves it and GONOPROXY=none",
		}},
		{"GOPROXY=direct", "direct", "", []string{"GOPROXY names `direct`", "set GOPROXY to a module proxy that serves it"}},
		{"not on the proxy", newTestProxy(t).url() + ",direct", "", []string{
			"the proxies GOPROXY lists before `direct` do not have it (reading file://",
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("GOPROXY", c.goproxy)
			t.Setenv("GOPRIVATE", c.goprivate)
			_, err := Fetch(app, Options{Update: true})
			if err == nil {
				t.Fatal("a module sent direct was fetched with no go on PATH")
			}
			for _, want := range append(c.want, "module example.com/private:", "which is not on PATH", "file:// directory") {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the error does not say %q:\n%v", want, err)
				}
			}
		})
	}

	// What the GOPRIVATE error says to set serves the module.
	t.Setenv("GOPROXY", p.url())
	t.Setenv("GOPRIVATE", "example.com/private")
	t.Setenv("GONOPROXY", "none")
	if _, err := Fetch(app, Options{Update: true}); err != nil {
		t.Fatalf("GONOPROXY=none with the proxy in GOPROXY: %v", err)
	}
}

// A private https proxy takes its credentials from the user info of its
// GOPROXY URL, sent as basic authentication, and an error names the URL
// without the password.
func TestFetch_APrivateProxyTakesCredentialsFromItsURL(t *testing.T) {
	p := newTestProxy(t)
	app, _ := setup(t, p)
	requirePrivate(t, p, app)
	files := http.FileServer(http.Dir(p.dir))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user, pass, ok := r.BasicAuth(); !ok || user != "ci" || pass != "s3cret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		files.ServeHTTP(w, r)
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")

	t.Setenv("GOPROXY", "http://ci:wrong-token@"+host)
	_, err := Fetch(app, Options{Update: true})
	if err == nil || !strings.Contains(err.Error(), "401 Unauthorized") {
		t.Fatalf("a wrong password: %v, want 401 Unauthorized", err)
	}
	if strings.Contains(err.Error(), "wrong-token") || !strings.Contains(err.Error(), "http://ci:xxxxx@"+host) {
		t.Errorf("the error does not name the URL without its password:\n%v", err)
	}

	t.Setenv("GOPROXY", "http://ci:s3cret@"+host)
	if _, err := Fetch(app, Options{Update: true}); err != nil {
		t.Fatalf("the credentials in GOPROXY's URL: %v", err)
	}
}
