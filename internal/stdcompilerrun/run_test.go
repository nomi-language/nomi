package stdcompilerrun_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/rt"

	"github.com/nomi-language/nomi/internal/stdcompilerrun"
	_ "github.com/nomi-language/nomi/vmhost" // installs the VM as the engine
)

// run is `compiler.run` with no project root and no virtual files.
func run(src string) (string, error) {
	return stdcompilerrun.RunSource(src, "", nil, nil)
}

// TestRunIsTheVMsPipeline covers what this package adds over the engine: the
// shared front end ahead of it, and an error for every stage that can refuse
// or fault. The engine is the VM, which nomi/vmhost installs.
//
// The rows are chosen so a Go implementation of evaluation could not fake any of
// them: captured stdout in program order, arithmetic actually evaluated, the
// standard library attached to a source that declares none of it, and derive
// synthesis run over a struct with no hand-written impl.
func TestRunIsTheVMsPipeline(t *testing.T) {
	ok := func(t *testing.T, src, want string) {
		t.Helper()
		got, err := run(src)
		if err != nil {
			t.Fatalf("want output, got error %q\nsource:\n%s", err, src)
		}
		if got != want {
			t.Errorf("stdout\n got %q\nwant %q\nsource:\n%s", got, want, src)
		}
	}
	errContains := func(t *testing.T, src, want string) {
		t.Helper()
		got, err := run(src)
		if err == nil {
			t.Fatalf("want an error, got output %q\nsource:\n%s", got, src)
		}
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error text %q does not contain %q\nsource:\n%s", err, want, src)
		}
	}

	t.Run("captured stdout, in program order", func(t *testing.T) {
		ok(t, "import std/io\n\nfn main() {\n  io.print(\"a\")\n  io.print(\"b\")\n}\n", "a\nb\n")
	})
	t.Run("arithmetic is EVALUATED, not merely checked", func(t *testing.T) {
		ok(t, "import std/io\n\nfn main() {\n  io.print(Debug.inspect(6 * 7))\n}\n", "42\n")
	})
	t.Run("the standard library is attached", func(t *testing.T) {
		// String.trim is declared nowhere in the source, so this fails for a
		// pipeline that parsed and evaluated without loading std.
		ok(t, "import std/io\n\nfn main() {\n"+
			"  io.print(String.trim(\"  x  \"))\n}\n", "x\n")
	})
	t.Run("derives are synthesized", func(t *testing.T) {
		// Debug.inspect on a struct with no hand-written impl needs the
		// synthesis passes, not just the VM.
		ok(t, "import std/io\n\nstruct P {\n  x: Int\n}\n\nfn main() {\n"+
			"  io.print(Debug.inspect(P{x: 1}))\n}\n", "P{x: 1}\n")
	})
	t.Run("no main runs the top level and stops", func(t *testing.T) {
		ok(t, "fn unused(): Int {\n  1\n}\n", "")
	})

	t.Run("a parse error is an error", func(t *testing.T) {
		errContains(t, "fn main() {\n", "expected '}'")
	})
	t.Run("an analysis error is an error", func(t *testing.T) {
		errContains(t, "fn main() {\n  _ = missing\n}\n", "undefined variable 'missing'")
	})
	t.Run("a runtime trap is an error", func(t *testing.T) {
		// Type-correct, and it faults when it RUNS. The arm no analyzer reaches.
		errContains(t, "import std/io\n\nfn main() {\n"+
			"  io.print(Debug.inspect(9223372036854775807 + 1))\n}\n", "integer overflow")
	})
	t.Run("partial output is DISCARDED on a fault", func(t *testing.T) {
		// The program prints before it traps, and the answer is an error rather
		// than the prefix — so the error is not merely "the text so far".
		got, err := run("import std/io\n\nfn main() {\n  io.print(\"before\")\n" +
			"  io.print(Debug.inspect(1 / 0))\n}\n")
		if err == nil {
			t.Fatalf("want an error, got output %q", got)
		}
		if strings.Contains(err.Error(), "before") {
			t.Errorf("the error text carries the partial stdout, so a caller cannot tell a "+
				"fault from output: %q", err)
		}
	})
	t.Run("a deadline that runs out while main blocks is an error, as nomi run reports it", func(t *testing.T) {
		// deadline_floor_test's "a nested deadline still tightens": the
		// program prints, then sleeps past its deadline. The answer is the
		// fault text, and the printed line is discarded.
		src := "import {\n  std/duration.Duration\n  std/io\n  std/timer\n}\n\n" +
			"struct Run {\n  context: Context\n}\n\n" +
			"fn boot(): Run {\n  Run{context: Context.root()}\n}\n\n" +
			"fn main() {\n  with Run.context = Context.with_timeout(Run.context, Duration.milliseconds(20))\n" +
			"  io.print(\"entered\")\n  timer.sleep(Duration.seconds(5))\n  io.print(\"outlasted\")\n}\n"
		got, err := run(src)
		if err == nil || err.Error() != rt.MainDeadlineFault {
			t.Fatalf("the VM answers %q, %v; want the error %q", got, err, rt.MainDeadlineFault)
		}
	})
	t.Run("a failed assertion in main is an error", func(t *testing.T) {
		src := "import std/assertions.AssertionFailure\n\n" +
			"fn main(): Result<Unit, AssertionFailure> {\n  assert 1 == 2\n  Ok(Unit)\n}\n"
		got, err := run(src)
		if err == nil || !strings.HasPrefix(err.Error(), "line 4: assertion failed") {
			t.Fatalf("the VM answers %q, %v; want the error \"line 4: assertion failed ...\"", got, err)
		}
	})
	t.Run("the pipeline is re-entrant", func(t *testing.T) {
		ok(t, "import std/compiler\nimport std/io\n\nfn main() {\n"+
			"  case compiler.run(\"import std/io\\n\\nfn main() {\\n  io.print(\\\"deep\\\")\\n}\\n\") {\n"+
			"    Ok(t) -> io.print(\"got \" + t)\n    Err(_) -> io.print(\"err\")\n  }\n}\n",
			"got deep\n\n")
	})
}

// TestProjectRootIsThreaded: a project-relative import inside a run source
// resolves against the project root it is given, and against nothing without
// one, which is a DIFFERENT answer rather than a slower one.
//
// Both directions, because only asserting the success half would pass for an
// implementation that ignored the root and resolved from the process working
// directory.
func TestProjectRootIsThreaded(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "helper.nomi"),
		[]byte("pub fn greet(): String {\n  \"hi\"\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := "import helper\nimport std/io\n\nfn main() {\n  io.print(helper.greet())\n}\n"

	if got, err := stdcompilerrun.RunSource(src, "", nil, nil); err == nil {
		t.Errorf("with no project root a project-relative import resolved anyway (%q), so "+
			"the root parameter is unobservable", got)
	}
	if got, err := stdcompilerrun.RunSource(src, dir, nil, nil); err != nil || got != "hi\n" {
		t.Errorf("with the project root set the import must resolve: out=%q err=%v", got, err)
	}
}
