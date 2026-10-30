package lsp

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nomi-language/nomi/internal/projectgen"
)

// writeTexts writes texts, by module key, into a temporary directory, with
// manifest as its nomi.toml when manifest is not empty.
func writeTexts(tb testing.TB, texts map[string]string, manifest string) string {
	dir := tb.TempDir()
	if manifest != "" {
		if err := os.WriteFile(filepath.Join(dir, "nomi.toml"), []byte(manifest), 0o644); err != nil {
			tb.Fatal(err)
		}
	}
	for key, text := range texts {
		path := filepath.Join(dir, filepath.FromSlash(key)+".nomi")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			tb.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			tb.Fatal(err)
		}
	}
	return dir
}

// BenchmarkLoweringEdit times one lowering run of an edited document,
// every file of its project open, alternating between two texts, as the
// server's run after an edit lowers it (the document's analysis when it
// is the program `nomi check` builds, else its text). The projects are the
// corpus's largest multi-file project by lines and by files, and projects
// internal/projectgen generates at about 2k and 8k lines, which show how
// the run grows with the project. "analyzed/op" is the share of runs that
// lowered the document's analysis: an entry's program, or, for a document
// an entry reaches, the program of the files it reaches, from the entry's
// kept analyses.
// "analyze-ms/op" is the server's analysis of the same edit, untimed, for
// comparison.
func BenchmarkLoweringEdit(b *testing.B) {
	// An edit replaces old with new in the file key, or, when text is set,
	// makes text the file's text.
	type edit struct{ name, key, old, new, text string }
	type project struct {
		name     string
		texts    func(b testing.TB) map[string]string
		edits    []edit
		manifest string
	}
	var projects []project
	for _, dir := range []string{"07-structs-and-enums/struct_spread", "15-app-and-defer/effects"} {
		projects = append(projects, project{name: dir, texts: func(b testing.TB) map[string]string {
			texts := map[string]string{}
			root := filepath.Join("..", "..", "tests", filepath.FromSlash(dir))
			filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
				if err == nil && !d.IsDir() && filepath.Ext(path) == ".nomi" {
					data, _ := os.ReadFile(path)
					rel, _ := filepath.Rel(root, path)
					texts[strings.TrimSuffix(filepath.ToSlash(rel), ".nomi")] = string(data)
				}
				return nil
			})
			return texts
		}})
	}
	for _, lines := range []int{2000, 8000} {
		gen := projectgen.Generate(projectgen.Config{Seed: 1, Lines: lines})
		var edits []edit
		for _, kind := range []projectgen.EditKind{projectgen.BodyEntry, projectgen.BodyLeaf, projectgen.SignatureHub} {
			key, text := gen.Edit(kind, 0)
			edits = append(edits, edit{name: string(kind), key: key, text: text})
		}
		projects = append(projects, project{
			name:     fmt.Sprintf("generated %dk", lines/1000),
			texts:    func(testing.TB) map[string]string { return gen.Files },
			edits:    edits,
			manifest: projectgen.Manifest,
		})
	}
	for _, p := range projects {
		edits := p.edits
		if edits == nil {
			edits = []edit{{name: "entry body"}}
		}
		for _, bench := range edits {
			b.Run(p.name+"/"+bench.name, func(b *testing.B) {
				texts := p.texts(b)
				key := bench.key
				if key == "" {
					for k := range texts {
						if k == "main" || key == "" && strings.HasSuffix(k, "_test") {
							key = k
						}
					}
				}
				lines := 0
				for _, t := range texts {
					lines += strings.Count(t, "\n")
				}
				dir := writeTexts(b, texts, p.manifest)
				edited := strings.Replace(texts[key], bench.old, bench.new, 1)
				switch {
				case bench.text != "":
					edited = bench.text
				case bench.old == "":
					edited = texts[key] + "\n// edited\n"
				}
				if edited == texts[key] {
					b.Fatalf("the edit %s leaves %s as it was", bench.name, key)
				}
				s := NewServer()
				uri := func(key string) string { return pathToURI(filepath.Join(dir, key+".nomi")) }
				for key, text := range texts {
					s.docs.Open(uri(key), text)
				}
				path := filepath.Join(dir, key+".nomi")
				alt := []string{edited, texts[key]}
				s.docs.Update(uri(key), alt[1])
				a, _ := s.loweringAnalysis(uri(key), alt[1])
				loweringDiagnostics(path, alt[1], a)
				analyzed := 0
				var analyzing time.Duration
				b.ResetTimer()
				for i := range b.N {
					text := alt[i%2]
					b.StopTimer()
					start := time.Now()
					s.docs.Update(uri(key), text)
					analyzing += time.Since(start)
					a, _ := s.loweringAnalysis(uri(key), text)
					if a != nil {
						analyzed++
					}
					b.StartTimer()
					release := s.docs.View()
					loweringDiagnostics(path, text, a)
					release()
				}
				b.ReportMetric(float64(lines), "lines")
				b.ReportMetric(float64(analyzing.Microseconds())/1000/float64(b.N), "analyze-ms/op")
				b.ReportMetric(float64(analyzed)/float64(b.N), "analyzed/op")
			})
		}
	}
}
