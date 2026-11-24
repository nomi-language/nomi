package runners

// Prebuilt runners: how `nomi build` finds a runner without building one,
// the way `deno compile` uses the `denort` that ships with Deno.
//
// The runners come in two tiers, never a combination: the lean one
// (nomi-vm-nonet) has no optional family, and the full one (nomi-vm) has
// every family. A new family joins the full tier and adds no runner.
// std/compiler's runner (nomi-vm-compiler) is the full one with the front
// end linked.
//
// A release `nomi` carries its own platform's two runners, gzipped
// (embeddedRunners, built in by scripts/release.sh with the
// nomi_hostrunners tag). A runner of any variant is looked up in this order
// (Runner):
//
//  1. The runner this `nomi` carries, when the target is its own platform:
//     unpacked once into (4)'s directory and taken from there after.
//  2. The variant's runner beside this `nomi`, and beside the file it
//     resolves to when it is a symlink, when that runner is built for the
//     target.
//  3. Not in a release `nomi`: a source checkout of the compiler. Build the
//     runner with `go build` and cache it, as a development tree always has.
//     A versioned `nomi` that is not a release archive (`go install
//     ...@version`) builds it the same way from that version in the module
//     cache, which Go fetches.
//  4. A runner unpacked or downloaded earlier for this release and target,
//     under the cache root's `nomi-vm/<version>/<goos>_<goarch>/`.
//  5. Download: the archive that holds the variant, from the release this
//     `nomi` was cut as, verified against the release's checksum file, with
//     only that runner extracted into (4).
//
// Every runner taken from a file (2, 4 and 5) must come from the same commit
// as this `nomi` and be the variant asked for: each carries Go's VCS stamp
// and its build tags in its build info, which internal/gobuildinfo reads
// from a binary of any platform, a WebAssembly module included, without
// running it. The tags tell the variants apart (Variant.buildTags), so a
// plain runner is never taken for std/compiler's. A mismatch is refused. In
// a source checkout a mismatched or dirty sibling is passed over instead,
// because step 3 can build the right one. The runner a `nomi` carries was
// built from its commit by construction, so (1) checks only what it finds
// already unpacked.
//
// A release publishes each platform's two runners in an archive of their
// own, `nomi_<version>_<goos>_<goarch>_runners.tar.gz` (releaseArchiveName),
// which another platform's `nomi` downloads for `--target`; the platform's
// main archive holds `nomi` alone. js/wasm and wasip1/wasm, where `nomi`
// does not run, have no main archive. The js/wasm runners archive carries wasm_exec.js beside the runners,
// from the Go that built them, and the download caches both (WasmExec). The
// wasip1 one also carries `nomi-vm-reactor` and `nomi-vm-reactor-nonet`, the
// reactors a program with an `export fn` is appended to (Variant); each
// runner is looked up, cached and checked under its own name, and its build
// info must name its own main package.
//
// On darwin/arm64 the full runner is 17.2 MB stripped and the lean one
// 9.9 MB (6.7 and 3.9 MB gzipped, as `nomi` carries them). std/compiler's
// runners link the whole front end: 38.2 MB (13.8 MB gzipped). They ship in
// an archive of their own per target,
// `nomi_<version>_<goos>_<goarch>_compiler.tar.gz`: `nomi-vm-compiler`, and
// for wasip1 also `nomi-vm-reactor-compiler`, with wasm_exec.js in the
// js/wasm one.

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
	"slices"
	"strings"
	"time"

	"github.com/nomi-language/nomi/internal/artifactcache"
	"github.com/nomi-language/nomi/internal/gobuildinfo"
)

// ReleaseVersion is the release tag this `nomi` was cut as. scripts/release.sh
// sets it with `-ldflags -X github.com/nomi-language/nomi/internal/runners.ReleaseVersion=<tag>`; it is
// empty for every other build. It names the release a cross-target runner is
// downloaded from, which the VCS stamp cannot: a commit is not a tag. When it
// is a release's semantic version, it is also the release a module's
// [module].nomi is held to and the version `nomi add` and `nomi update` take
// the first-party lib/ modules at (cmd/nomi's release, modcache.Release).
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
	// Tags is the -tags it was built with, sorted, which name its variant
	// (Variant.buildTags).
	Tags string
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
		case "-tags":
			id.Tags = sortedTags(s.Value)
		}
	}
	return id
}

// sortedTags is a -tags value with its tags in order, so two spellings of one
// set compare equal.
func sortedTags(tags string) string {
	if tags == "" {
		return ""
	}
	list := strings.Split(tags, ",")
	slices.Sort(list)
	return strings.Join(list, ",")
}

