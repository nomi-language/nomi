package runners

import (
	"errors"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
)

func buildInfo(path, version string, settings ...string) *debug.BuildInfo {
	info := &debug.BuildInfo{Main: debug.Module{Path: path, Version: version}}
	for i := 0; i+1 < len(settings); i += 2 {
		info.Settings = append(info.Settings, debug.BuildSetting{Key: settings[i], Value: settings[i+1]})
	}
	return info
}

func TestPlanCompilerSource(t *testing.T) {
	const rev = "4e913602829bed058abac93886b76fc892d1465a"
	for _, tc := range []struct {
		name      string
		info      *debug.BuildInfo
		override  string
		version   string // "" means a local plan
		dir       string
		whySubstr string
	}{
		{name: "no build info", whySubstr: "no build information"},
		{name: "another main module (an embedder)",
			info: buildInfo("example.com/embedder", "v1.2.3"), whySubstr: "not built as the compiler's own module"},
		{name: "devel", info: buildInfo(CompilerModulePath, "(devel)"), whySubstr: "development build"},
		{name: "empty version", info: buildInfo(CompilerModulePath, ""), whySubstr: "development build"},
		{name: "dirty checkout", info: buildInfo(CompilerModulePath, "v0.1.1-0.20261001203905-4e913602829b+dirty",
			"vcs.revision", rev, "vcs.modified", "true"), whySubstr: "modified checkout"},
		{name: "dirty checkout at a tag", info: buildInfo(CompilerModulePath, "v0.1.1+dirty",
			"vcs.revision", rev, "vcs.modified", "true"), whySubstr: "modified checkout"},
		{name: "clean checkout at an untagged commit", info: buildInfo(CompilerModulePath,
			"v0.1.1-0.20261001203905-4e913602829b", "vcs.revision", rev), whySubstr: "untagged commit"},
		{name: "release built in a clean checkout at its tag", info: buildInfo(CompilerModulePath, "v0.1.1",
			"vcs.revision", rev, "vcs.modified", "false"), version: "v0.1.1"},
		{name: "go install at a tag", info: buildInfo(CompilerModulePath, "v0.1.1"), version: "v0.1.1"},
		{name: "go install at a pseudo-version", info: buildInfo(CompilerModulePath,
			"v0.1.1-0.20261001203905-4e913602829b"), version: "v0.1.1-0.20261001203905-4e913602829b"},
		{name: "prerelease tag", info: buildInfo(CompilerModulePath, "v0.2.0-rc.1", "vcs.revision", rev),
			version: "v0.2.0-rc.1"},
		{name: "override beats a version", info: buildInfo(CompilerModulePath, "v0.1.1"),
			override: "/src/nomi", dir: "/src/nomi"},
		{name: "override on a devel build", info: buildInfo(CompilerModulePath, "(devel)"),
			override: "/src/nomi", dir: "/src/nomi"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := planCompilerSource(tc.info, tc.override)
			if got.Version != tc.version || got.Dir != tc.dir {
				t.Fatalf("plan = %+v, want Version %q Dir %q", got, tc.version, tc.dir)
			}
			if tc.whySubstr != "" && !strings.Contains(got.Why, tc.whySubstr) {
				t.Fatalf("plan.Why = %q, want it to mention %q", got.Why, tc.whySubstr)
			}
		})
	}
}

// fakeCompilerDir is a directory shaped like the compiler module: a go.mod
// declaring it, with a go line, and a go.sum.
func fakeCompilerDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module "+CompilerModulePath+"\n\ngo 1.27.0\n")
	write("go.sum", "golang.org/x/mod v0.36.0 h1:dep=\n")
	return dir
}

func TestVersionedCompilerIdentity(t *testing.T) {
	a, b := versionedCompilerIdentity("v0.99.0"), versionedCompilerIdentity("v0.99.1")
	if a == b {
		t.Fatal("two versions share an identity")
	}
	if a != versionedCompilerIdentity("v0.99.0") || !strings.HasPrefix(a, "sha256:") {
		t.Fatalf("identity %q is not a stable sha256", a)
	}
}

func TestCheckoutSource(t *testing.T) {
	dir := fakeCompilerDir(t)
	src, err := checkoutSource(dir)
	if err != nil || src.Dir != dir || src.Version != "" {
		t.Fatalf("checkoutSource(%s) = %+v, %v", dir, src, err)
	}
	other := t.TempDir()
	if err := os.WriteFile(filepath.Join(other, "go.mod"), []byte("module example.com/other\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := checkoutSource(other); err == nil || !strings.Contains(err.Error(), compilerSourceEnv) {
		t.Fatalf("a checkout of another module was accepted, or the error does not name %s: %v", compilerSourceEnv, err)
	}
	if _, err := checkoutSource(filepath.Join(other, "missing")); err == nil {
		t.Fatal("a missing directory was accepted")
	}
}

func TestNoCompilerSourceError(t *testing.T) {
	plan := planCompilerSource(buildInfo(CompilerModulePath, "(devel)"), "")
	err := error(&noCompilerSourceError{why: plan.Why})
	if !errors.Is(err, ErrNoCompilerSource) {
		t.Fatal("the reasoned error does not match ErrNoCompilerSource")
	}
	msg := err.Error()
	for _, want := range []string{"development build", compilerSourceEnv, "go install github.com/nomi-language/nomi/cmd/nomi@<version>"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message does not mention %q:\n%s", want, msg)
		}
	}
}
