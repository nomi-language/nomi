package lsp

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nomi-language/nomi/internal/artifactcache"
	"github.com/nomi-language/nomi/internal/fileuri"
)

// appUsesFiles is a project whose functions read app fields: pricing's
// taxed reads `App.tax_rate`, total calls it, and the entry's summary
// calls total, two calls above the read; report reads `App.label` itself,
// calls summary and reaches std::fs; plain uses and reaches nothing.
var appUsesFiles = map[string]string{
	"nomi.toml": "[package]\nname = \"app\"\nentry_points = [\"main\"]\n",
	"app.nomi":  "pub struct App\n    tax_rate: Int\n    label: String\nend\n",
	"pricing.nomi": "import app.App\n\n" +
		"pub fn taxed(n: Int) -> Int\n    n + n * App.tax_rate / 100\nend\n\n" +
		"pub fn total(n: Int) -> Int\n    taxed(n) + 1\nend\n",
	"main.nomi": "import\n    std::fs\n    std::io\n    app.App\n    pricing\nend\n\n" +
		"pub fn boot() -> Result<App, String>\n    Ok(App{tax_rate: 20, label: \"shop\"})\nend\n\n" +
		"fn summary(n: Int) -> String\n    \"${pricing.total(n)}\"\nend\n\n" +
		"fn report(n: Int) -> String\n    notes = fs.read_text(\"notes.txt\") |> Result.with_default(\"\")\n    \"${App.label} ${summary(n)} ${notes}\"\nend\n\n" +
		"fn plain(n: Int) -> Int\n    n * 2\nend\n\n" +
		"fn main()\n    io.print(report(plain(2)))\nend\n",
}

// checkAppUses holds the hovers of appUsesFiles, open in s: a function's
// declaration and each call of it end with the app fields it reads,
// itself or through what it calls, after what it reaches outside the
// machine; one that uses and reaches nothing has neither line. The same
// in pricing.nomi, a file other than the entry.
func checkAppUses(t *testing.T, s *Server, c *rpcClient, dir string) {
	t.Helper()
	main, pricing := appUsesFiles["main.nomi"], appUsesFiles["pricing.nomi"]
	mainURI := fileuri.FromPath(filepath.Join(dir, "main.nomi"))
	pricingURI := fileuri.FromPath(filepath.Join(dir, "pricing.nomi"))
	const taxRate = "Uses `App.tax_rate`."
	const both = "Uses `App.label`, `App.tax_rate`."
	waitReachAt(t, s, c, mainURI, main, "summary(n: Int", taxRate)
	waitReachAt(t, s, c, mainURI, main, "summary(n)}", taxRate)
	waitReachAt(t, s, c, mainURI, main, "report(n: Int", "Has effects: `std::fs`.", both)
	waitReachAt(t, s, c, mainURI, main, "report(plain", "Has effects: `std::fs`.", both)
	waitReachAt(t, s, c, mainURI, main, "main()", "Has effects: `std::fs`, `std::io`.", both)
	waitReachAt(t, s, c, mainURI, main, "plain(n: Int")
	waitReachAt(t, s, c, mainURI, main, "plain(2)")
	waitReachAt(t, s, c, mainURI, main, "boot()")
	waitReachAt(t, s, c, pricingURI, pricing, "total(n: Int", taxRate)
	waitReachAt(t, s, c, pricingURI, pricing, "taxed(n) +", taxRate)
	waitReachAt(t, s, c, pricingURI, pricing, "taxed(n: Int", taxRate)
}

// A hover on a function, or a call of one, says which app fields it uses
// (effects.UsesLine), from the walk that says what it reaches.
func TestAppFieldHover_SaysWhichFieldsAFunctionUses(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the stdlib; -short")
	}
	dir := writeProject(t, cloneFiles(appUsesFiles))
	s, c := startEffectServer(t, dir, nil)
	c.open(fileuri.FromPath(filepath.Join(dir, "main.nomi")), appUsesFiles["main.nomi"])
	c.open(fileuri.FromPath(filepath.Join(dir, "pricing.nomi")), appUsesFiles["pricing.nomi"])
	checkAppUses(t, s, c, dir)
}

// A program opened from its record says which app fields its functions
// use as one checked whole does, in every file.
func TestAppFieldHover_AProgramOpenedFromItsRecord(t *testing.T) {
	if testing.Short() {
		t.Skip("lowers the stdlib; -short")
	}
	t.Setenv(artifactcache.RootEnv, t.TempDir())
	dir := writeProject(t, cloneFiles(appUsesFiles))
	mainURI := fileuri.FromPath(filepath.Join(dir, "main.nomi"))

	first := NewServer()
	first.lowering.delay = 10 * time.Millisecond
	c1 := startRPC(t, first, dir)
	c1.open(mainURI, appUsesFiles["main.nomi"])
	waitStored(t, first, 1)

	s, c := startEffectServer(t, dir, nil)
	c.open(mainURI, appUsesFiles["main.nomi"])
	c.open(fileuri.FromPath(filepath.Join(dir, "pricing.nomi")), appUsesFiles["pricing.nomi"])
	checkAppUses(t, s, c, dir)
	if opened, _ := s.docs.StoredPrograms(); opened != 1 {
		t.Fatalf("the server opened %d programs from records, want 1", opened)
	}
}

// cloneFiles is a copy of files, which writeProject may add to.
func cloneFiles(files map[string]string) map[string]string {
	out := make(map[string]string, len(files))
	for name, text := range files {
		out[name] = strings.Clone(text)
	}
	return out
}
