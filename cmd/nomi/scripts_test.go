package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/pkgcache"
	"github.com/nomi-language/nomi/internal/stdlibbindings/families"
)

// goPackageRef matches a `./cmd/...`-style package argument in a `go run` /
// `go build` / `go install` / `go test` invocation. The scripts and the
// Makefile spell the module with `-C`, so the package argument is always
// module-relative and always starts `./`.
var goPackageRef = regexp.MustCompile(`(?:^|\s)(\./[A-Za-z0-9._/-]+)`)

// goInvocation matches the command word so a bare `./foo` in some other
// position — a path argument, a cd target — is not read as a package.
var goInvocation = regexp.MustCompile(`\bgo\s+(?:run|build|install|test)\b`)

// TestScriptsNameGoPackagesThatExist walks every shell script under scripts/
// and the Makefile, and requires each Go package they invoke to be a real
// directory.
//
// Every gate in this project is a Go test suite and none of them executes a
// shell script, so deleting a package a script still calls leaves `go build
// ./...`, `go vet ./...` and every test green, and the breakage shows up only
// when someone runs the script.
//
// A path that stopped resolving is statically checkable without running the
// build, which is what this does. It does not check that a script works, only
// that the packages it names are present. `make test-tour` and the tour build
// remain the real checks.
func TestScriptsNameGoPackagesThatExist(t *testing.T) {
	root, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("resolving repo root: %v", err)
	}

	files := []string{filepath.Join(root, "Makefile")}
	entries, err := os.ReadDir(filepath.Join(root, "scripts"))
	if err != nil {
		t.Fatalf("reading scripts/: %v", err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			files = append(files, filepath.Join(root, "scripts", e.Name()))
		}
	}

	// Anti-vacuity: a scan that finds no invocations would pass while
	// checking nothing, which is the failure mode this whole file is about.
	checked := 0

	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		for i, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue
			}
			if !goInvocation.MatchString(line) {
				continue
			}
			for _, m := range goPackageRef.FindAllStringSubmatch(line, -1) {
				ref := m[1]
				// `./...` is a wildcard, not a directory.
				if strings.HasSuffix(ref, "...") {
					continue
				}
				dir := filepath.Join(root, filepath.FromSlash(strings.TrimSuffix(ref, "/")))
				checked++
				info, err := os.Stat(dir)
				if err != nil {
					t.Errorf("%s:%d names Go package %s, which does not exist at %s\n\t%s",
						filepath.Base(file), i+1, ref, dir, strings.TrimSpace(line))
					continue
				}
				if !info.IsDir() {
					t.Errorf("%s:%d names Go package %s, which is not a directory",
						filepath.Base(file), i+1, ref)
				}
			}
		}
	}

	if checked == 0 {
		t.Fatal("no Go package references found in Makefile or scripts/, so every " +
			"assertion above is vacuous — the matcher has stopped matching")
	}
	t.Logf("%d Go package reference(s) across the Makefile and scripts/ resolve to real directories", checked)
}

// releaseBinaryEntry matches one row of scripts/release.sh's `binaries=(...)`
// list: `"nomi-vm:./cmd/nomi-vm"`, i.e. installed name then package.
var releaseBinaryEntry = regexp.MustCompile(`^\s*"([A-Za-z0-9._-]+):(\.[A-Za-z0-9._/-]*)"\s*$`)

// TestReleaseScriptNamesGoPackagesThatExist covers the packages the release
// builds, which the scan above cannot see.
//
// That scan is line-scoped: it wants the `go build` and the `./pkg` on one
// line. scripts/release.sh loops over a list and builds `"$pkg"`, so its two
// package references sit in a data array and match nothing — the release
// script would be the one script in scripts/ exempt from the check, and a
// renamed cmd/ directory would stay invisible until somebody pushed a tag.
func TestReleaseScriptNamesGoPackagesThatExist(t *testing.T) {
	root, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("resolving repo root: %v", err)
	}
	script := filepath.Join(root, "scripts", "release.sh")
	data, err := os.ReadFile(script)
	if err != nil {
		t.Fatalf("reading %s: %v", script, err)
	}

	inList := false
	checked := 0
	for i, line := range strings.Split(string(data), "\n") {
		switch {
		case strings.HasPrefix(line, "binaries=("):
			inList = true
			continue
		case inList && strings.HasPrefix(line, ")"):
			inList = false
			continue
		case !inList:
			continue
		}
		m := releaseBinaryEntry.FindStringSubmatch(line)
		if m == nil {
			t.Errorf("release.sh:%d is inside binaries=(...) but is not a `\"name:./pkg\"` row: %s",
				i+1, strings.TrimSpace(line))
			continue
		}
		dir := filepath.Join(root, filepath.FromSlash(m[2]))
		checked++
		info, err := os.Stat(dir)
		if err != nil {
			t.Errorf("release.sh:%d builds %q from Go package %s, which does not exist at %s",
				i+1, m[1], m[2], dir)
			continue
		}
		if !info.IsDir() {
			t.Errorf("release.sh:%d names Go package %s, which is not a directory", i+1, m[2])
		}
	}

	// A release builds `nomi-vm`, `nomi-vm-nonet` (the runner without the
	// network that `nomi build` picks for a program that never reaches it)
	// and `nomi`, which carries the other two. Pinning the count, not just
	// "more than zero", is what catches the list being emptied or a row
	// being dropped — either would leave the loop above passing while
	// shipping less than a release is.
	if checked != 3 {
		t.Fatalf("found %d binary row(s) in release.sh's binaries=(...), want 3 (nomi-vm, nomi-vm-nonet, nomi)", checked)
	}
}

