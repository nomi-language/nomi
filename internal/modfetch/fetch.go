package modfetch

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/mod/module"
	"golang.org/x/mod/sumdb"
	"golang.org/x/mod/sumdb/dirhash"
	modzip "golang.org/x/mod/zip"

	"github.com/nomi-language/nomi/internal/modcache"
)

// Options says what one fetch may do.
type Options struct {
	// Offline refuses the network: a module the cache lacks is an error that
	// names it.
	Offline bool
	// Update lets the fetch select versions from the requirements alone
	// (modcache.Resolve) and write nomi.lock: the build list, and the hash
	// of each module nomi.lock does not hold yet, which the checksum
	// database vouches for. `nomi fetch`, `add`, `remove` and `update` set
	// it; so does GOFLAGS=-mod=mod.
	Update bool
	// Log receives one line per download. Nil is silent.
	Log io.Writer
	// Release is the release a `nomi` was cut as (a semantic version such
	// as v0.5.0), or "" for one built from source. A release takes the
	// first-party lib/ modules at its own version: Add with no version and
	// Update move them there (atRelease).
	Release string
}

// offlineError is a download --offline refused.
type offlineError struct{ what string }

func (e *offlineError) Error() string {
	return e.what + " is not in the module cache, and --offline forbids downloading it"
}

// fetcher is one fetch's state.
type fetcher struct {
	cfg     config
	cache   string
	offline bool
	update  bool
	log     io.Writer
	release string

	// root is the project's directory, which holds nomi.lock.
	root string
	// lock is the project's nomi.lock as read, and hashes the h1: hash of
	// each module version it holds and each one this fetch verified, by
	// `path version`.
	lock      *modcache.Lock
	hashes    map[string]string
	db        *sumdb.Client
	dbOps     *sumdbOps
	dbErr     error
	dbStarted bool
}

func newFetcher(root string, opts Options) (*fetcher, error) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, err
	}
	cache, err := modcache.Root()
	if err != nil {
		return nil, err
	}
	lock, err := modcache.ReadLock(root)
	if err != nil {
		return nil, err
	}
	hashes := map[string]string{}
	for _, m := range lock.Modules {
		hashes[m.Path+" "+m.Version.Version] = m.Hash
	}
	return &fetcher{
		cfg:     cfg,
		cache:   cache,
		offline: opts.Offline,
		update:  opts.Update || cfg.modMode == "mod",
		log:     opts.Log,
		release: opts.Release,
		root:    root,
		lock:    lock,
		hashes:  hashes,
	}, nil
}

// Fetch puts every module the project at root needs into the module cache,
// verifying each against nomi.lock, and answers the project's module graph.
// With Update it selects versions from the requirements and writes
// nomi.lock and the go.mod nomi keeps (WriteGoMod).
func Fetch(root string, opts Options) (*modcache.Graph, error) {
	f, err := newFetcher(root, opts)
	if err != nil {
		return nil, err
	}
	return f.fetch(root)
}

func (f *fetcher) fetch(root string) (*modcache.Graph, error) {
	walk := modcache.Walk
	if f.update {
		walk = modcache.Resolve
	}
	g, err := walk(root, f.materialize)
	if err != nil || g == nil {
		return nil, err
	}
	if f.update {
		if _, err := WriteGoMod(root); err != nil {
			return nil, err
		}
		return g, f.writeLock(g)
	}
	return g, nil
}

// writeLock writes nomi.lock as g's build list: each module from the
// proxy at its selected version, with the hash this fetch verified or
// nomi.lock held, when that differs from the file.
func (f *fetcher) writeLock(g *modcache.Graph) error {
	lock := &modcache.Lock{}
	for _, d := range g.Deps {
		if d.Local || d.Dir == "" {
			continue
		}
		hash, ok := f.hashes[d.Path+" "+d.Version.Version]
		if !ok {
			return fmt.Errorf("no verified hash for module %s %s", d.Path, d.Version.Version)
		}
		lock.Modules = append(lock.Modules, modcache.Locked{Version: d.Version, Hash: hash, Direct: d.Direct})
	}
	path := filepath.Join(f.root, modcache.LockName)
	data := lock.Bytes()
	if old, err := os.ReadFile(path); err == nil && string(old) == string(data) {
		return nil
	}
	if len(lock.Modules) == 0 {
		if _, err := os.Stat(path); err != nil {
			return nil
		}
	}
	return writeFileAtomic(path, data, 0o644)
}

