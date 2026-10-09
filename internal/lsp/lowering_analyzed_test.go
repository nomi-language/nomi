package lsp

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/nomi-language/nomi/internal/analyzedlowering"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// loweringProbe is appended to a corpus file to give its lowering a
// blocker: `lowering_probe_skip` uses `continue` outside a loop, which the
// front end accepts and the IR builder declines when it is called from a
// lambda (loweringBlocked). The test reaches it, and makes the file a test
// file.
const loweringProbe = `

fn lowering_probe_skip(n: Int): Int {
  if n % 2 == 1 {
    continue
  }
  n
}

fn lowering_probe(xs: List<Int>): List<Int> {
  Iter.map(xs, |x| lowering_probe_skip(x)) |> Iter.to_list()
}

test "lowering probe" {
  assert lowering_probe([1, 2]) == [2]
}
`

// loweringCaptureProbe is appended to a corpus file to give its lowering
// a call of the generic std function `io.capture`, which the builder
// instantiates for the program, and an invalid backtick typed literal,
// which the lowering check reports (vmhost.Program.checkLiterals).
const loweringCaptureProbe = `

test "lowering capture probe" {
  import std/io
  import std/regex.Regex
  run = io.capture("", || io.print("x"))
  assert run.output == "x\n"
  _ = Regex` + "`[`" + `
}
`

// loweringVariants are the texts of a corpus file the equivalence test
// lowers: the file, the file with loweringProbe or loweringCaptureProbe
// appended, and the file with one line in its middle removed (which
// usually leaves it with errors, so the source path answers it).
func loweringVariants(src string) map[string]string {
	lines := strings.Split(src, "\n")
	mid := len(lines) / 2
	cut := strings.Join(append(append([]string(nil), lines[:mid]...), lines[mid+1:]...), "\n")
	return map[string]string{
		"as written":         src,
		"with a blocker":     src + loweringProbe,
		"with a capture":     src + loweringCaptureProbe,
		"without its middle": cut,
	}
}

var loweringVariantNames = []string{"as written", "with a blocker", "with a capture", "without its middle"}

// Lowering a document from its analysis (analyzedlowering.Check)
// reports what lowering its text with its own front end
// (vmhost.CheckLowering) reports, diagnostic for diagnostic: message,
// range and code. Every corpus file is checked in one of loweringVariants,
// the k-th file in the k-th variant (mod 4), which takes about 12 seconds;
// NOMI_LOWERING_EQUIVALENCE=full checks every file in every variant,
// about 45 seconds.
func TestLoweringFromAnalysisMatchesTheSource(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the corpus; -short")
	}
	root, err := filepath.Abs(filepath.Join("..", "..", "tests"))
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	if err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(path, ".nomi") {
			paths = append(paths, path)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	sort.Strings(paths)
	s := NewServer()
	// Per variant: texts, texts lowered from their analysis, those with
	// lowering diagnostics, and those whose program has other project
	// files.
	total, analyzed, blocked, several := map[string]int{}, map[string]int{}, map[string]int{}, map[string]int{}
	full := os.Getenv("NOMI_LOWERING_EQUIVALENCE") == "full"
	for k, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		uri := pathToURI(path)
		variants := loweringVariants(string(data))
		names := loweringVariantNames
		if !full {
			names = names[k%len(names) : k%len(names)+1]
		}
		for _, name := range names {
			text := variants[name]
			s.docs.Open(uri, text)
			a, latest := s.loweringAnalysis(uri, text)
			if !latest {
				t.Fatalf("%s %s: the opened text is not the document's latest", path, name)
			}
			total[name]++
			want := loweringDiagnostics(path, text, nil)
			if a == nil {
				continue
			}
			if _, ok, _ := analyzedlowering.Check(path, text, a); !ok {
				continue
			}
			analyzed[name]++
			if len(a.Files) > 0 {
				several[name]++
			}
			got := loweringDiagnostics(path, text, a)
			if len(want) > 0 {
				blocked[name]++
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("%s %s: from the analysis\n%s\nfrom the source\n%s", path, name, renderDiagnostics(got), renderDiagnostics(want))
			}
		}
		s.docs.Close(uri)
	}
	for _, name := range loweringVariantNames {
		t.Logf("%s: %d texts, %d lowered from their analysis (%d of several files), %d of those with lowering diagnostics", name, total[name], analyzed[name], several[name], blocked[name])
	}
	// Most corpus files are programs of one file, which the analysis
	// answers; the probes give most of them a lowering diagnostic.
	for _, name := range []string{"as written", "with a blocker", "with a capture"} {
		if analyzed[name] < total[name]/2 {
			t.Errorf("%s: only %d of %d texts lowered from their analysis", name, analyzed[name], total[name])
		}
	}
	for _, name := range []string{"with a blocker", "with a capture"} {
		if blocked[name] < analyzed[name]/2 {
			t.Errorf("%s: only %d of %d texts lowered from their analysis have lowering diagnostics", name, blocked[name], analyzed[name])
		}
	}
}

