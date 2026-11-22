package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// dbgProject is an app whose module calc is calcSource, declaring
// double, and whose nomi.toml gives calc the entry calcEntry under
// [effects.self.modules].
func dbgProject(t *testing.T, calcSource, calcEntry string) string {
	t.Helper()
	app := t.TempDir()
	mustWrite(t, filepath.Join(app, "nomi.toml"), effectsModule+"\n[effects.self]\nstd = [\"std::io\"]\n\n[effects.self.modules]\n\"calc\" = "+calcEntry+"\n\"main\" = [\"std::io\"]\n")
	mustWrite(t, filepath.Join(app, "main.nomi"), `import
    std::io
    calc
end

fn main()
    io.print("${calc.double(2)}")
end
`)
	mustWrite(t, filepath.Join(app, "calc.nomi"), calcSource)
	return app
}

// A `dbg` is an effect no list names: a module whose entry is empty may
// hold one, and check and run pass, the run printing the dbg's line. The
// same module printing with io.print is refused as before. `nomi effects
// --write` tightens a looser entry to the empty one, since the dbg needs
// no name, and --tree shows the dbg as needing no entry.
func TestEffects_ADbgNeedsNoEntry(t *testing.T) {
	const debugged = "pub fn double(n: Int) -> Int\n    n * 2 |> dbg\nend\n"
	app := dbgProject(t, debugged, "[]")
	if check := runNomiIn(t, app, "off", "check", "."); check.exit != 0 {
		t.Fatalf("nomi check refuses a dbg in a module whose entry is empty:\n%s", check)
	}
	run := runNomiIn(t, app, "off", "run", "main.nomi")
	if run.exit != 0 || !strings.Contains(run.stdout, "4\n") || !strings.Contains(run.stdout+run.stderr, "dbg line") {
		t.Fatalf("nomi run does not run the dbg:\n%s", run)
	}

	// The lists pass, and the build still refuses the dbg.
	bin := exe(filepath.Join(t.TempDir(), "app"))
	build := runNomiIn(t, app, "off", "build", "main.nomi", "-o", bin)
	if want := "nomi build: 1 dbg remains:\n  calc.nomi:2:14 |> dbg\n"; build.exit == 0 || build.stderr != want {
		t.Errorf("nomi build of a program with a dbg its lists pass:\n%s\nwant stderr:\n%s", build, want)
	}

	tree := runNomiIn(t, app, "off", "effects", "--tree")
	if tree.exit != 0 || !strings.Contains(tree.stdout, "dbg  needs no entry") || strings.Contains(tree.stdout, "refused") {
		t.Errorf("nomi effects --tree does not show the dbg as needing no entry:\n%s", tree)
	}

	loose := dbgProject(t, debugged, `["std::io"]`)
	if write := runNomiIn(t, loose, "off", "effects", "--write"); write.exit != 0 {
		t.Fatalf("nomi effects --write:\n%s", write)
	}
	data, err := os.ReadFile(filepath.Join(loose, "nomi.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "\"calc\" = []\n") || !strings.Contains(string(data), "std = [\"std::io\"]\n") {
		t.Errorf("nomi effects --write does not write calc's entry empty:\n%s", data)
	}

	printing := dbgProject(t, "import std::io\n\npub fn double(n: Int) -> Int\n    io.print(\"doubling\")\n    n * 2\nend\n", "[]")
	check := runNomiIn(t, printing, "off", "check", ".")
	want := "`calc` has the effect std::io, and its entry under [effects.self.modules] allows it no effects"
	if check.exit == 0 || !strings.Contains(check.stderr, want) {
		t.Errorf("nomi check of io.print in a module whose entry is empty:\n%s\nwant stderr holding %q", check, want)
	}
}
