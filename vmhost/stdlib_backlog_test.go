package vmhost_test

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/std"
	"github.com/nomi-language/nomi/vmhost"
)

// stdlibBacklogCases are stdlib `//!` prompt cases that were BLOCKED on the
// VM and now run, one group per builder change. Each is named by its module and
// the case name's suffix as `nomi test` prints it. internal/expectation's TestExpectation_Stdlib
// holds what they print to stdlib.expect; this test holds that they run.
var stdlibBacklogCases = []struct{ cause, module, name string }{
	// `Maybe.None` written as a field access with no expected type.
	{"Maybe.None field access", "maybe", "impl / some? //! test lines 24-25"},
	{"Maybe.None field access", "maybe", "impl / none? //! test lines 35-36"},
	// A bare variant operand typed by the call's solved signature, and the
	// prelude plans inside an assertion subject.
	{"prelude with_default / from_maybe", "maybe", "impl / with_default //! test lines 57-58"},
	{"prelude with_default / from_maybe", "results", "impl / with_default //! test lines 68-69"},
	{"prelude with_default / from_maybe", "results", "impl / from_maybe //! test lines 119-120"},
	// A generic std call with a type argument nothing constrains.
	{"unsolved generic std call", "results", "impl / map //! test lines 57-58"},
	{"unsolved generic std call", "results", "impl / to_maybe //! test lines 108-109"},
	// `List.head([])` and `List.tail([])`.
	{"untyped empty list operand", "lists", "impl / head //! test lines 50-51"},
	{"untyped empty list operand", "lists", "impl / tail //! test lines 61-62"},
	// `String.join([], ", ")`: an empty list passed to a std host.
	{"untyped empty list operand", "strings", "impl / join //! test lines 86-92"},
	// Lists of Byte, and `==` over Bytes.
	{"Byte lists", "strings", "impl / to_bytes //! test lines 129-132"},
	{"Byte lists", "bytes", "impl / length //! test lines 69-71"},
	{"Byte lists", "bytes", "impl / at //! test lines 76-80"},
	{"Byte lists", "bytes", "impl / slice //! test lines 86-90"},
	{"Byte lists", "bytes", "impl / concat //! test lines 95-102"},
	{"Byte lists", "bytes", "impl / to_list //! test lines 107-113"},
	{"Byte lists", "bytes", "impl / from_list //! test lines 118-120"},
	{"Byte lists", "bytes", "impl / to_string //! test lines 125-128"},
	{"Byte lists", "bytes", "impl / each_while //! test lines 135-140"},
	{"Byte lists", "bytes", "impl / add //! test lines 147-151"},
	{"Byte lists", "bytes", "impl / inspect //! test lines 169-172"},
	// A defaulted std function called short (`Time.new(h, m)`) inside
	// its own module's test body, the module's once cells (`Int.max_value`)
	// and bare siblings (`day_nanos`) read there, `Task.spawn_all` over a
	// List source, and `.Completed(42)` where an Outcome<Int> is expected.
	{"std sibling called bare in its own test", "calendar", "day_nanos //! test line 376"},
	{"std short call / sibling in a std test", "calendar", "impl / in_zone //! test lines 1195-1203"},
	{"std short call / sibling in a std test", "calendar", "impl / new //! test lines 335-337"},
	{"std short call / sibling in a std test", "calendar", "impl / new //! test lines 514-515"},
	{"std short call / sibling in a std test", "calendar", "impl / parse //! test lines 1289-1294"},
	{"std once read in a test body", "int", "impl / max_value //! test line 31"},
	{"std once read in a test body", "int", "impl / min_value //! test line 38"},
	{"std once read in a test body", "int", "impl / wrapping_add //! test line 72"},
	{"std once read in a test body", "int", "impl / wrapping_sub //! test line 77"},
	{"Task.spawn_all over a List", "tasks", "impl / await_all //! test lines 123-129"},
	{"dot-variant prelude constructor", "tasks", "impl / outcome //! test lines 140-146"},
	{"Task.spawn_all over a List", "tasks", "impl / spawn_all //! test lines 98-104"},
	// A std marker value (`ChannelClosed`), `Range.contains?` over a Decimal
	// range with its host comparator, and `Display.to_string(bytes)` beside
	// the inherent `Bytes.to_string`.
	{"std marker value", "channels", "impl / close //! test lines 104-107"},
	{"Decimal Range.contains?", "ranges", "impl / contains? //! test lines 67-70"},
	{"interface impl shadowed by an inherent method", "bytes", "impl / to_string //! test lines 159-161"},
	// A bound lambda whose `if` takes the checker's result type, a std
	// generic instance over `[]` and a Maybe of a tuple, and a Vector
	// intrinsic (vm-backlog-generics).
	{"lambda result from the checker", "maybe", "impl / flat_map //! test lines 68-76"},
	{"std instance over an empty list", "lists", "impl / next_item //! test lines 35-36"},
	{"Vector.next_item intrinsic", "vectors", "impl / next_item //! test lines 60-61"},
}

