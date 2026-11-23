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
	"runtime"
	"strings"
	"time"

	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
)

// errNotFound is a proxy's 404 or 410, which a `,` in GOPROXY falls through.
var errNotFound = errors.New("not found")

// directError says GOPROXY reached `direct` for a module: it comes from its
// version control repository, which nomi reads only through the go command
// on PATH (goJSON).
type directError struct {
	// private is set when GONOPROXY, or GOPRIVATE, matched the module.
	private bool
	// tried is what each proxy before `direct` answered.
	tried []string
	// notFound is set when there was a proxy before `direct` and every one
	// answered that it does not have the module or version.
	notFound bool
}

func (e *directError) Error() string { return "GOPROXY reached direct" }

// noGo is the error for module path that GOPROXY sends `direct` when there
// is no `go` on PATH: why it went there, and what to set to serve it from
// a module proxy instead.
func (e *directError) noGo(path string) error {
	why := "GOPROXY names `direct`"
	fix := "set GOPROXY to a module proxy that serves it"
	switch {
	case e.private:
		why = "GONOPROXY, or GOPRIVATE when GONOPROXY is unset, matches it, which sends it `direct`"
		fix = "set GOPROXY to a module proxy that serves it and GONOPROXY=none, so GOPRIVATE no longer skips the proxy"
	case len(e.tried) > 0:
		why = "the proxies GOPROXY lists before `direct` do not have it (" + strings.Join(e.tried, "; ") + ")"
	}
	return fmt.Errorf("module %s: %s, and nomi reads a module's version control repository only through the go command, "+
		"which is not on PATH; %s: an https:// server (credentials go in its URL, https://user:token@host/path) or a "+
		"file:// directory laid out as one, and GONOSUMDB or GOPRIVATE to its path when the checksum database "+
		"cannot know it", path, why, fix)
}

// maxDownload bounds one downloaded file; Go's module zips are at most 500 MB.
const maxDownload = 500 << 20

var httpClient = &http.Client{Timeout: 5 * time.Minute}

// proxyGet fetches name ("@v/list", "@latest", "@v/<version>.zip", ...) for
// module path through GOPROXY, as the go command walks its list. When every
// proxy answered not found, the error wraps errNotFound (missingVersion).
func (f *fetcher) proxyGet(path, name string) ([]byte, error) {
	if f.offline {
		return nil, &offlineError{what: path}
	}
	enc, err := module.EscapePath(path)
	if err != nil {
		return nil, err
	}
	var errs []string
	notFound := true
	for _, p := range f.cfg.proxiesFor(path) {
		switch p.url {
		case "off":
			if len(errs) == 0 {
				return nil, fmt.Errorf("module lookup disabled by GOPROXY=off")
			}
			return nil, &proxyError{answers: errs, notFound: notFound}
		case "direct":
			return nil, &directError{private: f.cfg.private(path), tried: errs, notFound: len(errs) > 0 && notFound}
		}
		data, err := readURL(p.url + "/" + enc + "/" + name)
		if err == nil {
			return data, nil
		}
		errs = append(errs, err.Error())
		notFound = notFound && errors.Is(err, errNotFound)
		if !p.fallAny && !errors.Is(err, errNotFound) {
			return nil, err
		}
	}
	if len(errs) == 0 {
		return nil, fmt.Errorf("GOPROXY lists no proxy")
	}
	return nil, &proxyError{answers: errs, notFound: notFound}
}

// proxyError is what every proxy GOPROXY lists answered, none with the
// file asked for. It is errNotFound when each answered not found.
type proxyError struct {
	answers  []string
	notFound bool
}

func (e *proxyError) Error() string { return strings.Join(e.answers, "; ") }

func (e *proxyError) Is(target error) bool { return e.notFound && target == errNotFound }

// missingVersion reports whether err, from asking GOPROXY for a module
// version, says that every proxy it asked lacks it.
func missingVersion(err error) bool {
	if d := (*directError)(nil); errors.As(err, &d) {
		return d.notFound
	}
	return errors.Is(err, errNotFound)
}

// fileURLPath is the operating system path of a file URL's path. On
// Windows a drive is written after the URL's slash, file:///C:/proxy, so
// that slash is not part of the path.
func fileURLPath(urlPath string, windows bool) string {
	if !windows {
		return urlPath
	}
	if len(urlPath) >= 3 && urlPath[0] == '/' && urlPath[2] == ':' {
		c := urlPath[1]
		if 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' {
			urlPath = urlPath[1:]
		}
	}
	return strings.ReplaceAll(urlPath, "/", `\`)
}

// readURL reads an http(s) or file URL. The user info of an http(s) URL
// (https://user:token@proxy.example/mod) is its credentials, which the
// request sends as basic authentication; nomi reads no .netrc and no
// GOAUTH. An error names the URL with its password left out.
func readURL(raw string) ([]byte, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	shown := u.Redacted()
	switch u.Scheme {
	case "file":
		if u.Host != "" { // file://host/path is not a local path
			return nil, fmt.Errorf("%s: a file URL names no host", shown)
		}
		data, err := os.ReadFile(fileURLPath(u.Path, runtime.GOOS == "windows"))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil, fmt.Errorf("reading %s: %w", shown, errNotFound)
			}
			return nil, fmt.Errorf("reading %s: %w", shown, err)
		}
		return data, nil
	case "http", "https":
		// net/http sends the URL's user info as basic authentication, and
		// its own errors leave the password out.
		resp, err := httpClient.Get(raw)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", shown, errors.Unwrap(err))
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
			detail := strings.TrimSpace(string(body))
			if detail != "" {
				return nil, fmt.Errorf("reading %s: %s: %w", shown, detail, errNotFound)
			}
			return nil, fmt.Errorf("reading %s: %s: %w", shown, resp.Status, errNotFound)
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("reading %s: %s", shown, resp.Status)
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, maxDownload+1))
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", shown, err)
		}
		if len(data) > maxDownload {
			return nil, fmt.Errorf("reading %s: larger than %d bytes", shown, maxDownload)
		}
		return data, nil
	}
	return nil, fmt.Errorf("GOPROXY entry %q: unsupported scheme %q (want https, http or file)", shown, u.Scheme)
}

// latest answers the version `@latest` names for path: the proxy's @latest,
// else the highest release in its version list.
func (f *fetcher) latest(path string) (string, error) {
	data, err := f.proxyGet(path, "@latest")
	if d := (*directError)(nil); errors.As(err, &d) {
		return f.goLatest(path, d)
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
func (f *fetcher) goDownload(m module.Version, direct *directError) (*goDownloaded, error) {
	out, err := f.goJSON(m.Path, direct, "mod", "download", "-json", m.Path+"@"+m.Version)
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
func (f *fetcher) goLatest(path string, d *directError) (string, error) {
	out, err := f.goJSON(path, d, "list", "-m", "-json", path+"@latest")
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

// goJSON runs the go command on PATH with args, for module path, which
// GOPROXY sent `direct` (d), and answers its JSON. With no `go` on PATH it
// is d's error, which says what to set instead (directError.noGo). Only the
// go command on PATH reads version control; nomi has no version control
// reader of its own.
func (f *fetcher) goJSON(path string, d *directError, args ...string) ([]byte, error) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		return nil, d.noGo(path)
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
