package docscheck

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var (
	// agentsTestName is a Go test or fuzz target AGENTS.md names, or the
	// prefix of several (`TestBodySum_*`, `-run 'TestLeaves_Cost'`), as a
	// `-run` pattern matches them.
	agentsTestName = regexp.MustCompile(`\b(?:Test|Fuzz)[A-Z_][A-Za-z0-9_]*`)
	// agentsPath is a path in the repository AGENTS.md names, under one of
	// the top-level directories its layout table lists.
	agentsPath = regexp.MustCompile(`(?:^|[\s(` + "`" + `'"])\.?/?((?:internal|cmd|vmhost|vmrunner|hostadapt|std|rt|lib|scripts|editors|tour|docs|testdata|tests|tree-sitter-nomi|benchmarks)/[A-Za-z0-9_./<>*-]*)`)
	// agentsEnv is an environment variable AGENTS.md names.
	agentsEnv = regexp.MustCompile(`\bNOMI_[A-Z0-9_]*[A-Z0-9]`)
	// testFunc is a test or fuzz target's declaration.
	testFunc = regexp.MustCompile(`(?m)^func ((?:Test|Fuzz)\w+)\(`)
)

// TestAgentsNamesWhatExists holds AGENTS.md, the repository's working
// guide, to the tree: every test or fuzz target it names is declared in a
// tracked test file, or begins the name of one; every repository path it
// names exists, is one git ignores (a generated page, a build's output),
// or lies under a package (a target's `testdata/fuzz`), and a std module
// may be named by its path without `.nomi`; and every NOMI_ variable it
// names is read or set by tracked code outside Markdown. A guide that
// names a test, a package or a variable that is gone sends its reader to
// nothing.
func TestAgentsNamesWhatExists(t *testing.T) {
	root, tests := trackedFilesMatching(t, "*_test.go")
	declared := map[string]bool{}
	for _, rel := range tests {
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range testFunc.FindAllStringSubmatch(string(data), -1) {
			declared[m[1]] = true
		}
	}
	if len(declared) < 1000 {
		t.Fatalf("found %d test functions; the scan is not reading the repository", len(declared))
	}
	_, all := trackedFilesMatching(t)
	_, code := trackedFilesMatching(t, "*.go", "*.sh", "*.mjs", "*.js", "*.yml", "Makefile")
	var corpus strings.Builder
	for _, rel := range code {
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		corpus.Write(data)
	}
	read := map[string]bool{}
	for _, v := range agentsEnv.FindAllString(corpus.String(), -1) {
		read[v] = true
	}

	data, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	checked := 0
	seen := map[string]bool{}
	miss := func(what, name string) {
		if !seen[what+name] {
			seen[what+name] = true
			t.Errorf("AGENTS.md names %s %s, which %s", what, name, map[string]string{
				"the test":     "no tracked test file declares",
				"the path":     "does not exist",
				"the variable": "no tracked code reads or sets",
			}[what])
		}
	}
	for _, name := range agentsTestName.FindAllString(text, -1) {
		checked++
		if declared[name] {
			continue
		}
		found := false
		for d := range declared {
			if strings.HasPrefix(d, name) {
				found = true
				break
			}
		}
		if !found {
			miss("the test", name)
		}
	}
	for _, m := range agentsPath.FindAllStringSubmatch(text, -1) {
		p := strings.TrimRight(m[1], ".,;:")
		if strings.ContainsAny(p, "<>*") || strings.Contains(p, "...") {
			continue
		}
		checked++
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(p))); err == nil {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(p)+".nomi")); err == nil {
			continue
		}
		if ignored(root, p) || underAPackage(all, p) {
			continue
		}
		miss("the path", p)
	}
	for _, v := range agentsEnv.FindAllString(text, -1) {
		checked++
		if !read[v] {
			miss("the variable", v)
		}
	}
	if checked < 200 {
		t.Fatalf("checked %d names in AGENTS.md; the scan is not reading it", checked)
	}
	t.Logf("checked %d names in AGENTS.md", checked)
}

// ignored reports whether git ignores p, as a file or as a directory.
func ignored(root, p string) bool {
	return exec.Command("git", "-C", root, "check-ignore", "-q", "--no-index", p).Run() == nil ||
		exec.Command("git", "-C", root, "check-ignore", "-q", "--no-index", p+"/").Run() == nil
}

// underAPackage reports whether some tracked file lies under p inside a
// directory of the repository, as `testdata/fuzz` lies inside each fuzz
// target's package.
func underAPackage(tracked []string, p string) bool {
	for _, f := range tracked {
		if strings.Contains(f, "/"+strings.TrimSuffix(p, "/")+"/") {
			return true
		}
	}
	return false
}
