package lsp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/fileuri"
	"github.com/nomi-language/nomi/vmhost"
)

// A refused call in a dependency's file is shown in the project's entry at
// each of the project's calls that leads there, not at the top of the file:
// each mark headed by what the project's code reaches there, then the
// refusal, with the same code and the dependency's call as its
// relatedInformation. The quick fix on a mark edits nomi.toml, and with it
// applied every mark clears, since they are one refusal.
func TestEffectLead_MarksTheProjectsCalls(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the stdlib; -short")
	}
	main := "import\n    std::io\n    pricing::rules\nend\n\nfn main()\n    io.print(rules.load(\"a.txt\"))\n    io.print(rules.load(\"b.txt\"))\nend\n"
	manifest := "[module]\nname = \"app\"\nentry_points = [\"main\"]\n\n[effects.self]\nstd = [\"std::io\"]\n\n" +
		"[effects.deps]\n\n[effects.deps.modules]\n\"pricing\" = []\n"
	dir := writeProject(t, map[string]string{
		"app/nomi.toml":      manifest + "\n[dependencies]\npricing = { dir = \"../pricing\" }\n",
		"app/main.nomi":      main,
		"pricing/nomi.toml":  "[module]\nname = \"pricing\"\n",
		"pricing/rules.nomi": "import std::fs\n\npub fn load(path: String) -> String\n    fs.read_text(path) |> Result.with_default(\"\")\nend\n",
	})
	path := filepath.Join(dir, "app", "main.nomi")
	found, err := vmhost.CheckLowering(context.Background(), path, main)
	diags, fixes := problemDiagnosticsAndFixes(path, main, found.Problems, err)
	if len(diags) != 2 {
		t.Fatalf("lowering diagnostics %s; want one at each call of load", renderDiagnostics(diags))
	}
	head := "your code has the effect std::fs in pricing here: `main` calls `load`\n" +
		"pricing has the effect std::fs, and its entry under [effects.deps.modules] allows it no effects\n"
	rules := fileuri.FromPath(filepath.Join(dir, "pricing", "rules.nomi"))
	for i, d := range diags {
		line := uint32(6 + i)
		if d.Range.Start.Line != line || d.Range.Start.Character != 13 || d.Range.End.Character != 23 {
			t.Errorf("mark %d at %+v; want line %d, the callee rules.load", i, d.Range, line+1)
		}
		if d.Code == nil || d.Code.Value != vmhost.EffectNotAllowedCode || !strings.HasPrefix(d.Message, head) {
			t.Errorf("mark %d is %v: %q; want %s headed\n%s", i, d.Code, d.Message, vmhost.EffectNotAllowedCode, head)
		}
		if len(d.RelatedInformation) != 1 || string(d.RelatedInformation[0].Location.URI) != rules ||
			d.RelatedInformation[0].Location.Range.Start.Line != 3 {
			t.Errorf("mark %d's relatedInformation %+v; want the call in %s at line 4", i, d.RelatedInformation, rules)
		}
	}
	if len(fixes) != 2 {
		t.Fatalf("fixes %+v; want one on each mark", fixes)
	}
	actions := buildParseFixActions(main, fileuri.FromPath(path), diskContent, fixes, diags[1:])
	manifestPath := filepath.Join(dir, "app", "nomi.toml")
	if len(actions) != 1 || actions[0].Edit == nil || len(actions[0].Edit.Changes[fileuri.FromPath(manifestPath)]) != 1 {
		t.Fatalf("code actions on the second mark %+v; want one edit of nomi.toml", actions)
	}
	fixed := applyFixEdit(t, manifest, fixes[1].Fix.Edits[0])
	if err := os.WriteFile(manifestPath, []byte(fixed), 0o644); err != nil {
		t.Fatal(err)
	}
	if diags := loweringDiagnostics(path, main, nil); len(diags) != 0 {
		t.Errorf("with the fix applied, the lowering reports %s", renderDiagnostics(diags))
	}
}
