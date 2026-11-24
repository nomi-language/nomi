package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestModules_AReleaseRefusesAModuleThatNeedsALaterNomi: `nomi new` on a
// release writes its release as [module].nomi, and one built from source
// writes none. A release refuses a project, or a dependency, whose
// [module].nomi names a later release: `nomi add` leaves nomi.toml as it
// was, and `nomi check` reports one error, with its code, and checks
// nothing else. A `nomi` built from source refuses neither and prints
// nothing about it.
func TestModules_AReleaseRefusesAModuleThatNeedsALaterNomi(t *testing.T) {
	c := newModuleCLI(t)
	c.bin = releaseNomi(t)
	c.env = append(c.env, "NOMI_RELEASE_BASE_URL="+unreachableRelease)
	const later = "v0.0.1"
	c.host.Tag("example.com/later", "v1.0.0", map[string]string{
		"nomi.toml":  "[module]\nname = \"later\"\nnomi = \"" + later + "\"\n",
		"later.nomi": "pub fn one() -> Int\n    1\nend\n",
	})

	work := t.TempDir()
	c.must(work, "new", "app")
	app := filepath.Join(work, "app")
	manifest := mustRead(t, filepath.Join(app, "nomi.toml"))
	if want := "[module]\nname = \"app\"\nnomi = \"" + releaseTestVersion + "\"\nentry_points = [\"main\"]\n"; manifest != want {
		t.Errorf("a release's nomi new wrote\n%s\nwant\n%s", manifest, want)
	}
	source := *c
	source.bin = nomiBin
	source.must(work, "new", "dev")
	if got := mustRead(t, filepath.Join(work, "dev", "nomi.toml")); strings.Contains(got, "nomi =") {
		t.Errorf("a nomi built from source wrote a release into nomi.toml:\n%s", got)
	}

	_, stderr, err := c.run(app, "add", "example.com/later")
	want := "nomi add: module example.com/later v1.0.0 needs nomi " + later + " or later, and this nomi is " + releaseTestVersion + "; install a newer nomi"
	if err == nil || !strings.HasSuffix(stderr, "\n"+want) {
		t.Errorf("nomi add of a module that needs a later nomi: %v\n%s\nwant %s", err, stderr, want)
	}
	if got := mustRead(t, filepath.Join(app, "nomi.toml")); got != manifest {
		t.Errorf("a refused nomi add changed nomi.toml:\n%s", got)
	}
	source.must(app, "add", "example.com/later")
	mustWrite(t, filepath.Join(app, "main.nomi"), "import\n    std::io\n    later\nend\n\nfn main()\n    io.print(\"${later.one()}\")\nend\n")
	if out, stderr, err := source.run(app, "run", "main.nomi"); err != nil || out != "1" || stderr != "" {
		t.Errorf("a nomi built from source ran a project of a module that needs %s: %v\n%s\n%s", later, err, out, stderr)
	}

	out, _, err := c.run(app, "check", "--format", "json", "main.nomi")
	if err == nil || strings.Count(out, `"type":"diagnostic"`) != 1 || !strings.Contains(out, `"code":"nomi-version"`) ||
		!strings.Contains(out, "module example.com/later v1.0.0 needs nomi "+later+" or later") {
		t.Errorf("nomi check of a project whose dependency needs a later nomi: %v\n%s", err, out)
	}

	mustWrite(t, filepath.Join(app, "nomi.toml"), strings.Replace(manifest, releaseTestVersion, later, 1)+"\n[unknown_to_this_nomi]\nkey = 1\n")
	_, stderr, err = c.run(app, "check", "main.nomi")
	if want := "error: module app (this project) needs nomi " + later + " or later, and this nomi is " + releaseTestVersion + "; install a newer nomi"; err == nil ||
		!strings.HasPrefix(stderr, want) || strings.Count(stderr, "error:") != 1 {
		t.Errorf("nomi check of a project that needs a later nomi: %v\n%s\nwant one error, %s", err, stderr, want)
	}
}
