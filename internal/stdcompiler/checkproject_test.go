package stdcompiler_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/rt"

	"github.com/nomi-language/nomi/internal/stdcompiler"
)

// TestCheckProjectDiagnosticsArePinned pins `compiler.check_project`'s answers
// absolutely, as (line, col, message) triples from ProjectSpecDiagnostics. The
// rows cover every field a ProjectSpec carries: entry point, file map, and the
// three states of `Option<String>`.
func TestCheckProjectDiagnosticsArePinned(t *testing.T) {
	for _, tc := range []struct {
		name     string
		entry    string
		files    map[string]string
		manifest string // "" for None
		want     []string
	}{{
		name:  "a private sibling function is one diagnostic, at its position",
		entry: "main",
		files: map[string]string{
			"main": "import api.{make, secret}\n\nfn main()\n  w = make(\"Ada\")\n  _ = secret(w)\nend\n",
			"api": "pub struct Widget\n  name: String\nend\n\npub fn label(w: Widget) -> String\n  w.name\nend\n\n" +
				"private fn secret(w: Widget) -> String\n  \"secret \" + w.name\nend\n\n" +
				"pub fn make(name: String) -> Widget\n  Widget{name:}\nend\n",
		},
		want: []string{"1:19 `secret` is private to `api.nomi`\nhelp: drop `private` to name it from the other files of its package, or write `pub` to name it from any package"},
	}, {
		name:     "a manifest produces the entry-point diagnostics, both directions",
		entry:    "main",
		manifest: "[package]\nname = \"demo\"\nentry_points = [\"other\"]\n",
		files: map[string]string{
			"main":  "import other\n\nfn main()\n  other.helper()\n  Unit\nend\n",
			"other": "pub fn helper() -> Unit\n  Unit\nend\n",
		},
		want: []string{
			"3:4 main has a `fn main` but is not declared in nomi.toml entry_points; " +
				"add \"main\" to entry_points, or move `fn main` to an entry file or to a tool under tools/",
			"1:1 other is declared in nomi.toml entry_points but has no `fn main`; " +
				"add one, or remove \"other\" from entry_points",
		},
	}, {
		name:  "without a manifest the same project is clean",
		entry: "main",
		files: map[string]string{
			"main":  "import other\n\nfn main()\n  other.helper()\n  Unit\nend\n",
			"other": "pub fn helper() -> Unit\n  Unit\nend\n",
		},
		want: nil,
	}, {
		name:  "a fault in a sibling file is reported",
		entry: "main",
		files: map[string]string{
			"main": "import api\n\nfn main()\n  Unit\nend\n",
			"api":  "pub fn broken() -> Int\n  undefined_name\nend\n",
		},
		want: []string{
			"1:8 imported module 'api' is unused — remove the import",
			"2:3 undefined variable 'undefined_name'",
		},
	}, {
		name:  "a sibling parse error is reported, and alone",
		entry: "main",
		files: map[string]string{
			"main": "import api\n\nfn main()\n  Unit\nend\n",
			"api":  "pub fn broken() -> Int\n",
		},
		want: []string{"1:5 expected `end` to close a function's body"},
	}, {
		name:  "file names may carry .nomi and are normalized",
		entry: "main.nomi",
		files: map[string]string{
			"main.nomi": "import api.{make, secret}\n\nfn main()\n  w = make(\"Ada\")\n  _ = secret(w)\nend\n",
			"api.nomi": "pub struct Widget\n  name: String\nend\n\n" +
				"private fn secret(w: Widget) -> String\n  \"secret \" + w.name\nend\n\n" +
				"pub fn make(name: String) -> Widget\n  Widget{name:}\nend\n",
		},
		want: []string{"1:19 `secret` is private to `api.nomi`\nhelp: drop `private` to name it from the other files of its package, or write `pub` to name it from any package"},
	}, {
		name:  "a single-file project checks against the standard library",
		entry: "main",
		files: map[string]string{
			"main": "fn main()\n  _ = String.trim(\"  x  \")\nend\n",
		},
		want: nil,
	}} {
		t.Run(tc.name, func(t *testing.T) {
			var manifest *string
			if tc.manifest != "" {
				manifest = &tc.manifest
			}
			got := stdcompiler.ProjectSpecDiagnostics(specOf(tc.entry, tc.files, manifest), "")
			var lines []string
			for _, d := range got {
				lines = append(lines, diagText(d))
			}
			if strings.Join(lines, "\n") != strings.Join(tc.want, "\n") {
				t.Errorf("diagnostics\n got:\n  %s\nwant:\n  %s",
					strings.Join(lines, "\n  "), strings.Join(tc.want, "\n  "))
			}
		})
	}

	// `Some("")` is NOT None, and ProjectSpec carries the manifest as a
	// POINTER for exactly this value: a `string` field would make the two
	// indistinguishable and silently give `Some("")` None's behaviour.
	//
	// An empty manifest is not a manifest with no sections, it is INVALID —
	// `[package] name is required (non-empty, non-whitespace)` — so Some
	// answers that one diagnostic where None is clean.
	t.Run("an EMPTY manifest is Some, not None", func(t *testing.T) {
		files := map[string]string{"main": "fn main()\n  Unit\nend\n"}
		got := stdcompiler.ProjectSpecDiagnostics(specOf("main", files, new("")), "")
		want := "0:0 <compiler.Project manifest>: [package] name is required (non-empty, non-whitespace)"
		if len(got) != 1 || diagText(got[0]) != want {
			t.Errorf("Some(\"\") answered %v, want the one diagnostic %q", got, want)
		}
		if none := stdcompiler.ProjectSpecDiagnostics(specOf("main", files, nil), ""); len(none) != 0 {
			t.Errorf("None should be clean, got %d: %v", len(none), none)
		}
	})

	// A project that cannot be assembled is one diagnostic at 0:0, which
	// names no file, not a trap: the project is data.
	t.Run("a missing entry point is a diagnostic", func(t *testing.T) {
		got := stdcompiler.ProjectSpecDiagnostics(
			specOf("absent", map[string]string{"main": "fn main()\n  Unit\nend\n"}, nil), "")
		const want = `0:0 compiler.check_project entry "absent" is not present in files`
		if len(got) != 1 || diagText(got[0]) != want {
			t.Errorf("got %v, want the one diagnostic %q", got, want)
		}
	})
	t.Run("an unparseable manifest is a diagnostic", func(t *testing.T) {
		got := stdcompiler.ProjectSpecDiagnostics(
			specOf("main", map[string]string{"main": "fn main()\n  Unit\nend\n"},
				new("[module\n")), "")
		if len(got) != 1 || got[0].Line != 0 || !strings.HasPrefix(got[0].Message, "parse <compiler.Project manifest>: ") {
			t.Errorf("got %v, want one diagnostic at 0:0 naming the manifest", got)
		}
	})
}

// specOf is a ProjectSpec by field name, so a row reads as three inputs rather
// than as a struct literal repeated ten times.
func specOf(entry string, files map[string]string, manifest *string) stdcompiler.ProjectSpec {
	return stdcompiler.ProjectSpec{EntryPoint: entry, Files: files, Manifest: manifest}
}

func diagText(d rt.Diagnostic) string {
	return strconv.FormatInt(d.Line, 10) + ":" + strconv.FormatInt(d.Col, 10) + " " + d.Message
}
