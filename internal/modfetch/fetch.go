// Package modfetch puts the Go modules a Nomi project needs into the Go
// module cache, through the module proxy protocol and the checksum database,
// with golang.org/x/mod and without the go command (spec §31). A pure-Nomi
// dependency therefore needs no Go toolchain. internal/modcache decides which
// modules a project needs and reads them; this package downloads, verifies
// and extracts the ones the cache lacks, and keeps go.mod and go.sum, the
// lock file, up to date.
//
// Every downloaded module zip is checked against go.sum. A module go.sum does
// not list is checked against the checksum database (GOSUMDB, unless
// GONOSUMDB, GOPRIVATE or GOSUMDB=off exempts it) and, when the command may
// update go.sum (`nomi add`, `nomi fetch`, GOFLAGS=-mod=mod), written there;
// otherwise it is an error that says to run `nomi fetch`, as the go command's
// -mod=readonly default refuses a missing go.sum line. A module GOPROXY sends
// `direct` (GOPRIVATE, GONOPROXY, or a proxy that does not have it) is
// downloaded by the go command, which reads version control.
package modfetch

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
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
	// Update lets the fetch write go.sum lines for modules go.sum does not
	// list yet, and add to go.mod the requirements of a needed Nomi module
	// that it does not list (or raise one it lists at a lower version). `nomi
	// fetch` and `nomi add` set it; so does GOFLAGS=-mod=mod.
	Update bool
	// Log receives one line per download. Nil is silent.
	Log io.Writer
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

	sums      *goSum
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
	sums, err := readGoSum(filepath.Join(root, "go.sum"))
	if err != nil {
		return nil, err
	}
	return &fetcher{
		cfg:     cfg,
		cache:   cache,
		offline: opts.Offline,
		update:  opts.Update || cfg.modMode == "mod",
		log:     opts.Log,
		sums:    sums,
	}, nil
}

// Fetch puts every module the project at root needs into the module cache,
// verifying each, and answers the project's module graph. With Update it also
// writes go.mod (requirements a needed Nomi module brings) and go.sum.
func Fetch(root string, opts Options) (*modcache.Graph, error) {
	f, err := newFetcher(root, opts)
	if err != nil {
		return nil, err
	}
	return f.fetch(root)
}

func (f *fetcher) fetch(root string) (*modcache.Graph, error) {
	for {
		g, err := modcache.Walk(root, f.materialize)
		if err != nil {
			return nil, err
		}
		if g == nil {
			return nil, nil
		}
		if f.update {
			changed, err := f.addRequirements(root, g)
			if err != nil {
				return nil, err
			}
			if changed {
				continue
			}
		}
		if err := f.sums.write(); err != nil {
			return nil, err
		}
		return g, nil
	}
}

// addRequirements writes to go.mod what the needed Nomi modules require and
// go.mod does not list, as indirect requirements, and raises a listed
// requirement a needed Nomi module requires at a higher version: minimal
// version selection over what the Nomi modules need. It reports whether
// go.mod changed.
func (f *fetcher) addRequirements(root string, g *modcache.Graph) (bool, error) {
	want := map[string]string{}
	for _, m := range g.Unlisted {
		want[m.Path] = m.Version
	}
	listed := map[string]string{}
	for _, r := range g.GoMod.Require {
		listed[r.Mod.Path] = r.Mod.Version
	}
	for _, d := range g.Deps {
		if !d.Nomi || d.Dir == "" {
			continue
		}
		reqs, err := readRequires(filepath.Join(d.Dir, "go.mod"))
		if err != nil {
			return false, err
		}
		for _, r := range reqs {
			if have, ok := listed[r.Path]; ok && semver.Compare(r.Version, have) > 0 {
				if cur, ok := want[r.Path]; !ok || semver.Compare(r.Version, cur) > 0 {
					want[r.Path] = r.Version
				}
			}
		}
	}
	if len(want) == 0 {
		return false, nil
	}
	return true, editGoMod(root, func(mf *modfile.File) error {
		for path, v := range want {
			if _, ok := listed[path]; ok {
				if err := mf.AddRequire(path, v); err != nil {
					return err
				}
				continue
			}
			mf.AddNewRequire(path, v, true)
		}
		return nil
	})
}

