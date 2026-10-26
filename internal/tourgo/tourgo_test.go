package tourgo_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/tourgo"
	"github.com/nomi-language/nomi/internal/tourgo/tourgogen"
	"github.com/nomi-language/nomi/vmhost"
)

const root = "../.."

// TestTourGoFilesAreTheLinkedSources holds each tour block's Go file, which
// the page shows read-only, to the file in this tree the tour compiles in,
// byte for byte.
func TestTourGoFilesAreTheLinkedSources(t *testing.T) {
	blocks, err := tourgogen.Blocks(root)
	if err != nil {
		t.Fatal(err)
	}
	files := 0
	for _, b := range blocks {
		goFiles, err := tourgogen.GoFiles(b.Code)
		if err != nil {
			t.Fatal(err)
		}
		for name, shown := range goFiles {
			files++
			path := tourgogen.LinkedPath(tourgogen.GoPackageOf(shown), name)
			linked, err := os.ReadFile(filepath.Join(root, path))
			if err != nil {
				t.Fatalf("%s shows %s, which the tour compiles from %s: %v", b.Name, name, path, err)
			}
			if shown != string(linked) {
				t.Errorf("%s shows a %s that differs from %s, which the tour compiles; copy one over the other", b.Name, name, path)
			}
		}
	}
	if files == 0 {
		t.Fatal("no tour block shows a Go file; the extraction is wrong")
	}
}

// run loads a tour block as the tour's runner does, with the linked
// adapters, and answers its output, or the error the runner shows.
func run(t *testing.T, code string) (string, error) {
	t.Helper()
	entrySrc, entryName, files, manifest, err := vmhost.SplitMultiFile(code)
	if err != nil {
		t.Fatal(err)
	}
	hosts, err := tourgo.Hosts(entryName, entrySrc, files)
	if err != nil {
		return "", err
	}
	var out bytes.Buffer
	p, err := vmhost.LoadSource(entryName, entrySrc, vmhost.WithVirtualFiles(files),
		vmhost.WithVirtualManifest(manifest), vmhost.WithHosts(hosts), vmhost.WithErrorOutput(&out))
	if err != nil {
		return out.String(), err
	}
	err = p.Run(context.Background(), &out, nil, false)
	return out.String(), err
}

// block is the tour's gopkg block whose code holds marker.
func block(t *testing.T, marker string) string {
	t.Helper()
	blocks, err := tourgogen.Blocks(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range blocks {
		if strings.Contains(b.Code, marker) {
			return b.Code
		}
	}
	t.Fatalf("no tour block with Go bindings holds %q", marker)
	return ""
}

// TestHostsAnswerAnEditedBlock edits the tour's blocks as a visitor might.
// A binding renamed, moved into another file or called differently still
// runs on the linked adapter; one that names a Go function the tour did
// not link, or that changes the types the adapter converts, is refused at
// the declaration with a message that says so.
func TestHostsAnswerAnEditedBlock(t *testing.T) {
	std := block(t, "go strings.ToUpper")
	urls := block(t, "go urltools.Parse")
	if _, err := run(t, std); err != nil {
		t.Fatalf("the tour's block does not run: %v", err)
	}

	renamed := strings.ReplaceAll(std, "upper", "shout")
	if out, err := run(t, renamed); err != nil || !strings.HasPrefix(out, "HI\n") {
		t.Fatalf("a renamed binding: %q, %v", out, err)
	}

	// The urls block's bindings moved into its entry file.
	entryOnly := strings.Replace(urls, "// FILE: urls.nomi\n", "", 1)
	entryOnly = strings.ReplaceAll(entryOnly, "urls.parse(", "parse(")
	entryOnly = strings.ReplaceAll(entryOnly, "urls.escape_query(", "escape_query(")
	entryOnly = strings.ReplaceAll(entryOnly, "urls.unescape_query(", "unescape_query(")
	entryOnly = strings.Replace(entryOnly, "    urls\n", "", 1)
	// The struct moved too, so its runtime name is main.ParsedURL, not the
	// urls.ParsedURL the adapter builds: refused.
	moved := "main.nomi:35:59: this example's Go code is fixed in the tour: `urltools.Parse` is linked for " +
		"`fn parse(raw: String) -> Result<ParsedURL, String>` with the types urls.nomi declares, " +
		"so the types this declaration names must be those, declared there"
	if _, err := run(t, entryOnly); err == nil || err.Error() != moved {
		t.Fatalf("bindings moved into the entry with their struct: %v, want\n%s", err, moved)
	}
	// So is the file renamed, which renames the struct with it.
	renamedFile := strings.ReplaceAll(urls, "urls", "links")
	if _, err := run(t, renamedFile); err == nil || !strings.Contains(err.Error(), "with the types urls.nomi declares") {
		t.Fatalf("the bindings' file renamed: %v", err)
	}
	// A binding without a struct moves freely: escape_query alone in the
	// entry, beside a main that calls it.
	escape := "import std/io\n\ngopkg \"urltools\"\n\nfn escape(value: String) -> String go urltools.EscapeQuery\n\n" +
		"fn main() {\n    io.print(escape(\"a b\"))\n}\n"
	if out, err := run(t, escape); err != nil || out != "a+b\n" {
		t.Fatalf("a binding moved into the entry: %q, %v", out, err)
	}

	for _, c := range []struct {
		name, code, want string
	}{
		{"an unlinked function", strings.Replace(urls, "go urltools.Parse\n", "go urltools.Resolve\n", 1),
			"urls.nomi:17:59: this example's Go code is fixed in the tour: `urltools.Resolve` is not available " +
				"(from urltools the tour links EscapeQuery, Parse, ParseQuery and UnescapeQuery)"},
		{"an unlinked package", strings.Replace(std, "gopkg \"strconv\"", "gopkg \"strconv\"\n\ngopkg \"os\"\n\nfn cwd() -> Result<String, String> go os.Getwd", 1),
			"main.nomi:9:39: this example's Go code is fixed in the tour: `os.Getwd` is not available (the tour links no Go package \"os\")"},
		{"a changed signature", strings.Replace(std, "fn upper(s: String) -> String", "fn upper(s: String) -> Result<String, String>", 1),
			"main.nomi:7:50: this example's Go code is fixed in the tour: `strings.ToUpper` is linked for " +
				"`fn upper(s: String) -> String`, so a declaration that binds it takes and returns those types"},
		{"a changed struct", strings.Replace(urls, "    raw_query: String\n", "", 1),
			"`urltools.Parse` is linked for `fn parse(raw: String) -> Result<ParsedURL, String>`"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := run(t, c.code)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want %q", err, c.want)
			}
		})
	}
}

// TestHostsAnswerNothingForAProgramWithoutGo: a block with no `go` binding
// gets no host table.
func TestHostsAnswerNothingForAProgramWithoutGo(t *testing.T) {
	hosts, err := tourgo.Hosts("main", "fn main() {\n}\n", nil)
	if hosts != nil || err != nil {
		t.Fatalf("got %v, %v", hosts != nil, err)
	}
}
