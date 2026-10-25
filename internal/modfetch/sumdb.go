package modfetch

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/mod/sumdb"
)

// knownSumDBs are the checksum databases the go command knows by name, with
// their verifier keys and URLs.
var knownSumDBs = map[string]struct{ key, url string }{
	"sum.golang.org":       {key: "sum.golang.org+033de0ae+Ac4zctda0e5eza+HJyk9SxEdh+s3Ux18htTTAD8OuAn8", url: "https://sum.golang.org"},
	"sum.golang.google.cn": {key: "sum.golang.org+033de0ae+Ac4zctda0e5eza+HJyk9SxEdh+s3Ux18htTTAD8OuAn8", url: "https://sum.golang.google.cn"},
}

// newSumDB is the checksum database GOSUMDB names (`name`, `name+key` or
// `name+key url`), or nil for GOSUMDB=off.
func (f *fetcher) newSumDB() (*sumdb.Client, *sumdbOps, error) {
	spec := strings.Fields(f.cfg.sumdb)
	if len(spec) == 0 || spec[0] == "off" {
		return nil, nil, nil
	}
	key, dbURL := spec[0], ""
	if len(spec) > 1 {
		dbURL = strings.TrimSuffix(spec[1], "/")
	}
	if known, ok := knownSumDBs[key]; ok {
		if dbURL == "" {
			dbURL = known.url
		}
		key = known.key
	}
	name, _, ok := strings.Cut(key, "+")
	if !ok {
		return nil, nil, fmt.Errorf("GOSUMDB=%s: a checksum database other than sum.golang.org needs its key (name+hash+key)", f.cfg.sumdb)
	}
	if dbURL == "" {
		dbURL = "https://" + name
	}
	ops := &sumdbOps{f: f, name: name, key: key, url: dbURL, dir: filepath.Join(f.cache, "cache", "download", "sumdb")}
	client := sumdb.NewClient(ops)
	if f.cfg.noSumDB != "" {
		client.SetGONOSUMDB(f.cfg.noSumDB)
	}
	return client, ops, nil
}

// sumdbOps is the checksum database client's view of the world: the
// database over HTTP, its tiles and lookups cached in the module cache where
// the go command caches them (cache/download/sumdb), and the latest signed
// tree head nomi has seen beside them.
type sumdbOps struct {
	f    *fetcher
	name string
	key  string
	url  string
	dir  string

	mu       sync.Mutex
	security []string
}

func (o *sumdbOps) ReadRemote(path string) ([]byte, error) {
	if o.f.offline {
		return nil, &offlineError{what: "the checksum database " + o.name}
	}
	return readURL(o.url + path)
}

func (o *sumdbOps) ReadConfig(file string) ([]byte, error) {
	if file == "key" {
		return []byte(o.key), nil
	}
	data, err := os.ReadFile(o.configPath(file))
	if os.IsNotExist(err) {
		return nil, nil
	}
	return data, err
}

func (o *sumdbOps) WriteConfig(file string, old, new []byte) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	path := o.configPath(file)
	cur, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if !bytes.Equal(cur, old) {
		return sumdb.ErrWriteConflict
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return writeFileAtomic(path, new, 0o644)
}

// configPath keeps nomi's latest tree head apart from the go command's
// (GOPATH/pkg/sumdb), in the module cache's sumdb directory.
func (o *sumdbOps) configPath(file string) string {
	return filepath.Join(o.dir, filepath.FromSlash(file))
}

func (o *sumdbOps) ReadCache(file string) ([]byte, error) {
	return os.ReadFile(filepath.Join(o.dir, filepath.FromSlash(file)))
}

func (o *sumdbOps) WriteCache(file string, data []byte) {
	path := filepath.Join(o.dir, filepath.FromSlash(file))
	if os.MkdirAll(filepath.Dir(path), 0o755) == nil {
		_ = writeFileAtomic(path, data, 0o644)
	}
}

func (o *sumdbOps) Log(string) {}

func (o *sumdbOps) SecurityError(msg string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.security = append(o.security, msg)
}

func (o *sumdbOps) securityText() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return strings.Join(o.security, "\n")
}
