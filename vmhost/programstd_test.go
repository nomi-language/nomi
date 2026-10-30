package vmhost

import (
	"context"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/fileuri"
	"github.com/nomi-language/nomi/std"
)

// A reference editor of a std module lowered with its program runs in the
// module's scope, typed literal included, and with the module's own names
// imported in its context, as a module page's editors are.
func TestProgramStd_AReferenceEditorRuns(t *testing.T) {
	for _, c := range []struct{ name, context string }{
		{"in the module's scope", ""},
		{"with the module imported", "import std/uuid.{self, Uuid}"},
	} {
		t.Run(c.name, func(t *testing.T) {
			var out strings.Builder
			body := "id = Uuid`0190A0B2-7F3C-7C3E-8A1B-1F2E3D4C5B6A`\n" +
				"assert Uuid.parse(\"0190a0b2-7f3c-7c3e-8a1b-1f2e3d4c5b6a\") == Ok(id)\n" +
				"assert Uuid.version(id) == 7\n" +
				"io.print(Debug.inspect(id))\n"
			cases, err := StdlibReference("uuid", "import std/io\n"+body, c.context, &out)
			if err != nil {
				t.Fatal(err)
			}
			if len(cases) != 1 || cases[0].Err != nil || len(cases[0].Blocked) > 0 {
				t.Fatalf("cases %+v\n%s", cases, out.String())
			}
			if want := "Uuid`0190a0b2-7f3c-7c3e-8a1b-1f2e3d4c5b6a`\n"; cases[0].Output != want {
				t.Errorf("output %q, want %q", cases[0].Output, want)
			}
		})
	}
}

// A std module's reference editor calls the module's own functions through
// its file alias, as a program does: `sql.with_connection(...)`, generic, in a
// binding and in an assertion subject, and `sql.close_quietly(...)`, plain and
// piped. Each lowers as the bare call does (irbuild's ownModuleCall).
func TestProgramStd_AModuleCallsItsOwnFunctionsQualified(t *testing.T) {
	body := "import std/sql_testing.Fake\n" +
		"fake = Fake.new()\n" +
		"count = sql.with_connection(fn -> Ok(fake), fn conn -> Conn.exec(conn, Sql\"DELETE FROM t\"))\n" +
		"assert count == Ok(0)\n" +
		"again = Fake.new()\n" +
		"assert sql.with_connection(fn -> Ok(again), fn conn -> Conn.exec(conn, Sql\"DELETE FROM u\")) == Ok(0)\n" +
		"other = Fake.new()\n" +
		"sql.close_quietly(other)\n" +
		"assert Fake.is_closed(other)\n" +
		"third = Fake.new()\n" +
		"third |> sql.close_quietly()\n" +
		"assert Fake.is_closed(third)\n" +
		"io.print(\"${Fake.log(fake)} ${Fake.log(again)}\")\n"
	var out strings.Builder
	cases, err := StdlibReference("sql", "import std/io\n"+body, "", &out)
	if err != nil {
		t.Fatalf("the front end rejects the editor, so nothing below is tested: %v", err)
	}
	if len(cases) != 1 || cases[0].Err != nil || len(cases[0].Blocked) > 0 {
		t.Fatalf("cases %+v\n%s", cases, out.String())
	}
	if want := "[DELETE FROM t, CLOSE] [DELETE FROM u, CLOSE]\n"; cases[0].Output != want {
		t.Errorf("output %q, want %q", cases[0].Output, want)
	}
}

