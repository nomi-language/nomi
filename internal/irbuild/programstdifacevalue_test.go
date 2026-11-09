package irbuild

import (
	"os"
	"path/filepath"
	"testing"
)

// A value of a std module's interface lowered with the program (std/sql's
// ToSql) passed as an interface value to another file's function dispatches
// to its type's impl, however the impl header names the interface: through
// the module's qualifier (`impl sql.ToSql`) in the caller's file or a third
// file, or by its imported bare name. The qualified header was filed as a
// shared std interface's impl, in no table a program interface's dispatch
// reads: from the caller's file the VM stopped at the dispatch ("ToSql.to_sql
// dispatches on , ..."), and from a third file the boxing call declined.
func TestProgramStdInterfaceValue_DispatchesToAQualifiedImpl(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	dir := t.TempDir()
	files := map[string]string{
		"main.nomi": `import {
    bind
    color.{Color, Shade}
    std::io
    std::sql
}

enum Size {
    Small
    Large
}

impl sql.ToSql for Size {
    fn to_sql(value: Size) -> sql.Value {
        case value {
            .Small -> sql.Value.Text("small")
            .Large -> sql.Value.Text("large")
        }
    }
}

fn main() {
    io.print(bind.bind(Size.Large) == sql.Value.Text("large"))
    io.print(bind.bind(Color.Red) == sql.Value.Text("red"))
    io.print(bind.bind(Shade.Dark) == sql.Value.Text("dark"))
    io.print(bind.bind(3) == sql.Value.Int(3))
    io.print(bind.bind(Some("a")) == sql.Value.Text("a"))
}
`,
		"bind.nomi": `import std::sql.{ToSql, Value}

pub fn bind(value: ToSql) -> Value {
    ToSql.to_sql(value)
}
`,
		"color.nomi": `import std::sql.{self, ToSql}

pub enum Color {
    Red
    Blue
}

impl sql.ToSql for Color {
    fn to_sql(value: Color) -> sql.Value {
        case value {
            .Red -> sql.Value.Text("red")
            .Blue -> sql.Value.Text("blue")
        }
    }
}

pub enum Shade {
    Light
    Dark
}

impl ToSql for Shade {
    fn to_sql(value: Shade) -> sql.Value {
        case value {
            .Light -> sql.Value.Text("light")
            .Dark -> sql.Value.Text("dark")
        }
    }
}
`,
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0600); err != nil {
			t.Fatal(err)
		}
	}
	entry := filepath.Join(dir, "main.nomi")
	if _, err := Analyze(entry); err != nil {
		t.Fatalf("the front end rejects this, so nothing below is tested: %v", err)
	}
	const want = "True\nTrue\nTrue\nTrue\nTrue\n"
	got := vmReference(entry)
	if got.stdout != want || got.stderr != "" || got.exit != 0 {
		t.Fatalf("VM run (exit %d):\n--- stdout ---\n%s--- stderr ---\n%s--- want ---\n%s", got.exit, got.stdout, got.stderr, want)
	}
}
