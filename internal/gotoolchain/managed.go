package gotoolchain

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// The managed toolchain.
//
// A `nomi` with no `go` on PATH builds Go bindings with the Go release it was
// built with (runtime.Version), downloaded once from go.dev's official
// archive for this host into <cache>/go/<version>/go, and verified against the
// SHA-256 in pinned (pinned_gen.go, generated from go.dev's release list by
// `go generate ./internal/gotoolchain` and compiled into the binary), never
// against a checksum downloaded beside the archive. The same toolchain builds
// for every --target, since Go cross-compiles and FFI builds are
// CGO_ENABLED=0.

const (
	// toolchainRootEnv replaces <user cache>/nomi/go, where toolchains are
	// kept. Tests set it: under `go test` the real directory is refused.
	toolchainRootEnv = "NOMI_TOOLCHAIN_ROOT"
	// downloadURLEnv replaces https://go.dev/dl/, where archives are
	// downloaded from. Tests point it at a local server.
	downloadURLEnv = "NOMI_GO_DOWNLOAD_URL"
)

// testPin adds one archive checksum, "<archive name>=<sha256>", at link time
// (-ldflags -X), for a test that builds `nomi` and serves it a fake archive.
// A release leaves it empty.
var testPin string

var (
	downloadMu       sync.Mutex
	downloadAllowed  bool
	downloadProgress io.Writer
	downloadOffline  bool
)

// AllowDownload lets this process download the managed toolchain, announcing
// the download on progress. The `nomi` CLI calls it; the language server
// never does, so editing never starts a 70 MB download.
func AllowDownload(progress io.Writer) {
	downloadMu.Lock()
	defer downloadMu.Unlock()
	downloadAllowed, downloadProgress = true, progress
}

// SetOffline makes a download an error that says --offline refused it.
func SetOffline(offline bool) {
	downloadMu.Lock()
	defer downloadMu.Unlock()
	downloadOffline = offline
}

// releasePattern is a Go release's version, as runtime.Version spells it.
var releasePattern = regexp.MustCompile(`^go[1-9][0-9]*\.[0-9]+(\.[0-9]+)?((rc|beta)[0-9]+)?$`)

// ManagedVersion is the Go release the managed toolchain is: the one this
// binary was built with. false for a development Go, which has no archive.
func ManagedVersion() (string, bool) {
	v := runtime.Version()
	if !releasePattern.MatchString(v) {
		return v, false
	}
	return v, true
}

// ArchiveName is go.dev's archive of version for goos/goarch.
func ArchiveName(version, goos, goarch string) string {
	ext := ".tar.gz"
	if goos == "windows" {
		ext = ".zip"
	}
	return version + "." + goos + "-" + goarch + ext
}

//go:generate go run ./pingen -o pinned_gen.go

// PinnedArchives is a copy of the archive checksums compiled into this
// binary, by archive name.
func PinnedArchives() map[string]string {
	out := make(map[string]string, len(pinned))
	for k, v := range pinned {
		out[k] = v
	}
	return out
}

// pinnedArchiveVersion is the Go version an archive name in pinned starts
// with: `1.27.0` in `go1.27.0.darwin-arm64.tar.gz`.
var pinnedArchiveVersion = regexp.MustCompile(`^go([0-9]+\.[0-9]+(?:\.[0-9]+)?(?:(?:rc|beta)[0-9]+)?)\.`)

// GoLine is the version a new project's go.mod names on its `go` line: the
// newest Go release this binary pins, so the managed toolchain can build it.
func GoLine() string {
	best := ""
	for name := range pinned {
		m := pinnedArchiveVersion.FindStringSubmatch(name)
		if m == nil {
			continue
		}
		if best == "" || compareGoVersions(m[1], best) > 0 {
			best = m[1]
		}
	}
	if best == "" {
		return "1.27.0"
	}
	return best
}