// describeTags names a -tags value in a refusal.
func describeTags(tags string) string {
	if tags == "" {
		return "no build tags"
	}
	return "the tags " + tags
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
// a runner for another platform. A variable so a test can stand in runners
// without building them.
var readIdentity = func(bin string) (buildIdentity, error) {
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
	mainPath, name := v.mainPath(), v.BinaryName()
	if runner.Path != mainPath {
		return &errRunnerMismatch{fmt.Sprintf("%s is not a %s (its main package is %q, want %q)",
			bin, name, runner.Path, mainPath)}
	}
	if want := sortedTags(v.buildTags()); runner.Tags != want {
		return &errRunnerMismatch{fmt.Sprintf("%s is not a %s (it was built with %s, and a %s with %s)",
			bin, name, describeTags(runner.Tags), name, describeTags(want))}
	}
	if runner.GOOS != target.GOOS || runner.GOARCH != target.GOARCH {
		return &errRunnerMismatch{fmt.Sprintf("%s is built for %s/%s, not %s",
			bin, runner.GOOS, runner.GOARCH, target)}
	}
	if self.Revision == "" {
		return &errRunnerMismatch{fmt.Sprintf("this nomi records no source commit, so it cannot check that "+
			"the %s at %s matches it; build nomi from a commit, or from a release", name, bin)}
	}
	if runner.Revision != self.Revision || runner.Modified != self.Modified {
		return &errRunnerMismatch{fmt.Sprintf("the %s at %s was built from %s and this nomi from %s; "+
			"a %s must come from the same commit as the nomi that uses it. "+
			"Install nomi and %s from one release",
			name, bin, runner.commit(), self.commit(), name, name)}
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
	name := exeName(v.BinaryName(), target.GOOS)
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
			return "", fmt.Errorf("reading the %s at %s: %w", v.BinaryName(), bin, err)
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
			return "", fmt.Errorf("%w", err)
		}
		return bin, nil
	}
	return "", nil
}

// embeddedRunner answers the gzipped runner of variant v this `nomi`
// carries for target, or nil: a release `nomi` carries its own platform's
// full and lean runners (embeddedRunners).
func embeddedRunner(target Target, v Variant) []byte {
	if target != HostTarget() {
		return nil
	}
	return embeddedRunners[v.BinaryName()]
}

// unpackRunner writes the gzipped runner gz to bin. A variable so a test can
// count the runners unpacked.
var unpackRunner = func(gz []byte, bin string) error {
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		return err
	}
	data, err := io.ReadAll(zr)
	if err != nil {
		return err
	}
	return writeFileAtomic(bin, data, 0o755)
}

