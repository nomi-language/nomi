package analysis

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// ErrManifestMissing is returned by LoadManifest when nomi.toml is
// absent. Callers may treat this as "not a Nomi module" or as an
// error depending on context.
var ErrManifestMissing = errors.New("nomi.toml not found")

// Manifest is the in-memory representation of nomi.toml. Fields
// mirror the design doc's TOML schema:
//
//	[module]
//	name = "..."
//	entry_points = [...]
type Manifest struct {
	Name        string   // [module].name
	EntryPoints []string // [module].entry_points (paths under module root, .nomi implicit)
}

// manifestFile is the TOML decoding shape. Separate from Manifest so
// the exported type doesn't expose nested anonymous structs.
type manifestFile struct {
	Module struct {
		Name        string   `toml:"name"`
		EntryPoints []string `toml:"entry_points"`
	} `toml:"module"`
}

// LoadManifest reads nomi.toml from the given directory and returns
// the parsed manifest. Returns ErrManifestMissing if the file doesn't
// exist. Returns a descriptive error if the file is malformed or
// fails the basic schema invariants. See ParseManifestData for the
// validation rules.
func LoadManifest(dir string) (*Manifest, error) {
	path := filepath.Join(dir, "nomi.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrManifestMissing
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return parseManifestData(data, path, isStdlibManifestDir(dir))
}

// ParseManifestData parses nomi.toml content from a byte slice and
// validates the basic schema invariants. The `sourceLabel` is the
// diagnostic prefix used in error messages — typically the file path
// when the data came from disk, or a synthetic label like
// "<virtual nomi.toml>" when staged in memory by the tour playground.
//
// Validation rules:
//
//   - [module].name is required and must not be whitespace-only.
//   - [module].name must not be "std" (reserved for the standard library).
//   - [module].entry_points entries must be non-empty strings.
//   - [module].entry_points must not contain duplicates.
func ParseManifestData(data []byte, sourceLabel string) (*Manifest, error) {
	return parseManifestData(data, sourceLabel, false)
}

func parseManifestData(data []byte, sourceLabel string, allowStdName bool) (*Manifest, error) {
	var raw manifestFile
	if err := toml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse %s: %w", sourceLabel, err)
	}
	if strings.TrimSpace(raw.Module.Name) == "" {
		return nil, fmt.Errorf("%s: [module] name is required (non-empty, non-whitespace)", sourceLabel)
	}
	if raw.Module.Name == "std" && !allowStdName {
		return nil, fmt.Errorf("%s: [module] name %q is reserved for the standard library; choose a different name", sourceLabel, raw.Module.Name)
	}
	seen := map[string]bool{}
	for _, e := range raw.Module.EntryPoints {
		if e == "" {
			return nil, fmt.Errorf("%s: entry_points contains an empty string — every entry must name a file", sourceLabel)
		}
		if seen[e] {
			return nil, fmt.Errorf("%s: entry_points contains duplicate %q", sourceLabel, e)
		}
		seen[e] = true
	}
	return &Manifest{
		Name:        raw.Module.Name,
		EntryPoints: append([]string(nil), raw.Module.EntryPoints...),
	}, nil
}

// IsForeignStdlibDir reports whether dir is the root of a standard library
// other than this binary's: its nomi.toml names the module "std", and it is
// not StdlibPath(). Another Nomi checkout's std is one.
func IsForeignStdlibDir(dir string) bool {
	data, err := os.ReadFile(filepath.Join(dir, "nomi.toml"))
	if err != nil {
		return false
	}
	var raw manifestFile
	if err := toml.Unmarshal(data, &raw); err != nil {
		return false
	}
	return raw.Module.Name == "std" && !isStdlibManifestDir(dir)
}

func isStdlibManifestDir(dir string) bool {
	stdPath, err := StdlibPath()
	if err != nil {
		return false
	}
	return sameDir(dir, stdPath)
}

func sameDir(a, b string) bool {
	absA, err := filepath.Abs(a)
	if err != nil {
		return false
	}
	absB, err := filepath.Abs(b)
	if err != nil {
		return false
	}
	if realA, err := filepath.EvalSymlinks(absA); err == nil {
		absA = realA
	}
	if realB, err := filepath.EvalSymlinks(absB); err == nil {
		absB = realB
	}
	return absA == absB
}
