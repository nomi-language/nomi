package ffirun

import (
	"reflect"
	"testing"
)

// A named type from the standard library is read through go/types and spelled
// as reflect spells it: `sqlite.Conn`'s `*sql.DB` field is what the adapter
// for `sqlite.open_raw` reaches.
func TestGoTypes_ANonLocalPackagesTypeIsImported(t *testing.T) {
	gt := newGoTypes(t.TempDir())
	ty, err := gt.importedNamed("database/sql", "DB", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if ty.Kind() != reflect.Struct || ty.Name() != "DB" || ty.PkgPath() != "database/sql" || ty.String() != "sql.DB" {
		t.Fatalf("got kind %v name %q pkg %q string %q", ty.Kind(), ty.Name(), ty.PkgPath(), ty.String())
	}
	if ty.NumField() == 0 {
		t.Fatal("sql.DB read with no fields")
	}
	// A duration keeps the projection's own reading of time's named types.
	d, err := gt.importedNamed("time", "Duration", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if d.Kind() != reflect.Int64 || d.String() != "time.Duration" {
		t.Fatalf("time.Duration read as %v %q", d.Kind(), d.String())
	}
}