// A std module's `//!` prompt or reference editor that imports another module
// lowered with its program only in its own body runs: std/sql's editor
// importing std/sql_testing's Fake, whose type names sql's Dialect. The prompt's
// import is the only way std/sql reaches std/sql_testing, so it must bring the
// module into the program (irbuild's withProgramStd) and join the two
// modules' types into one build (typeDeclEdges, nomiImportGraph), or the call
// to Fake.new is not supported. LoadStdlib is `nomi test std/sql.nomi`.
func TestProgramStd_APromptImportsAnotherProgramStdModule(t *testing.T) {
	body := "import std/sql_testing.Fake\n" +
		"fake = Fake.new()\n" +
		"assert sql.with_connection(fn -> Ok(fake), fn conn -> Conn.exec(conn, Sql\"DELETE FROM t\")) == Ok(0)\n" +
		"assert Fake.is_closed(fake)\n" +
		"io.print(\"${Fake.log(fake)}\")\n"
	var out strings.Builder
	cases, err := StdlibReference("sql", "import std/io\n"+body, "", &out)
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 1 || cases[0].Err != nil || len(cases[0].Blocked) > 0 {
		t.Fatalf("cases %+v\n%s", cases, out.String())
	}
	if want := "[DELETE FROM t, CLOSE]\n"; cases[0].Output != want {
		t.Errorf("output %q, want %q", cases[0].Output, want)
	}

	p, err := LoadStdlib(fileuri.ToPath(std.Load().FileURI("sql")))
	if err != nil {
		t.Fatal(err)
	}
	sawPrompt := false
	for _, c := range p.Cases(io.Discard, TestOptions{}) {
		if c.Err != nil || len(c.Blocked) > 0 {
			t.Errorf("std/sql: %s: %v %v", c.Name, c.Err, c.Blocked)
		}
		sawPrompt = sawPrompt || strings.Contains(c.Name, "with_connection")
	}
	if !sawPrompt {
		t.Errorf("std/sql has no with_connection prompt, which imports std/sql_testing")
	}
}

// A reference editor of a std module lowered once for the process imports a
// module lowered with its program and runs: std/io's editor calling
// path.join, and building a Sandbox for fs_testing.run over fs.read_text;
// std/strings' editor calling Uuid.nil. A shared module never imports a
// listed module itself, so the editor's import is the only way in, and the
// listed modules are lowered with the editor as a program's files are
// (irbuild's generateStdlibTestIRWithProgramStd). Each was blocked: "qualified
// call, file.fn: path.join", "a struct literal: Sandbox", "qualified call,
// Type.method: Uuid.nil". A false assertion over path.join fails, so the
// case ran.
func TestProgramStd_ASharedModulesEditorImportsAListedModule(t *testing.T) {
	for _, c := range []struct{ module, body, want string }{
		{"io", "import std/path\nprint(path.join(\"data\", \"log.txt\"))\nassert path.join(\"a/\", \"/b\") == \"a/b\"\n", "data/log.txt\n"},
		{"io", "import std/fs\nimport std/fs_testing.{self, Sandbox}\n" +
			"ran = fs_testing.run(#Sandbox{files: %{\"a.txt\" => \"x\"}}, fn -> fs.read_text(\"a.txt\"))\n" +
			"assert ran.value == Result.Ok(\"x\")\n" +
			"print(fs_testing.text(ran, \"/work/a.txt\"))\n", "Some(x)\n"},
		{"strings", "import std/io\nimport std/uuid.Uuid\nio.print(Uuid.nil())\nassert Uuid.version(Uuid.nil()) == 0\n", "00000000-0000-0000-0000-000000000000\n"},
	} {
		var out strings.Builder
		cases, err := StdlibReference(c.module, c.body, "", &out)
		if err != nil {
			t.Errorf("std/%s: the front end rejects the editor, so nothing is tested: %v\n%s", c.module, err, c.body)
			continue
		}
		if len(cases) != 1 || cases[0].Err != nil || len(cases[0].Blocked) > 0 {
			t.Errorf("std/%s: want one passing case, got %+v\n%s", c.module, cases, c.body)
			continue
		}
		if cases[0].Output != c.want {
			t.Errorf("std/%s: output %q, want %q", c.module, cases[0].Output, c.want)
		}
	}
	cases, err := StdlibReference("io", "import std/path\nassert path.join(\"a\", \"b\") == \"a/c\"\n", "", io.Discard)
	if err != nil {
		t.Fatalf("the front end rejects the failing editor: %v", err)
	}
	if len(cases) != 1 || cases[0].Err == nil || len(cases[0].Blocked) > 0 {
		t.Errorf("a false assertion over path.join: want one failing case, got %+v", cases)
	}
}