// releaseRunner answers the runner of variant v for target from this
// `nomi`'s release: the cached one when there is one, otherwise the one this
// `nomi` carries, unpacked, or a fresh download. Answering one marks the
// release's directory used (useRelease).
func releaseRunner(target Target, v Variant, progress io.Writer) (string, error) {
	version := ReleaseVersion
	if version == "" {
		return "", fmt.Errorf("`nomi build` needs a %s for %s. None is installed beside this nomi, "+
			"it is a development build whose source tree is gone, so it has nothing to build one from, and it "+
			"is not a release build, so there is no release to download one from. Install nomi from a release "+
			"archive, or set "+compilerSourceEnv+" to a checkout of the compiler", v.BinaryName(), target)
	}
	if strings.ContainsAny(version, `/\`) || strings.Contains(version, "..") {
		return "", fmt.Errorf("release version %q cannot name a cache directory", version)
	}
	root, err := cacheRoot()
	if err != nil {
		return "", err
	}
	releases := filepath.Join(root, "nomi-vm")
	bin, err := releaseRunnerIn(filepath.Join(releases, version, target.GOOS+"_"+target.GOARCH), version, target, v, progress)
	if err != nil {
		return "", err
	}
	useRelease(releases, version)
	return bin, nil
}

// useRelease marks version's directory under releases used
// (artifactcache.Touch), and at most once a day (sweepEvery,
// artifactcache.Due) removes the other releases' directories nothing has
// used for artifactcache.Unused (artifactcache.Prune). A release whose
// directory is removed unpacks or downloads its runners again the next time
// a build needs one. Windows removes no file another process holds open,
// such as a runner a build of that release is copying: the prune leaves
// what it cannot remove, and that release unpacks or downloads what is
// missing when it next needs it.
func useRelease(releases, version string) {
	dir := filepath.Join(releases, version)
	if info, err := os.Stat(dir); err == nil {
		artifactcache.Touch(dir, info)
	}
	if artifactcache.Due(releases, sweepEvery) {
		artifactcache.Prune(releases, version, artifactcache.Unused)
	}
}

// releaseRunnerIn answers the runner of variant v for target in dir, the
// cache's directory for target under version: the one there, else the one
// this `nomi` carries, unpacked, else a fresh download.
func releaseRunnerIn(dir, version string, target Target, v Variant, progress io.Writer) (string, error) {
	bin := filepath.Join(dir, exeName(v.BinaryName(), target.GOOS))
	self := selfIdentity()
	if _, err := os.Stat(bin); err == nil {
		id, err := readIdentity(bin)
		complete := !target.NeedsWasmExec() || fileExists(filepath.Join(dir, WasmExecName))
		if err == nil && complete && checkRunner(self, id, bin, target, v) == nil {
			return bin, nil
		}
		// A damaged or foreign file where the download goes is replaced.
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("creating %s: %w", dir, err)
	}
	if gz := embeddedRunner(target, v); gz != nil {
		if err := unpackRunner(gz, bin); err != nil {
			return "", fmt.Errorf("unpacking the %s this nomi carries into %s: %w", v.BinaryName(), dir, err)
		}
		return bin, nil
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
		return "", fmt.Errorf("the downloaded %s: %w", v.BinaryName(), err)
	}
	return bin, nil
}

func releaseBaseURL() string {
	if v := os.Getenv(releaseBaseURLEnv); v != "" {
		return strings.TrimRight(v, "/")
	}
	return defaultReleaseBaseURL
}

// releaseArchiveName is the archive scripts/release.sh writes for target that
// holds the runner of variant v: the target's runners archive, or for
// std/compiler's variants its compiler archive.
func releaseArchiveName(version string, target Target, v Variant) string {
	ext := ".tar.gz"
	if target.GOOS == "windows" {
		ext = ".zip"
	}
	suffix := "_runners"
	if v.Compiler {
		suffix = "_compiler"
	}
	return fmt.Sprintf("nomi_%s_%s_%s%s%s", version, target.GOOS, target.GOARCH, suffix, ext)
}

// releaseChecksumsName is the release's checksum manifest.
func releaseChecksumsName(version string) string {
	return fmt.Sprintf("nomi_%s_checksums.txt", version)
}

// maxArchiveBytes bounds a download. A runners archive is about 10 MB, and
// a WebAssembly target's compiler archive about 30 MB.
const maxArchiveBytes = 512 << 20

var httpClient = &http.Client{Timeout: 10 * time.Minute}

// downloadRunner fetches the archive holding variant v for target and version
// from base (releaseArchiveName), checks its
// SHA-256 against the release's checksum manifest, and writes the archive's
// runner of variant v (nomi-vm, or nomi-vm-reactor) to dest, and for js/wasm
// its wasm_exec.js beside dest first. Nothing is written to dest unless
// every check passes.
func downloadRunner(base, version string, target Target, v Variant, dest string, progress io.Writer) error {
	archive := releaseArchiveName(version, target, v)
	name := exeName(v.BinaryName(), target.GOOS)
	archiveURL := base + "/" + version + "/" + archive
	sumsURL := base + "/" + version + "/" + releaseChecksumsName(version)
	why := "A release nomi builds no runner: it fetches one that is not installed beside it from the release it was cut as"
	offline := func(url string, err error) error {
		advice := "Connect to the network and re-run"
		var status *httpStatusError
		if errors.As(err, &status) {
			advice = fmt.Sprintf("The server responded, so check that %s is a published release with an "+
				"archive for %s", version, target)
		}
		return fmt.Errorf("no %s for %s is installed, and downloading it from the %s release failed: "+
			"%s: %v. %s. %s, or unpack %s from %s yourself into %s",
			v.BinaryName(), target, version, url, err, why, advice, v.BinaryName(), archiveURL, filepath.Dir(dest))
	}
	if progress != nil {
		fmt.Fprintf(progress, "nomi: downloading %s for %s from %s\n", v.BinaryName(), target, archiveURL)
	}
	sums, err := httpGet(sumsURL, 1<<20)
	if err != nil {
		return offline(sumsURL, err)
	}
	want, ok := checksumFor(sums, archive)
	if !ok {
		return fmt.Errorf("the %s release's checksum file %s lists no %s", version, sumsURL, archive)
	}
	data, err := httpGet(archiveURL, maxArchiveBytes)
	if err != nil {
		return offline(archiveURL, err)
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != want {
		return fmt.Errorf("%s does not match the release's checksum file %s: the file says sha256 %s, "+
			"the download is %s. Nothing was installed", archiveURL, sumsURL, want, got)
	}
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
		return fmt.Errorf("reading %s: %w", archiveURL, err)
	}
	if target.NeedsWasmExec() {
		js, err := extractTarGz(data, WasmExecName)
		if err == errNotInArchive {
			err = fmt.Errorf("the archive holds no %s", WasmExecName)
		}
		if err != nil {
			return fmt.Errorf("reading %s: %w", archiveURL, err)
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
