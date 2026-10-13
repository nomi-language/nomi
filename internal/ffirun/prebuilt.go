package ffirun

// Prebuilt runners: how `nomi build` finds a runner without building one, the
// way `deno compile` copies the `denort` that ships with Deno.
//
// A release archive carries `nomi-vm` beside `nomi` and `nomi-lsp`, built
// from the same commit (scripts/release.sh). For a pure-Nomi program the
// runner is looked up in this order (Runner):
//
//  1. `nomi-vm` beside this `nomi`, and beside the file it resolves to
//     when it is a symlink, when that runner is built for the target.
//  2. A source checkout of the compiler: build the runner with `go build`
//     and cache it, as a development tree always has. A versioned `nomi`
//     that is not a release archive (`go install ...@version`) builds it the
//     same way from that version in the module cache, which Go fetches.
//  3. A runner downloaded earlier for this release and target, under the
//     cache root's `nomi-vm/<version>/<goos>_<goarch>/`.
//  4. Download: the target's archive from the release this `nomi` was cut
//     as, verified against the release's checksum file, with only
//     `nomi-vm` extracted into (3).
//
// Every prebuilt runner (1, 3 and 4) must come from the same commit as this
// `nomi`: both carry Go's VCS stamp in their build info, which
// internal/gobuildinfo reads from a binary of any platform, a WebAssembly
// module included, without running it. A mismatch is refused. In a source checkout a mismatched or dirty sibling is
// passed over instead, because step 2 can build the right one.
//
// A release also ships a runner-only archive for js/wasm and wasip1/wasm. The
// js/wasm one carries wasm_exec.js beside the runner, from the Go that built
// it, and the download caches both (WasmExec). The wasip1 one also carries
// `nomi-vm-reactor`, the reactor a program with an `export fn` is appended
// to (Variant); each runner is looked up, cached and checked under its own
// name, and its build info must name its own main package.
//
// std/compiler's runner variant links the whole front end: 16.7 MB stripped
// against the plain runner's 9.5 MB and `nomi`'s 21.3 MB (6.5, 3.7 and 8.3 MB
// gzipped) on darwin/arm64. Releases ship only the plain runner, so a program
// importing std/compiler builds its runner with Go: from the checkout, or from
// the compiler module at a versioned `nomi`'s version.

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"github.com/nomi-language/nomi/internal/gobuildinfo"
)

// ReleaseVersion is the release tag this `nomi` was cut as. scripts/release.sh
// sets it with `-ldflags -X github.com/nomi-language/nomi/internal/ffirun.ReleaseVersion=<tag>`; it is
// empty for every other build. It names the release a cross-target runner is
// downloaded from, which the VCS stamp cannot: a commit is not a tag.
var ReleaseVersion string

// releaseBaseURLEnv overrides where releases are downloaded from. Tests point
// it at a local server; nothing else needs it.
const releaseBaseURLEnv = "NOMI_RELEASE_BASE_URL"

// defaultReleaseBaseURL is the GitHub release download root install.md uses.
const defaultReleaseBaseURL = "https://github.com/nomi-language/nomi/releases/download"

// runnerMainPath is the main package every prebuilt command runner is built
// from.
const runnerMainPath = "github.com/nomi-language/nomi/cmd/nomi-vm"

// reactorMainPath is the main package the prebuilt wasip1 reactor is built
// from.
const reactorMainPath = "github.com/nomi-language/nomi/cmd/nomi-vm-reactor"

// buildIdentity is what a Go binary's build info says about where it came
// from.
type buildIdentity struct {
	// Path is the main package.
	Path string
	// Revision is the VCS commit, empty when none was stamped.
	Revision string
	// Modified is set when the tree had uncommitted changes.
	Modified bool
	// GOOS and GOARCH are the platform it was built for.
	GOOS, GOARCH string
}

func identityOf(info *debug.BuildInfo) buildIdentity {
	var id buildIdentity
	if info == nil {
		return id
	}
	id.Path = info.Path
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			id.Revision = s.Value
		case "vcs.modified":
			id.Modified = s.Value == "true"
		case "GOOS":
			id.GOOS = s.Value
		case "GOARCH":
			id.GOARCH = s.Value
		}
	}
	return id
}

