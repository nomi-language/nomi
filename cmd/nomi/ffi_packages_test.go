package main

import (
	"path/filepath"
	"testing"
)

// TestRunFile_FFIStructsOfAnotherPackage runs an adapter split across
// packages: the bound package declares aliases to a struct, an integer and a
// callback of core, a nested module the project replaces locally, and takes
// and returns model's struct, which holds core's structs in a list and a
// pointer. Every one crosses by its fields, both ways.
func TestRunFile_FFIStructsOfAnotherPackage(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; compiles a Go FFI wrapper; -short")
	}
	root := t.TempDir()
	files := map[string]string{
		"go.mod":      "module example.com/app\n\ngo 1.26.3\n\nrequire example.com/core v0.0.0\n\nreplace example.com/core => ./core\n",
		"nomi.toml":   "[module]\nname = \"app\"\nentry_points = [\"main\"]\n",
		"core/go.mod": "module example.com/core\n\ngo 1.26.3\n",
		"core/core.go": `package core

type ID = int64

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

func MakeCell(text string, n ID) Cell { return Cell{Text: text, N: n} }

func Greet(u model.User) model.User {
	u.Name = "hi " + u.Name
	u.Cells = append(u.Cells, core.Cell{Text: u.Address.City, N: 7})
	u.Best = &u.Cells[0]
	return u
}

func CountIf(cells []Cell, keep core.Check) ID {
	n := ID(0)
	for _, c := range cells {
		if keep(c.Text) {
			n++
		}
	}
	return n
}
`,
		"main.nomi": `import std/io

gopkg "example.com/app/adapter"

struct Cell {
  text: String
  n: Int
}

derive Display for Cell

struct Address {
  city: String
}

struct User {
  name: String
  address: Address
  cells: List<Cell>
  best: Maybe<Cell>
}

fn make_cell(text: String, n: Int) -> Cell go adapter.MakeCell

fn greet(u: User) -> User go adapter.Greet

fn count_if(cells: List<Cell>, keep: (String) -> Bool) -> Int go adapter.CountIf

fn main() {
  cell = make_cell("a", 3)
  io.print("${cell}")
  user = greet(#User{name: "Ada", address: #Address{city: "Oslo"}, cells: [cell], best: None})
  io.print("${user.name} ${user.address.city} ${user.cells}")
  case user.best {
    Some(best) -> io.print("best ${best}")
    None -> io.print("no best")
  }
  io.print("${count_if(user.cells, fn(text) -> text == "Oslo")}")
}
`,
	}
	for path, content := range files {
		mustWrite(t, filepath.Join(root, filepath.FromSlash(path)), content)
	}
	out, err := runNomi(t, t.TempDir(), "run", filepath.Join(root, "main.nomi"))
	if err != nil {
		t.Fatalf("nomi run: %v\n%s", err, out)
	}
	const want = "#Cell{text: a, n: 3}\n" +
		"hi Ada Oslo [#Cell{text: a, n: 3}, #Cell{text: Oslo, n: 7}]\n" +
		"best #Cell{text: a, n: 3}\n" +
		"1\n"
	if out != want {
		t.Fatalf("nomi run printed\n%s\nwant\n%s", out, want)
	}
}
