package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// testRunSource has a case of every kind `--run` selects by name: an
// attached test (line 15), a top-level test (20), a group's test (25), and
// a group whose `subtests` block (31) boots once per row, its "creates" at
// line 34 and its "counts" at 38, which runs a `subtests` statement of three
// rows. The boot prints the row it boots.
const testRunSource = `import std::io

struct Config
  store: String
end

fn boot(startup: Startup) -> Result<Config, String>
  store = Map.get(startup.env, "STORE") |> Maybe.with_default("none")
  io.print("booting ${store}")
  Ok(Config{store:})
end

fn main() end

//! assert double(2) == 4
fn double(x: Int) -> Int
  x * 2
end

test "top level"
  assert double(1) == 2
end

group "plain"
  test "inner"
    assert True
  end
end

group "todo api"
  subtests ["memory", "sqlite"], store
    boot boot(Startup{env: {"STORE" => store}})

    test "creates"
      assert Config.store == store
    end

    test "counts"
      subtests [1, 2, 3], n
        assert n > 0
      end
    end
  end
end
`

func writeTestRunFixture(t *testing.T) (dir, path string) {
	t.Helper()
	dir = t.TempDir()
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	path = filepath.Join(dir, "run_test.nomi")
	mustWrite(t, path, testRunSource)
	return dir, path
}

// `--run` runs the cases whose names, as the report prints them after
// `file :: `, the pattern matches; the summary counts only those, and a row
// of a group's `subtests` block none of whose cases match never boots.
func TestTestCommand_RunSelectsCasesByName(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	dir, _ := writeTestRunFixture(t)
	cacheRoot := t.TempDir()
	for _, c := range []struct {
		name    string
		args    []string
		ran     []string
		summary string
		absent  []string
	}{
		{"top-level", []string{"--run", "^top level$"},
			[]string{"ok run_test.nomi :: top level\n"}, "1 passed, 0 failed", nil},
		{"attached", []string{"--run=double"},
			[]string{"ok run_test.nomi :: double //! test line 15\n"}, "1 passed, 0 failed", nil},
		{"group", []string{"--run", "plain"},
			[]string{"ok run_test.nomi :: plain / inner\n"}, "1 passed, 0 failed", []string{"top level", "todo api"}},
		{"group row", []string{"--run", `\[memory\]`},
			[]string{"ok run_test.nomi :: todo api [memory] / creates\n", "ok run_test.nomi :: todo api [memory] / counts (3 cases)\n", "booting memory"},
			"4 passed, 0 failed", []string{"sqlite", "top level", "plain"}},
		{"one test of one row", []string{"--run", `todo api \[sqlite\] / creates`},
			[]string{"ok run_test.nomi :: todo api [sqlite] / creates\n"}, "1 passed, 0 failed", []string{"memory", "counts"}},
		{"statement rows run with their test", []string{"--run", "counts"},
			[]string{"ok run_test.nomi :: todo api [memory] / counts (3 cases)\n", "ok run_test.nomi :: todo api [sqlite] / counts (3 cases)\n"},
			"6 passed, 0 failed", []string{"creates"}},
		{"with --line", []string{"--line", "34", "--run", "sqlite"},
			[]string{"ok run_test.nomi :: todo api [sqlite] / creates\n"}, "1 passed, 0 failed", []string{"memory", "counts"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			args := append([]string{"test", "run_test.nomi"}, c.args...)
			stdout, stderr, err := runNomiSplit(t, cacheRoot, dir, args...)
			if err != nil {
				t.Fatalf("%v: %v\nstdout:\n%s\nstderr:\n%s", args, err, stdout, stderr)
			}
			for _, want := range append(c.ran, "test result: ok. "+c.summary+"\n") {
				if !strings.Contains(stdout, want) {
					t.Errorf("%v: the report lacks %q:\n%s", args, want, stdout)
				}
			}
			for _, not := range c.absent {
				if strings.Contains(stdout, not) {
					t.Errorf("%v: the report holds %q:\n%s", args, not, stdout)
				}
			}
		})
	}
}

