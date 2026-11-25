package analysis

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/nomi-language/nomi/internal/pkgcache"
)

// WriteRequirements is data, a nomi.toml's text, with its [dependencies]
// table written as deps (`nomi add`, `remove` and `update`), by
// writeEntries' rules. A new [dependencies] goes after [package]. The
// edited text must parse back to deps, or nothing is written.
//
// Only that table is checked, before and after (pkgcache.ParseManifest).
// The rest of the file is analysis's to check when a command reads the
// project (LoadManifest), so an error there, or one the edit itself
// resolves, such as a tool's table for a dependency the edit adds, never
// blocks an edit.
func WriteRequirements(data []byte, deps []pkgcache.Requirement) ([]byte, error) {
	was, err := pkgcache.ParseManifest(data)
	if err != nil {
		return nil, fmt.Errorf("the file does not parse (%v); edit its [dependencies] table by hand", err)
	}
	entries := func(reqs []pkgcache.Requirement) []tableEntry {
		out := make([]tableEntry, len(reqs))
		for i, d := range reqs {
			out[i] = tableEntry{key: d.Name, value: dependencyValue(d)}
		}
		return out
	}
	edited, err := writeEntries(data, entries(deps), entries(was.Requires))
	if err != nil {
		return nil, err
	}
	m, err := pkgcache.ParseManifest(edited)
	if err != nil {
		return nil, fmt.Errorf("the edited file does not parse (%v); edit its [dependencies] table by hand", err)
	}
	if !slices.EqualFunc(m.Requires, sortedRequirements(deps), pkgcache.Requirement.Same) {
		return nil, errors.New("the edited file does not hold the dependencies written; edit its [dependencies] table by hand")
	}
	return edited, nil
}

// tableEntry is one entry of [dependencies] as writeEntries writes it: its
// key and its value's text.
type tableEntry struct{ key, value string }

// writeEntries is data with [dependencies] written as want, of which the
// text now holds was. Every line outside the table is kept byte for byte,
// and inside it an entry want holds unchanged keeps its lines, an entry
// that changes keeps its comments and the text after its value, and an
// entry want lacks goes with its comments. A new entry goes at the end of
// the table, after a blank line when blank lines separate the table's
// entries. With no table, the table goes after [package], or at the end
// when data has none; a table left with no entry and no comment goes. A
// file with CRLF line endings gets them on each line written. An entry
// written as its own table or with dotted keys can be kept, not changed.
func writeEntries(data []byte, want, was []tableEntry) ([]byte, error) {
	const name = "dependencies"
	text := string(data)
	cr := ""
	if strings.Contains(text, "\r\n") {
		cr = "\r"
	}
	lines := strings.Split(text, "\n")
	wanted := map[string]tableEntry{}
	for _, e := range want {
		wanted[e.key] = e
	}
	old := map[string]tableEntry{}
	for _, e := range was {
		old[e.key] = e
	}
	at := ManifestTableLayout(data, name)
	inline := map[string]bool{}
	for _, run := range at.Body {
		if len(run.Key) == 1 {
			inline[run.Key[0]] = true
		}
	}
	for key, e := range old {
		if w, keep := wanted[key]; !inline[key] && (!keep || w != e) {
			return nil, fmt.Errorf("nomi.toml writes dependency %s as a table of its own or with dotted keys; edit its [%s] by hand", key, name)
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
		e, keep := wanted[run.Key[0]]
		switch {
		case !keep:
			return nil, false
		case e == old[e.key]:
			return runLines(lines, run.First, run.Last), true
		}
		first := strings.TrimSuffix(lines[run.Line-1], "\r")
		last := strings.TrimSuffix(lines[run.Last-1], "\r")
		return append(runLines(lines, run.First, run.Line-1), first[:run.ValueStart]+e.value+last[run.ValueEnd:]), true
	})
	spaced := false
	for _, run := range at.Body {
		if run.Key == nil && slices.ContainsFunc(runLines(lines, run.First, run.Last), isBlank) {
			spaced = true
		}
	}
	var added []string
	for _, e := range want {
		if _, had := old[e.key]; !had {
			added = append(added, e.key+" = "+e.value)
		}
	}
	if spaced && len(added) > 0 && len(body) > 0 {
		body = append(body, "")
	}
	body = append(body, added...)
	var table []string
	if len(body) > 0 {
		header := "[" + name + "]"
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
		prev := ManifestTableLayout(data, "package")
		if prev.Header == 0 {
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
		if prev.Last < len(lines) && !isBlank(lines[prev.Last]) {
			insert = append(insert, cr)
		}
		lines = slices.Concat(lines[:prev.Last], insert, lines[prev.Last:])
	}
	return []byte(strings.Join(lines, "\n")), nil
}

// dependencyValue is d's entry as WriteRequirements writes it: an inline
// table of its repo, subdir, version or commit, dir, effects and
// effects_in_tests, each when it has one, a list on the one line an inline
// table is written on.
func dependencyValue(d pkgcache.Requirement) string {
	var parts []string
	if d.Repo != "" {
		parts = append(parts, fmt.Sprintf("repo = %q", d.Repo))
	}
	if d.Subdir != "" {
		parts = append(parts, fmt.Sprintf("subdir = %q", d.Subdir))
	}
	switch {
	case pkgcache.IsCommit(d.Version):
		parts = append(parts, fmt.Sprintf("commit = %q", d.Version))
	case d.Version != "":
		parts = append(parts, fmt.Sprintf("version = %q", d.Version))
	}
	if d.Dir != "" {
		parts = append(parts, fmt.Sprintf("dir = %q", d.Dir))
	}
	for _, key := range []struct {
		name  string
		names []string
	}{{"effects", d.Effects}, {"effects_in_tests", d.EffectsInTests}} {
		if key.names != nil {
			quoted := make([]string, len(key.names))
			for i, n := range key.names {
				quoted[i] = fmt.Sprintf("%q", n)
			}
			parts = append(parts, key.name+" = ["+strings.Join(quoted, ", ")+"]")
		}
	}
	return "{ " + strings.Join(parts, ", ") + " }"
}

// sortedRequirements is deps sorted by name, as a manifest holds them.
func sortedRequirements(deps []pkgcache.Requirement) []pkgcache.Requirement {
	out := slices.Clone(deps)
	slices.SortFunc(out, func(a, b pkgcache.Requirement) int { return strings.Compare(a.Name, b.Name) })
	return out
}
