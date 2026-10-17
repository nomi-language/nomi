package lsp

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

const dungeonDir = "/Users/davidbaldwin/Code/nomi/dungeon"

// dungeonTexts reads the dungeon's .nomi files by module key, or skips tb.
func dungeonTexts(tb testing.TB) map[string]string {
	tb.Helper()
	entries, err := os.ReadDir(dungeonDir)
	if err != nil {
		tb.Skipf("no dungeon project at %s", dungeonDir)
	}
	texts := map[string]string{}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".nomi") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dungeonDir, e.Name()))
		if err != nil {
			tb.Fatal(err)
		}
		texts[strings.TrimSuffix(e.Name(), ".nomi")] = string(data)
	}
	return texts
}

var dungeonImport = regexp.MustCompile(`\b(command|game|world)\.`)

// scaledDungeon is the dungeon with its command, game and world files
// copied k times (command0 ... world{k-1}), each copy importing its own,
// and a main.nomi that is the dungeon's entry over copy 0 plus a call into
// every other copy's game.
func scaledDungeon(tb testing.TB, k int) map[string]string {
	src := dungeonTexts(tb)
	out := map[string]string{}
	var calls strings.Builder
	var imports strings.Builder
	for i := range k {
		for _, key := range []string{"command", "game", "world"} {
			text := src[key]
			head, body, _ := strings.Cut(text, "\n}\n")
			if !strings.Contains(head, "import") {
				head, body = "", text
			} else {
				head += "\n}\n"
			}
			if strings.HasPrefix(text, "import world") {
				line, rest, _ := strings.Cut(text, "\n")
				head, body = line+"\n", rest
			}
			head = dungeonImport.ReplaceAllString(head, fmt.Sprintf("${1}%d.", i))
			text = head + body
			if key == "game" {
				text += "\npub fn demo() -> Game {\n    Game.start()\n}\n"
			}
			out[fmt.Sprintf("%s%d", key, i)] = text
		}
		if i > 0 {
			fmt.Fprintf(&imports, "    game%d\n", i)
			fmt.Fprintf(&calls, "    _ = game%d.demo()\n", i)
		}
	}
	main := dungeonImport.ReplaceAllString(src["main"], "${1}0.")
	main = strings.Replace(main, "    std/io\n", "    std/io\n"+imports.String(), 1)
	main = strings.Replace(main, "fn main() {\n", "fn main() {\n"+calls.String(), 1)
	out["main"] = main
	return out
}

// writeTexts writes texts, by module key, into a temporary directory.
func writeTexts(tb testing.TB, texts map[string]string) string {
	dir := tb.TempDir()
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
// dungeon (dungeonDir; skipped when it is absent), the dungeon copied 4 and
// 16 times (scaledDungeon), and the corpus's largest multi-file project by
// lines and by files. "analyzed/op" is the share of runs that lowered the
// document's analysis: an entry's program, or, for a document an entry
// reaches, the program of the files it reaches, from the entry's kept
// analyses.
// "analyze-ms/op" is the server's analysis of the same edit, untimed, for
// comparison.
func BenchmarkLoweringEdit(b *testing.B) {
	type edit struct{ name, key, old, new string }
	dungeonEdits := []edit{
		{"game body", "game", "{ 3 } else { 1 }", "{ 4 } else { 1 }"},
		{"entry body", "main", "You wake up in the dark.", "You wake up."},
		{"game signature", "game", "fn strength(game: Game) -> Int {", "fn strength(game: Game, _bonus: Int = 0) -> Int {"},
		{"world body", "world", `.North -> "north"`, `.North -> "the north"`},
	}
	type project struct {
		name  string
		texts func(b testing.TB) map[string]string
		edits []edit
	}
	projects := []project{{"dungeon", dungeonTexts, dungeonEdits}}
	for _, k := range []int{4, 16} {
		projects = append(projects, project{fmt.Sprintf("dungeon x%d", k), func(b testing.TB) map[string]string { return scaledDungeon(b, k) }, []edit{
			{"entry body", "main", "You wake up in the dark.", "You wake up."},
		}})
	}
	for _, dir := range []string{"07-structs-and-enums/struct_spread", "15-app-and-defer/effects"} {
		projects = append(projects, project{dir, func(b testing.TB) map[string]string {
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
		}, nil})
	}
	for _, p := range projects {
		edits := p.edits
		if edits == nil {
			edits = []edit{{"entry body", "", "", ""}}
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
				dir := writeTexts(b, texts)
				edited := strings.Replace(texts[key], bench.old, bench.new, 1)
				if bench.old == "" {
					edited = texts[key] + "\n// edited\n"
				}
				if edited == texts[key] {
					b.Skipf("%q is not in %s", bench.old, key)
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
