package modfetch

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/mod/module"
)

// config is the Go environment that decides how modules are fetched. Each
// variable means what it means to the go command
// (https://go.dev/ref/mod#environment-variables).
type config struct {
	proxies []proxyEntry // GOPROXY
	noProxy string       // GONOPROXY, else GOPRIVATE
	noSumDB string       // GONOSUMDB, else GOPRIVATE
	sumdb   string       // GOSUMDB
	modMode string       // GOFLAGS -mod=
	cacheRW bool         // GOFLAGS -modcacherw
}

// proxyEntry is one element of GOPROXY. fallAny is set when the element was
// followed by `|`, so any error from it moves on to the next one; after `,`
// only a "not found" does.
type proxyEntry struct {
	url     string // "direct", "off", or a URL
	fallAny bool
}

const defaultGOPROXY = "https://proxy.golang.org,direct"

func loadConfig() (config, error) {
	c := config{
		proxies: parseGOPROXY(envOr("GOPROXY", defaultGOPROXY)),
		noProxy: envOr("GONOPROXY", os.Getenv("GOPRIVATE")),
		noSumDB: envOr("GONOSUMDB", os.Getenv("GOPRIVATE")),
		sumdb:   envOr("GOSUMDB", "sum.golang.org"),
	}
	for _, f := range strings.Fields(os.Getenv("GOFLAGS")) {
		switch {
		case strings.HasPrefix(f, "-mod="), strings.HasPrefix(f, "--mod="):
			c.modMode = f[strings.Index(f, "=")+1:]
		case f == "-modcacherw" || f == "--modcacherw" || f == "-modcacherw=true":
			c.cacheRW = true
		}
	}
	switch c.modMode {
	case "", "readonly", "mod":
	case "vendor":
		return c, fmt.Errorf("GOFLAGS=-mod=vendor: nomi reads Nomi modules from the module cache and has no vendor mode; unset it")
	default:
		return c, fmt.Errorf("GOFLAGS=-mod=%s: invalid -mod value; want readonly or mod", c.modMode)
	}
	return c, nil
}

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

func parseGOPROXY(s string) []proxyEntry {
	var out []proxyEntry
	for s != "" {
		i := strings.IndexAny(s, ",|")
		elem, sep := s, byte(0)
		if i >= 0 {
			elem, sep, s = s[:i], s[i], s[i+1:]
		} else {
			s = ""
		}
		elem = strings.TrimSpace(elem)
		if elem == "" {
			continue
		}
		out = append(out, proxyEntry{url: strings.TrimSuffix(elem, "/"), fallAny: sep == '|'})
	}
	if len(out) == 0 {
		out = []proxyEntry{{url: "off"}}
	}
	return out
}

// proxiesFor are the GOPROXY entries for module path: only `direct` when
// GONOPROXY (or GOPRIVATE) matches it.
func (c config) proxiesFor(path string) []proxyEntry {
	if c.noProxy != "" && module.MatchPrefixPatterns(c.noProxy, path) {
		return []proxyEntry{{url: "direct"}}
	}
	return c.proxies
}

// checksDB reports whether module path's checksums are checked against the
// checksum database.
func (c config) checksDB(path string) bool {
	if c.sumdb == "off" {
		return false
	}
	return c.noSumDB == "" || !module.MatchPrefixPatterns(c.noSumDB, path)
}
