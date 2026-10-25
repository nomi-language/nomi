package gotoolchain

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

// fakeGoArchive is a gzipped tar laid out as go.dev's archives are, holding
// go/bin/go and go/VERSION.
func fakeGoArchive(t *testing.T, version, script string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	add := func(name string, mode int64, body string) {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	add("go/VERSION", 0o644, version+"\n")
	add("go/bin/go", 0o755, script)
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// serveArchive serves data as name and counts the requests.
func serveArchive(t *testing.T, name string, data []byte) (*httptest.Server, *int32) {
	t.Helper()
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		if r.URL.Path != "/"+name {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func pin(t *testing.T, name, sum string) {
	t.Helper()
	old, had := pinned[name]
	pinned[name] = sum
	t.Cleanup(func() {
		if had {
			pinned[name] = old
		} else {
			delete(pinned, name)
		}
	})
}

func TestInstall_RefusesAnArchiveWhoseChecksumDiffers(t *testing.T) {
	const version = "go1.99.0"
	name := ArchiveName(version, "linux", "amd64")
	data := fakeGoArchive(t, version, "#!/bin/sh\necho fake\n")
	srv, _ := serveArchive(t, name, data)
	t.Setenv(downloadURLEnv, srv.URL)
	// The pinned checksum is of other bytes, as when a mirror or the
	// network serves a modified archive.
	other := sha256.Sum256([]byte("the real archive"))
	pin(t, name, hex.EncodeToString(other[:]))

	root := t.TempDir()
	err := Install(root, version, "linux", "amd64", nil)
	if err == nil {
		t.Fatal("Install accepted an archive whose checksum is not the pinned one")
	}
	for _, want := range []string{"does not match the checksum compiled into this nomi", "nothing was installed"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not say %q: %v", want, err)
		}
	}
	entries, _ := os.ReadDir(root)
	if len(entries) != 0 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("a refused archive left %v in the toolchain cache", names)
	}
}

func TestInstall_RefusesAnArchiveWithNoPinnedChecksum(t *testing.T) {
	err := Install(t.TempDir(), "go1.98.0", "plan9", "386", nil)
	if err == nil || !strings.Contains(err.Error(), "no checksum for go1.98.0.plan9-386.tar.gz is compiled into this nomi") {
		t.Fatalf("Install of an unpinned archive: %v", err)
	}
}

func TestManaged_InstallsOnceThenReusesIt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake archive's go is a shell script")
	}
	version, ok := ManagedVersion()
	if !ok {
		t.Skipf("this test binary was built with %s, which has no archive", version)
	}
	name := ArchiveName(version, runtime.GOOS, runtime.GOARCH)
	data := fakeGoArchive(t, version, "#!/bin/sh\necho managed\n")
	srv, hits := serveArchive(t, name, data)
	t.Setenv(downloadURLEnv, srv.URL)
	sum := sha256.Sum256(data)
	pin(t, name, hex.EncodeToString(sum[:]))
	root := t.TempDir()
	t.Setenv(toolchainRootEnv, root)
	t.Setenv("PATH", "")
	t.Cleanup(func() { downloadAllowed, downloadProgress, downloadOffline = false, nil, false })

	// The language server's process: never downloads.
	_, err := FindForFFIWrapper()
	if err == nil || !strings.Contains(err.Error(), "run `nomi fetch`") {
		t.Fatalf("a process that may not download: %v", err)
	}
	if atomic.LoadInt32(hits) != 0 {
		t.Fatal("a process that may not download fetched the archive")
	}

	// Offline: refused, naming --offline.
	AllowDownload(nil)
	SetOffline(true)
	if _, err := FindForFFIWrapper(); err == nil || !strings.Contains(err.Error(), "--offline") {
		t.Fatalf("an offline process: %v", err)
	}
	SetOffline(false)

	var progress bytes.Buffer
	AllowDownload(&progress)
	got, err := FindForFFIWrapper()
	if err != nil {
		t.Fatalf("FindForFFIWrapper with the managed toolchain allowed: %v", err)
	}
	want := filepath.Join(root, version, "go", "bin", "go")
	if got != want {
		t.Fatalf("the managed go is %s, want %s", got, want)
	}
	if !strings.Contains(progress.String(), "downloading Go "+strings.TrimPrefix(version, "go")) {
		t.Errorf("the download was not announced: %q", progress.String())
	}
	if _, err := FindForBuild(); err != nil {
		t.Fatal(err)
	}
	if n := atomic.LoadInt32(hits); n != 1 {
		t.Fatalf("the archive was fetched %d times, want once", n)
	}
}

func TestFind_NOMIGOWins(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin", exeName("go"))
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(goOverrideEnv, bin)
	if got, err := FindForFFIWrapper(); err != nil || got != bin {
		t.Fatalf("NOMI_GO=%s: got %q, %v", bin, got, err)
	}
	t.Setenv(goOverrideEnv, dir)
	if got, err := FindForFFIWrapper(); err != nil || got != bin {
		t.Fatalf("NOMI_GO=<GOROOT> %s: got %q, %v", dir, got, err)
	}
	t.Setenv(goOverrideEnv, filepath.Join(dir, "missing"))
	if _, err := FindForFFIWrapper(); err == nil || !strings.Contains(err.Error(), "NOMI_GO=") {
		t.Fatalf("a NOMI_GO that does not exist: %v", err)
	}
}

func TestArchivePath_RefusesAnEntryOutsideTheArchive(t *testing.T) {
	for _, name := range []string{"../evil", "go/../../evil", "/etc/passwd"} {
		if _, err := archivePath("/dest", name); err == nil {
			t.Errorf("archivePath accepted %q", name)
		}
	}
	if got, err := archivePath("/dest", "go/bin/go"); err != nil || got != filepath.Join("/dest", "go", "bin", "go") {
		t.Errorf("archivePath(go/bin/go) = %q, %v", got, err)
	}
}
