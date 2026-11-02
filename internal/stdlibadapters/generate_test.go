package stdlibadapters

import (
	"bytes"
	"os"
	"testing"

	"github.com/nomi-language/nomi/internal/hostgen/stdtable"
	"github.com/nomi-language/nomi/internal/stdlibbindings"
)

// TestGeneratedFileIsCurrent regenerates the adapter files from the
// binding table and the std facades and requires the checked-in files to
// match: adapters_gen.go, and the network adapters split from it,
// adapters_net_gen.go and adapters_nonet_gen.go. A row added to
// internal/stdlibbindings, or a `host fn` signature changed in std, fails
// here until `go generate ./internal/stdlibadapters` is rerun.
func TestGeneratedFileIsCurrent(t *testing.T) {
	want, err := stdtable.GenerateFiles()
	if err != nil {
		t.Fatalf("generation fails: %v", err)
	}
	for name, src := range want {
		got, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, src) {
			t.Errorf("%s is stale (%d bytes checked in, %d generated); run "+
				"`go generate ./internal/stdlibadapters` from the repository root", name, len(got), len(src))
		}
	}
}

// TestEveryBindingIsAdapted holds the population: one adapter per row of the
// table's two lists, in order, no more and no fewer.
func TestEveryBindingIsAdapted(t *testing.T) {
	rows := append(stdlibbindings.Funcs(), stdlibbindings.RtFuncs()...)
	names := Names()
	if len(names) != len(rows) {
		t.Fatalf("%d adapters for %d binding rows", len(names), len(rows))
	}
	for i, b := range rows {
		if names[i] != b.Name {
			t.Errorf("adapter %d is %s, binding row %d is %s", i, names[i], i, b.Name)
		}
	}
	table, err := Bind(&testEnv)
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if len(table) != len(rows) {
		t.Fatalf("Bind answers %d adapters for %d rows", len(table), len(rows))
	}
}