// TestStdlibBacklog_CasesRunOnTheVM runs each module the table names on the
// VM and requires every named case to report `ok`.
func TestStdlibBacklog_CasesRunOnTheVM(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; lowers and runs stdlib modules' prompts on the VM; -short")
	}
	pinStdlibEnv(t)
	restoreEnv, err := vmhost.DefaultTestEnv()
	if err != nil {
		t.Fatal(err)
	}
	defer restoreEnv()
	reports := map[string]string{}
	for _, c := range stdlibBacklogCases {
		out, seen := reports[c.module]
		if !seen {
			path := strings.TrimPrefix(std.Load().FileURI(c.module), "file://")
			p, err := vmhost.LoadStdlib(path)
			if err != nil {
				t.Fatalf("std/%s: lowering its prompt cases: %v", c.module, err)
			}
			var buf bytes.Buffer
			rep := vmhost.NewTestReport(&buf)
			p.Test(&buf, rep, path, vmhost.TestOptions{},
				func(name string) string { return vmhost.TestName(path, name) })
			rep.Summary()
			out = buf.String()
			reports[c.module] = out
		}
		found := false
		for _, line := range strings.Split(out, "\n") {
			if strings.HasSuffix(line, ":: "+c.name) {
				found = true
				if !strings.HasPrefix(line, "ok ") {
					t.Errorf("%s: std/%s %q does not run on the VM:\n%s", c.cause, c.module, c.name, line)
				}
			}
			if strings.HasPrefix(line, "BLOCKED ") && strings.Contains(line, ":: "+c.name+" ") {
				found = true
				t.Errorf("%s: std/%s %q is blocked on the VM:\n%s", c.cause, c.module, c.name, line)
			}
		}
		if !found {
			t.Errorf("%s: std/%s reported no case named %q", c.cause, c.module, c.name)
		}
	}
}

// stdlibBacklogUserSource exercises the same builder changes from a user
// file, where no stdlib module's own scope applies. Every assertion states
// its expected value, so a case that runs and passes is a right answer.
const stdlibBacklogUserSource = `import std/channels.{Channel, Sender, ChannelClosed}
import std/tasks.{Task}

test "Maybe.None as a field access" {
  assert Maybe.none?(Maybe.None)
  refute Maybe.some?(Maybe.None)
}

test "a bare variant typed by the call" {
  assert Maybe.with_default(Maybe.None, 4) == 4
  assert Result.from_maybe(None, "missing") == Result.Err("missing")
}

test "an unconstrained type argument" {
  assert Result.map(Result.Ok(3), |x| x * 2) == Result.Ok(6)
  assert Result.to_maybe(Result.Ok(3)) == Some(3)
}

test "untyped empty lists" {
  assert List.head([]) == None
  assert String.join([], ", ") == ""
}

test "lists of Byte and Bytes equality" {
  a = try Byte.from_int(97)
  b = try Byte.from_int(98)
  assert Bytes.from_list([a, b]) |> Bytes.to_list() == [a, b]
  refute Bytes.from_list([a]) == Bytes.from_list([b])
  assert Display.to_string(Bytes.from_list([a, b])) == "Bytes(2)"
}

test "a std marker value" {
  ch = Channel.buffered<Int>(1)
  refute Sender.send(ch.sender, 7) == Result.Err(ChannelClosed)
  Sender.close(ch.sender)
  assert Sender.send(ch.sender, 7) == Result.Err(ChannelClosed)
}

test "Task.spawn_all over a List" {
  results = concurrent {
    [1, 2, 3]
    |> Task.spawn_all(|n: Int| n * 2, max_running: 2)
    |> Task.await_all()
  }
  assert results == [2, 4, 6]
}

test "Decimal Range.contains?" {
  assert Range.contains?(1.25d..=2.50d, 2.50d)
  refute Range.contains?(1.25d..2.50d, 2.50d)
  refute Range.contains?(1.25d..2.50d, 1.2d)
}
`

// TestStdlibBacklog_UserFileRunsOnTheVM runs stdlibBacklogUserSource on the VM
// and requires every case to run and pass.
func TestStdlibBacklog_UserFileRunsOnTheVM(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; lowers the stdlib; -short")
	}
	pinStdlibEnv(t)
	restoreEnv, err := vmhost.DefaultTestEnv()
	if err != nil {
		t.Fatal(err)
	}
	defer restoreEnv()
	const name = "backlog_test.nomi"
	p, err := vmhost.LoadSource(name, stdlibBacklogUserSource)
	if err != nil {
		t.Fatalf("lowering: %v", err)
	}
	var buf bytes.Buffer
	rep := vmhost.NewTestReport(&buf)
	p.Test(&buf, rep, name, vmhost.TestOptions{}, func(n string) string { return vmhost.TestName(name, n) })
	failed := rep.Summary()
	out := buf.String()
	if failed || strings.Contains(out, "BLOCKED") || strings.Count(out, "\nok ")+boolInt(strings.HasPrefix(out, "ok ")) != 8 {
		t.Fatalf("every case must run and pass on the VM:\n%s", out)
	}
}

// pinStdlibEnv keeps escape sequences out of a captured report and clears
// NOMI_ENV, as a fresh shell has it.
func pinStdlibEnv(t *testing.T) {
	t.Helper()
	t.Setenv("NOMI_COLOR", "never")
	if prior, ok := os.LookupEnv("NOMI_ENV"); ok {
		if err := os.Unsetenv("NOMI_ENV"); err != nil {
			t.Fatalf("clearing NOMI_ENV: %v", err)
		}
		t.Cleanup(func() { _ = os.Setenv("NOMI_ENV", prior) })
	}
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