// materialize answers m's extracted directory, downloading it first when the
// cache lacks it, and checks its zip hash against nomi.lock either way.
func (f *fetcher) materialize(m module.Version) (string, error) {
	if dir := modcache.Cached(f.cache, m); dir != "" {
		hashFile, err := modcache.VersionFile(f.cache, m, ".ziphash")
		if err != nil {
			return "", err
		}
		data, err := os.ReadFile(hashFile)
		if err != nil {
			return "", err
		}
		if err := f.verify(m, "", strings.TrimSpace(string(data))); err != nil {
			return "", err
		}
		return dir, nil
	}
	if err := f.download(m); err != nil {
		var off *offlineError
		if errors.As(err, &off) {
			return "", &offlineError{what: "module " + m.Path + " " + m.Version}
		}
		return "", fmt.Errorf("downloading %s %s: %w", m.Path, m.Version, err)
	}
	return modcache.Cached(f.cache, m), nil
}

// download fetches m's go.mod, info and zip into the module cache, verifies
// them, and extracts the zip, in the layout the go command writes and reads.
func (f *fetcher) download(m module.Version) error {
	if err := f.writable(); err != nil {
		return err
	}
	dlDir, err := modcache.DownloadDir(f.cache, m.Path)
	if err != nil {
		return err
	}
	zipPath, _ := modcache.VersionFile(f.cache, m, ".zip")
	modPath, _ := modcache.VersionFile(f.cache, m, ".mod")
	infoPath, _ := modcache.VersionFile(f.cache, m, ".info")
	hashPath, _ := modcache.VersionFile(f.cache, m, ".ziphash")
	ev, err := module.EscapeVersion(m.Version)
	if err != nil {
		return err
	}
	if f.log != nil && !f.offline {
		fmt.Fprintf(f.log, "nomi: downloading %s %s\n", m.Path, m.Version)
	}
	zipData, err := f.proxyGet(m.Path, "@v/"+ev+".zip")
	if d := (*directError)(nil); errors.As(err, &d) {
		return f.downloadDirect(m, d)
	}
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dlDir, 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(modPath); err != nil {
		modData, err := f.proxyGet(m.Path, "@v/"+ev+".mod")
		if err != nil {
			return err
		}
		if err := f.verifyGoMod(m, modData); err != nil {
			return err
		}
		if err := writeFileAtomic(modPath, modData, 0o644); err != nil {
			return err
		}
	}
	if _, err := os.Stat(infoPath); err != nil {
		info, err := f.proxyGet(m.Path, "@v/"+ev+".info")
		if err != nil {
			info, _ = json.Marshal(struct{ Version, Time string }{m.Version, time.Now().UTC().Format(time.RFC3339)})
		}
		if err := writeFileAtomic(infoPath, info, 0o644); err != nil {
			return err
		}
	}
	tmpZip, err := os.CreateTemp(dlDir, ev+".zip.tmp-")
	if err != nil {
		return err
	}
	tmpName := tmpZip.Name()
	defer os.Remove(tmpName)
	if _, err := tmpZip.Write(zipData); err != nil {
		tmpZip.Close()
		return err
	}
	if err := tmpZip.Close(); err != nil {
		return err
	}
	hash, err := dirhash.HashZip(tmpName, dirhash.Hash1)
	if err != nil {
		return fmt.Errorf("hashing the module zip: %w", err)
	}
	if err := f.verify(m, "", hash); err != nil {
		return err
	}
	if err := os.Rename(tmpName, zipPath); err != nil {
		return err
	}
	if err := writeFileAtomic(hashPath, []byte(hash), 0o644); err != nil {
		return err
	}
	return f.extract(m, zipPath)
}

// downloadDirect has the go command download m, which GOPROXY sent
// `direct` (direct), then checks the hash it wrote against nomi.lock as any
// download is.
func (f *fetcher) downloadDirect(m module.Version, direct *directError) error {
	d, err := f.goDownload(m, direct)
	if err != nil {
		return err
	}
	if d.Sum != "" {
		if err := f.verify(m, "", d.Sum); err != nil {
			return err
		}
	}
	if d.GoModSum != "" {
		if err := f.verify(m, "/go.mod", d.GoModSum); err != nil {
			return err
		}
	}
	return nil
}

