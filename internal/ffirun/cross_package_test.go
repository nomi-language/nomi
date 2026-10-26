package ffirun

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stageCrossPackageProject writes a project whose bound package, adapter,
// names types of two other packages: model, in the project's own module, and
// core, a nested module the project's go.mod replaces with a local
// directory. files are written over the defaults, by path from the root.
func stageCrossPackageProject(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	defaults := map[string]string{
		"go.mod": `module example.com/app

go 1.26.3

require example.com/core v0.0.0

replace example.com/core => ./core
`,
		"nomi.toml": "[module]\nname = \"app\"\n",
		"core/go.mod": `module example.com/core

go 1.26.3
`,
		"core/core.go": `package core

type ID = int64

type Bytes = []byte

type Tags = map[string]int64

type Check = func(string) bool

type Cell struct {
	Text string
	N    ID
}
`,
		"model/model.go": `package model

import "example.com/core"

type Address struct {
	City string
}

type User struct {
	Name    string
	Address Address
	Cells   []core.Cell
	Best    *core.Cell
}
`,
		"adapter/adapter.go": `package adapter

import (
	"example.com/app/model"
	"example.com/core"
)

type Cell = core.Cell

type ID = core.ID

type User = model.User

func MakeCell(t string, n ID) Cell { return Cell{Text: t, N: n} }

func Direct(c core.Cell) core.Cell { return c }

func Greet(u model.User) (model.User, error) { return u, nil }

func Aliased(u User) []User { return []User{u} }

func Raw(b core.Bytes) core.Bytes { return b }

func Count(tags core.Tags) core.Tags { return tags }

func Keep(items []string, check core.Check) int64 { return 0 }
`,
	}
	for path, content := range files {
		defaults[path] = content
	}
	for path, content := range defaults {
		mustWriteHelper(t, filepath.Join(root, filepath.FromSlash(path)), content)
	}
	return root
}

const crossPackageNomiStructs = `struct Cell {
  text: String
  n: Int
}

struct Address {
  city: String
}

struct User {
  name: String
  address: Address
  cells: List<Cell>
  best: Maybe<Cell>
}
`

