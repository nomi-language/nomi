package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// groupSubtestsSource is a group whose `subtests` block boots an app per
// row: line 13 is the block, line 16 its first test and line 20 its second.
const groupSubtestsSource = `struct Config {
  store: String
}

fn boot(startup: Startup) -> Result<Config, String> {
  Ok(Config{store: Map.get(startup.env, "STORE") |> Maybe.with_default("none")})
}

fn main() {}

group "todo api" {
  // one row per store
  subtests ["memory", "sqlite"], store {
    boot boot(Startup{env: %{"STORE" => store}})

    test "boots the row's store" {
      assert Config.store == "memory"
    }

    test "counts" {
      subtests [1, 2], n {
        assert n > 0
      }
    }
  }
}
`

// Each row's tests are cases named `group [row] / test`, a failing row fails
// only its own case, and a statement-level `subtests` inside counts its rows.
func TestTestCommand_GroupSubtestsNamesAndCountsEachRow(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	testPath := filepath.Join(t.TempDir(), "rows_test.nomi")
	mustWrite(t, testPath, groupSubtestsSource)
	out, _ := runNomi(t, cacheRoot, "test", testPath)
	for _, want := range []string{
		"ok " + testPath + " :: todo api [memory] / boots the row's store\n",
		"FAIL " + testPath + " :: todo api [sqlite] / boots the row's store\n",
		"ok " + testPath + " :: todo api [memory] / counts (2 cases)\n",
		"ok " + testPath + " :: todo api [sqlite] / counts (2 cases)\n",
		"test result: FAILED. 5 passed, 1 failed",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the report lacks %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "= \"sqlite\"") {
		t.Errorf("the failing row's report should show the row's booted store:\n%s", out)
	}
}

// `--line` at a test in the block runs it for every row, and `--line` at the
// block's own line runs the whole group, as the group's header does.
func TestTestCommand_GroupSubtestsLineFilter(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	testPath := filepath.Join(t.TempDir(), "rows_test.nomi")
	mustWrite(t, testPath, groupSubtestsSource)

	out, _ := runNomi(t, cacheRoot, "test", testPath, "--line", "20", "--format", "json")
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		var rec struct {
			Type, Name string
			Line       int
		}
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("a line that is not JSON: %q\n%s", line, out)
		}
		if rec.Type == "test" {
			names = append(names, rec.Name)
			if rec.Line != 20 {
				t.Errorf("%s: line %d, want the test's first line, 20", rec.Name, rec.Line)
			}
		}
	}
	want := []string{testPath + " :: todo api [memory] / counts", testPath + " :: todo api [sqlite] / counts"}
	if strings.Join(names, "\n") != strings.Join(want, "\n") {
		t.Errorf("--line 20 ran %q, want %q", names, want)
	}

	out, _ = runNomi(t, cacheRoot, "test", testPath, "--line", "13")
	if !strings.Contains(out, "5 passed, 1 failed") {
		t.Errorf("--line 13, the `subtests` line, should run the whole group:\n%s", out)
	}
}

// What the front end refuses about a group's `subtests` block, with its
// message: rows that are not literal, a `?` in a row, a pattern that does
// not match every row, a name that is no String column, and an item beside
// the block.
func TestCheckCommand_GroupSubtestsRefusals(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	cacheRoot := t.TempDir()
	dir := t.TempDir()
	for _, c := range []struct {
		name, body, want string
	}{
		{"computed rows", "subtests rows(), n {\n    test \"t\" {\n      assert n > 0\n    }\n  }",
			"the rows of a `subtests` block in a `group` are a list literal, such as `[\"memory\", \"sqlite\"]`: each row's tests are named and counted when the tests are collected, before anything runs"},
		{"a row that is not a literal", "subtests [1, 1 + 1], n {\n    test \"t\" {\n      assert n > 0\n    }\n  }",
			"a row of a `subtests` block in a `group` is a literal value, read when the tests are collected: a String, a number, True or False, or a tuple of them"},
		{"a ? in a row", "subtests [1, parse(\"2\")?], n {\n    test \"t\" {\n      assert n > 0\n    }\n  }",
			"`?` cannot be used in a row of a `subtests` block in a `group`: the rows are literal values, read when the tests are collected"},
		{"a refutable pattern", "subtests [(1, 2)], (1, n) {\n    test \"t\" {\n      assert n > 0\n    }\n  }",
			"a `subtests` pattern must match every row; bind it with a name or an irrefutable pattern and match it in the body"},
		{"a name that is no column", "subtests [(1, \"a\")], (n, s), \"x\" {\n    test \"t\" {\n      assert n > 0 and s != \"\"\n    }\n  }",
			"the name of a row of a `subtests` block in a `group` is a String column its pattern binds, such as `subtests [(\"memory\", 1)], (store, workers), store`"},
		{"a name column that is no String", "subtests [(1, \"a\")], (n, s), n {\n    test \"t\" {\n      assert n > 0 and s != \"\"\n    }\n  }",
			"the name of a `subtests` row must be a String, got Int"},
		{"a setup beside the block", "setup 1\n\n  subtests [1, 2], n {\n    test \"t\" {\n      assert n > 0\n    }\n  }",
			"a `group` with a `subtests` block holds nothing else: its clock, boot, setup and tests go inside the block, which runs them once per row"},
		{"a test after the block", "subtests [1, 2], n {\n    test \"t\" {\n      assert n > 0\n    }\n  }\n\n  test \"u\" {\n    assert True\n  }",
			"a `group` with a `subtests` block holds nothing else: its clock, boot, setup and tests go inside the block, which runs them once per row"},
		{"a block inside the block", "subtests [1, 2], n {\n    subtests [3], m {\n      test \"t\" {\n        assert n < m\n      }\n    }\n  }",
			"`subtests` in a group cannot hold another `subtests`: each row of the outer one boots the group once"},
	} {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(dir, strings.ReplaceAll(c.name, " ", "_")+"_test.nomi")
			src := "fn rows() -> List<Int> {\n  [1]\n}\n\nfn parse(s: String) -> Result<Int, String> {\n  Int.parse(s) |> Result.map_err(fn _ -> s)\n}\n\n" +
				"group \"g\" {\n  " + c.body + "\n}\n"
			mustWrite(t, path, src)
			out, err := runNomi(t, cacheRoot, "check", path)
			if err == nil {
				t.Fatalf("nomi check accepts it:\n%s\n%s", src, out)
			}
			if !strings.Contains(out, "error: "+c.want+"\n") {
				t.Errorf("the error lacks %q:\n%s", c.want, out)
			}
		})
	}
}