// commit is the identity's commit as `nomi --version` spells it.
func (id buildIdentity) commit() string {
	if id.Revision == "" {
		return "an unrecorded commit"
	}
	r := id.Revision
	if len(r) > 12 {
		r = r[:12]
	}
	if id.Modified {
		r += "-dirty"
	}
	return "commit " + r
}

// selfIdentity is this process's identity. A variable so a test can stand in
// a released `nomi`.
var selfIdentity = func() buildIdentity {
	info, _ := debug.ReadBuildInfo()
	return identityOf(info)
}

// executablePath is this process's executable. A variable for tests.
var executablePath = os.Executable

// readIdentity reads a binary's build info without running it, so it works on
// a runner for another platform.
func readIdentity(bin string) (buildIdentity, error) {
	info, err := gobuildinfo.ReadFile(bin)
	if err != nil {
		return buildIdentity{}, err
	}
	return identityOf(info), nil
}

// errRunnerMismatch is a prebuilt runner that does not come from this
// `nomi`'s commit.
type errRunnerMismatch struct{ msg string }

func (e *errRunnerMismatch) Error() string { return e.msg }

// checkRunner requires runner (read from bin) to be a nomi runner of variant
// v built for target from the same commit as self.
func checkRunner(self, runner buildIdentity, bin string, target Target, v Variant) error {
	if runner.Path != v.mainPath() {
		return &errRunnerMismatch{fmt.Sprintf("%s is not a %s (its main package is %q, want %q)",
			bin, v.binaryName(), runner.Path, v.mainPath())}
	}
	if runner.GOOS != target.GOOS || runner.GOARCH != target.GOARCH {
		return &errRunnerMismatch{fmt.Sprintf("%s is built for %s/%s, not %s",
			bin, runner.GOOS, runner.GOARCH, target)}
	}
	if self.Revision == "" {
		return &errRunnerMismatch{fmt.Sprintf("this nomi records no source commit, so it cannot check that "+
			"the nomi-vm at %s matches it; build nomi from a commit, or from a release", bin)}
	}
	if runner.Revision != self.Revision || runner.Modified != self.Modified {
		return &errRunnerMismatch{fmt.Sprintf("the nomi-vm at %s was built from %s and this nomi from %s; "+
			"a nomi-vm must come from the same commit as the nomi that appends programs to it. "+
			"Install nomi and nomi-vm from one release archive",
			bin, runner.commit(), self.commit())}
	}
	return nil
}

// exeName is name with the executable suffix of goos.
func exeName(name, goos string) string {
	if goos == "windows" {
		return name + ".exe"
	}
	return name
}

// siblingRunners are the places a runner of variant v installed beside this
// `nomi` can be: its directory, and the directory of the file it resolves to
// when it is a symlink (Homebrew links bin/nomi into the Cellar).
func siblingRunners(target Target, v Variant) []string {
	exe, err := executablePath()
	if err != nil {
		return nil
	}
	name := exeName(v.binaryName(), target.GOOS)
	out := []string{filepath.Join(filepath.Dir(exe), name)}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil && filepath.Dir(resolved) != filepath.Dir(exe) {
		out = append(out, filepath.Join(filepath.Dir(resolved), name))
	}
	return out
}

// siblingRunner answers a runner installed beside this `nomi` for target, or
// "" when there is none. haveSource says whether step 2 can build a runner, in
// which case a sibling that is dirty or mismatched is passed over rather than
// refused.
func siblingRunner(target Target, v Variant, haveSource bool) (string, error) {
	self := selfIdentity()
	for _, bin := range siblingRunners(target, v) {
		info, err := os.Stat(bin)
		if err != nil || info.IsDir() {
			continue
		}
		id, err := readIdentity(bin)
		if err != nil {
			if haveSource {
				continue
			}
			return "", fmt.Errorf("ffirun: reading the %s at %s: %w", v.binaryName(), bin, err)
		}
		// A runner for another platform beside nomi is not this target's.
		if id.GOOS != target.GOOS || id.GOARCH != target.GOARCH {
			continue
		}
		if haveSource && (self.Modified || id.Modified) {
			// A dirty build names its commit but not its edits, and the
			// source can build the exact runner.
			continue
		}
		if err := checkRunner(self, id, bin, target, v); err != nil {
			if haveSource {
				continue
			}
			return "", fmt.Errorf("ffirun: %w", err)
		}
		return bin, nil
	}
	return "", nil
}

