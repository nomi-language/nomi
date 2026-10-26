package virtualproject

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
)

// Project is an in-memory Nomi project ready for the analyzer/runtime.
type Project struct {
	EntrySource string
	EntryName   string
	// VirtualFiles are the other Nomi sources by module name, and the
	// message catalogs by their path with `.toml` (`locales/pl/cart.toml`).
	VirtualFiles map[string]string
	Manifest     *analysis.Manifest
	// DataFiles are the files a program reads at run time, by their path
	// from the project root: every marked file that is not a Nomi source or
	// nomi.toml (`notes.txt`, `data/prices.csv`), catalogs' `.toml` files
	// included, each ending in one newline unless it is empty (dataText).
	// The tour seeds a block's in-memory file system with them.
	DataFiles map[string]string
	// Files is set for a block that names modules, one with a `go.mod`
	// section: every section by its path from the project root, as the
	// same files on disk would hold them (a data file's text as dataText
	// gives it). Its imports resolve through go.mod's requirements, so it
	// is written as these files in a directory and loaded from there
	// (vmhost.LoadMarked), and VirtualFiles and Manifest are unset.
	// EntryName is then the entry's path (`main.nomi`), and EntrySource
	// its text.
	Files map[string]string
	// First is the path of the source's first section, the tab a reader
	// sees first (`tables.nomi`), when it has any.
	First string
}

var (
	fileMarker      = regexp.MustCompile(`^\s*//\s*FILE:\s*([A-Za-z0-9_.\-/]+\.[A-Za-z0-9]+)\s*$`)
	hasTopLevelMain = regexp.MustCompile(`(?m)^fn\s+main\b`)
)

// FileNames answers the path of each file src's `// FILE:` lines mark, as
// written, in order: none for a source with no markers.
func FileNames(src string) []string {
	var names []string
	for _, line := range strings.Split(src, "\n") {
		if m := fileMarker.FindStringSubmatch(line); m != nil {
			names = append(names, m[1])
		}
	}
	return names
}

// FromMarkedSource parses a source string carrying `// FILE: <name>` markers.
// With no markers, the whole input is returned as a single-file project named
// "main".
func FromMarkedSource(src string) (Project, error) {
	files := map[string]string{}
	currentName := ""
	firstNomiName := ""
	firstName := ""
	var currentBuf strings.Builder
	flush := func() {
		if currentName == "" {
			return
		}
		files[currentName] = currentBuf.String()
		currentBuf.Reset()
	}

	hasMarker := false
	for _, line := range strings.SplitAfter(src, "\n") {
		trimmed := strings.TrimRight(line, "\n")
		if m := fileMarker.FindStringSubmatch(trimmed); m != nil {
			flush()
			currentName = m[1]
			if firstName == "" {
				firstName = currentName
			}
			if firstNomiName == "" && isNomiSource(currentName) {
				firstNomiName = currentName
			}
			hasMarker = true
			continue
		}
		if currentName != "" {
			currentBuf.WriteString(line)
		}
	}
	flush()

	if !hasMarker {
		return Project{EntrySource: src, EntryName: "main"}, nil
	}
	var project Project
	var err error
	if namesModules(files) {
		project, err = fromModuleFiles(files)
	} else {
		project, err = fromFiles(files, firstNomiName)
	}
	project.First = path.Clean(firstName)
	return project, err
}

// namesModules reports whether a block's sections include a `go.mod`, at
// its root or in a module of its own under it.
func namesModules(files map[string]string) bool {
	for name := range files {
		if path.Base(name) == "go.mod" {
			return true
		}
	}
	return false
}

// fromModuleFiles is the project of a block that names modules: its files
// as they would be on disk, and its entry, the first file of the root
// module (one in no directory holding a `nomi.toml` or `go.mod` of its own)
// that declares `fn main`, else the root module's first Nomi file.
func fromModuleFiles(marked map[string]string) (Project, error) {
	files := make(map[string]string, len(marked))
	moduleDirs := map[string]bool{}
	for name, body := range marked {
		clean := path.Clean(name)
		if strings.HasPrefix(clean, "/") || clean == ".." || strings.HasPrefix(clean, "../") {
			return Project{}, fmt.Errorf("// FILE: %s is outside the project", name)
		}
		if _, dup := files[clean]; dup {
			return Project{}, fmt.Errorf("// FILE: %s is marked twice", name)
		}
		if isNomiSource(clean) {
			files[clean] = body
		} else {
			files[clean] = dataText(body)
		}
		if base := path.Base(clean); (base == "nomi.toml" || base == "go.mod") && path.Dir(clean) != "." {
			moduleDirs[path.Dir(clean)] = true
		}
	}
	inRoot := func(name string) bool {
		for dir := path.Dir(name); dir != "."; dir = path.Dir(dir) {
			if moduleDirs[dir] {
				return false
			}
		}
		return true
	}
	entry := ""
	for _, name := range sortedFileNames(files) {
		if isNomiSource(name) && inRoot(name) && hasTopLevelMain.MatchString(files[name]) {
			entry = name
			break
		}
	}
	if entry == "" {
		for _, name := range sortedFileNames(files) {
			if isNomiSource(name) && inRoot(name) {
				entry = name
				break
			}
		}
	}
	if entry == "" {
		return Project{}, fmt.Errorf("no Nomi file is in the block's root module")
	}
	data := map[string]string{}
	for name, body := range files {
		if !isNomiSource(name) && name != "nomi.toml" {
			data[name] = body
		}
	}
	return Project{EntrySource: files[entry], EntryName: entry, DataFiles: data, Files: files}, nil
}

