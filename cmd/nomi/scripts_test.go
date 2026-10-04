package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
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
// THIS EXISTS BECAUSE OF A COVERAGE HOLE, not a hypothesis. Every gate in this
// project is a Go test suite and none of them executes a shell script, so
// deleting `cmd/nomi-stdlibbindings` left `scripts/build-tour-wasm.sh` calling
// a package that no longer existed and NOTHING in the tree could see it:
// `go build ./...`, `go vet ./...`, the corpus and a nine-package test gate all
// passed. It surfaced when a human ran `make start-tour` and got
//
//	stat .../cmd/nomi-stdlibbindings: directory not found
//
// A path that stopped resolving is statically checkable without running the
// build, which is what this does. It does NOT check that a script works — only
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

	// A release carries `nomi`, `nomi-lsp` and `nomi-runner`. Pinning the
	// count, not just "more than zero", is what catches the list being emptied
	// or a row being dropped — either would leave the loop above passing while
	// shipping less than a release is.
	if checked != 3 {
		t.Fatalf("found %d binary row(s) in release.sh's binaries=(...), want 3 (nomi, nomi-lsp, nomi-runner)", checked)
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