// materialize answers m's extracted directory, downloading it first when the
// cache lacks it, and checks its zip hash against go.sum either way.
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
	if errors.Is(err, errDirect) {
		return f.downloadDirect(m)
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

// downloadDirect has the go command download m, then checks the hash it
// wrote against go.sum as any download is.
func (f *fetcher) downloadDirect(m module.Version) error {
	d, err := f.goDownload(m)
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

// verifyGoMod checks a downloaded go.mod file against go.sum's /go.mod line.
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
// "/go.mod"), against go.sum, and when go.sum does not list it, against the
// checksum database; an updating fetch then writes it to go.sum. A
// non-updating one refuses a zip go.sum does not list, which is the go
// command's -mod=readonly rule; a go.mod line it may leave out.
func (f *fetcher) verify(m module.Version, suffix, hash string) error {
	key := m.Path + " " + m.Version + suffix
	if listed := f.sums.lines[key]; len(listed) > 0 {
		for _, h := range listed {
			if h == hash {
				return nil
			}
		}
		return fmt.Errorf("verifying %s%s: checksum mismatch\n\tdownloaded: %s\n\tgo.sum:     %s\n\n"+
			"SECURITY ERROR\nThis download does not match the checksum go.sum records for it, so the module, "+
			"the proxy or go.sum has been tampered with. Nothing was installed",
			m.Path+"@"+m.Version, suffix, hash, listed[0])
	}
	if !f.update && suffix == "" {
		return fmt.Errorf("missing go.sum entry for module %s %s; run `nomi fetch` to add it", m.Path, m.Version)
	}
	if f.cfg.checksDB(m.Path) {
		if err := f.checkDB(m, suffix, hash); err != nil {
			return err
		}
	}
	if f.update {
		f.sums.add(key, hash)
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

// Added is what Add put in go.mod.
type Added struct {
	Module module.Version
	// Dir is the module's directory: its extracted copy in the module
	// cache, or the directory AddLocal added.
	Dir string
	// Replace is the replace target AddLocal wrote, relative to the
	// project root; "" for a module from the proxy.
	Replace string
}

// Add requires module arg (`path` or `path@version`; no version, or
// `@latest`, is the latest release) in the project at root's go.mod as a
// direct requirement, with what it requires (at least the versions it asks
// for), downloads what the project then needs, and writes go.sum.
func Add(root, arg string, opts Options) (Added, error) {
	opts.Update = true
	f, err := newFetcher(root, opts)
	if err != nil {
		return Added{}, err
	}
	path, query, _ := strings.Cut(arg, "@")
	if err := module.CheckPath(path); err != nil {
		return Added{}, fmt.Errorf("%s: %w", arg, err)
	}
	switch {
	case query == "" || query == "latest":
	case semver.IsValid(query) && semver.Canonical(query) == query || module.IsPseudoVersion(query):
	case semver.IsValid(query):
		return Added{}, fmt.Errorf("%s: write the full version, %s", arg, semver.Canonical(query))
	default:
		return Added{}, fmt.Errorf("%s: a version is `latest` or a full semantic version such as v1.2.3", arg)
	}
	// A Go package's path names the module that holds it, as `go get`
	// reads one: the longest prefix of it the proxy has.
	var m module.Version
	var modData []byte
	var firstErr error
	for candidate := path; ; {
		m, modData, err = f.resolve(candidate, query)
		if err == nil {
			path = candidate
			break
		}
		var off *offlineError
		if firstErr == nil {
			firstErr = err
		}
		parent := candidate[:max(strings.LastIndex(candidate, "/"), 0)]
		if errors.As(err, &off) || parent == "" || module.CheckPath(parent) != nil {
			return Added{}, firstErr
		}
		candidate = parent
	}
	reqs, err := parseRequires(m.Path+"@"+m.Version+"/go.mod", modData)
	if err != nil {
		return Added{}, err
	}
	if err := editGoMod(root, func(mf *modfile.File) error {
		listed := map[string]*modfile.Require{}
		for _, r := range mf.Require {
			listed[r.Mod.Path] = r
		}
		if mf.Module != nil && mf.Module.Mod.Path == path {
			return fmt.Errorf("%s is this project's own module", path)
		}
		var list []*modfile.Require
		for _, r := range mf.Require {
			if r.Mod.Path == path {
				continue
			}
			list = append(list, &modfile.Require{Mod: r.Mod, Indirect: r.Indirect})
		}
		list = append(list, &modfile.Require{Mod: m})
		for _, r := range reqs {
			if mf.Module != nil && r.Path == mf.Module.Mod.Path {
				continue
			}
			if have, ok := listed[r.Path]; ok {
				if semver.Compare(r.Version, have.Mod.Version) > 0 {
					for _, l := range list {
						if l.Mod.Path == r.Path {
							l.Mod.Version = r.Version
						}
					}
				}
				continue
			}
			list = append(list, &modfile.Require{Mod: r, Indirect: true})
			listed[r.Path] = list[len(list)-1]
		}
		mf.SetRequireSeparateIndirect(list)
		if strings.HasPrefix(m.Path, libPrefix) {
			return alignLib(mf, m.Version)
		}
		return alignLib(mf, "")
	}); err != nil {
		return Added{}, err
	}
	g, err := f.fetch(root)
	if err != nil {
		return Added{}, err
	}
	added := Added{Module: m}
	if g != nil {
		if d, ok := g.Find(path); ok {
			added.Module, added.Dir = d.Version, d.Dir
		}
	}
	return added, nil
}

// libPrefix is the path prefix of the first-party modules under the nomi
// repository's lib/ (the sqlite driver, sqlkit, ...). They are versioned
// by nomi's commits and tested together, and one requires another only as
// a floor, so a project takes them all from one commit.
const libPrefix = "github.com/nomi-language/nomi/lib/"

// alignLib requires every lib/ module go.mod lists at one version: version,
// the one a project just added, else the newest of them, which is the
// latest commit since each is a pseudo-version of a nomi commit.
func alignLib(mf *modfile.File, version string) error {
	if version == "" {
		for _, r := range mf.Require {
			if strings.HasPrefix(r.Mod.Path, libPrefix) && semver.Compare(r.Mod.Version, version) > 0 {
				version = r.Mod.Version
			}
		}
	}
	if version == "" {
		return nil
	}
	for _, r := range mf.Require {
		if strings.HasPrefix(r.Mod.Path, libPrefix) && r.Mod.Version != version {
			if err := mf.AddRequire(r.Mod.Path, version); err != nil {
				return err
			}
		}
	}
	return nil
}

// resolve answers module path at query (`latest` when empty, else a full
// version) and its go.mod.
func (f *fetcher) resolve(path, query string) (module.Version, []byte, error) {
	version := query
	if query == "" || query == "latest" {
		v, err := f.latest(path)
		if err != nil {
			return module.Version{}, nil, fmt.Errorf("finding the latest version of %s: %w", path, err)
		}
		version = v
	}
	m := module.Version{Path: path, Version: version}
	if err := module.Check(m.Path, m.Version); err != nil {
		return module.Version{}, nil, err
	}
	data, err := f.goModOf(m)
	if err != nil {
		return module.Version{}, nil, err
	}
	return m, data, nil
}

// goModOf answers m's go.mod: from the module cache, else the proxy,
// verified either way.
func (f *fetcher) goModOf(m module.Version) ([]byte, error) {
	modPath, err := modcache.VersionFile(f.cache, m, ".mod")
	if err != nil {
		return nil, err
	}
	if data, err := os.ReadFile(modPath); err == nil {
		return data, f.verifyGoMod(m, data)
	}
	ev, err := module.EscapeVersion(m.Version)
	if err != nil {
		return nil, err
	}
	data, err := f.proxyGet(m.Path, "@v/"+ev+".mod")
	if errors.Is(err, errDirect) {
		if err := f.downloadDirect(m); err != nil {
			return nil, err
		}
		return os.ReadFile(modPath)
	}
	if err != nil {
		var off *offlineError
		if errors.As(err, &off) {
			return nil, &offlineError{what: "module " + m.Path + " " + m.Version}
		}
		return nil, fmt.Errorf("reading %s %s's go.mod: %w", m.Path, m.Version, err)
	}
	if err := f.verifyGoMod(m, data); err != nil {
		return nil, err
	}
	if err := f.writable(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(modPath), 0o755); err != nil {
		return nil, err
	}
	return data, writeFileAtomic(modPath, data, 0o644)
}

func readRequires(path string) ([]module.Version, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return parseRequires(path, data)
}

func parseRequires(label string, data []byte) ([]module.Version, error) {
	mf, err := modfile.ParseLax(label, data, nil)
	if err != nil {
		return nil, err
	}
	out := make([]module.Version, 0, len(mf.Require))
	for _, r := range mf.Require {
		out = append(out, r.Mod)
	}
	return out, nil
}

// editGoMod applies edit to root/go.mod and writes it back formatted.
func editGoMod(root string, edit func(*modfile.File) error) error {
	path := filepath.Join(root, "go.mod")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	mf, err := modfile.Parse(path, data, nil)
	if err != nil {
		return err
	}
	if err := edit(mf); err != nil {
		return err
	}
	mf.Cleanup()
	out, err := mf.Format()
	if err != nil {
		return err
	}
	return writeFileAtomic(path, out, 0o644)
}

// goSum is a project's go.sum: each `path version[/go.mod]` key's hashes.
type goSum struct {
	path  string
	lines map[string][]string
	added bool
}

func readGoSum(path string) (*goSum, error) {
	s := &goSum{path: path, lines: map[string][]string{}}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s, nil
		}
		return nil, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 3 {
			continue
		}
		key := fields[0] + " " + fields[1]
		s.lines[key] = append(s.lines[key], fields[2])
	}
	return s, nil
}

func (s *goSum) add(key, hash string) {
	for _, h := range s.lines[key] {
		if h == hash {
			return
		}
	}
	s.lines[key] = append(s.lines[key], hash)
	s.added = true
}

// drop removes the lines of each key keep answers false for.
func (s *goSum) drop(keep func(key string) bool) {
	for k := range s.lines {
		if !keep(k) {
			delete(s.lines, k)
			s.added = true
		}
	}
}

// write writes go.sum back, sorted as the go command sorts it, when lines
// were added.
func (s *goSum) write() error {
	if !s.added {
		return nil
	}
	keys := make([]string, 0, len(s.lines))
	for k := range s.lines {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		pi, vi, _ := strings.Cut(keys[i], " ")
		pj, vj, _ := strings.Cut(keys[j], " ")
		if pi != pj {
			return pi < pj
		}
		mi, mj := strings.TrimSuffix(vi, "/go.mod"), strings.TrimSuffix(vj, "/go.mod")
		if mi != mj {
			return semver.Compare(mi, mj) < 0
		}
		return vi < vj
	})
	var b strings.Builder
	for _, k := range keys {
		for _, h := range s.lines[k] {
			b.WriteString(k + " " + h + "\n")
		}
	}
	return writeFileAtomic(s.path, []byte(b.String()), 0o644)
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
