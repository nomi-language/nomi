package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/modcache"
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
// list: `"nomi-lsp:./cmd/nomi-lsp"`, i.e. installed name then package.
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

	// A release carries `nomi`, `nomi-lsp`, `nomi-vm` and `nomi-vm-nonet`,
	// the runner without the network that `nomi build` picks for a program
	// that never reaches it. Pinning the count, not just "more than zero", is
	// what catches the list being emptied or a row being dropped — either
	// would leave the loop above passing while shipping less than a release is.
	if checked != 4 {
		t.Fatalf("found %d binary row(s) in release.sh's binaries=(...), want 4 (nomi, nomi-lsp, nomi-vm, nomi-vm-nonet)", checked)
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

// TestReleaseTagsNameEveryLibModule: scripts/release-tags.sh prints the
// root tag and one tag for each module under lib/ whose nomi.toml gives it
// a path there, which the proxy serves a release of only from that tag, and
// each such module has the go.mod the proxy reads its path from. A library
// added without a go.mod, or one the script's search misses, fails here
// rather than at a release `nomi`'s first `nomi add` of it.
func TestReleaseTagsNameEveryLibModule(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("runs a bash script; the Unix jobs check it")
	}
	root, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	const version = "v0.0.1"
	want := []string{version}
	err = filepath.WalkDir(filepath.Join(root, "lib"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "testdata" || strings.HasPrefix(d.Name(), ".")) {
			return filepath.SkipDir
		}
		if d.IsDir() || d.Name() != modcache.ManifestName {
			return nil
		}
		dir := filepath.Dir(path)
		mf, err := modcache.ReadModuleFile(dir)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, dir)
		rel = filepath.ToSlash(rel)
		if mf == nil || mf.Path != "github.com/nomi-language/nomi/"+rel {
			t.Errorf("%s/nomi.toml does not give the module the path github.com/nomi-language/nomi/%s, which the proxy serves it at", rel, rel)
			return nil
		}
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
			t.Errorf("%s has a nomi.toml and no go.mod, so the module proxy cannot serve it", rel)
		}
		want = append(want, rel+"/"+version)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(want[1:])
	cmd := exec.Command("bash", filepath.Join(root, "scripts", "release-tags.sh"), version)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	// Exit 1 says entries between the libraries do not name the version yet,
	// which a release fixes before tagging; the tags are printed either way.
	if exit := (*exec.ExitError)(nil); err != nil && !(errors.As(err, &exit) && exit.ExitCode() == 1) {
		t.Fatalf("release-tags.sh %s: %v\n%s", version, err, stderr.String())
	}
	got := strings.Fields(string(out))
	if len(want) < 2 || strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("release-tags.sh %s printed\n%s\nwant the root tag and one per lib/ module\n%s", version, strings.Join(got, "\n"), strings.Join(want, "\n"))
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
