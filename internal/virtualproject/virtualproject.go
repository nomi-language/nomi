package virtualproject

import (
	"encoding/base64"
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
	// phrase catalogs by their path with `.toml` (`locales/pl/cart.toml`).
	VirtualFiles map[string]string
	Manifest     *analysis.Manifest
	// DataFiles are the files a program reads at run time, by their path
	// from the project root: every marked file that is not a Nomi source or
	// nomi.toml (`notes.txt`, `data/prices.csv`), catalogs' `.toml` files
	// included, each ending in one newline unless it is empty (dataText),
	// and a `(base64)` section's file holding its decoded bytes.
	// The tour seeds a block's in-memory file system with them.
	DataFiles map[string]string
	// Files is set for a block that names packages (namesPackages): every
	// section by its path from the project root, as the same files on disk
	// would hold them (a data file's text as dataText gives it). Its
	// imports resolve through its nomi.toml's [dependencies], so it
	// is written as these files in a directory and loaded from there
	// (vmhost.LoadMarked), and VirtualFiles and Manifest are unset.
	// EntryName is then the entry's path (`main.nomi`), and EntrySource
	// its text.
	Files map[string]string
	// Dir is set for a block of sibling projects (projectsDir): the
	// top-level directory of the project it runs (`rates`). Files,
	// EntryName, DataFiles and TestFiles still name files by their path
	// from the block's root (`rates/tables.nomi`), as its tabs do.
	Dir string
	// First is the path of the source's first section, the tab a reader
	// sees first (`tables.nomi`), when it has any.
	First string
	// TestFiles are the block's other Nomi files that declare tests (in a
	// block of packages, its root package's), by their marked paths in block
	// order. A block runs their tests after the entry's, as `nomi test`
	// runs every test file of a project: each enters the program on its
	// own (FromMarkedSourceAt).
	TestFiles []string
}

// FileMarker is a block's `// FILE: <path>` line, a directive on a line of
// its own: the path, then ` (base64)` for a file that is not text, whose
// section holds its bytes in standard base64 (line breaks ignored). The
// tour client's FILE_MARKER (tour/src/lib/highlight.mjs) is the same
// expression, which TestFileMarkersMatch (internal/docscheck) holds.
var FileMarker = regexp.MustCompile(`^\s*//\s*FILE:\s*([A-Za-z0-9_.\-/]+\.[A-Za-z0-9]+)(\s+\(base64\))?\s*$`)

var (
	hasTopLevelMain = regexp.MustCompile(`(?m)^fn\s+main\b`)
	// declaresTests matches a file with a top-level `test` or `group`.
	declaresTests = regexp.MustCompile(`(?m)^(test|group)\s+"`)
)

// Marker is the path line's `// FILE:` marker names, and whether the
// section is base64, or ok false for a line that is no marker.
func Marker(line string) (name string, base64 bool, ok bool) {
	m := FileMarker.FindStringSubmatch(line)
	if m == nil {
		return "", false, false
	}
	return m[1], m[2] != "", true
}

// Base64Body is the body of the `// FILE: <path> (base64)` section of a
// file holding data: standard base64, 76 characters a line, each line
// ending in a newline.
func Base64Body(data []byte) string {
	var b strings.Builder
	text := base64.StdEncoding.EncodeToString(data)
	for len(text) > 76 {
		b.WriteString(text[:76] + "\n")
		text = text[76:]
	}
	if text != "" {
		b.WriteString(text + "\n")
	}
	return b.String()
}

// FileNames answers the path of each file src's `// FILE:` lines mark, as
// written, in order: none for a source with no markers.
func FileNames(src string) []string {
	var names []string
	for _, line := range strings.Split(src, "\n") {
		if name, _, ok := Marker(line); ok {
			names = append(names, name)
		}
	}
	return names
}

// FromMarkedSource parses a source string carrying `// FILE: <name>` markers.
// With no markers, the whole input is returned as a single-file project named
// "main".
func FromMarkedSource(src string) (Project, error) {
	return fromMarked(src, "", false)
}

// FromMarkedSourceAsPackages is FromMarkedSource with the block written as
// files whether or not it names packages: a tool of its root package runs in
// the project on disk (vmhost.ToolMarked).
func FromMarkedSourceAsPackages(src string) (Project, error) {
	return fromMarked(src, "", true)
}

// FromMarkedSourceAt is the project of src entered at the marked file
// entry, whatever file declares `fn main`: how a block runs the tests of
// one of its TestFiles.
func FromMarkedSourceAt(src, entry string) (Project, error) {
	if entry == "" {
		return Project{}, fmt.Errorf("no entry given")
	}
	return fromMarked(src, entry, false)
}