func renderDiagnostics(ds []protocol.Diagnostic) string {
	var b strings.Builder
	for _, d := range ds {
		code := ""
		if d.Code != nil {
			code = fmt.Sprint(d.Code.Value)
		}
		fmt.Fprintf(&b, "  %d:%d-%d:%d [%s] %q\n", d.Range.Start.Line, d.Range.Start.Character, d.Range.End.Line, d.Range.End.Character, code, d.Message)
	}
	if b.Len() == 0 {
		return "  (none)\n"
	}
	return b.String()
}

// recordLoweringInput replaces the lowering with one that records, per
// text, the analysis each run was handed.
func recordLoweringInput(t *testing.T) func(text string) (*analyzedlowering.Analyzed, bool) {
	t.Helper()
	var mu sync.Mutex
	got := map[string]*analyzedlowering.Analyzed{}
	saved := checkLoweringFn
	checkLoweringFn = func(path, src string, a *analyzedlowering.Analyzed) (error, error) {
		mu.Lock()
		got[src] = a
		mu.Unlock()
		return saved(path, src, a)
	}
	t.Cleanup(func() { checkLoweringFn = saved })
	return func(text string) (*analyzedlowering.Analyzed, bool) {
		mu.Lock()
		defer mu.Unlock()
		a, ok := got[text]
		return a, ok
	}
}

// The run an open starts waits for the document's analysis and lowers it,
// rather than the text: the program is main.nomi alone.
func TestLoweringRun_LowersTheDocumentsAnalysis(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the stdlib; -short")
	}
	input := recordLoweringInput(t)
	s, uri, _, published := openLoweringDoc(t, loweringBlocked)
	awaitPublish(t, published, "with the lowering diagnostic", func(d []protocol.Diagnostic) bool {
		return len(notSupported(d)) == 1
	})
	a, ran := input(loweringBlocked)
	if !ran {
		t.Fatal("no run lowered the opened text")
	}
	snap := s.docs.Snapshot(uri)
	if a == nil || a.FA != snap.Analysis || snap.Program == nil || &a.Nodes[0] != &snap.Program.Nodes[0] {
		t.Fatalf("the run lowered %+v; want the document's installed analysis", a)
	}
}

// A document whose program has another project file is lowered from its
// analysis, which checked that file as the front end does
// (lowering_multifile_test.go compares the two).
func TestLoweringRun_ProgramOfSeveralFilesLowersTheAnalysis(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the stdlib; -short")
	}
	src := "import std/io\nimport shapes\n\nfn main() {\n    io.inspect(shapes.area(2))\n}\n"
	dir := writeProject(t, map[string]string{
		"main.nomi":   src,
		"shapes.nomi": "pub fn area(r: Int): Int {\n    r * 3\n}\n",
		"nomi.toml":   "[module]\nname = \"app\"\nentry_points = [\"main\"]\n",
	})
	s := NewServer()
	uri := pathToURI(filepath.Join(dir, "main.nomi"))
	s.docs.Open(uri, src)
	snap := s.docs.Snapshot(uri)
	if snap.Analysis == nil || len(snap.Analysis.TypeErrors) > 0 || len(snap.Errors) > 0 {
		t.Fatalf("the document does not analyze cleanly: %+v %+v", snap.Errors, snap.Analysis)
	}
	if snap.Program == nil || len(snap.Program.Files) != 1 || snap.Program.Files[0].Key != "shapes" {
		t.Fatalf("the analysis offers %+v; want main.nomi's program with shapes.nomi", snap.Program)
	}
	a, latest := s.loweringAnalysis(uri, src)
	if a == nil || !latest || a.FA != snap.Analysis || len(a.Files) != 1 {
		t.Fatalf("loweringAnalysis = %+v, %v; want the document's program", a, latest)
	}
	if _, ok, err := analyzedlowering.Check(filepath.Join(dir, "main.nomi"), src, a); !ok || err != nil {
		t.Fatalf("lowered from the analysis: %v, %v", ok, err)
	}
}