// A struct of another package the bound package imports projects by its
// fields as one of the bound package's does: through an alias the bound
// package declares, named directly, nested in another package's struct, and
// from a nested module the project replaces locally. An alias to a basic,
// slice, map or func type crosses as its target does.
func TestPrepareBindsStructsOfAnotherPackage(t *testing.T) {
	t.Setenv(cacheRootEnv, t.TempDir())
	root := stageCrossPackageProject(t, map[string]string{
		"main.nomi": `gopkg "example.com/app/adapter"

` + crossPackageNomiStructs + `
fn make_cell(t: String, n: Int) -> Cell go adapter.MakeCell

fn direct(c: Cell) -> Cell go adapter.Direct

fn greet(u: User) -> Result<User, String> go adapter.Greet

fn aliased(u: User) -> List<User> go adapter.Aliased

fn raw(b: Bytes) -> Bytes go adapter.Raw

fn count(tags: Map<String, Int>) -> Map<String, Int> go adapter.Count

fn keep(items: List<String>, check: (String) -> Bool) -> Int go adapter.Keep
`,
	})
	r, err := Prepare(filepath.Join(root, "main.nomi"))
	if err != nil {
		t.Fatalf("Prepare refuses bindings that name another package's types: %v", err)
	}
	wrapper, err := os.ReadFile(r.WrapperPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"example.com/core"`, `"example.com/app/model"`} {
		if !strings.Contains(string(wrapper), want) {
			t.Errorf("the wrapper does not import %s", want)
		}
	}
}

// A type another package binds as opaque stays opaque when the bound
// package's signature names it.
func TestPrepareKeepsAnotherPackagesOpaqueBinding(t *testing.T) {
	t.Setenv(cacheRootEnv, t.TempDir())
	root := stageCrossPackageProject(t, map[string]string{
		"main.nomi": `gopkg "example.com/app/adapter"

gopkg "example.com/core"

opaque type RawCell go core.Cell

fn direct(c: RawCell) -> RawCell go adapter.Direct
`,
	})
	if _, err := Prepare(filepath.Join(root, "main.nomi")); err != nil {
		t.Fatalf("Prepare refuses another package's opaque binding: %v", err)
	}
}

// Another package's struct is held to the Nomi struct as one of the bound
// package's is, and an error names it by its package.
func TestPrepareRejectsAnotherPackagesStructOfADifferentShape(t *testing.T) {
	t.Setenv(cacheRootEnv, t.TempDir())
	root := stageCrossPackageProject(t, map[string]string{
		"core/core.go": `package core

type Cell struct {
	Text  string
	N     int64
	Ch    chan int
	count int64
}

type Missing = Gone
`,
		"adapter/adapter.go": `package adapter

import (
	"net/url"

	"example.com/app/model"
	"example.com/core"
)

type Cell = core.Cell

func MakeCell(t string) Cell { return Cell{Text: t} }

func Greet(u model.User) model.User { return u }

func Nowhere() core.Absent { return core.Absent{} }

func Gone() core.Missing { return 0 }

func Parse(s string) url.URL { return url.URL{} }
`,
		"main.nomi": `gopkg "example.com/app/adapter"

struct Cell {
  text: String
  n: Int
  ch: Int
  count: Int
}

struct Address {
  city: String
}

struct User {
  name: String
  address: String
  cells: List<Cell>
  best: Maybe<Cell>
}

struct Url {
  path: String
}

fn make_cell(t: String) -> Cell go adapter.MakeCell

fn greet(u: User) -> User go adapter.Greet

fn nowhere() -> Cell go adapter.Nowhere

fn gone() -> Int go adapter.Gone

fn parse(s: String) -> Url go adapter.Parse
`,
	})
	_, err := Prepare(filepath.Join(root, "main.nomi"))
	if err == nil {
		t.Fatal("Prepare accepts bindings whose structs do not match another package's Go structs")
	}
	msg := err.Error()
	for _, want := range []string{
		`Go function "MakeCell" in package "example.com/app/adapter" has unsupported return struct field "ch" type (Go core.Cell, in package "example.com/core"): channel type chan int is not automatically converted`,
		`return struct field "count" exists in Nomi Cell but not in Go core.Cell`,
		`parameter 1 "u" struct field "address" projects to a struct (Go model.Address), but Nomi User declares String`,
		`Go function "Nowhere" in package "example.com/app/adapter" has unsupported return type: named type core.Absent: Go package "example.com/core" declares no type Absent`,
		`Go function "Gone" in package "example.com/app/adapter" has unsupported return type: Go alias core.Missing: unbound named type Gone`,
		`Go function "Parse" in package "example.com/app/adapter" has unsupported return type: unbound named type url.URL`,
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("the error does not say %q:\n%s", want, msg)
		}
	}
}

// A package the bound package imports that no go.mod line provides is
// reported at the binding with its import path.
func TestPrepareRejectsAnImportedPackageOutsideTheModuleGraph(t *testing.T) {
	t.Setenv(cacheRootEnv, t.TempDir())
	root := stageCrossPackageProject(t, map[string]string{
		"adapter/adapter.go": `package adapter

import "example.com/elsewhere"

func Make() elsewhere.Thing { return elsewhere.Thing{} }
`,
		"main.nomi": `gopkg "example.com/app/adapter"

struct Thing {
  name: String
}

fn make() -> Thing go adapter.Make
`,
	})
	_, err := Prepare(filepath.Join(root, "main.nomi"))
	if err == nil {
		t.Fatal("Prepare accepts a type of a package outside the module graph")
	}
	want := `main.nomi:7:31: Go function "Make" in package "example.com/app/adapter" has unsupported return type: ` +
		`named type elsewhere.Thing: Go package "example.com/elsewhere" is not in the project's own module`
	if !strings.Contains(err.Error(), want) {
		t.Errorf("the error does not say %q:\n%s", want, err)
	}
}