// A `--run` that selects nothing is an error naming the pattern, never an
// empty pass, alone or with `--line`; an invalid pattern is refused before
// anything runs.
func TestTestCommand_RunMatchingNothingFails(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	dir, path := writeTestRunFixture(t)
	cacheRoot := t.TempDir()
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"--run", "nothing"}, `nomi test: no test matches --run "nothing"`},
		{[]string{"--line", "25", "--run", "memory"}, `nomi test: no test at run_test.nomi:25 matches --run "memory"`},
		{[]string{"--run", "a("}, `nomi test: invalid --run "a(": error parsing regexp: missing closing ): ` + "`a(`"},
	} {
		args := append([]string{"test", "run_test.nomi"}, c.args...)
		stdout, stderr, err := runNomiSplit(t, cacheRoot, dir, args...)
		if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() == 0 {
			t.Fatalf("%v: exit = %v, want a failing status\nstdout:\n%s\nstderr:\n%s", args, err, stdout, stderr)
		}
		if stdout != "" || stderr != c.want+"\n" {
			t.Errorf("%v: stdout:\n%s\nstderr %q, want %q", args, stdout, stderr, c.want+"\n")
		}
	}

	stdout, _, err := runNomiSplit(t, cacheRoot, dir, "test", "run_test.nomi", "--run", "nothing", "--format", "json")
	if err == nil {
		t.Fatalf("json: a pattern matching nothing passed:\n%s", stdout)
	}
	records := parseJSONRecords(t, stdout)
	if len(records) != 2 || records[0].Type != "file" || records[0].File != path || records[0].Message == nil ||
		*records[0].Message != `no test matches --run "nothing"` || records[1].Type != "summary" || records[1].Failed != 1 {
		t.Errorf("json: want a failed file record naming the pattern and the summary, got:\n%s", stdout)
	}
}

// Over a directory, a file in which `--run` selects nothing is not loaded,
// so another file's compile error does not fail a run that never reaches it.
func TestTestCommand_RunSkipsFilesItSelectsNothingIn(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	dir, _ := writeTestRunFixture(t)
	mustWrite(t, filepath.Join(dir, "broken_test.nomi"), "test \"broken\"\n  assert 1 == \"one\"\nend\n")
	stdout, stderr, err := runNomiSplit(t, t.TempDir(), dir, "test", ".", "--run", "top level")
	if err != nil {
		t.Fatalf("%v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if !strings.Contains(stdout, "ok run_test.nomi :: top level\n") || !strings.Contains(stdout, "1 passed, 0 failed") || strings.Contains(stdout, "broken") {
		t.Errorf("want only the top-level test:\n%s", stdout)
	}
}

// `--run` reaches the wrapper process a test file with Go bindings runs in.
func TestFFI_TestRunSelectsCases(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; builds FFI wrappers; -short")
	}
	dir := t.TempDir()
	if goModAbove(dir) {
		t.Skip("a go.mod above the temp directory")
	}
	env := buildEnv(t.TempDir())
	tests := filepath.Join(dir, "upper_test.nomi")
	mustWrite(t, tests, `gopkg "strings" as strings

fn upper(s: String) -> String go strings.ToUpper

test "upper"
  assert upper("hi") == "HI"
end

test "fails"
  assert upper("hi") == "hi"
end
`)
	r := runProcess(t, env, t.TempDir(), "", nomiBin, "test", tests, "--run", "^upper$")
	if r.exit != 0 || !strings.Contains(r.stdout, "1 passed, 0 failed") || strings.Contains(r.stdout, "fails") {
		t.Fatalf("want only `upper` to run:\n%s", r.transcript())
	}
	r = runProcess(t, env, t.TempDir(), "", nomiBin, "test", tests, "--run", "fails", "--format", "json")
	if r.exit == 0 || !strings.Contains(r.stdout, `"passed":0,"failed":1`) || strings.Contains(r.stdout, `"name":"`+tests+` :: upper"`) {
		t.Fatalf("json: want only `fails` to run:\n%s", r.transcript())
	}
}
