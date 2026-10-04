package virtualproject

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
)

// Project is an in-memory Nomi project ready for the analyzer/runtime.
type Project struct {
	EntrySource  string
	EntryName    string
	VirtualFiles map[string]string
	Manifest     *analysis.Manifest
}

var (
	fileMarker      = regexp.MustCompile(`^\s*//\s*FILE:\s*([A-Za-z0-9_.\-/]+\.[A-Za-z0-9]+)\s*$`)
	hasTopLevelMain = regexp.MustCompile(`(?m)^fn\s+main\b`)
)

// FromMarkedSource parses a source string carrying `// FILE: <name>` markers.
// With no markers, the whole input is returned as a single-file project named
// "main".
func FromMarkedSource(src string) (Project, error) {
	files := map[string]string{}
	currentName := ""
	firstNomiName := ""
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

	return fromFiles(files, firstNomiName)
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
	for name, body := range files {
		if !isNomiSource(name) {
			return Project{}, fmt.Errorf("unrecognized FILE extension: %q (only .nomi sources and nomi.toml are supported)", name)
		}
		virtualFiles[NormalizeModuleName(name)] = body
	}
	if len(virtualFiles) == 0 {
		virtualFiles = nil
	}

	return Project{
		EntrySource:  entrySource,
		EntryName:    entryName,
		VirtualFiles: virtualFiles,
		Manifest:     manifest,
	}, nil
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
