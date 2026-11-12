package lsp

import (
	"github.com/nomi-language/nomi/internal/fileuri"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const namedArgDecls = `fn connect(host: String, port: Int = 8080, timeout: Int = 30) -> Int
    port + timeout
end

fn each_item(items: List<Int>, f: (Int) -> Int) -> Int
    0
end

interface Greeter
    fn greet(who: self, greeting: String) -> String
end

struct Server
    host: String
end

impl Server
    fn open(server: Server, retries: Int = 3, verbose: Bool = False) -> Int
        retries
    end
end
`

// namedLabels lists the named-argument items, in the order offered.
func namedLabels(labels []string) []string {
	var out []string
	for _, l := range labels {
		if strings.HasSuffix(l, ":") {
			out = append(out, l)
		}
	}
	return out
}

func TestCompletion_NamedArgumentNames(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string // the named-argument items, in order
	}{
		{"first argument", "fn f() -> Int\n    connect(‸\nend\n", []string{"port:", "timeout:", "host:"}},
		{"after a positional", "fn f() -> Int\n    connect(\"h\", ‸)\nend\n", []string{"port:", "timeout:"}},
		{"after two positionals", "fn f() -> Int\n    connect(\"h\", 1, ‸)\nend\n", []string{"timeout:"}},
		{"a named argument already given", "fn f() -> Int\n    connect(\"h\", timeout: 5, ‸)\nend\n", []string{"port:"}},
		{"a named argument after the cursor", "fn f() -> Int\n    connect(\"h\", ‸, timeout: 5)\nend\n", []string{"port:"}},
		{"typed prefix", "fn f() -> Int\n    connect(\"h\", ti‸\nend\n", []string{"timeout:"}},
		{"owner function", "fn f(s: Server) -> Int\n    Server.open(s, ‸)\nend\n", []string{"retries:", "verbose:"}},
		{"owner function in a pipe stage", "fn f(s: Server) -> Int\n    s |> Server.open(‸)\nend\n", []string{"retries:", "verbose:"}},
		{"lambda-typed parameter", "fn f() -> Int\n    each_item([1], ‸)\nend\n", []string{"f:"}},
		{"stdlib owner function", "import std::tasks.Task\n\nfn f() -> Int\n    concurrent\n        [1, 2] |> Task.spawn_all(fn n -> n, ‸\n    end\n    0\nend\n", []string{"max_running:"}},
		{"stdlib owner function, unpiped", "import std::tasks.Task\n\nfn f() -> Int\n    concurrent\n        Task.spawn_all([1], ‸)\n    end\n    0\nend\n", []string{"f:", "max_running:"}},
		{"interface function", "fn f(s: Server) -> String\n    Greeter.greet(s, ‸)\nend\n", []string{"greeting:"}},
		{"self-typed parameters", "fn f() -> Int\n    Comparable.compare(1, ‸)\n    0\nend\n", nil},
		{"every slot filled", "fn f() -> Int\n    connect(\"h\", 1, 2, ‸)\nend\n", nil},
		{"not in a named argument's value", "fn f() -> Int\n    connect(\"h\", port: ‸)\nend\n", nil},
		{"not in a nested expression", "fn f() -> Int\n    connect(\"h\", 1 + ‸)\nend\n", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := namedArgDecls + "\n" + tt.src
			if strings.HasPrefix(tt.src, "import ") {
				i := strings.Index(tt.src, "\n\n") + 2
				src = tt.src[:i] + namedArgDecls + "\n" + tt.src[i:]
			}
			items := complete(t, src)
			got := namedLabels(itemLabels(items))
			if !slices.Equal(got, tt.want) {
				t.Fatalf("named arguments = %v, want %v (all: %v)", got, tt.want, itemLabels(items))
			}
		})
	}
}

// A defaulted parameter's name ranks first; the expression candidates stay
// in the list, since a positional argument is an expression.
func TestCompletion_NamedArgumentsRankAndInsert(t *testing.T) {
	src := namedArgDecls + "\nfn f() -> Int\n    p = 1\n    connect(\"h\", ‸)\nend\n"
	items := completeWith(t, true, src)
	labels := itemLabels(items)
	if len(labels) < 2 || labels[0] != "port:" || labels[1] != "timeout:" {
		t.Fatalf("the defaulted parameters do not rank first: %v", labels)
	}
	labelsInclude(t, items, "p", "connect")
	if te := textEditOf(t, mustItem(t, items, "port:")); te.NewText != "port: ${1}" {
		t.Fatalf("snippet = %q, want port: ${1}", te.NewText)
	}
	if it := mustItem(t, items, "port:"); it.FilterText == nil || *it.FilterText != "port" {
		t.Fatalf("filterText = %v, want port", it.FilterText)
	}

	items = completeWith(t, false, src)
	if te := textEditOf(t, mustItem(t, items, "port:")); te.NewText != "port: " {
		t.Fatalf("plain insert = %q, want %q", te.NewText, "port: ")
	}
}

// A function of another project file, called through its file object and
// through a selective import.
func TestCompletion_NamedArgumentsOfImportedFunctions(t *testing.T) {
	dir := writeProject(t, map[string]string{
		"net.nomi":  "pub fn dial(host: String, retries: Int = 3) -> Int\n    retries\nend\n",
		"main.nomi": "fn main() end\n",
	})
	s := NewServer()
	s.docs.SetWorkspaceRoot(dir)
	uri := fileuri.FromPath(filepath.Join(dir, "main.nomi"))

	items := completeIn(t, s, uri, "import net\n\nfn main()\n    x = net.dial(\"h\", ‸)\nend\n").Items
	if got := namedLabels(itemLabels(items)); !slices.Equal(got, []string{"retries:"}) {
		t.Fatalf("file-qualified: named arguments = %v", got)
	}
	items = completeIn(t, s, uri, "import net.dial\n\nfn main()\n    x = dial(‸)\nend\n").Items
	if got := namedLabels(itemLabels(items)); !slices.Equal(got, []string{"retries:", "host:"}) {
		t.Fatalf("selective import: named arguments = %v", got)
	}
}
