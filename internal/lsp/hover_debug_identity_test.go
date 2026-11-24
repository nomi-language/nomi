package lsp

import (
	"strings"
	"testing"
)

// Hover lists Debug for a type only where an impl or a derive of Debug is
// written for that type's own declaration. std/duration and the program
// each declare a `Duration`, and only std's writes `impl Debug`: std's
// lists it, and the program's, whose Debug is the one every type has,
// lists none. Likewise std/url and the program each declare a `Url`, and
// only the program's writes Debug.
func TestHover_DebugIsTheTypesOwnWrittenImpl(t *testing.T) {
	src := `import
  std::duration
  std::url
end

struct Duration
  n: Int
end

struct Url
  href: String
end

impl Debug for Url
  fn inspect(_u: Url) -> String
    "Url"
  end
end

fn same(d: duration.Duration, u: url.Url) -> (duration.Duration, url.Url)
  (d, u)
end
`
	uri := "file:///test.nomi"
	s := NewServer()
	s.docs.Open(uri, src)
	doc := s.docs.Get(uri)
	if doc == nil {
		t.Fatal("doc not found after Open")
	}
	fa := doc.Analysis
	for _, c := range []struct {
		module, name string
		debug        bool
	}{
		{"", "Duration", false},
		{"std/duration", "Duration", true},
		{"", "Url", true},
		{"std/url", "Url", false},
	} {
		scope := fa.ModuleScope
		if c.module != "" {
			file := fa.Program().File(c.module)
			if file == nil {
				t.Fatalf("%s is not in the program", c.module)
			}
			scope = file.ModuleScope
		}
		sym := scope.LookupLocal(c.name)
		if sym == nil {
			t.Fatalf("%s %s not found", c.module, c.name)
		}
		got := renderHoverWithAnalysis(sym, fa)
		if listed := strings.Contains(got, "`Debug`"); listed != c.debug {
			t.Errorf("%s %s lists Debug: %v, want %v:\n%s", c.module, c.name, listed, c.debug, got)
		}
	}
}