// A std interface used as a value, implemented by the program, runs: std's
// test-only std/_fixtures/conn declares Conn and FromValue and a Pool that
// holds a Conn and a function, and is lowered with the program
// (irbuild's programstd.go), so none of it has an rt type or a row. The
// program's Memory is boxed as a Conn and dispatched from std's body, std's
// generic Pool.column decodes through std's and the program's FromValue,
// std's enums cross both ways, Debug renders a Pool holding std's Closed
// or the program's Memory, and std's functions over an Iter, generic or
// not, are called with their default left out.
//
// The fixture is in the stdlib only for a test binary run with
// NOMI_STD_FIXTURES=1, so the test runs itself again in a child process with
// it set.
func TestProgramStd_AStdInterfaceValueWithAProgramImplRuns(t *testing.T) {
	if !std.FixturesLoaded() {
		cmd := exec.Command(os.Args[0], "-test.run=^TestProgramStd_AStdInterfaceValueWithAProgramImplRuns$", "-test.count=1", "-test.v")
		cmd.Env = append(os.Environ(), "NOMI_STD_FIXTURES=1")
		out, err := cmd.CombinedOutput()
		if err != nil || !strings.Contains(string(out), "--- PASS: TestProgramStd_AStdInterfaceValueWithAProgramImplRuns") {
			t.Fatalf("the child with std's fixtures failed: %v\n%s", err, out)
		}
		return
	}
	const src = `import std/io
import std/_fixtures/conn
import std/_fixtures/conn.{Closed, Conn, FromValue, Pool, Value}

struct Memory {
    label: String
    rows: List<List<Value>>
}

impl Conn for Memory {
    fn query(conn: Memory, sql: String) -> Result<List<List<Value>>, conn.Error> {
        if sql == "refuse" {
            Err(#conn.Error.Refused{reason: "closed"})
        } else {
            Ok(conn.rows)
        }
    }

    fn name(conn: Memory) -> String {
        conn.label
    }
}

struct Celsius {
    degrees: Int
}

impl FromValue for Celsius {
    fn from_value(value: Value, column: Int) -> Result<Celsius, conn.Error> {
        case value {
            Value.Number(n) -> Ok(#Celsius{degrees: n})
            _ -> Err(#conn.Error.Refused{reason: "column ${column} is not a temperature"})
        }
    }
}

fn main() {
    memory = #Memory{label: "memory", rows: [[Value.Number(1)], [Value.Number(2)]]}
    pool = #Pool{conn: memory, log: &("conn " + &1)}
    io.print(Pool.describe(pool))
    ints: Result<List<Int>, conn.Error> = Pool.column(pool, "select")
    io.print(Debug.inspect(ints))
    temps: Result<List<Celsius>, conn.Error> = Pool.column(pool, "select")
    io.print(Debug.inspect(temps))
    texts: Result<List<String>, conn.Error> = Pool.column(pool, "select")
    io.print(Debug.inspect(texts))
    refused: Result<List<Int>, conn.Error> = Pool.column(pool, "refuse")
    io.print(Debug.inspect(refused))
    conns: List<Conn> = [memory, #Closed{name: "closed"}]
    io.print(Debug.inspect(#Pool{conn: #Closed{name: "shut"}, log: &(&1)}))
    io.print(Debug.inspect(pool))
    shut: Result<List<Int>, conn.Error> = Pool.column(#Pool{conn: #Closed{name: "shut"}, log: &(&1)}, "select")
    io.print(Debug.inspect(shut))
    io.print(conn.names(conns))
    io.print(conn.names(conns, " | "))
    io.print(conn.shown([1, 2, 3]))
    io.print(conn.shown(%["x"], "/"))
}
`
	p, err := LoadSource("main", src)
	if err != nil {
		t.Fatalf("the front end rejects the program: %v", err)
	}
	if err := p.Unsupported(); err != nil {
		t.Fatalf("the program does not lower: %v", err)
	}
	var out strings.Builder
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatalf("run: %v\n%s", err, out.String())
	}
	want := "conn memory\n" +
		"Ok([1, 2])\n" +
		"Ok([#Celsius{degrees: 1}, #Celsius{degrees: 2}])\n" +
		"Err(#Decode{column: 0, found: Number})\n" +
		"Err(#Refused{reason: \"closed\"})\n" +
		"#Pool{conn: #Closed{name: \"shut\"}, log: <function>}\n" +
		"#Pool{conn: #Memory{label: \"memory\", rows: [[Number(1)], [Number(2)]]}, log: <function>}\n" +
		"Err(#Refused{reason: \"shut is closed to select\"})\n" +
		"memory, closed\n" +
		"memory | closed\n" +
		"1, 2, 3\n" +
		"x\n"
	if out.String() != want {
		t.Errorf("output:\n%s\nwant:\n%s", out.String(), want)
	}
}

