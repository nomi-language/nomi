package vmhost

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// leadFiles is dependencyFiles with the dependency held to an entry that
// allows nothing, so its read of a file is refused.
func leadFiles() map[string]string {
	return withEntry(dependencyFiles(effectManifest+"\n[effects.dependencies]\n", ""), "effects = []")
}

// refusalOf loads app/main.nomi from a project of files, holds it to its
// lists as `nomi check` does, and answers the refusal, with the project's
// directory.
func refusalOf(t *testing.T, files map[string]string) (*EffectError, string) {
	t.Helper()
	dir := writeProject(t, files)
	p, err := Load(filepath.Join(dir, "app", "main.nomi"))
	if err != nil {
		t.Fatalf("the program does not load: %v", err)
	}
	var refused *EffectError
	if err := p.effectError(p.checkedRoots()...); !errors.As(err, &refused) {
		t.Fatalf("the program is held to its lists and gives %v; want a refusal", err)
	}
	return refused, dir
}

// leadsOf is each related location of d, a refusal, as
// `file:line:col: message` with the project's directory dir left out.
func leadsOf(d Diagnostic, dir string) []string {
	var out []string
	for _, r := range d.Related {
		path := strings.TrimPrefix(r.Path, dir+string(filepath.Separator))
		out = append(out, fmt.Sprintf("%s:%d:%d-%d:%d: %s", filepath.ToSlash(path), r.Line, r.Col, r.EndLine, r.EndCol, r.Message))
	}
	return out
}

// A refused call in a dependency's file names, as its related locations,
// each place the project's own code last steps toward it, whatever the
// kind of edge, worded as the chain's steps are: a direct call, a call two
// functions deep in the project (only the last one), and a function used
// as a value.
func TestEffectLead_TheProjectsLastStep(t *testing.T) {
	cases := []struct {
		name, main string
		want       []string
	}{{
		name: "a direct call",
		main: "import\n    std::io\n    pricing::rules\nend\n\nfn main()\n    io.print(rules.load(\"prices.txt\"))\nend\n",
		want: []string{"app/main.nomi:7:14-7:24: your code has the effect std::fs in pricing here: `main` calls `load`"},
	}, {
		name: "two functions deep",
		main: "import\n    std::io\n    pricing::rules\nend\n\nfn prices() -> String\n    rules.load(\"prices.txt\")\nend\n\n" +
			"fn report() -> String\n    \"Prices: ${prices()}\"\nend\n\nfn main()\n    io.print(report())\nend\n",
		want: []string{"app/main.nomi:7:5-7:15: your code has the effect std::fs in pricing here: `prices` calls `load`"},
	}, {
		name: "a function value",
		main: "import\n    std::io\n    pricing::rules\nend\n\nfn main()\n    read = rules.load\n    io.print(read(\"prices.txt\"))\nend\n",
		want: []string{"app/main.nomi:7:17-7:22: your code has the effect std::fs in pricing here: `main` uses `load` as a value, which counts whether or not it is called"},
	}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			files := leadFiles()
			files["app/main.nomi"] = c.main
			refused, dir := refusalOf(t, files)
			if len(refused.Diagnostics) != 1 {
				t.Fatalf("%d refusals; want one:\n%v", len(refused.Diagnostics), refused.Diagnostics)
			}
			d := refused.Diagnostics[0]
			if !strings.HasSuffix(filepath.ToSlash(d.Path), "pricing/rules.nomi") || d.Code != EffectNotAllowedCode {
				t.Errorf("the refusal is %s at %s; want %s in pricing/rules.nomi", d.Code, d.Path, EffectNotAllowedCode)
			}
			if got := leadsOf(d, dir); strings.Join(got, "\n") != strings.Join(c.want, "\n") {
				t.Errorf("related locations:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(c.want, "\n"))
			}
		})
	}
}

// When several of the project's calls lead to one refused call, the
// refusal holds each, in position order, and its text names the first and
// counts the rest, all of them with --all-calls.
func TestEffectLead_SeveralCalls(t *testing.T) {
	files := leadFiles()
	files["app/main.nomi"] = "import\n    std::io\n    pricing::rules\nend\n\nfn main()\n    io.print(rules.load(\"a.txt\"))\n" +
		"    io.print(rules.load(\"b.txt\"))\n    io.print(rules.load(\"c.txt\"))\nend\n"
	refused, dir := refusalOf(t, files)
	if len(refused.Diagnostics) != 1 {
		t.Fatalf("%d refusals; want one", len(refused.Diagnostics))
	}
	at := func(line int) string {
		return fmt.Sprintf("app/main.nomi:%d:14-%d:24: your code has the effect std::fs in pricing here: `main` calls `load`", line, line)
	}
	want := []string{at(7), at(8), at(9)}
	if got := leadsOf(refused.Diagnostics[0], dir); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("related locations:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	text := inProject(refused.Error(), dir)
	note := "app/main.nomi:7:14: note: your code has the effect std::fs in pricing here: `main` calls `load`; and 2 more places in your code; --all-calls lists them"
	if !strings.HasSuffix(text, "\n"+note) || strings.Count(text, "note:") != 1 {
		t.Errorf("the text:\n%s\nwant one note, last:\n%s", text, note)
	}
	t.Setenv(AllCallsEnv, "1")
	if n := strings.Count(refused.Error(), "note:"); n != 3 {
		t.Errorf("with %s the text has %d notes; want 3:\n%s", AllCallsEnv, n, refused.Error())
	}
}

// A refused call in the project's own file has no related location: the
// refusal is already where the project's author reads it.
func TestEffectLead_NoneInTheProjectsOwnFile(t *testing.T) {
	_, ds := effectDiagnostics(t, "main.nomi", map[string]string{
		"nomi.toml": effectManifest,
		"main.nomi": "import\n    std::fs\n    std::io\nend\n\nfn main()\n    io.print(fs.read_text(\"notes.txt\"))\nend\n",
	})
	if len(ds) != 1 || len(ds[0].Related) != 0 {
		t.Errorf("diagnostics %+v; want one with no related location", ds)
	}
}