// FromSources builds a virtual project from a map keyed by import path without
// `.nomi`, as exposed by std/compiler.compiler.Project.
func FromSources(entryPoint string, sources map[string]string, manifest *analysis.Manifest) (Project, error) {
	entryKey := NormalizeModuleName(entryPoint)
	normalized := make(map[string]string, len(sources))
	for name, source := range sources {
		normalized[NormalizeModuleName(name)] = source
	}
	entrySource, ok := normalized[entryKey]
	if !ok {
		return Project{}, fmt.Errorf("entry %q is not present in files", entryKey)
	}

	delete(normalized, entryKey)
	if len(normalized) == 0 {
		normalized = nil
	}
	return Project{
		EntrySource:  entrySource,
		EntryName:    entryKey,
		VirtualFiles: normalized,
		Manifest:     manifest,
	}, nil
}

func fromFiles(files map[string]string, fallbackEntry string) (Project, error) {
	files = cloneFiles(files)
	var manifest *analysis.Manifest
	if tomlSrc, ok := files["nomi.toml"]; ok {
		m, err := analysis.ParseManifestData([]byte(tomlSrc), "<virtual nomi.toml>")
		if err != nil {
			return Project{}, err
		}
		manifest = m
		delete(files, "nomi.toml")
	}

	entrySource := ""
	entryName := ""
	for _, name := range sortedFileNames(files) {
		body := files[name]
		if !isNomiSource(name) {
			continue
		}
		if hasTopLevelMain.MatchString(body) {
			entrySource = body
			entryName = NormalizeModuleName(name)
			delete(files, name)
			break
		}
	}
	if entrySource == "" {
		if fallbackEntry != "" {
			if body, ok := files[fallbackEntry]; ok && isNomiSource(fallbackEntry) {
				entrySource = body
				entryName = NormalizeModuleName(fallbackEntry)
				delete(files, fallbackEntry)
			}
		}
	}
	if entrySource == "" {
		for _, name := range sortedFileNames(files) {
			body := files[name]
			if isNomiSource(name) {
				entrySource = body
				entryName = NormalizeModuleName(name)
				delete(files, name)
				break
			}
		}
	}
	if entryName == "" {
		entryName = "main"
	}

	virtualFiles := map[string]string{}
	dataFiles := map[string]string{}
	for name, body := range files {
		switch {
		case isNomiSource(name):
			virtualFiles[NormalizeModuleName(name)] = body
		case strings.HasSuffix(name, ".toml"):
			// A message catalog, which the declaring file's expansion
			// reads by its path from the module root.
			virtualFiles[strings.TrimPrefix(name, "./")] = body
			dataFiles[strings.TrimPrefix(name, "./")] = body
		default:
			// A file the program reads at run time.
			dataFiles[strings.TrimPrefix(name, "./")] = dataText(body)
		}
	}
	if len(virtualFiles) == 0 {
		virtualFiles = nil
	}
	if len(dataFiles) == 0 {
		dataFiles = nil
	}

	return Project{
		EntrySource:  entrySource,
		EntryName:    entryName,
		VirtualFiles: virtualFiles,
		Manifest:     manifest,
		DataFiles:    dataFiles,
	}, nil
}

// Write writes a project that names modules (Files) as its files under
// dir, emptying dir first, and answers dir with its symbolic links
// resolved: the directory the front end names the files under.
func (p Project) Write(dir string) (string, error) {
	if p.Files == nil {
		return "", fmt.Errorf("the project names no modules, so it is not written as files")
	}
	if dir == "" {
		return "", fmt.Errorf("a project that names modules is loaded from a directory, and none was given")
	}
	if err := os.RemoveAll(dir); err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	for _, name := range sortedFileNames(p.Files) {
		file := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(file, []byte(p.Files[name]), 0o644); err != nil {
			return "", err
		}
	}
	return root, nil
}

// dataText is a data file's text as its section holds it: the blank lines
// that separate it from the next section are not part of it, and a text
// that is not empty ends with one newline, as a text file does.
func dataText(body string) string {
	text := strings.TrimRight(body, "\n")
	if text == "" {
		return ""
	}
	return text + "\n"
}

// NormalizeModuleName strips syntactic file-system sugar from a project module
// name so it matches import keys.
func NormalizeModuleName(name string) string {
	name = strings.TrimPrefix(name, "./")
	return strings.TrimSuffix(name, ".nomi")
}

func cloneFiles(files map[string]string) map[string]string {
	cloned := make(map[string]string, len(files))
	for name, body := range files {
		cloned[name] = body
	}
	return cloned
}

func sortedFileNames(files map[string]string) []string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func isNomiSource(name string) bool {
	return strings.HasSuffix(name, ".nomi")
}