// compareGoVersions orders two Go versions such as 1.27.0 and 1.27rc1 by
// their numeric parts; a release sorts after its release candidates.
func compareGoVersions(a, b string) int {
	split := func(v string) ([]int, bool) {
		pre := false
		for _, tag := range []string{"rc", "beta"} {
			if i := strings.Index(v, tag); i >= 0 {
				v, pre = v[:i], true
			}
		}
		var out []int
		for _, p := range strings.Split(v, ".") {
			n := 0
			fmt.Sscanf(p, "%d", &n)
			out = append(out, n)
		}
		for len(out) < 3 {
			out = append(out, 0)
		}
		return out, pre
	}
	pa, preA := split(a)
	pb, preB := split(b)
	for i := range pa {
		if pa[i] != pb[i] {
			if pa[i] < pb[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case preA == preB:
		return strings.Compare(a, b)
	case preA:
		return -1
	}
	return 1
}

// pinnedSum is the SHA-256 this binary trusts for archive.
func pinnedSum(archive string) (string, bool) {
	if name, sum, ok := strings.Cut(testPin, "="); ok && name == archive {
		return sum, true
	}
	sum, ok := pinned[archive]
	return sum, ok
}

// ToolchainRoot is where managed toolchains are kept: NOMI_TOOLCHAIN_ROOT,
// else <user cache>/nomi/go, beside the FFI build cache.
func ToolchainRoot() (string, error) {
	if v := os.Getenv(toolchainRootEnv); v != "" {
		return v, nil
	}
	if testing.Testing() {
		return "", fmt.Errorf("%s is unset under `go test`; a test must never read or write the real toolchain cache",
			toolchainRootEnv)
	}
	uc, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("locating the user cache directory: %w", err)
	}
	return filepath.Join(uc, "nomi", "go"), nil
}

// Managed answers the managed toolchain's `go`, downloading and installing it
// first when this process may (AllowDownload) and it is not installed.
func Managed() (string, error) {
	version, ok := ManagedVersion()
	if !ok {
		return "", fmt.Errorf("this nomi was built with %s, which is not a Go release with an archive to download", version)
	}
	root, err := ToolchainRoot()
	if err != nil {
		return "", err
	}
	bin := managedBin(root, version)
	if isFile(bin) {
		return bin, nil
	}
	downloadMu.Lock()
	allowed, progress, offline := downloadAllowed, downloadProgress, downloadOffline
	downloadMu.Unlock()
	switch {
	case offline:
		return "", fmt.Errorf("%s is not downloaded yet, and --offline forbids downloading it; run `nomi fetch` with a network first", version)
	case !allowed:
		return "", fmt.Errorf("%s is not downloaded yet, and only a command (`nomi run`, `test`, `check`, `build` or `fetch`) downloads it; run `nomi fetch`", version)
	}
	if err := Install(root, version, runtime.GOOS, runtime.GOARCH, progress); err != nil {
		return "", err
	}
	return bin, nil
}

func managedBin(root, version string) string {
	return filepath.Join(root, version, "go", "bin", exeName("go"))
}

var installMu sync.Mutex

// Install downloads version's archive for goos/goarch into root/<version>,
// after checking its SHA-256 against the one compiled into this binary. An
// archive with no pinned checksum, or whose checksum differs, installs
// nothing.
func Install(root, version, goos, goarch string, progress io.Writer) error {
	installMu.Lock()
	defer installMu.Unlock()
	if isFile(managedBin(root, version)) {
		return nil
	}
	archive := ArchiveName(version, goos, goarch)
	want, ok := pinnedSum(archive)
	if !ok {
		return fmt.Errorf("no checksum for %s is compiled into this nomi, so it cannot verify a download of Go %s for %s/%s",
			archive, strings.TrimPrefix(version, "go"), goos, goarch)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	base := os.Getenv(downloadURLEnv)
	if base == "" {
		base = "https://go.dev/dl/"
	}
	url := strings.TrimSuffix(base, "/") + "/" + archive
	tmp, err := os.CreateTemp(root, archive+".tmp-")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	got, err := fetchArchive(url, tmp, progress, version, goos, goarch)
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("the download of %s does not match the checksum compiled into this nomi "+
			"(got sha256 %s, want %s); nothing was installed", url, got, want)
	}
	stage, err := os.MkdirTemp(root, version+".tmp-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if strings.HasSuffix(archive, ".zip") {
		info, err := tmp.Stat()
		if err != nil {
			return err
		}
		err = extractZip(tmp, info.Size(), stage)
		if err != nil {
			return fmt.Errorf("extracting %s: %w", archive, err)
		}
	} else if err := extractTarGz(tmp, stage); err != nil {
		return fmt.Errorf("extracting %s: %w", archive, err)
	}
	if !isFile(filepath.Join(stage, "go", "bin", exeName("go"))) {
		return fmt.Errorf("%s holds no go/bin/%s", archive, exeName("go"))
	}
	dest := filepath.Join(root, version)
	_ = os.RemoveAll(dest)
	return os.Rename(stage, dest)
}

var archiveClient = &http.Client{Timeout: 30 * time.Minute}

// fetchArchive writes url's body to w and answers its SHA-256.
func fetchArchive(url string, w io.Writer, progress io.Writer, version, goos, goarch string) (string, error) {
	resp, err := archiveClient.Get(url)
	if err != nil {
		return "", fmt.Errorf("downloading Go: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("downloading %s: %s", url, resp.Status)
	}
	if progress != nil {
		size := ""
		if resp.ContentLength > 0 {
			size = fmt.Sprintf(" (%.0f MB)", float64(resp.ContentLength)/(1<<20))
		}
		fmt.Fprintf(progress, "nomi: downloading Go %s for %s/%s%s, once, to build Go bindings without a Go installation\n",
			strings.TrimPrefix(version, "go"), goos, goarch, size)
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(w, h), resp.Body); err != nil {
		return "", fmt.Errorf("downloading %s: %w", url, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// archivePath is where an archive entry goes under dest, refusing one that
// would land outside it.
func archivePath(dest, name string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(name))
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("entry %q is outside the archive", name)
	}
	return filepath.Join(dest, clean), nil
}

func extractTarGz(r io.Reader, dest string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		path, err := archivePath(dest, hdr.Name)
		if err != nil {
			return err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(path, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := writeEntry(path, tr, os.FileMode(hdr.Mode).Perm()); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if _, err := archivePath(filepath.Dir(path), hdr.Linkname); err != nil || filepath.IsAbs(hdr.Linkname) {
				return fmt.Errorf("entry %q links outside the archive", hdr.Name)
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(hdr.Linkname, path); err != nil {
				return err
			}
		}
	}
}

func extractZip(r io.ReaderAt, size int64, dest string) error {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return err
	}
	for _, f := range zr.File {
		path, err := archivePath(dest, f.Name)
		if err != nil {
			return err
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(path, 0o755); err != nil {
				return err
			}
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		err = writeEntry(path, rc, f.Mode().Perm())
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func writeEntry(path string, r io.Reader, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if perm == 0 {
		perm = 0o644
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
