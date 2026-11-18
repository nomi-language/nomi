package main

import (
	"path/filepath"
	"strings"
	"testing"
)

const dollarProgram = `import std::io

fn main()
    name = "Ada"
    io.print("Hello, $name")
    io.print(name)
end
`

// A string that writes `$name` where `name` is in scope is a warning with
// the rewrite as its help, from `nomi run`, which runs the program as
// written, from `nomi check`, which passes the file, and from `--format
// json`, with its code and fix. Every run reports it, with a cache too.
func TestDollarWarning_RunAndCheckWarnAndGoOn(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "main.nomi")
	mustWrite(t, entry, dollarProgram)
	const warning = "warning: `$name` is text here, not the value of `name`: Nomi interpolates with `${...}`, not `$`"
	const help = "= help: to interpolate, write `\"Hello, ${name}\"`"

	for _, cache := range []string{"off", t.TempDir()} {
		for range 2 {
			run := runNomiIn(t, dir, cache, "run", entry)
			if run.exit != 0 || run.stdout != "Hello, $name\nAda\n" || !strings.Contains(run.stderr, warning) || !strings.Contains(run.stderr, "main.nomi:5:22") || !strings.Contains(run.stderr, help) {
				t.Fatalf("nomi run (cache %s): %v", cache, run)
			}
		}
	}

	check := runNomiIn(t, dir, "off", "check", entry)
	if check.exit != 0 || !strings.Contains(check.stderr, warning) || !strings.Contains(check.stderr, help) {
		t.Fatalf("nomi check: %v", check)
	}

	jsonCheck := runNomiIn(t, dir, "off", "check", "--format", "json", entry)
	if jsonCheck.exit != 0 || !strings.Contains(jsonCheck.stdout, `"severity":"warning","code":"dollar-interpolation"`) ||
		!strings.Contains(jsonCheck.stdout, `"text":"${name}"`) || !strings.Contains(jsonCheck.stdout, `"status":"ok"`) {
		t.Fatalf("nomi check --format json: %v", jsonCheck)
	}
}