// TestScriptsAreExecutable requires every scripts/*.sh to carry the owner
// execute bit.
//
// This is a scar. scripts/release.sh was committed mode 100644 after a tool
// rewrote it and dropped the bit, and nothing noticed: `go build ./...`,
// `go vet ./...` and the two checks above all passed, because none of them
// runs a script. The Makefile and .github/workflows/release.yml invoke these
// files as `./scripts/<name>.sh`, which is exec-bit sensitive, so the failure
// mode is a release job that dies with "Permission denied" on a tag push.
func TestScriptsAreExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no executable bit to read; git keeps the scripts' mode, which the Unix jobs check")
	}
	root, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("resolving repo root: %v", err)
	}
	dir := filepath.Join(root, "scripts")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading scripts/: %v", err)
	}
	checked := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sh") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			t.Fatalf("stat %s: %v", e.Name(), err)
		}
		checked++
		if info.Mode().Perm()&0o100 == 0 {
			t.Errorf("scripts/%s is mode %v, not executable — `./scripts/%s` will fail with Permission denied",
				e.Name(), info.Mode().Perm(), e.Name())
		}
	}
	if checked == 0 {
		t.Fatal("no .sh files found under scripts/, so the assertion above is vacuous")
	}
}

// TestReleaseTagsChecksTheLibPackages: scripts/release-tags.sh prints the
// one tag a release makes and exits 0 on this tree, whose lib/ packages each
// name themselves and name each other by dir alone. On a copy of the script
// over a lib/ of its own, it refuses a package whose [package] gives no name
// and an entry that names this repository by version, each by file and
// line, and prints no tag.
func TestReleaseTagsChecksTheLibPackages(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("runs a bash script; the Unix jobs check it")
	}
	root, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, "scripts", "release-tags.sh")
	const version = "v0.0.1"
	run := func(script string) (stdout, stderr string, exit int) {
		t.Helper()
		cmd := exec.Command("bash", script, version)
		var errOut strings.Builder
		cmd.Stderr = &errOut
		out, err := cmd.Output()
		if exitErr := (*exec.ExitError)(nil); errors.As(err, &exitErr) {
			return string(out), errOut.String(), exitErr.ExitCode()
		} else if err != nil {
			t.Fatalf("release-tags.sh %s: %v", version, err)
		}
		return string(out), errOut.String(), 0
	}

	modules := 0
	err = filepath.WalkDir(filepath.Join(root, "lib"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "testdata" || strings.HasPrefix(d.Name(), ".")) {
			return filepath.SkipDir
		}
		if !d.IsDir() && d.Name() == pkgcache.ManifestName {
			modules++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if modules == 0 {
		t.Fatal("found no nomi.toml under lib/, so the run below checks nothing")
	}
	if out, errOut, exit := run(script); exit != 0 || out != version+"\n" {
		t.Fatalf("release-tags.sh %s on this tree exits %d and prints\n%s%s\nwant exit 0 and the one tag %s", version, exit, out, errOut, version)
	}

	fake := t.TempDir()
	text, err := os.ReadFile(script)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(fake, "scripts", "release-tags.sh"), string(text))
	mustWrite(t, filepath.Join(fake, "lib", "good", "nomi.toml"), "[package]\nname = \"good\"\n\n[dependencies]\nother = { dir = \"../other\" }\n")
	mustWrite(t, filepath.Join(fake, "lib", "nameless", "nomi.toml"), "[package]\nentry_points = [\"main\"]\n")
	mustWrite(t, filepath.Join(fake, "lib", "pinned", "nomi.toml"),
		"[package]\nname = \"pinned\"\n\n[dependencies]\ngood = { repo = \"github.com/nomi-language/nomi\", subdir = \"lib/good\", version = \"v0.0.1\" }\n")
	out, errOut, exit := run(filepath.Join(fake, "scripts", "release-tags.sh"))
	if exit != 1 || out != "" {
		t.Errorf("release-tags.sh over a nameless module and a pinned entry exits %d and prints %q, want exit 1 and no tag", exit, out)
	}
	for _, want := range []string{
		"lib/nameless/nomi.toml: [package] gives no name",
		"lib/pinned/nomi.toml:5: good names github.com/nomi-language/nomi by version; a library names another by dir alone",
		"2 problems above",
	} {
		if !strings.Contains(errOut, want) {
			t.Errorf("release-tags.sh says\n%s\nwant a line holding %q", errOut, want)
		}
	}
	if strings.Contains(errOut, "lib/good/") {
		t.Errorf("release-tags.sh names a module with no problem:\n%s", errOut)
	}
}

// scripts/release.sh builds each -nonet runner, the lean one, with every
// optional family's tag, as internal/runners builds one and as its identity check
// expects (families.LeanTags). A family added without its tag there
// would ship a lean runner that links it.
func TestReleaseScriptBuildsTheLeanRunnerWithEveryFamilyTag(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("scripts", "release.sh"))
	if err != nil {
		t.Fatal(err)
	}
	want := `*-nonet) tags="` + strings.Join(families.LeanTags(), ",") + `" ;;`
	if !strings.Contains(string(data), want) {
		t.Errorf("scripts/release.sh does not build the lean runner with every family's tag: want the line\n\t%s", want)
	}
}
