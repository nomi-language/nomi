package analysis

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/nomi-language/nomi/internal/modcache"
)

// WriteDependencies is data, a nomi.toml's text, with its [dependencies]
// table written as deps (`nomi add`, `remove` and `update`). Every line
// outside the table is kept byte for byte, and inside it an entry deps
// holds unchanged keeps its lines, an entry that changes keeps its
// comments and the text after its value, and an entry deps lacks goes with
// its comments. A new entry goes at the end of the table, after a blank
// line when blank lines separate the table's entries. With no table, the
// table goes after the [module] table, or at the end when there is none;
// a table left with no entry and no comment goes. A file with CRLF line
// endings gets them on each line written. An entry written as its own
// table or with dotted keys can be kept, not changed. The edited text must
// parse back to deps, or nothing is written.
//
// Only the [dependencies] table is checked, before and after
// (modcache.ParseModuleFile). The rest of the file is analysis's to check
// when a command reads the project (LoadManifest), so an error there, or
// one the edit itself resolves, such as a tool's table for a dependency
// the edit adds, never blocks an edit.
func WriteDependencies(data []byte, deps []modcache.Requirement) ([]byte, error) {
	was, err := modcache.ParseModuleFile(data)
	if err != nil {
		return nil, fmt.Errorf("the file does not parse (%v); edit its [dependencies] table by hand", err)
	}
	text := string(data)
	cr := ""
	if strings.Contains(text, "\r\n") {
		cr = "\r"
	}
	lines := strings.Split(text, "\n")
	want := map[string]modcache.Requirement{}
	for _, d := range deps {
		want[d.Name] = d
	}
	old := map[string]modcache.Requirement{}
	for _, d := range was.Requires {
		old[d.Name] = d
	}
	at := ManifestTableLayout(data, "dependencies")
	inline := map[string]bool{}
	for _, run := range at.Body {
		if len(run.Key) == 1 {
			inline[run.Key[0]] = true
		}
	}
	for name, d := range old {
		if w, keep := want[name]; !inline[name] && (!keep || w != d) {
			return nil, fmt.Errorf("nomi.toml writes dependency %s as a table of its own or with dotted keys; edit its [dependencies] by hand", name)
		}
	}
	ended := func(table []string) []string {
		out := make([]string, len(table))
		for i, line := range table {
			out[i] = line + cr
		}
		return out
	}
	body := keptRuns(lines, at.Body, func(run ManifestLines) ([]string, bool) {
		if len(run.Key) != 1 {
			return runLines(lines, run.First, run.Last), true
		}
		d, keep := want[run.Key[0]]
		switch {
		case !keep:
			return nil, false
		case d == old[d.Name]:
			return runLines(lines, run.First, run.Last), true
		}
		first := strings.TrimSuffix(lines[run.Line-1], "\r")
		last := strings.TrimSuffix(lines[run.Last-1], "\r")
		return append(runLines(lines, run.First, run.Line-1), first[:run.ValueStart]+dependencyValue(d)+last[run.ValueEnd:]), true
	})
	spaced := false
	for _, run := range at.Body {
		if run.Key == nil && slices.ContainsFunc(runLines(lines, run.First, run.Last), isBlank) {
			spaced = true
		}
	}
	var added []string
	for _, d := range deps {
		if _, had := old[d.Name]; !had {
			added = append(added, d.Name+" = "+dependencyValue(d))
		}
	}
	if spaced && len(added) > 0 && len(body) > 0 {
		body = append(body, "")
	}
	body = append(body, added...)
	var table []string
	if len(body) > 0 {
		header := "[dependencies]"
		if at.Header > 0 {
			header = strings.TrimSuffix(lines[at.Header-1], "\r")
		}
		table = append([]string{header}, body...)
	}
	switch {
	case at.Header > 0:
		start := at.Header - 1
		if len(table) == 0 {
			// A table that goes takes the blank lines above it along.
			for start > 0 && isBlank(lines[start-1]) {
				start--
			}
		}
		lines = slices.Concat(lines[:start], ended(table), lines[at.Last:])
	case len(table) > 0:
		mod := ManifestTableLayout(data, "module")
		if mod.Header == 0 {
			if text != "" && !strings.HasSuffix(text, "\n") {
				text += cr + "\n"
			}
			if text != "" {
				text += cr + "\n"
			}
			lines = strings.Split(text+strings.Join(ended(table), "\n")+cr+"\n", "\n")
			break
		}
		insert := append([]string{cr}, ended(table)...)
		if mod.Last < len(lines) && !isBlank(lines[mod.Last]) {
			insert = append(insert, cr)
		}
		lines = slices.Concat(lines[:mod.Last], insert, lines[mod.Last:])
	}
	edited := []byte(strings.Join(lines, "\n"))
	m, err := modcache.ParseModuleFile(edited)
	if err != nil {
		return nil, fmt.Errorf("the edited file does not parse (%v); edit its [dependencies] table by hand", err)
	}
	if !slices.Equal(m.Requires, sortedRequirements(deps)) {
		return nil, errors.New("the edited file does not hold the dependencies written; edit its [dependencies] table by hand")
	}
	return edited, nil
}

// dependencyValue is d's entry as WriteDependencies writes it: an inline
// table of its module, version and dir, each when it has one.
func dependencyValue(d modcache.Requirement) string {
	var parts []string
	if d.Module != "" {
		parts = append(parts, fmt.Sprintf("module = %q", d.Module))
	}
	if d.Version != "" {
		parts = append(parts, fmt.Sprintf("version = %q", d.Version))
	}
	if d.Dir != "" {
		parts = append(parts, fmt.Sprintf("dir = %q", d.Dir))
	}
	return "{ " + strings.Join(parts, ", ") + " }"
}

// sortedRequirements is deps sorted by name, as a manifest holds them.
func sortedRequirements(deps []modcache.Requirement) []modcache.Requirement {
	out := slices.Clone(deps)
	slices.SortFunc(out, func(a, b modcache.Requirement) int { return strings.Compare(a.Name, b.Name) })
	return out
}
