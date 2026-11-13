package irbuild

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nomi-language/nomi/rt"
)

// The VM renders a Bytes held as a Display through the impl recorded under
// rt.BytesTypeName (vm.displayErased), since an rt.Bytes is no record and
// carries no type name of its own. The builder records std/bytes' Display
// impl under the name its type symbol has (irRecordStdDisplay), so the two
// names must be one: a lowered program that holds a Bytes as a Display has
// a Display impl recorded under rt.BytesTypeName.
func TestBytesTypeName_IsTheNameStdBytesDisplayIsRecordedUnder(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const src = `fn main()
    d: Display = Bytes.from_list([1, 2])
    dbg "${d}"
end
`
	path := filepath.Join(t.TempDir(), "main.nomi")
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := Analyze(path)
	if err != nil {
		t.Fatalf("the front end rejects the fixture, so nothing below is tested: %v", err)
	}
	res, _, err := GenerateIR(p)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, m := range res.IRModules() {
		for _, impl := range m.DisplayImpls() {
			if impl.Type.Name() == rt.BytesTypeName {
				return
			}
			names = append(names, impl.Type.Name())
		}
	}
	t.Fatalf("no Display impl is recorded under rt.BytesTypeName %q; recorded: %v", rt.BytesTypeName, names)
}
