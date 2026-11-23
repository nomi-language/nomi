package modcache

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
)

// ManifestName is the file that makes a directory a Nomi module.
const ManifestName = "nomi.toml"

// DependencyEntry is one entry of nomi.toml's [dependencies] table as it
// decodes: `module` and `version` name a module the proxy serves, and
// `dir` a directory that holds the module, relative to the manifest's
// directory. analysis's manifest declares the table with this type, so the
// keys it checks are the ones this package reads.
type DependencyEntry struct {
	Module  string `toml:"module"`
	Version string `toml:"version"`
	Dir     string `toml:"dir"`
}

// Requirement is one checked entry of a [dependencies] table.
type Requirement struct {
	// Name is the entry's key: the name the module's files import the
	// dependency by.
	Name string
	// Module and Version are the module path and the version a consumer
	// of this module takes the dependency at. Version is "" for an entry
	// with only a dir.
	Module, Version string
	// Dir is the dependency's directory as the entry writes it, or "". It
	// stands for the dependency when this module is the one being built or
	// is itself loaded from a directory, and is not read when this module
	// was downloaded, since it names its author's disk.
	Dir string
}

// ModuleFile is what this package reads of a nomi.toml: the module's
// name and path, and its [dependencies] table, sorted by name.
type ModuleFile struct {
	Name     string
	Path     string
	Requires []Requirement
}

// Require answers the requirement named name.
func (f *ModuleFile) Require(name string) (Requirement, bool) {
	for _, r := range f.Requires {
		if r.Name == name {
			return r, true
		}
	}
	return Requirement{}, false
}

// ManifestProblem is an error in the part of a nomi.toml this package
// reads, at the key path Key (`dependencies`, `sqlite`, `version`), so a
// caller holding the text can say its line.
type ManifestProblem struct {
	Key []string
	Msg string
}

func (p *ManifestProblem) Error() string {
	if len(p.Key) > 0 && p.Key[0] == "dependencies" {
		return "[dependencies] " + p.Msg
	}
	return "[module] " + p.Msg
}

// moduleName is the form of a name an import path can start with: a
// [dependencies] key, and the [module].name `nomi new` and `nomi init`
// write.
var moduleName = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

// IsModuleName reports whether name has the form of a name an import path
// can start with: lowercase letters, digits and `_`, not starting with a
// digit.
func IsModuleName(name string) bool { return moduleName.MatchString(name) }

// ParseModuleFile reads data, a nomi.toml's text, for its module name and
// path and its [dependencies] table. The other tables are analysis's to
// check (analysis.LoadManifest). An entry holds `module` and `version`,
// `dir`, or all three; a version is a full semantic version or a
// pseudo-version; two entries may not name one module path, and no entry
// may take the module's own name or `std`.
func ParseModuleFile(data []byte) (*ModuleFile, error) {
	var raw struct {
		Module struct {
			Name string `toml:"name"`
			Path string `toml:"path"`
		} `toml:"module"`
		Dependencies map[string]DependencyEntry `toml:"dependencies"`
	}
	if _, err := toml.Decode(string(data), &raw); err != nil {
		return nil, err
	}
	f := &ModuleFile{Name: raw.Module.Name, Path: raw.Module.Path}
	if f.Path != "" {
		if err := module.CheckPath(f.Path); err != nil {
			return nil, &ManifestProblem{Key: []string{"module", "path"}, Msg: fmt.Sprintf("path %q is not a module path: %v", f.Path, err)}
		}
	}
	names := make([]string, 0, len(raw.Dependencies))
	for name := range raw.Dependencies {
		names = append(names, name)
	}
	sort.Strings(names)
	byPath := map[string]string{}
	for _, name := range names {
		e := raw.Dependencies[name]
		at := func(key string, format string, args ...any) error {
			path := []string{"dependencies", name}
			if key != "" {
				path = append(path, key)
			}
			return &ManifestProblem{Key: path, Msg: fmt.Sprintf(format, args...)}
		}
		switch {
		case !IsModuleName(name):
			return nil, at("", "%q is not a dependency name: a name is lowercase letters, digits and `_`, as an import writes it", name)
		case name == "std":
			return nil, at("", "std is the standard library's name; name the dependency another way")
		case name == f.Name:
			return nil, at("", "%s is this module's own name; name the dependency another way", name)
		case e.Module == "" && e.Version != "":
			return nil, at("version", "%s has a version but no `module`", name)
		case e.Module == "" && e.Dir == "":
			return nil, at("", "%s needs `module` and `version`, or `dir`", name)
		case e.Module != "" && e.Version == "" && e.Dir == "":
			return nil, at("", "%s needs a `version`", name)
		}
		if e.Module != "" {
			if err := module.CheckPath(e.Module); err != nil {
				return nil, at("module", "%s: %q is not a module path: %v", name, e.Module, err)
			}
			if e.Module == f.Path {
				return nil, at("module", "%s is this module's own path", e.Module)
			}
			if other, taken := byPath[e.Module]; taken {
				return nil, at("module", "%s and %s both name %s; keep one", other, name, e.Module)
			}
			byPath[e.Module] = name
		}
		if e.Version != "" {
			if !semver.IsValid(e.Version) || semver.Canonical(e.Version) != e.Version && !module.IsPseudoVersion(e.Version) {
				return nil, at("version", "%s: %q is not a full version such as v1.2.3", name, e.Version)
			}
			if err := module.Check(e.Module, e.Version); err != nil {
				return nil, at("version", "%s: %v", name, err)
			}
		}
		f.Requires = append(f.Requires, Requirement{Name: name, Module: e.Module, Version: e.Version, Dir: e.Dir})
	}
	return f, nil
}

// ReadModuleFile is ParseModuleFile of the nomi.toml in dir, or nil and no
// error when dir has none.
func ReadModuleFile(dir string) (*ModuleFile, error) {
	path := filepath.Join(dir, ManifestName)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	f, err := ParseModuleFile(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return f, nil
}

// LocalDir is r's directory, absolute, for the module rooted at root.
func (r Requirement) LocalDir(root string) string {
	dir := filepath.FromSlash(r.Dir)
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(root, dir)
	}
	return filepath.Clean(dir)
}

// String is r as `nomi add` names it in its output: its module path, or
// its directory.
func (r Requirement) String() string {
	if r.Module != "" {
		return r.Module
	}
	return strings.TrimSuffix(filepath.ToSlash(r.Dir), "/")
}
