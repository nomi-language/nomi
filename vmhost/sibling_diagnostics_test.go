package vmhost_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmhost"
)

// A NON-ENTRY FILE'S CHECKER DIAGNOSTICS ARE PART OF THE PROGRAM.
//
// The front end runs `CheckTypes` on every sibling module because the
// recordings it produces feed the entry's ImplManifest, and a sibling's
// diagnostics fail the program: a sibling file is compiled and run, and
// nothing else ever checks it. Measured at `4772329c`, when the result was
// discarded, all three of these passed `nomi check` and RAN:
//
//	pub fn report(): String { 42 }            in a sibling
//	Svc.missing                                in a sibling, naming no app field
//	Nonexistent.context                       in a sibling, naming nothing at all
//
// An app read names its type (`Svc.context`), so the sibling imports `Svc`
// like any other name it uses.
//
// Each case below is paired with a positive: the same program with the mistake
// corrected must still load, or a blanket "reject siblings" would pass these.
func TestAnalyze_SiblingFileTypeErrorFailsTheProgram(t *testing.T) {
	for _, c := range []struct {
		name    string
		sibling string
		want    string
	}{
		{
			name:    "an ordinary return-type mismatch",
			sibling: "pub fn report(): String {\n  42\n}\n",
			want:    "return type mismatch",
		},
		{
			name:    "an app read naming no app field",
			sibling: "import svc.Svc\n\npub fn report(): String {\n  Svc.missing\n}\n",
			want:    "type 'Svc' has no member 'missing'",
		},
		{
			name:    "a qualifier that names nothing at all",
			sibling: "\npub fn report(): String {\n  case Context.deadline_remaining(Nowhere.context) {\n    Some(_) -> \"bounded\"\n    None -> \"unbounded\"\n  }\n}\n",
			want:    "undefined type or variant 'Nowhere'",
		},
	} {
		c := c
		t.Run(c.name, func(t *testing.T) {
			err := loadAppProjectWithLeaf(t, c.sibling)
			if err == nil {
				t.Fatalf("a sibling file's %s passed project analysis", c.want)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("rejected for some other reason; want %q, got: %v", c.want, err)
			}
			if !strings.Contains(err.Error(), "leaf.nomi:") {
				t.Errorf("the diagnostic does not name the file it is about, so it reads as an entry-file error: %v", err)
			}
		})
	}
}

// The positive for the case above: a sibling reading the app's context
// through the imported type.
func TestAnalyze_SiblingAppFieldReadLoads(t *testing.T) {
	if err := loadAppProjectWithLeaf(t, "import svc.Svc\n\npub fn report(): String {\n  case Context.deadline_remaining(Svc.context) {\n    Some(_) -> \"bounded\"\n    None -> \"unbounded\"\n  }\n}\n"); err != nil {
		t.Fatalf("a correct sibling app-field read was rejected: %v", err)
	}
}

// loadAppProjectWithLeaf builds a three-file project — `svc.nomi` declaring
// the app's payload struct, `main.nomi` with `boot` and `main`, plus
// `leaf.nomi` supplied by the caller — and returns whatever LoadSource says
// about it. That is the layout of `tests/15-app-and-defer/effects/`.
func loadAppProjectWithLeaf(t *testing.T, leaf string) error {
	t.Helper()
	src := strings.Join([]string{
		"// FILE: svc.nomi",
		"pub struct Svc {",
		"  context: Context",
		"  label: String = \"svc\"",
		"}",
		"",
		"// FILE: leaf.nomi",
		strings.TrimRight(leaf, "\n"),
		"",
		"// FILE: main.nomi",
		"import {",
		"",
		"  std/io",
		"  svc.Svc",
		"  leaf",
		"}",
		"",
		"fn boot(): Svc {",
		"  Svc{context: Context.root(), }",
		"}",
		"",
		"fn main() {",
		"  io.print(leaf.report())",
		"}",
		"",
		"// FILE: nomi.toml",
		"[module]",
		`name = "cxd"`,
		`entry_points = ["main"]`,
	}, "\n")
	entrySrc, entryName, virtualFiles, manifest, err := vmhost.SplitMultiFile(src)
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	_, err = vmhost.LoadSource(entryName, entrySrc,
		vmhost.WithVirtualFiles(virtualFiles),
		vmhost.WithVirtualManifest(manifest),
	)
	return err
}
