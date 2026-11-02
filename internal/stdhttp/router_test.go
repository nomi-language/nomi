package stdhttp

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/rt"
)

func strs(l *rt.List[string]) []string {
	var out []string
	for ; l != nil; l = l.Tail {
		out = append(out, l.Head)
	}
	return out
}

// A Router matches as Go's ServeMux does, since it is one: precedence by
// specificity, GET matching HEAD, hosts, wildcards and {$}, the redirects to
// a cleaned or slashed path, and 405 with Allow. The cases follow the ones
// net/http's own ServeMux tests use (Copyright The Go Authors, BSD license).
func TestRoute_MatchesAsServeMuxDoes(t *testing.T) {
	patterns := []string{
		"/",                       // 0
		"/static/",                // 1
		"GET /users/{id}",         // 2
		"GET /users/{id}/edit",    // 3
		"/files/{path...}",        // 4
		"GET /{$}",                // 5
		"example.com/",            // 6
		"POST /users/{id}",        // 7
		"/b/{bucket}/o/{name...}", // 8
	}
	res := NewMux(listOf(patterns))
	if res.Tag != rt.TagOk {
		t.Fatalf("NewMux: %v", strs(res.Err))
	}
	m := res.Ok
	for _, c := range []struct{ method, host, target, want string }{
		{"GET", "x.com", "/", "match 5 GET /{$}"},
		{"GET", "x.com", "/other", "match 0 /"},
		{"GET", "x.com", "/static/a.css", "match 1 /static/"},
		{"GET", "x.com", "/users/7", "match 2 GET /users/{id} id 7"},
		{"HEAD", "x.com", "/users/7", "match 2 GET /users/{id} id 7"},
		{"POST", "x.com", "/users/7", "match 7 POST /users/{id} id 7"},
		{"GET", "x.com", "/users/7/edit", "match 3 GET /users/{id}/edit id 7"},
		{"GET", "x.com", "/files/a/b/c.txt", "match 4 /files/{path...} path a/b/c.txt"},
		{"GET", "x.com", "/b/k/o/a/b", "match 8 /b/{bucket}/o/{name...} bucket k name a/b"},
		{"GET", "example.com", "/anything", "match 6 example.com/"},
		{"GET", "x.com", "/static", "answer 307"},
		{"GET", "x.com", "/a/../static/x", "answer 307"},
		{"DELETE", "x.com", "/users/7/edit", "match 0 /"},
	} {
		got := strings.Join(strs(Route(m, c.method, c.host, c.target)), " ")
		if !strings.HasPrefix(got, c.want) {
			t.Errorf("%s %s%s: got %q, want %q...", c.method, c.host, c.target, got, c.want)
		}
	}
	// With no pattern for every method, a path another method has is a 405.
	only := NewMux(listOf([]string{"GET /users/{id}"})).Ok
	allow := strings.Join(strs(Route(only, "DELETE", "x.com", "/users/7")), " ")
	if !strings.HasPrefix(allow, "answer 405") || !strings.Contains(allow, "Allow GET, HEAD") {
		t.Errorf("405 lacks Allow: %q", allow)
	}
	for _, bad := range [][]string{
		{"/a/{x}", "/a/{y}"}, // conflict
		{"GET /a/{x"},        // bad wildcard
		{"/a/{x...}/b"},      // ... not last
	} {
		if r := NewMux(listOf(bad)); r.Tag != rt.TagErr || strs(r.Err)[0] != "Pattern" {
			t.Errorf("NewMux(%q) is not a Pattern error", bad)
		}
	}
}
