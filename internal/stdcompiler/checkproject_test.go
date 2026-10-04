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
// three states of `Maybe<Toml>`.
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
			"main": "import api.{make, secret}\n\nfn main(): String {\n  w = make(\"Ada\")\n  secret(w)\n}\n",
			"api": "pub struct Widget {\n  name: String\n}\n\npub fn label(w: Widget): String {\n  w.name\n}\n\n" +
				"fn secret(w: Widget): String {\n  \"secret \" + w.name\n}\n\n" +
				"pub fn make(name: String): Widget {\n  Widget{name}\n}\n",
		},
		want: []string{"1:1 'secret' is private and cannot be imported"},
	}, {
		name:     "a manifest produces the entry-point diagnostics, both directions",
		entry:    "main",
		manifest: "[module]\nname = \"demo\"\nentry_points = [\"other\"]\n",
		files: map[string]string{
			"main":  "import other\n\nfn main() {\n  other.helper()\n  Unit\n}\n",
			"other": "pub fn helper(): Unit {\n  Unit\n}\n",
		},
		want: []string{
			"3:4 entry callback in main but file is not declared in nomi.toml entry_points — " +
				"add \"main\" to entry_points or move the entry callback to an entry file",
			"1:1 other is declared in nomi.toml entry_points but has no entry callback — " +
				"add `fn main() { ... }`, or remove \"other\" from entry_points",
		},
	}, {
		name:  "without a manifest the same project is clean",
		entry: "main",
		files: map[string]string{
			"main":  "import other\n\nfn main() {\n  other.helper()\n  Unit\n}\n",
			"other": "pub fn helper(): Unit {\n  Unit\n}\n",
		},
		want: nil,
	}, {
		name:  "a fault in a sibling file is reported",
		entry: "main",
		files: map[string]string{
			"main": "import api\n\nfn main() {\n  Unit\n}\n",
			"api":  "pub fn broken(): Int {\n  undefined_name\n}\n",
		},
		want: []string{
			"1:8 imported module 'api' is unused — remove the import",
			"2:3 undefined variable 'undefined_name'",
		},
	}, {
		name:  "a sibling parse error is reported, and alone",
		entry: "main",
		files: map[string]string{
			"main": "import api\n\nfn main() {\n  Unit\n}\n",
			"api":  "pub fn broken(): Int {\n",
		},
		want: []string{"1:22 expected '}' to close block"},
	}, {
		name:  "file names may carry .nomi and are normalized",
		entry: "main.nomi",
		files: map[string]string{
			"main.nomi": "import api.{make, secret}\n\nfn main(): String {\n  w = make(\"Ada\")\n  secret(w)\n}\n",
			"api.nomi": "pub struct Widget {\n  name: String\n}\n\n" +
				"fn secret(w: Widget): String {\n  \"secret \" + w.name\n}\n\n" +
				"pub fn make(name: String): Widget {\n  Widget{name}\n}\n",
		},
		want: []string{"1:1 'secret' is private and cannot be imported"},
	}, {
		name:  "a single-file project checks against the standard library",
		entry: "main",
		files: map[string]string{
			"main": "fn main(): String {\n  String.trim(\"  x  \")\n}\n",
		},
		want: nil,
	}} {
		t.Run(tc.name, func(t *testing.T) {
			var manifest *string
			if tc.manifest != "" {
				manifest = &tc.manifest
			}
			got, err := stdcompiler.ProjectSpecDiagnostics(specOf(tc.entry, tc.files, manifest), "")
			if err != nil {
				t.Fatal(err)
			}
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

	// `Some(Toml"")` is NOT None, and ProjectSpec carries the manifest as a
	// POINTER for exactly this value: a `string` field would make the two
	// indistinguishable and silently give `Some(Toml"")` None's behaviour.
	//
	// The discriminator turned out sharper than expected and is recorded as
	// MEASURED rather than as designed: an empty manifest is not a manifest
	// with no sections, it is INVALID — `[module] name is required (non-empty,
	// non-whitespace)` — so Some errors where None is clean. Two different
	// answers, one of which is not even a diagnostic list.
	t.Run("an EMPTY manifest is Some, not None", func(t *testing.T) {
		files := map[string]string{"main": "fn main() {\n  Unit\n}\n"}
		if _, err := stdcompiler.ProjectSpecDiagnostics(specOf("main", files, new("")), ""); err == nil {
			t.Error("Some(Toml\"\") produced no error, so it was treated as None and the " +
				"pointer in ProjectSpec is doing nothing")
		} else if want := "<compiler.Project manifest>: [module] name is required " +
			"(non-empty, non-whitespace)"; err.Error() != want {
			t.Errorf("error text\n got %q\nwant %q", err.Error(), want)
		}
		none, err := stdcompiler.ProjectSpecDiagnostics(specOf("main", files, nil), "")
		if err != nil {
			t.Fatalf("None: %v", err)
		}
		if len(none) != 0 {
			t.Errorf("None should be clean, got %d: %v", len(none), none)
		}
	})

	// The two fault paths, both of which have nowhere to go as data because std
	// declares no error channel: the `compiler.check_project` host traps with
	// this error's text.
	t.Run("a missing entry point is an error", func(t *testing.T) {
		_, err := stdcompiler.ProjectSpecDiagnostics(
			specOf("absent", map[string]string{"main": "fn main() {\n  Unit\n}\n"}, nil), "")
		if err == nil {
			t.Fatal("want an error for an entry point absent from files")
		}
		const want = `compiler.check_project entry "absent" is not present in files`
		if err.Error() != want {
			t.Errorf("error text\n got %q\nwant %q", err.Error(), want)
		}
	})
	t.Run("an unparseable manifest is an error", func(t *testing.T) {
		_, err := stdcompiler.ProjectSpecDiagnostics(
			specOf("main", map[string]string{"main": "fn main() {\n  Unit\n}\n"},
				new("[module\n")), "")
		if err == nil {
			t.Fatal("want an error for a manifest that is not TOML")
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