func fromMarked(src, forced string, asPackages bool) (Project, error) {
	files := map[string]string{}
	// binary holds the sections marked `(base64)`, whose files hold their
	// decoded bytes as they are: no newline is trimmed or added.
	binary := map[string]bool{}
	currentName := ""
	var nomiNames []string
	firstName := ""
	var order []string
	var currentBuf strings.Builder
	var decodeErr error
	flush := func() {
		if currentName == "" {
			return
		}
		files[currentName] = currentBuf.String()
		currentBuf.Reset()
		if !binary[currentName] {
			return
		}
		data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(files[currentName]))
		switch {
		case isNomiSource(currentName) || strings.HasSuffix(currentName, ".toml"):
			err = fmt.Errorf("// FILE: %s is marked (base64), and a Nomi file or a .toml file is text", currentName)
		case err != nil:
			err = fmt.Errorf("// FILE: %s is marked (base64), and its section is not base64: %v", currentName, err)
		}
		if err != nil && decodeErr == nil {
			decodeErr = err
		}
		files[currentName] = string(data)
	}

	hasMarker := false
	for _, line := range strings.SplitAfter(src, "\n") {
		trimmed := strings.TrimRight(line, "\n")
		if name, b64, ok := Marker(trimmed); ok {
			flush()
			currentName = name
			binary[name] = b64
			order = append(order, currentName)
			if firstName == "" {
				firstName = currentName
			}
			if isNomiSource(currentName) {
				nomiNames = append(nomiNames, currentName)
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
	if decodeErr != nil {
		return Project{}, decodeErr
	}
	if forced != "" {
		if _, ok := files[forced]; !ok || !isNomiSource(forced) {
			return Project{}, fmt.Errorf("// FILE: %s is not a Nomi file of the block", forced)
		}
	}
	var project Project
	var err error
	var entry string
	candidates := nomiNames
	if asPackages || namesPackages(files) {
		project, entry, candidates, err = fromPackageFiles(files, binary, order, forced)
	} else {
		entry = forced
		if entry == "" {
			entry = entryOf(files)
		}
		if entry == "" {
			entry = fallbackEntry(files, nomiNames)
		}
		project, err = fromFiles(files, binary, entry)
		if !isNomiSource(entry) {
			entry = ""
		}
	}
	project.First = path.Clean(firstName)
	if err == nil && forced == "" {
		for _, name := range candidates {
			if path.Clean(name) != path.Clean(entry) && declaresTests.MatchString(files[name]) {
				project.TestFiles = append(project.TestFiles, name)
			}
		}
	}
	return project, err
}

// entryOf is a block's file that declares `fn main`, the first by name, or
// "" when none does.
func entryOf(files map[string]string) string {
	for _, name := range sortedFileNames(files) {
		if isNomiSource(name) && hasTopLevelMain.MatchString(files[name]) {
			return name
		}
	}
	return ""
}

// fallbackEntry is the entry of a block with no `fn main`: its first Nomi
// file that declares tests, so a block that shows a solution before its
// test file runs those tests, else its first Nomi file.
func fallbackEntry(files map[string]string, nomiNames []string) string {
	for _, name := range nomiNames {
		if declaresTests.MatchString(files[name]) {
			return name
		}
	}
	if len(nomiNames) > 0 {
		return nomiNames[0]
	}
	return ""
}

// namesPackages reports whether a block names packages: a section is a
// nomi.toml in a directory under its root, a package of its own, or its
// root nomi.toml has a [dependencies] table.
func namesPackages(files map[string]string) bool {
	for name, body := range files {
		clean := path.Clean(name)
		if path.Base(clean) != "nomi.toml" {
			continue
		}
		if path.Dir(clean) != "." || analysis.ManifestTableLayout([]byte(body), "dependencies").Header > 0 {
			return true
		}
	}
	return false
}

// fromPackageFiles is the project of a block that names packages: its files
// as they would be on disk, and its entry, the first file of the root
// package (one in no directory holding a `nomi.toml` of its own)
// that declares `fn main`, else fallbackEntry over the root package's Nomi
// files in the block's order (order is the sections' paths as marked). In
// a block of sibling projects, the root package is the project projectsDir
// picks.
func fromPackageFiles(marked map[string]string, binary map[string]bool, order []string, forced string) (Project, string, []string, error) {
	files := make(map[string]string, len(marked))
	packageDirs := map[string]bool{}
	for name, body := range marked {
		clean := path.Clean(name)
		if strings.HasPrefix(clean, "/") || clean == ".." || strings.HasPrefix(clean, "../") {
			return Project{}, "", nil, fmt.Errorf("// FILE: %s is outside the project", name)
		}
		if _, dup := files[clean]; dup {
			return Project{}, "", nil, fmt.Errorf("// FILE: %s is marked twice", name)
		}
		// A file ends as a file on disk does, with one newline: the blank
		// lines between sections are not part of it.
		files[clean] = dataText(body, binary[name])
		if path.Base(clean) == "nomi.toml" && path.Dir(clean) != "." {
			packageDirs[path.Dir(clean)] = true
		}
	}
	project, err := projectsDir(files, order)
	if err != nil {
		return Project{}, "", nil, err
	}
	rootDir := "."
	if project != "" {
		rootDir = project
	}
	inRoot := func(name string) bool {
		if project != "" && !strings.HasPrefix(name, project+"/") {
			return false
		}
		for dir := path.Dir(name); dir != rootDir; dir = path.Dir(dir) {
			if packageDirs[dir] {
				return false
			}
		}
		return true
	}
	var roots []string
	for _, name := range order {
		if name = path.Clean(name); isNomiSource(name) && inRoot(name) {
			roots = append(roots, name)
		}
	}
	entry := path.Clean(forced)
	if forced == "" {
		entry = ""
		for _, name := range sortedFileNames(files) {
			if isNomiSource(name) && inRoot(name) && hasTopLevelMain.MatchString(files[name]) {
				entry = name
				break
			}
		}
	}
	if entry == "" {
		entry = fallbackEntry(files, roots)
	}
	if entry == "" {
		if project != "" {
			return Project{}, "", nil, fmt.Errorf("no Nomi file is in the block's project %s/", project)
		}
		return Project{}, "", nil, fmt.Errorf("no Nomi file is in the block's root package")
	}
	data := map[string]string{}
	for name, body := range files {
		if !isNomiSource(name) && name != path.Join(rootDir, "nomi.toml") {
			data[name] = body
		}
	}
	return Project{EntrySource: files[entry], EntryName: entry, DataFiles: data, Files: files, Dir: project}, entry, roots, nil
}

// projectsDir is the project directory of a block of sibling projects: one
// with no nomi.toml at its root and a nomi.toml in two or more of its
// top-level directories, each a package of its own beside the others (an
// app, and a library its nomi.toml names with `dir = "../shared"`). Its project is the
// top-level directory of its first section, the file a reader sees first,
// and every file is in one of its top-level directories. The entry rule
// alone cannot pick the project, since a generator's tool declares `fn
// main` too. It answers "" for a block whose root is its project.
func projectsDir(files map[string]string, order []string) (string, error) {
	if _, ok := files["nomi.toml"]; ok {
		return "", nil
	}
	packages := map[string]bool{}
	for name := range files {
		if dir, base := path.Split(name); base == "nomi.toml" && dir != "" && strings.Count(dir, "/") == 1 {
			packages[dir] = true
		}
	}
	if len(packages) < 2 {
		return "", nil
	}
	for _, name := range order {
		if !strings.Contains(path.Clean(name), "/") {
			return "", fmt.Errorf("// FILE: %s is in none of the block's projects: in a block of several projects, each file is in a top-level directory", name)
		}
	}
	dir, _, _ := strings.Cut(path.Clean(order[0]), "/")
	return dir, nil
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

// fromFiles is the project of a block that names no packages, entered at
// entry when it is one of its Nomi files, else at its first Nomi file by
// name.
func fromFiles(files map[string]string, binary map[string]bool, entry string) (Project, error) {
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
	if body, ok := files[entry]; ok && isNomiSource(entry) {
		entrySource = body
		entryName = NormalizeModuleName(entry)
		delete(files, entry)
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
			// A phrase catalog, which the declaring file's expansion
			// reads by its path from the package root.
			virtualFiles[strings.TrimPrefix(name, "./")] = body
			dataFiles[strings.TrimPrefix(name, "./")] = body
		default:
			// A file the program reads at run time.
			dataFiles[strings.TrimPrefix(name, "./")] = dataText(body, binary[name])
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

// Write writes a project that names packages (Files) as its files under
// dir, emptying dir first, and answers dir with its symbolic links
// resolved: the directory the front end names the files under.
func (p Project) Write(dir string) (string, error) {
	if p.Files == nil {
		return "", fmt.Errorf("the project names no packages, so it is not written as files")
	}
	if dir == "" {
		return "", fmt.Errorf("a project that names packages is loaded from a directory, and none was given")
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

// dataText is a file's text as its section holds it: the blank lines
// that separate it from the next section are not part of it, and a text
// that is not empty ends with one newline, as a text file does. A binary
// section's file is its decoded bytes, unchanged.
func dataText(body string, binary bool) string {
	if binary {
		return body
	}
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