// releaseRunner answers the runner of variant v for target from this
// `nomi`'s release: the cached download when there is one, otherwise a fresh
// download.
func releaseRunner(target Target, v Variant, progress io.Writer) (string, error) {
	version := ReleaseVersion
	if version == "" {
		return "", fmt.Errorf("ffirun: `nomi build` needs a nomi-vm for %s. None is installed beside this nomi, "+
			"it is a development build whose source tree is gone, so it has nothing to build one from, and it "+
			"is not a release build, so there is no release to download one from. Install nomi from a release "+
			"archive, which ships nomi-vm beside it, or with Go installed set "+compilerSourceEnv+
			" to a checkout of the compiler", target)
	}
	if strings.ContainsAny(version, `/\`) || strings.Contains(version, "..") {
		return "", fmt.Errorf("ffirun: release version %q cannot name a cache directory", version)
	}
	root, err := cacheRoot()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(root, "nomi-vm", version, target.GOOS+"_"+target.GOARCH)
	bin := filepath.Join(dir, exeName(v.binaryName(), target.GOOS))
	self := selfIdentity()
	if _, err := os.Stat(bin); err == nil {
		id, err := readIdentity(bin)
		complete := !target.needsWasmExec() || fileExists(filepath.Join(dir, WasmExecName))
		if err == nil && complete && checkRunner(self, id, bin, target, v) == nil {
			return bin, nil
		}
		// A damaged or foreign file where the download goes is replaced.
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("ffirun: creating %s: %w", dir, err)
	}
	if err := downloadRunner(releaseBaseURL(), version, target, v, bin, progress); err != nil {
		return "", err
	}
	id, err := readIdentity(bin)
	if err == nil {
		err = checkRunner(self, id, bin, target, v)
	}
	if err != nil {
		_ = os.Remove(bin)
		return "", fmt.Errorf("ffirun: the downloaded %s: %w", v.binaryName(), err)
	}
	return bin, nil
}

func releaseBaseURL() string {
	if v := os.Getenv(releaseBaseURLEnv); v != "" {
		return strings.TrimRight(v, "/")
	}
	return defaultReleaseBaseURL
}

// releaseArchiveName is the archive scripts/release.sh writes for target.
func releaseArchiveName(version string, target Target) string {
	ext := ".tar.gz"
	if target.GOOS == "windows" {
		ext = ".zip"
	}
	return fmt.Sprintf("nomi_%s_%s_%s%s", version, target.GOOS, target.GOARCH, ext)
}

// releaseChecksumsName is the release's checksum manifest.
func releaseChecksumsName(version string) string {
	return fmt.Sprintf("nomi_%s_checksums.txt", version)
}

// maxArchiveBytes bounds a download. A release archive is about 20 MB.
const maxArchiveBytes = 512 << 20

var httpClient = &http.Client{Timeout: 10 * time.Minute}

// downloadRunner fetches target's archive for version from base, checks its
// SHA-256 against the release's checksum manifest, and writes the archive's
// runner of variant v (nomi-vm, or nomi-vm-reactor) to dest, and for js/wasm
// its wasm_exec.js beside dest first. Nothing is written to dest unless
// every check passes.
func downloadRunner(base, version string, target Target, v Variant, dest string, progress io.Writer) error {
	archive := releaseArchiveName(version, target)
	archiveURL := base + "/" + version + "/" + archive
	sumsURL := base + "/" + version + "/" + releaseChecksumsName(version)
	offline := func(url string, err error) error {
		advice := "Connect to the network and re-run"
		var status *httpStatusError
		if errors.As(err, &status) {
			advice = fmt.Sprintf("The server answered, so check that %s is a published release with an "+
				"archive for %s", version, target)
		}
		return fmt.Errorf("ffirun: no %s for %s is installed, and downloading it from the %s release failed: "+
			"%s: %v. `nomi build` fetches nomi-vm for a platform other than the installed one from the "+
			"release this nomi was cut as. %s, or unpack %s from %s yourself into %s",
			v.binaryName(), target, version, url, err, advice, v.binaryName(), archiveURL, filepath.Dir(dest))
	}
	if progress != nil {
		fmt.Fprintf(progress, "nomi build: downloading %s for %s from %s\n", v.binaryName(), target, archiveURL)
	}
	sums, err := httpGet(sumsURL, 1<<20)
	if err != nil {
		return offline(sumsURL, err)
	}
	want, ok := checksumFor(sums, archive)
	if !ok {
		return fmt.Errorf("ffirun: the %s release's checksum file %s lists no %s", version, sumsURL, archive)
	}
	data, err := httpGet(archiveURL, maxArchiveBytes)
	if err != nil {
		return offline(archiveURL, err)
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != want {
		return fmt.Errorf("ffirun: %s does not match the release's checksum file %s: the file says sha256 %s, "+
			"the download is %s. Nothing was installed", archiveURL, sumsURL, want, got)
	}
	name := exeName(v.binaryName(), target.GOOS)
	var runner []byte
	if target.GOOS == "windows" {
		runner, err = extractZip(data, name)
	} else {
		runner, err = extractTarGz(data, name)
	}
	if err == errNotInArchive {
		err = fmt.Errorf("the archive holds no %s", name)
	}
	if err != nil {
		return fmt.Errorf("ffirun: reading %s: %w", archiveURL, err)
	}
	if target.needsWasmExec() {
		js, err := extractTarGz(data, WasmExecName)
		if err == errNotInArchive {
			err = fmt.Errorf("the archive holds no %s", WasmExecName)
		}
		if err != nil {
			return fmt.Errorf("ffirun: reading %s: %w", archiveURL, err)
		}
		if err := writeFileAtomic(filepath.Join(filepath.Dir(dest), WasmExecName), js, 0o644); err != nil {
			return err
		}
	}
	return writeFileAtomic(dest, runner, 0o755)
}

// httpStatusError is a server that answered with something other than 200.
type httpStatusError struct{ status string }

func (e *httpStatusError) Error() string { return "HTTP " + e.status }

func httpGet(url string, limit int64) ([]byte, error) {
	resp, err := httpClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, &httpStatusError{resp.Status}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("larger than %d bytes", limit)
	}
	return data, nil
}

// checksumFor reads name's SHA-256 out of a `shasum -a 256` manifest, whose
// lines are `<hex>  <name>`, the name possibly `./`-prefixed or `*`-marked.
func checksumFor(manifest []byte, name string) (string, bool) {
	sc := bufio.NewScanner(bytes.NewReader(manifest))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) != 2 {
			continue
		}
		file := strings.TrimPrefix(strings.TrimPrefix(fields[1], "*"), "./")
		if file == name && len(fields[0]) == 64 {
			return strings.ToLower(fields[0]), true
		}
	}
	return "", false
}

// errNotInArchive is an archive without the member asked for.
var errNotInArchive = errors.New("the archive holds no nomi-vm")

// archiveMember reports whether an archive entry's name is the runner at the
// archive's top level.
func archiveMember(entry, name string) bool {
	return path.Clean(strings.TrimPrefix(entry, "./")) == name
}

func extractTarGz(data []byte, name string) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil, errNotInArchive
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag == tar.TypeReg && archiveMember(h.Name, name) {
			return io.ReadAll(io.LimitReader(tr, maxArchiveBytes))
		}
	}
}

func extractZip(data []byte, name string) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	for _, f := range zr.File {
		if !f.FileInfo().IsDir() && archiveMember(f.Name, name) {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(io.LimitReader(rc, maxArchiveBytes))
		}
	}
	return nil, errNotInArchive
}
