package vmhost_test

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// shopBlock is testdata/macros' shop and csvdata as one marked source, the
// macro module under macros/csvdata/ as the Macros chapter lays it out,
// with prices.csv's text replaced by prices.
func shopBlock(t *testing.T, prices string) string {
	t.Helper()
	read := func(path string) string {
		data, err := os.ReadFile("../testdata/macros/" + path)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	sections := []struct{ name, text string }{
		{"main.nomi", read("shop/main.nomi")},
		{"tables.nomi", read("shop/tables.nomi")},
		{"data/prices.csv", prices},
		{"data/names.csv", read("shop/data/names.csv")},
		{"nomi.toml", read("shop/nomi.toml")},
		{"go.mod", strings.Replace(read("shop/go.mod"), "../csvdata", "./macros/csvdata", 1)},
		{"macros/csvdata/csvdata.nomi", read("csvdata/csvdata.nomi")},
		{"macros/csvdata/nomi.toml", "[module]\nname = \"csvdata\"\n"},
		{"macros/csvdata/go.mod", read("csvdata/go.mod")},
	}
	var b strings.Builder
	for _, s := range sections {
		b.WriteString("// FILE: " + s.name + "\n" + s.text + "\n")
	}
	return b.String()
}

// A marked source whose root module requires a macro module of its own
// loads as the same files on disk would: the macro expands from the CSV
// section, and an error in a section is named by the section's path.
func TestLoadMarked_AProjectOfModulesLoadsAsItsFiles(t *testing.T) {
	src := shopBlock(t, "sku,cents\nmug,1450\nposter,2200\ntote,1800\n")
	p, hasTests, err := vmhost.LoadMarked(src, t.TempDir())
	if err != nil {
		t.Fatalf("LoadMarked: %v", err)
	}
	if hasTests {
		t.Fatal("the shop declares no test")
	}
	var out bytes.Buffer
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := out.String(); got != "Coffee mug: 1450 cents\n" {
		t.Fatalf("the shop printed %q", got)
	}

	// The same directory again, with a row in dollars: the directory is
	// emptied and written anew, and the error is at the cell.
	dir := t.TempDir()
	if _, _, err := vmhost.LoadMarked(src, dir); err != nil {
		t.Fatal(err)
	}
	_, _, err = vmhost.LoadMarked(shopBlock(t, "sku,cents\nmug,1450\nposter,2200\ntote,18.00\n"), dir)
	if err == nil {
		t.Fatal("a price in dollars loads")
	}
	if msg := err.Error(); !strings.HasPrefix(msg, "data/prices.csv:4:6: case branch type mismatch") || strings.Contains(msg, dir) {
		t.Fatalf("the error does not name data/prices.csv from the project root:\n%s", msg)
	}
}
