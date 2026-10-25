package ffirun

import (
	"path/filepath"
	"strings"
	"testing"
)

// rowsGo is a Go package whose structs a Nomi file binds under other names.
const rowsGo = `package taggedbinding

type Cell struct {
	Text string
}

type Row struct {
	Columns []string
	Cells   []Cell
}

func EachRow(prefix string, f func(Row) bool) int64 {
	n := int64(0)
	for _, r := range []Row{{Columns: []string{prefix}}} {
		if !f(r) {
			break
		}
		n++
	}
	return n
}

func Echo(r Row) Row { return r }

func Rows() []Row { return nil }

func Find(name string) (*Row, error) { return nil, nil }

func Pair() (Row, Cell) { return Row{}, Cell{} }
`

// A Go struct is a shape, not a name: a Nomi struct with the same fields
// fills its slot whatever either is called, in a callback's parameter, a
// parameter, a result, and inside a List, a Maybe, a Result and a tuple.
func TestPrepareBindsAGoStructUnderAnotherNomiName(t *testing.T) {
	t.Setenv(cacheRootEnv, t.TempDir())
	root := stageSourceBindingProject(t)
	mustWriteHelper(t, filepath.Join(root, "binding", "rows.go"), rowsGo)
	mustWriteHelper(t, filepath.Join(root, "main.nomi"), `gopkg "example.com/binding/source" as binding

struct RawCell {
  text: String
}

struct RawRow {
  columns: List<String>
  cells: List<RawCell>
}

fn each_row(prefix: String, f: (RawRow) -> Bool) -> Int go binding.EachRow

fn echo(r: RawRow) -> RawRow go binding.Echo

fn rows() -> List<RawRow> go binding.Rows

fn find(name: String) -> Result<Maybe<RawRow>, String> go binding.Find

fn pair() -> #(RawRow, RawCell) go binding.Pair
`)
	if _, err := Prepare(filepath.Join(root, "main.nomi")); err != nil {
		t.Fatalf("Prepare refuses Nomi structs named apart from their Go structs: %v", err)
	}
}

// Without a name to compare, the fields still must match, at every depth,
// and a Go struct cannot fill a slot that declares no struct, nor a Nomi
// struct one whose Go type is no struct.
func TestPrepareRejectsAGoStructAgainstADifferentShape(t *testing.T) {
	t.Setenv(cacheRootEnv, t.TempDir())
	root := stageSourceBindingProject(t)
	mustWriteHelper(t, filepath.Join(root, "binding", "rows.go"), rowsGo)
	mustWriteHelper(t, filepath.Join(root, "main.nomi"), `gopkg "example.com/binding/source" as binding

opaque type RawBox go binding.Box

struct RawCell {
  text: Int
}

struct RawRow {
  columns: List<String>
  cells: List<RawCell>
}

struct Thin {
  columns: List<String>
}

fn each_row(prefix: String, f: (Thin) -> Bool) -> Int go binding.EachRow

fn echo(r: String) -> RawRow go binding.Echo

fn rows() -> List<RawBox> go binding.Rows

fn find(name: String) -> Result<Maybe<List<RawRow>>, String> go binding.Find

fn upper(s: RawRow) -> String go binding.EchoUpper

fn pair() -> #(RawRow, Thin) go binding.Pair
`)
	_, err := Prepare(filepath.Join(root, "main.nomi"))
	if err == nil {
		t.Fatal("Prepare accepts bindings whose structs do not match their Go structs")
	}
	msg := err.Error()
	for _, want := range []string{
		`parameter 2 "f" struct field "cells" exists in Go Row but not in Nomi Thin`,
		`parameter 1 "r" projects to a struct (Go Row), but Nomi declares String`,
		`return struct field "cells" struct field "text" projects to String, but Nomi RawCell declares Int`,
		`return projects to List<a struct (Go Row)>, but Nomi declares List<RawBox>`,
		`return projects to Result<Maybe<a struct (Go Row)>, String>, but Nomi declares Result<Maybe<List<RawRow>>, String>`,
		`parameter 1 "s" projects to String, but Nomi declares RawRow`,
		`return struct field "text" exists in Go Cell but not in Nomi Thin`,
		`return struct field "columns" exists in Nomi Thin but not in Go Cell`,
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("the error does not say %q:\n%s", want, msg)
		}
	}
}