// extract unzips the verified zip into the module's directory: into a
// temporary sibling first, renamed into place, and made read-only as the go
// command keeps the cache unless GOFLAGS has -modcacherw.
func (f *fetcher) extract(m module.Version, zipPath string) error {
	dir, err := modcache.ExtractedDir(f.cache, m)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(filepath.Dir(dir), filepath.Base(dir)+".tmp-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if err := modzip.Unzip(tmp, m, zipPath); err != nil {
		return fmt.Errorf("extracting the module zip: %w", err)
	}
	if err := os.Rename(tmp, dir); err != nil {
		if modcache.Cached(f.cache, m) != "" {
			return nil // another process extracted it first
		}
		return err
	}
	if !f.cfg.cacheRW {
		_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				defer os.Chmod(path, 0o555)
				return nil
			}
			return os.Chmod(path, 0o444)
		})
	}
	return nil
}

// writable refuses, under `go test`, a module cache the test did not name:
// a test must never write the real one.
func (f *fetcher) writable() error {
	if testing.Testing() && os.Getenv("GOMODCACHE") == "" {
		return errors.New("modfetch: GOMODCACHE is unset under `go test`; set it to a t.TempDir() — " +
			"a test must never write the real module cache")
	}
	return nil
}

// verifyGoMod checks a downloaded go.mod file, which the go command reads
// from the module cache, against the checksum database.
func (f *fetcher) verifyGoMod(m module.Version, data []byte) error {
	hash, err := dirhash.Hash1([]string{"go.mod"}, func(string) (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(string(data))), nil
	})
	if err != nil {
		return err
	}
	return f.verify(m, "/go.mod", hash)
}

// verify checks hash, the h1: hash of m's zip (suffix "") or go.mod (suffix
// "/go.mod"), against nomi.lock, and when nomi.lock does not hold the
// zip's, against the checksum database; an updating fetch then keeps it for
// nomi.lock. A non-updating one refuses a zip nomi.lock does not hold,
// which is the go command's -mod=readonly rule. nomi.lock holds no go.mod
// hash: the checksum database checks a go.mod when it may, and the go
// command checks it again when it reads it.
func (f *fetcher) verify(m module.Version, suffix, hash string) error {
	key := m.Path + " " + m.Version
	if suffix == "" {
		if locked, ok := f.hashes[key]; ok {
			if locked == hash {
				return nil
			}
			return fmt.Errorf("verifying %s%s: checksum mismatch\n\tdownloaded: %s\n\tnomi.lock:  %s\n\n"+
				"SECURITY ERROR\nThis download does not match the checksum nomi.lock records for it, so the module, "+
				"the proxy or nomi.lock has been tampered with. Nothing was installed",
				m.Path+"@"+m.Version, suffix, hash, locked)
		}
		if !f.update {
			return fmt.Errorf("nomi.lock has no entry for module %s %s; run `nomi fetch` to add it", m.Path, m.Version)
		}
	} else if !f.update {
		return nil
	}
	if f.cfg.checksDB(m.Path) {
		if err := f.checkDB(m, suffix, hash); err != nil {
			return err
		}
	}
	if suffix == "" {
		f.hashes[key] = hash
	}
	return nil
}

// checkDB looks key up in the checksum database.
func (f *fetcher) checkDB(m module.Version, suffix, hash string) error {
	if !f.dbStarted {
		f.dbStarted = true
		f.db, f.dbOps, f.dbErr = f.newSumDB()
	}
	if f.dbErr != nil {
		return f.dbErr
	}
	if f.db == nil {
		return nil
	}
	lines, err := f.db.Lookup(m.Path, m.Version+suffix)
	if err != nil {
		if errors.Is(err, sumdb.ErrSecurity) {
			return fmt.Errorf("verifying %s@%s%s against the checksum database: SECURITY ERROR\n%s",
				m.Path, m.Version, suffix, f.dbOps.securityText())
		}
		var off *offlineError
		if errors.As(err, &off) {
			return err
		}
		return fmt.Errorf("verifying %s@%s%s against the checksum database %s: %w (GONOSUMDB or GOPRIVATE "+
			"exempts a private module)", m.Path, m.Version, suffix, f.dbOps.name, err)
	}
	want := m.Path + " " + m.Version + suffix + " " + hash
	for _, line := range lines {
		if line == want {
			return nil
		}
	}
	return fmt.Errorf("verifying %s@%s%s: checksum mismatch\n\tdownloaded: %s\n\t%s: %s\n\n"+
		"SECURITY ERROR\nThis download does not match the checksum database, so the module or the proxy "+
		"has been tampered with. Nothing was installed",
		m.Path, m.Version, suffix, hash, f.dbOps.name, strings.Join(lines, "; "))
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Chmod(name, perm); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}
