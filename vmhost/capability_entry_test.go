package vmhost_test

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// overListFiles is a project whose nomi.toml allows std::io alone, and
// whose main, exported function and test each read a file (std::fs).
var overListFiles = map[string]string{
	"nomi.toml": "[module]\nname = \"app\"\nentry_points = [\"main\"]\n\n[capabilities]\nallow = [\"std::io\"]\n",
	"main.nomi": `import
    std::fs
    std::io
end

pub fn notes() -> String
    case fs.read_text("notes.txt")
        Ok(text) -> text
        Err(_)   -> ""
    end
end

fn main()
    io.print(notes())
end

test "notes"
    assert notes() == ""
end
`,
}

// overListWant is what each refusal says, with the project's directory
// cut from it: the call's place, and the list it is over (the program's
// for a run, its tests' for a test).
var overListWant = []string{"main.nomi:7:10", "std::fs, and nomi.toml allows"}

// Every way vmhost loads, runs, tests, calls or builds a project holds it
// to its nomi.toml's [capabilities] lists, and refuses this one before
// anything in it runs, with the diagnostic at the call. A loader for a
// run and its tests at once (LoadMarked, which the tour's browser build,
// its doctests and the examples' pages use) refuses it as a load failure,
// as it refuses a program that does not compile, so the page shows it as
// a compile error. A new way in belongs in this table.
func TestCapabilityLists_EveryEntryPointRefuses(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, overListFiles)
	entry := filepath.Join(dir, "main.nomi")
	load := func(t *testing.T) *vmhost.Program {
		t.Helper()
		p, err := vmhost.Load(entry)
		if err != nil {
			t.Fatalf("the load holds a project to its lists only for what runs: %v", err)
		}
		return p
	}
	marked := "// FILE: main.nomi\n" + overListFiles["main.nomi"] + "\n// FILE: nomi.toml\n" + overListFiles["nomi.toml"]
	markedModules := marked + "\n// FILE: go.mod\nmodule example.com/app\n\ngo 1.25\n"
	var out bytes.Buffer
	cases := []struct {
		name string
		do   func(t *testing.T) error
	}{
		{"Check", func(t *testing.T) error { return vmhost.Check(entry) }},
		{"Run", func(t *testing.T) error { return load(t).Run(context.Background(), &out, nil, false) }},
		{"Call", func(t *testing.T) error {
			_, err := load(t).Call(context.Background(), "notes")
			return err
		}},
		{"Test", func(t *testing.T) error {
			var report bytes.Buffer
			rep := vmhost.NewTestReport(&report)
			load(t).Test(&out, rep, entry, vmhost.TestOptions{}, func(n string) string { return n })
			rep.Summary()
			if !strings.Contains(report.String(), "test result: FAILED") {
				t.Fatalf("the test report does not fail:\n%s", report.String())
			}
			return errors.New(report.String())
		}},
		{"BuildImage", func(t *testing.T) error {
			_, _, err := load(t).BuildImage()
			return err
		}},
		{"BuildExportsImage", func(t *testing.T) error {
			_, _, err := load(t).BuildExportsImage()
			return err
		}},
		{"BuildWebImage", func(t *testing.T) error {
			_, err := load(t).BuildWebImage()
			return err
		}},
		{"LoadMarked", func(t *testing.T) error {
			_, _, err := vmhost.LoadMarked(marked, t.TempDir())
			if err != nil && !vmhost.LoadFailed(err) {
				var ce *vmhost.CapabilityError
				if !errors.As(err, &ce) {
					t.Fatalf("LoadMarked refuses with %T, not a *CapabilityError: %v", err, err)
				}
			}
			return err
		}},
		{"LoadMarked, a project of modules", func(t *testing.T) error {
			_, _, err := vmhost.LoadMarked(markedModules, t.TempDir())
			if err != nil && !vmhost.LoadFailed(err) {
				t.Fatalf("LoadMarked refuses a project it writes out with an error LoadFailed does not see: %v", err)
			}
			return err
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out.Reset()
			err := c.do(t)
			if err == nil {
				t.Fatal("admits a program its nomi.toml does not allow")
			}
			text := strings.ReplaceAll(err.Error(), dir+string(filepath.Separator), "")
			if !strings.Contains(text, overListWant[0]) || !strings.Contains(text, overListWant[1]) {
				t.Fatalf("refuses with another error than the list's:\n%s", text)
			}
			if out.Len() > 0 {
				t.Fatalf("the refused program printed %q", out.String())
			}
		})
	}
}
