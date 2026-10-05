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
// the declaration its prompts attach to, as `nomi test` prints it before the
// ` //! test line…` suffix. The suffix is left out because it moves whenever a
// line is inserted above the declaration; every case attached to a
// declaration of that name must run, so `impl / new` in calendar covers each
// `new` there. internal/expectation's TestExpectation_Stdlib holds what they
// print to stdlib.expect; this test holds that they run.
var stdlibBacklogCases = []struct{ cause, module, decl string }{
	// `Maybe.None` written as a field access with no expected type.
	{"Maybe.None field access", "maybe", "impl / some?"},
	{"Maybe.None field access", "maybe", "impl / none?"},
	// A bare variant operand typed by the call's solved signature, and the
	// prelude plans inside an assertion subject.
	{"prelude with_default / to_result", "maybe", "impl / with_default"},
	{"prelude with_default / to_result", "results", "impl / with_default"},
	{"prelude with_default / to_result", "maybe", "impl / to_result"},
	// A generic std call with a type argument nothing constrains.
	{"unsolved generic std call", "results", "impl / map"},
	{"unsolved generic std call", "results", "impl / to_maybe"},
	// `List.head([])` and `List.tail([])`.
	{"untyped empty list operand", "lists", "impl / head"},
	{"untyped empty list operand", "lists", "impl / tail"},
	// `String.join([], ", ")`: an empty list passed to a std host.
	{"untyped empty list operand", "strings", "impl / join"},
	// Lists of Byte, and `==` over Bytes.
	{"Byte lists", "strings", "impl / to_bytes"},
	{"Byte lists", "bytes", "impl / length"},
	{"Byte lists", "bytes", "impl / at"},
	{"Byte lists", "bytes", "impl / slice"},
	{"Byte lists", "bytes", "impl / concat"},
	{"Byte lists", "bytes", "impl / to_list"},
	{"Byte lists", "bytes", "impl / from_list"},
	// The inherent `Bytes.to_string` and `Display.to_string(bytes)` beside it.
	{"Byte lists / interface impl shadowed by an inherent method", "bytes", "impl / to_string"},
	{"Byte lists", "bytes", "impl / each_while"},
	{"Byte lists", "bytes", "impl / add"},
	{"Byte lists", "bytes", "impl / inspect"},
	// A defaulted std function called short (`Time.new(h, m)`) inside
	// its own module's test body, the module's once cells (`Int.max_value`)
	// and bare siblings (`day_nanos`) read there, `Task.spawn_all` over a
	// List source, and `.Completed(42)` where an Outcome<Int> is expected.
	{"std sibling called bare in its own test", "calendar", "day_nanos"},
	{"std short call / sibling in a std test", "calendar", "impl / in_zone"},
	{"std short call / sibling in a std test", "calendar", "impl / new"},
	{"std short call / sibling in a std test", "calendar", "impl / parse"},
	{"std once read in a test body", "int", "impl / max_value"},
	{"std once read in a test body", "int", "impl / min_value"},
	{"std once read in a test body", "int", "impl / wrapping_add"},
	{"std once read in a test body", "int", "impl / wrapping_sub"},
	{"Task.spawn_all over a List", "tasks", "impl / await_all"},
	{"dot-variant prelude constructor", "tasks", "impl / outcome"},
	{"Task.spawn_all over a List", "tasks", "impl / spawn_all"},
	// A std marker value (`ChannelClosed`) and `Range.contains?` over a
	// Decimal range with its host comparator.
	{"std marker value", "channels", "impl / close"},
	{"Decimal Range.contains?", "ranges", "impl / contains?"},
	// A bound lambda whose `if` takes the checker's result type, a std
	// generic instance over `[]` and a Maybe of a tuple, and a Vector
	// intrinsic (vm-backlog-generics).
	{"lambda result from the checker", "maybe", "impl / flat_map"},
	{"std instance over an empty list", "lists", "impl / next_item"},
	{"Vector.next_item intrinsic", "vectors", "impl / next_item"},
}

// stdlibPromptDecl is the declaration a report line's `//!` case attaches
// to, and whether the line names such a case: `ok <path> :: impl / new //!
// test lines 335-337` answers "impl / new". A BLOCKED line carries its reason
// after the name, which the suffix search skips.
func stdlibPromptDecl(line string) (string, bool) {
	_, name, ok := strings.Cut(line, ":: ")
	if !ok {
		return "", false
	}
	decl, _, ok := strings.Cut(name, " //! test line")
	return decl, ok
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
		found := 0
		for _, line := range strings.Split(out, "\n") {
			if decl, ok := stdlibPromptDecl(line); !ok || decl != c.decl {
				continue
			}
			found++
			if !strings.HasPrefix(line, "ok ") {
				t.Errorf("%s: std/%s %q does not run on the VM:\n%s", c.cause, c.module, c.decl, line)
			}
		}
		if found == 0 {
			t.Errorf("%s: std/%s reported no `//!` case attached to %q", c.cause, c.module, c.decl)
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
  assert Maybe.to_result(None, "missing") == Result.Err("missing")
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
