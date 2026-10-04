package irbuild

import (
	"strings"
	"testing"
)

// TestPinned_StdNameCollision runs user types that share a name with a stdlib
// type the program does not import. A type's identity is its declaring file
// and its name, so each is the program's own type: its struct literal, field
// read, inherent function, Display impl and synthesized Debug are the
// program's, including functions that carry a std function's name
// (`Task.spawn`, `Sender.send`, `Supervisor.new`).
//
// Before the fix the builder lowered `Task.*`, `Channel.*`, `Sender.*`,
// `Receiver.*` and `Supervisor.spawn*` by spelling as the std concurrency
// intrinsics, which declined (`BLOCKED ... qualified call, Type.method:
// Task.value`), and the checker applied std/tasks' concurrent-block rule to a
// user `Task.spawn` and std/supervisors' boot rule to a user `Supervisor.new`.
// If the front end starts rejecting the fixture again, stdout is empty and
// the exit is non-zero, so this fails.
func TestPinned_StdNameCollision(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	var want strings.Builder
	for _, n := range []string{"Duration", "Date", "Request", "Response", "Header", "Json", "Seed",
		"Task", "Sender", "Receiver", "Channel", "Supervisor"} {
		// Inherent function, field read, Display.to_string, interpolation
		// (Display), Debug.inspect.
		want.WriteString("3\n3\n" + n + "(3)\n" + n + "(3)\n" + n + "{n: 3}\n")
	}
	want.WriteString("" +
		"Task(40)\n" + // Task.spawn(4), a constructor
		"5\n" + // Task.spawn_all
		"5\n" + // Task.await
		"5\n" + // Sender.send
		"5\n" + // Receiver.receive
		"Channel(40)\n" + // Channel.buffered(4)
		"Supervisor(40)\n" + // Supervisor.new(4)
		"Supervisor(40)\n" + // Supervisor.spawn(4)
		"5\n") // Supervisor.spawn_all
	got := vmReference(fixture("std_name_collision.nomi"))
	if got.stdout != want.String() || got.exit != 0 || got.stderr != "" {
		t.Fatalf("user types named after stdlib types do not run as their own types:\nwant stdout=%q\ngot  %s",
			want.String(), got)
	}
}
