package modfetch

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"

	"github.com/nomi-language/nomi/internal/gotoolchain"
)

// errNotFound is a proxy's 404 or 410, which a `,` in GOPROXY falls through.
var errNotFound = errors.New("not found")

// errDirect says GOPROXY reached `direct`: the module comes from its version
// control repository, which only the go command can read.
var errDirect = errors.New("direct")

// maxDownload bounds one downloaded file; Go's module zips are at most 500 MB.
const maxDownload = 500 << 20

var httpClient = &http.Client{Timeout: 5 * time.Minute}

// proxyGet fetches name ("@v/list", "@latest", "@v/<version>.zip", ...) for
// module path through GOPROXY, as the go command walks its list.
func (f *fetcher) proxyGet(path, name string) ([]byte, error) {
	if f.offline {
		return nil, &offlineError{what: path}
	}
	enc, err := module.EscapePath(path)
	if err != nil {
		return nil, err
	}
	var errs []string
	for _, p := range f.cfg.proxiesFor(path) {
		switch p.url {
		case "off":
			if len(errs) == 0 {
				return nil, fmt.Errorf("module lookup disabled by GOPROXY=off")
			}
			return nil, fmt.Errorf("%s", strings.Join(errs, "; "))
		case "direct":
			return nil, errDirect
		}
		data, err := readURL(p.url + "/" + enc + "/" + name)
		if err == nil {
			return data, nil
		}
		errs = append(errs, err.Error())
		if !p.fallAny && !errors.Is(err, errNotFound) {
			return nil, err
		}
	}
	if len(errs) == 0 {
		return nil, fmt.Errorf("GOPROXY lists no proxy")
	}
	return nil, fmt.Errorf("%s", strings.Join(errs, "; "))
}

// readURL reads an http(s) or file URL.
func readURL(raw string) ([]byte, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	switch u.Scheme {
	case "file":
		path := u.Path
		if u.Host != "" { // file://host/path is not a local path
			return nil, fmt.Errorf("%s: a file URL names no host", raw)
		}
		data, err := os.ReadFile(filepath.FromSlash(path))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil, fmt.Errorf("reading %s: %w", raw, errNotFound)
			}
			return nil, fmt.Errorf("reading %s: %w", raw, err)
		}
		return data, nil
	case "http", "https":
		resp, err := httpClient.Get(raw)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", raw, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
			detail := strings.TrimSpace(string(body))
			if detail != "" {
				return nil, fmt.Errorf("reading %s: %s: %w", raw, detail, errNotFound)
			}
			return nil, fmt.Errorf("reading %s: %s: %w", raw, resp.Status, errNotFound)
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("reading %s: %s", raw, resp.Status)
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, maxDownload+1))
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", raw, err)
		}
		if len(data) > maxDownload {
			return nil, fmt.Errorf("reading %s: larger than %d bytes", raw, maxDownload)
		}
		return data, nil
	}
	return nil, fmt.Errorf("GOPROXY entry %q: unsupported scheme %q (want https, http or file)", raw, u.Scheme)
}

// latest answers the version `@latest` names for path: the proxy's @latest,
// else the highest release in its version list.
func (f *fetcher) latest(path string) (string, error) {
	data, err := f.proxyGet(path, "@latest")
	if errors.Is(err, errDirect) {
		return f.goLatest(path)
	}
	if err == nil {
		var info struct{ Version string }
		if json.Unmarshal(data, &info) == nil && semver.IsValid(info.Version) {
			return info.Version, nil
		}
	}
	list, listErr := f.proxyGet(path, "@v/list")
	if listErr != nil {
		if err != nil {
			return "", err
		}
		return "", listErr
	}
	best := ""
	for _, v := range strings.Fields(string(list)) {
		if semver.IsValid(v) && semver.Prerelease(v) == "" && (best == "" || semver.Compare(v, best) > 0) {
			best = v
		}
	}
	if best == "" {
		return "", fmt.Errorf("module %s has no release version", path)
	}
	return best, nil
}

// goDownloaded is what `go mod download -json` reports.
type goDownloaded struct {
	Path, Version, Error, Info, GoMod, Zip, Dir, Sum, GoModSum string
}

// goDownload has the go command download m into the module cache, for a
// module GOPROXY sends `direct` (its version control repository). The go
// command verifies it as it does any module.
func (f *fetcher) goDownload(m module.Version) (*goDownloaded, error) {
	out, err := f.goJSON("mod", "download", "-json", m.Path+"@"+m.Version)
	if err != nil {
		return nil, err
	}
	var d goDownloaded
	if err := json.Unmarshal(out, &d); err != nil {
		return nil, fmt.Errorf("reading `go mod download -json %s@%s`: %w", m.Path, m.Version, err)
	}
	if d.Error != "" {
		return nil, fmt.Errorf("%s", d.Error)
	}
	return &d, nil
}

// goLatest asks the go command for path@latest.
func (f *fetcher) goLatest(path string) (string, error) {
	out, err := f.goJSON("list", "-m", "-json", path+"@latest")
	if err != nil {
		return "", err
	}
	var info struct {
		Version string
		Error   *struct{ Err string }
	}
	if err := json.Unmarshal(out, &info); err != nil {
		return "", fmt.Errorf("reading `go list -m -json %s@latest`: %w", path, err)
	}
	if info.Error != nil {
		return "", fmt.Errorf("%s", info.Error.Err)
	}
	return info.Version, nil
}

func (f *fetcher) goJSON(args ...string) ([]byte, error) {
	goBin, err := gotoolchain.FindForModules()
	if err != nil {
		return nil, fmt.Errorf("GOPROXY sends this module to its version control repository (direct), "+
			"which nomi reads through the go command: %w", err)
	}
	cmd := exec.Command(goBin, args...)
	// Outside any module, so no go.mod around the working directory has a say.
	cmd.Dir = os.TempDir()
	cmd.Env = append(os.Environ(), "GOWORK=off")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
	if stdout.Len() > 0 {
		return stdout.Bytes(), nil
	}
	if runErr != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = runErr.Error()
		}
		return nil, fmt.Errorf("go %s: %s", strings.Join(args, " "), detail)
	}
	return stdout.Bytes(), nil
}
