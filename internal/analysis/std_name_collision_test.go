package analysis_test

import (
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/std"
)

// A user file may declare a type named after a stdlib type it does not
// import. Its identity is (declaring file, name), so std/tasks' concurrent
// block rule and std/supervisors' boot rule, which are about the stdlib's
// `Task.spawn` and `Supervisor.new`, do not apply to its functions. Each case
// is paired with the stdlib type spelled the same way, which must still be
// rejected.

func analyzeBootScope(src string) []analysis.TypeError {
	nodes, _ := parser.ParseWithRecovery(lexer.Lex(src))
	lib := std.Load()
	fa := analysis.BuildFileWithStdlib(nodes, lib.Primitives, lib.Modules, "", nil)
	analysis.AttachStdlibProjectImpls(fa, lib.Files)
	var errs []analysis.TypeError
	errs = append(errs, analysis.BuildTypes(fa, nodes)...)
	errs = append(errs, analysis.CheckTypes(fa, nodes)...)
	errs = append(errs, analysis.CheckBootScope(fa, nodes)...)
	return errs
}

func TestConcurrentScope_UserTaskTypeSpawnIsNotTaskSpawn_OK(t *testing.T) {
	src := `struct Task {
  n: Int
}

impl Task {
  fn spawn(n: Int): Task {
    Task{n: n}
  }

  fn spawn_all(t: Task): Int {
    t.n
  }
}

fn demo(): Int {
  Task.spawn(4) |> Task.spawn_all()
}`
	errs := analyzeConcurrentRaw(src)
	if len(errs) != 0 {
		t.Fatalf("a user Task's functions are rejected: %v", errs)
	}
}

func TestConcurrentScope_StdTaskSpawnBesideNoUserTask_Errors(t *testing.T) {
	src := `fn demo(): Int {
  t = Task.spawn(|| 42)
  Task.await(t)
}`
	expectConcurrentError(t, analyzeConcurrent(src), "spawn outside concurrent block")
}

func TestBootScope_UserSupervisorNewIsNotSupervisorNew_OK(t *testing.T) {
	src := `struct Supervisor {
  name: String
}

impl Supervisor {
  fn new(name: String): Supervisor {
    Supervisor{name: name}
  }
}

fn make(): Supervisor {
  Supervisor.new("root")
}

fn demo(): String {
  make().name
}`
	if errs := analyzeBootScope(src); len(errs) != 0 {
		t.Fatalf("a user Supervisor's `new` is held to the boot rule: %v", errs)
	}
}

func TestBootScope_StdSupervisorNewOutsideBoot_Errors(t *testing.T) {
	src := `import {
  std/duration.Duration
  std/supervisors.Supervisor
}

fn make_group(): Supervisor {
  Supervisor.new(shutdown_timeout: Duration.seconds(5), max_running: 4)
}

fn main() {
  Unit
}`
	expectConcurrentError(t, analyzeBootScope(src), "the bound would be per call rather than per downstream")
}