// A std module tested from edited source (`nomi test std/sql.nomi` of a
// file that differs from the embedded one, a reference editor) runs its
// cases as the embedded module does, wherever its code sits: as written,
// checked again, with every line moved down one, and with lines added in
// its middle. The edited module's impls are its own in the impl index
// (analysis.AttachEditedStdlibImpls). With the shared copy's there, a
// `ToSql.to_sql(Some(3))` prompt dispatched to the shared copy's impl
// function, which the builder does not lower, and a moved file typed each
// impl by whatever stood at the shared copy's line and column, so a typed
// literal's handler had no function type. std/sql holds both: a typed
// literal (`Sql"..."`) and impls of its own interfaces for a std container.
// std/regex is a module lowered once for the process, which an edit lowers
// again (irbuild.GenerateEditedStdlibTestIR).
func TestProgramStd_AnEditedModuleRunsWhereverItsCodeSits(t *testing.T) {
	for _, module := range []string{"sql", "uuid", "money", "regex"} {
		embedded, ok := std.ReadFile(module)
		if !ok {
			t.Fatalf("no std/%s", module)
		}
		want := passingCases(t, module+" embedded", func() (*Program, error) {
			return LoadStdlib(fileuri.ToPath(std.Load().FileURI(module)))
		})
		if want == 0 {
			t.Fatalf("std/%s has no passing case to compare", module)
		}
		src := string(embedded)
		lines := strings.SplitAfter(src, "\n")
		middle := strings.Join(lines[:len(lines)/2], "") + "\n\n\n\n" + strings.Join(lines[len(lines)/2:], "")
		for name, edited := range map[string]string{
			"as written":      src,
			"moved down one":  "\n" + src,
			"lines in middle": middle,
		} {
			got := passingCases(t, module+" "+name, func() (*Program, error) {
				return LoadStdlibSource(module, edited)
			})
			if got != want {
				t.Errorf("std/%s %s: %d cases pass, the embedded module passes %d", module, name, got, want)
			}
		}
	}
}

// passingCases runs every case of the program load answers and counts
// the passing ones, reporting each case that fails or is blocked.
func passingCases(t *testing.T, label string, load func() (*Program, error)) int {
	t.Helper()
	p, err := load()
	if err != nil {
		t.Errorf("%s: %v", label, err)
		return 0
	}
	passed := 0
	for _, c := range p.Cases(io.Discard, TestOptions{}) {
		if c.Err != nil || len(c.Blocked) > 0 {
			t.Errorf("%s: %s: %v %v", label, c.Name, c.Err, c.Blocked)
			continue
		}
		passed++
	}
	return passed
}
