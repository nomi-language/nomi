package runners

import (
	"bytes"
	"compress/gzip"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/nomi-language/nomi/internal/artifactcache"
	"github.com/nomi-language/nomi/internal/buildversion"
)

func gzipped(t *testing.T, data string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write([]byte(data)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// carryRunners stands in the runners a release nomi carries, the full one
// reading "plain runner" and the lean one "lean runner", and answers a
// pointer to the count of runners unpacked.
func carryRunners(t *testing.T) *int {
	t.Helper()
	oldRunners, oldUnpack := embeddedRunners, unpackRunner
	t.Cleanup(func() { embeddedRunners, unpackRunner = oldRunners, oldUnpack })
	embeddedRunners = map[string][]byte{
		"nomi-vm":       gzipped(t, "plain runner"),
		"nomi-vm-nonet": gzipped(t, "lean runner"),
	}
	unpacked := new(int)
	unpackRunner = func(gz []byte, bin string) error {
		*unpacked++
		return oldUnpack(gz, bin)
	}
	return unpacked
}

// countingRelease serves nothing and counts the requests it gets.
func countingRelease(t *testing.T) *fakeRelease {
	t.Helper()
	rel := &fakeRelease{files: map[string][]byte{}}
	srv := httptest.NewServer(rel)
	t.Cleanup(srv.Close)
	t.Setenv(releaseBaseURLEnv, srv.URL)
	return rel
}

// TestEmbeddedRunner_TheHostTargetUsesTheCarriedRunner: a release nomi
// building for its own platform takes the runner it carries, with nothing
// beside it and no request to the release, unpacks it into the release's
// runner cache once, and takes the cached one after that. A runner that does
// not come from its commit beside it is not consulted.
func TestEmbeddedRunner_TheHostTargetUsesTheCarriedRunner(t *testing.T) {
	host := Target{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
	r := standInReleaseOn(t, host)
	unpacked := carryRunners(t)
	rel := countingRelease(t)
	// A sibling that is not a Go binary is refused when it is looked at.
	if err := os.WriteFile(filepath.Join(r.dir, exeName("nomi-vm-nonet", host.GOOS)), []byte("an older nomi-vm"), 0o755); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(r.cache, "nomi-vm", "v9.9.9", host.GOOS+"_"+host.GOARCH)
	for _, c := range []struct {
		v        Variant
		contents string
		unpacked int
	}{
		{Variant{NoNetwork: true}, "lean runner", 1},
		{Variant{NoNetwork: true}, "lean runner", 1},
		{Variant{}, "plain runner", 2},
		{Variant{}, "plain runner", 2},
	} {
		got, err := Runner(host, c.v, nil)
		if err != nil {
			t.Fatalf("%s: %v", c.v.BinaryName(), err)
		}
		want := filepath.Join(dir, exeName(c.v.BinaryName(), host.GOOS))
		if data, _ := os.ReadFile(got); got != want || string(data) != c.contents {
			t.Fatalf("%s: got %s holding %q, want %s holding %q", c.v.BinaryName(), got, data, want, c.contents)
		}
		if *unpacked != c.unpacked {
			t.Fatalf("%s: %d runners unpacked, want %d", c.v.BinaryName(), *unpacked, c.unpacked)
		}
	}
	if n := rel.requests.Load(); n != 0 {
		t.Fatalf("the release was asked %d time(s)", n)
	}
	r.noGoRan(t)
}

// TestEmbeddedRunner_AnotherTargetDownloads: the runners a release nomi
// carries are its own platform's, so another target's runner is downloaded
// from its runners archive, and std/compiler's runner for its own platform
// from the compiler archive. Nothing is unpacked.
func TestEmbeddedRunner_AnotherTargetDownloads(t *testing.T) {
	cross := Target{"linux", "amd64"}
	if runtime.GOOS == cross.GOOS && runtime.GOARCH == cross.GOARCH {
		cross = Target{"linux", "arm64"}
	}
	r := standInReleaseOn(t, cross)
	unpacked := carryRunners(t)
	archive := "nomi_v9.9.9_" + cross.GOOS + "_" + cross.GOARCH + "_runners.tar.gz"
	data := tarGz(t, map[string][]byte{"./nomi-vm": []byte("plain runner"), "./nomi-vm-nonet": []byte("lean runner")})
	rel := &fakeRelease{files: map[string][]byte{
		"/download/v9.9.9/" + archive:                []byte(data),
		"/download/v9.9.9/nomi_v9.9.9_checksums.txt": []byte(sha(data) + "  ./" + archive + "\n"),
	}}
	srv := httptest.NewServer(rel)
	t.Cleanup(srv.Close)
	t.Setenv(releaseBaseURLEnv, srv.URL)
	var progress bytes.Buffer
	got, err := Runner(cross, Variant{NoNetwork: true}, &progress)
	if err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(got); string(data) != "lean runner" || !strings.Contains(progress.String(), srv.URL+"/download/v9.9.9/"+archive) {
		t.Fatalf("got %s holding %q after %q, want the download from %s", got, data, progress.String(), archive)
	}
	if *unpacked != 0 {
		t.Fatalf("%d runners unpacked for another platform", *unpacked)
	}
	r.noGoRan(t)
}

// TestEmbeddedRunner_StdCompilerIsNotCarried: std/compiler's runner is not
// among those a release nomi carries, so even for its own platform it is
// fetched from the compiler archive.
func TestEmbeddedRunner_StdCompilerIsNotCarried(t *testing.T) {
	host := Target{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
	r := standInReleaseOn(t, host)
	unpacked := carryRunners(t)
	rel := countingRelease(t)
	_, err := Runner(host, Variant{Compiler: true}, nil)
	if want := "nomi_v9.9.9_" + host.GOOS + "_" + host.GOARCH + "_compiler"; err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("got %v, want a failed download naming %s", err, want)
	}
	if *unpacked != 0 || rel.requests.Load() == 0 {
		t.Fatalf("%d runners unpacked and %d requests, want none unpacked and the release asked", *unpacked, rel.requests.Load())
	}
	r.noGoRan(t)
}

// TestRunner_StdCompilerHasNoLeanRunner: the runners come in two tiers and
// std/compiler's is the full one, so asking for a lean one is refused before
// anything is looked up.
func TestRunner_StdCompilerHasNoLeanRunner(t *testing.T) {
	_, err := Runner(Target{"linux", "amd64"}, Variant{Compiler: true, NoNetwork: true}, nil)
	want := "std/compiler's runner has no lean variant: a program that imports std/compiler is appended to nomi-vm-compiler"
	if err == nil || err.Error() != want {
		t.Fatalf("got %v, want %q", err, want)
	}
}

// setAge sets path's modification time to age ago.
func setAge(t *testing.T, path string, age time.Duration) {
	t.Helper()
	at := time.Now().Add(-age)
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

// makePruneDue ages the files directly under releases, which are only the
// mark of its last prune, so the next runner answered prunes it
// (artifactcache.Due).
func makePruneDue(t *testing.T, releases string) {
	t.Helper()
	entries, err := os.ReadDir(releases)
	if err != nil {
		t.Fatal(err)
	}
	marks := 0
	for _, e := range entries {
		if !e.IsDir() {
			setAge(t, filepath.Join(releases, e.Name()), 2*sweepEvery)
			marks++
		}
	}
	if marks != 1 {
		t.Fatalf("%d files under %s, want the one mark of its last prune", marks, releases)
	}
}

// plantRelease makes version's directory under releases, holding a runner
// for host, last used age ago.
func plantRelease(t *testing.T, releases, version string, host Target, age time.Duration) {
	t.Helper()
	dir := filepath.Join(releases, version, host.GOOS+"_"+host.GOARCH)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, exeName("nomi-vm", host.GOOS)), []byte("plain runner"), 0o755); err != nil {
		t.Fatal(err)
	}
	setAge(t, filepath.Join(releases, version), age)
}

// releasesLeft names the release directories under releases, in order.
func releasesLeft(t *testing.T, releases string) string {
	t.Helper()
	entries, err := os.ReadDir(releases)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return strings.Join(out, " ")
}

// TestReleaseRunners_AReleaseNothingUsedIsPruned: answering a release's
// runner marks the release's directory used, and the prune it runs at most
// once a day removes another release's directory nothing has used for
// artifactcache.Unused, keeps one used within it, and keeps the running
// release's whatever its age.
func TestReleaseRunners_AReleaseNothingUsedIsPruned(t *testing.T) {
	host := Target{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
	r := standInReleaseOn(t, host)
	unpacked := carryRunners(t)
	releases := filepath.Join(r.cache, "nomi-vm")
	if _, err := Runner(host, Variant{}, nil); err != nil {
		t.Fatal(err)
	}
	plantRelease(t, releases, "v1.0.0", host, artifactcache.Unused+time.Hour)
	plantRelease(t, releases, "v1.1.0", host, artifactcache.Unused-time.Hour)
	setAge(t, filepath.Join(releases, "v9.9.9"), 3*artifactcache.Unused)
	makePruneDue(t, releases)
	if _, err := Runner(host, Variant{}, nil); err != nil {
		t.Fatal(err)
	}
	if got, want := releasesLeft(t, releases), "v1.1.0 v9.9.9"; got != want {
		t.Fatalf("releases left: %q, want %q", got, want)
	}
	info, err := os.Stat(filepath.Join(releases, "v9.9.9"))
	if err != nil {
		t.Fatal(err)
	}
	if age := time.Since(info.ModTime()); age > time.Hour {
		t.Fatalf("the running release's directory reads as last used %v ago after a runner was answered from it", age)
	}
	if *unpacked != 1 {
		t.Fatalf("%d runners unpacked, want 1: the second answer is the cached runner", *unpacked)
	}
	// Within the day, a runner answered runs no prune.
	plantRelease(t, releases, "v1.0.0", host, artifactcache.Unused+time.Hour)
	if _, err := Runner(host, Variant{}, nil); err != nil {
		t.Fatal(err)
	}
	if got, want := releasesLeft(t, releases), "v1.0.0 v1.1.0 v9.9.9"; got != want {
		t.Fatalf("releases left after a second answer within the day: %q, want %q", got, want)
	}
	r.noGoRan(t)
}

// TestReleaseRunners_APrunedReleaseUnpacksAgain: a release whose directory
// another release pruned unpacks the runner it carries again on its next
// build.
func TestReleaseRunners_APrunedReleaseUnpacksAgain(t *testing.T) {
	host := Target{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
	r := standInReleaseOn(t, host)
	unpacked := carryRunners(t)
	releases := filepath.Join(r.cache, "nomi-vm")
	lean := Variant{NoNetwork: true}
	if _, err := Runner(host, lean, nil); err != nil {
		t.Fatal(err)
	}
	setAge(t, filepath.Join(releases, "v9.9.9"), artifactcache.Unused+time.Hour)
	makePruneDue(t, releases)
	t.Cleanup(buildversion.SetStampForTest("v10.0.0"))
	if _, err := Runner(host, lean, nil); err != nil {
		t.Fatal(err)
	}
	if got, want := releasesLeft(t, releases), "v10.0.0"; got != want {
		t.Fatalf("releases left: %q, want %q", got, want)
	}
	t.Cleanup(buildversion.SetStampForTest("v9.9.9"))
	got, err := Runner(host, lean, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(releases, "v9.9.9", host.GOOS+"_"+host.GOARCH, exeName(lean.BinaryName(), host.GOOS))
	if data, _ := os.ReadFile(got); got != want || string(data) != "lean runner" {
		t.Fatalf("got %s holding %q, want %s holding the lean runner", got, data, want)
	}
	if *unpacked != 3 {
		t.Fatalf("%d runners unpacked, want 3: v9.9.9's, v10.0.0's and v9.9.9's again", *unpacked)
	}
	r.noGoRan(t)
}
