package main

// `nomi capabilities` reports what a project's programs reach outside the
// machine, from the roots `nomi check` holds to nomi.toml's
// [capabilities] lists, and with --write writes the tightest table that
// passes. The facts are vmhost's (vmhost.Reached, Reach.Tightest, which
// the same rule as the check derives), so a table it writes is one the
// check passes; this file only prints them and edits nomi.toml.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/capability"
	"github.com/nomi-language/nomi/vmhost"
)

const capabilitiesUsage = "usage: nomi capabilities [--write] [--why <name>] [--format text|json] [path]"

// capabilitiesArgs is `nomi capabilities`' parsed command line.
type capabilitiesArgs struct {
	path  string
	write bool
	json  bool
	why   string
}

func parseCapabilitiesArgs(args []string) (capabilitiesArgs, error) {
	parsed := capabilitiesArgs{path: "."}
	pathSet := false
	value := func(i *int, arg, flag string) (string, bool, error) {
		if v, ok := strings.CutPrefix(arg, flag+"="); ok {
			return v, true, nil
		}
		if arg != flag {
			return "", false, nil
		}
		*i++
		if *i >= len(args) {
			return "", false, fmt.Errorf("nomi capabilities: %s needs a value\n%s", flag, capabilitiesUsage)
		}
		return args[*i], true, nil
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--write" {
			parsed.write = true
			continue
		}
		if v, ok, err := value(&i, arg, "--format"); err != nil {
			return parsed, err
		} else if ok {
			switch v {
			case "text":
			case "json":
				parsed.json = true
			default:
				return parsed, fmt.Errorf("nomi capabilities: unknown format %q; use text or json\n%s", v, capabilitiesUsage)
			}
			continue
		}
		if v, ok, err := value(&i, arg, "--why"); err != nil {
			return parsed, err
		} else if ok {
			parsed.why = v
			continue
		}
		if isFlag(arg) {
			return parsed, unknownFlag("nomi capabilities", arg, capabilitiesUsage)
		}
		if pathSet {
			return parsed, fmt.Errorf("nomi capabilities: unexpected argument %q\n%s", arg, capabilitiesUsage)
		}
		parsed.path, pathSet = arg, true
	}
	if parsed.write && parsed.why != "" {
		return parsed, fmt.Errorf("nomi capabilities: --write and --why do not go together\n%s", capabilitiesUsage)
	}
	return parsed, nil
}

// runCapabilities is `nomi capabilities`, writing its report to w.
func runCapabilities(w io.Writer, args []string) error {
	parsed, err := parseCapabilitiesArgs(args)
	if err != nil {
		return err
	}
	r, err := reachOf(parsed.path)
	if err != nil {
		return err
	}
	if parsed.why != "" {
		return writeWhy(w, r, parsed.why, parsed.json)
	}
	tight := r.Tightest()
	report := capabilitiesReport{r: r, tight: tight, changes: tableChanges(r.Lists, tight)}
	if parsed.write {
		if report.written, err = writeTightest(r, tight, report.changes); err != nil {
			return err
		}
	}
	if parsed.json {
		return report.writeJSON(w)
	}
	if parsed.write {
		report.writeWritten(w)
		return nil
	}
	report.writeText(w)
	return nil
}

// reachOf is what the programs `nomi check path` checks reach, together.
// A program that does not check is reported as `nomi check` reports it,
// and the answer is errCheckFailed.
func reachOf(path string) (*vmhost.Reach, error) {
	files, _, singleFile, err := discoverCheckFiles(path)
	if err != nil {
		return nil, fmt.Errorf("nomi capabilities: %w", pathError(path, err))
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("nomi capabilities: no Nomi files found in %s", path)
	}
	var r *vmhost.Reach
	failed := false
	reported := map[string]bool{}
	for _, file := range files {
		one, err := vmhost.Reached(file, vmhost.WithWarnings(os.Stderr))
		if err != nil {
			failed = true
			if singleFile && ownLines(err) {
				vmhost.WriteFailure(os.Stderr, err)
			} else {
				writeCheckFailure(os.Stderr, file, err, reported)
			}
			continue
		}
		if r == nil {
			r = one
		} else {
			r.Add(one)
		}
	}
	if failed {
		return nil, errCheckFailed
	}
	return r, nil
}

// tableChange is one name a list gains or loses from one table to another:
// List is std, std_in_tests, go, go_in_tests or modules, Module the entry
// under [capabilities.modules] for modules, and Name "" for an entry with
// no names.
type tableChange struct {
	Change string `json:"change"`
	List   string `json:"list"`
	Module string `json:"module,omitempty"`
	Name   string `json:"name"`
}

// label is the list a change is to, as the report prints it.
func (c tableChange) label() string {
	if c.List == "modules" {
		return fmt.Sprintf("modules %q", c.Module)
	}
	return c.List
}

// tableChanges is what to add to and remove from old, nil for no table,
// to make it to: by list, the names to remove after the names to add.
// RequireModuleLists is not compared.
func tableChanges(old, to *vmhost.Capabilities) []tableChange {
	if old == nil {
		old = &vmhost.Capabilities{}
	}
	var adds, removes []tableChange
	diff := func(list, module string, from, into []string) {
		for _, n := range into {
			if !slices.Contains(from, n) {
				adds = append(adds, tableChange{Change: "add", List: list, Module: module, Name: n})
			}
		}
		for _, n := range from {
			if !slices.Contains(into, n) {
				removes = append(removes, tableChange{Change: "remove", List: list, Module: module, Name: n})
			}
		}
	}
	diff("std", "", old.Std, to.Std)
	diff("std_in_tests", "", old.StdInTests, to.StdInTests)
	diff("go", "", old.Go, to.Go)
	diff("go_in_tests", "", old.GoInTests, to.GoInTests)
	var modules []string
	for m := range old.Modules {
		modules = append(modules, m)
	}
	for m := range to.Modules {
		if _, ok := old.Modules[m]; !ok {
			modules = append(modules, m)
		}
	}
	slices.Sort(modules)
	for _, m := range modules {
		from, had := old.Modules[m]
		into, has := to.Modules[m]
		switch {
		case !had && len(into) == 0:
			adds = append(adds, tableChange{Change: "add", List: "modules", Module: m})
		case !has && len(from) == 0:
			removes = append(removes, tableChange{Change: "remove", List: "modules", Module: m})
		default:
			diff("modules", m, from, into)
		}
	}
	return append(adds, removes...)
}

// moduleReach is what one module's own files reach, as the report prints it.
type moduleReach struct {
	Path    string `json:"path"`
	Project bool   `json:"project,omitempty"`
	// Std is each std name the module's files reach; StdTestsOnly those
	// of them only the tests reach.
	Std          []string `json:"std"`
	StdTestsOnly []string `json:"std_tests_only"`
	// BindsGo reports that the module's files bind Go in calls the walks
	// reach, and GoTestsOnly that only the tests reach them.
	BindsGo     bool `json:"binds_go"`
	GoTestsOnly bool `json:"go_tests_only,omitempty"`
}

// moduleReaches is what each module the programs' code comes from reaches,
// the project first, then the rest by path.
func moduleReaches(r *vmhost.Reach) []moduleReach {
	byPath := map[string]*moduleReach{}
	use := func(path string) *moduleReach {
		if u, ok := byPath[path]; ok {
			return u
		}
		u := &moduleReach{Path: path, Project: path == r.Project, Std: []string{}, StdTestsOnly: []string{}}
		byPath[path] = u
		return u
	}
	for _, m := range r.Modules {
		use(m)
	}
	// fromMain is each module and std name, or "go" for a Go binding,
	// that a walk from main reaches.
	type key struct{ module, name string }
	fromMain := map[key]bool{}
	for _, w := range r.Walks {
		for _, c := range w.Calls {
			u := use(r.ModuleOf(c))
			name := c.Name
			if capability.BindsGo(c.Name) {
				u.BindsGo, name = true, "go"
			} else if !slices.Contains(u.Std, c.Name) {
				u.Std = append(u.Std, c.Name)
			}
			if !w.Tests {
				fromMain[key{u.Path, name}] = true
			}
		}
	}
	var out []moduleReach
	for _, u := range byPath {
		slices.Sort(u.Std)
		for _, n := range u.Std {
			if !fromMain[key{u.Path, n}] {
				u.StdTestsOnly = append(u.StdTestsOnly, n)
			}
		}
		u.GoTestsOnly = u.BindsGo && !fromMain[key{u.Path, "go"}]
		out = append(out, *u)
	}
	slices.SortFunc(out, func(a, b moduleReach) int {
		if a.Project != b.Project {
			if a.Project {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Path, b.Path)
	})
	return out
}

// capabilitiesReport is one run's report.
type capabilitiesReport struct {
	r       *vmhost.Reach
	tight   *vmhost.Capabilities
	changes []tableChange
	// written is the text of the tables --write wrote, "" when it wrote
	// nothing, the table being current.
	written string
}

// reportWidth is the width the text report wraps its lists at.
const reportWidth = 80

// wrapped is names after a first line's prefix, joined with ", " and
// wrapped at reportWidth, each later line indented to the prefix's width;
// "(none)" for no names.
func wrapped(prefix string, names []string) string {
	if len(names) == 0 {
		return prefix + "(none)"
	}
	indent := strings.Repeat(" ", len(prefix))
	var b strings.Builder
	line := prefix
	for i, n := range names {
		item := n
		if i < len(names)-1 {
			item += ","
		}
		switch {
		case line == prefix:
			line += item
		case len(line)+1+len(item) > reportWidth:
			b.WriteString(line + "\n")
			line = indent + item
		default:
			line += " " + item
		}
	}
	b.WriteString(line)
	return b.String()
}

// writeText prints what the programs reach: the four lists of the tightest
// table, what each module's own files reach, and how nomi.toml's table
// differs from it.
func (rep capabilitiesReport) writeText(w io.Writer) {
	t := rep.tight
	fmt.Fprintln(w, wrapped("std            ", t.Std))
	fmt.Fprintln(w, wrapped("std_in_tests   ", t.StdInTests))
	fmt.Fprintln(w, wrapped("go             ", t.Go))
	fmt.Fprintln(w, wrapped("go_in_tests    ", t.GoInTests))
	fmt.Fprintln(w)
	fmt.Fprintln(w, "modules")
	for _, u := range moduleReaches(rep.r) {
		name := "  " + u.Path
		if u.Project {
			name += " (this project)"
		}
		fmt.Fprintln(w, name)
		var names []string
		for _, n := range u.Std {
			if slices.Contains(u.StdTestsOnly, n) {
				n += " (tests only)"
			}
			names = append(names, n)
		}
		if len(names) > 0 {
			fmt.Fprintln(w, wrapped("    ", names))
		}
		switch {
		case u.BindsGo && u.GoTestsOnly:
			fmt.Fprintln(w, "    binds Go (tests only)")
		case u.BindsGo:
			fmt.Fprintln(w, "    binds Go")
		case len(names) == 0:
			fmt.Fprintln(w, "    nothing")
		}
	}
	fmt.Fprintln(w)
	switch {
	case rep.r.Lists == nil:
		fmt.Fprintln(w, "nomi.toml has no [capabilities] table; nomi capabilities --write writes this one")
	case len(rep.changes) == 0:
		fmt.Fprintln(w, "nomi.toml's [capabilities] table allows exactly this")
	default:
		fmt.Fprintln(w, "nomi.toml's [capabilities] table differs:")
		rep.writeChanges(w, "missing", "unused")
		if refused := len(rep.r.NotAllowed(rep.r.Lists)); refused > 0 {
			fmt.Fprintf(w, "nomi check refuses %s; nomi capabilities --write writes the table above\n", calls(refused))
		} else {
			fmt.Fprintln(w, "nomi check passes; an unused name allows what nothing here reaches")
		}
	}
	rep.writeDeclaredLess(w)
}

// calls is "1 call" or "n calls".
func calls(n int) string {
	if n == 1 {
		return "1 call"
	}
	return fmt.Sprintf("%d calls", n)
}

// writeChanges prints each change on its own line, an addition labelled
// add and a removal remove.
func (rep capabilitiesReport) writeChanges(w io.Writer, add, remove string) {
	width := 0
	for _, c := range rep.changes {
		width = max(width, len(c.label()))
	}
	for _, c := range rep.changes {
		word := add
		if c.Change == "remove" {
			word = remove
		}
		name := c.Name
		if name == "" {
			name = "(an empty entry)"
		}
		fmt.Fprintf(w, "  %-8s %-*s  %s\n", word, width, c.label(), name)
	}
}

// writeDeclaredLess prints each call a dependency's own nomi.toml does not
// allow, which no project table can: `nomi check` refuses it until that
// module's own table does.
func (rep capabilitiesReport) writeDeclaredLess(w io.Writer) {
	seen := map[string]bool{}
	for _, n := range rep.r.NotAllowed(rep.tight) {
		key := n.Over + "\x00" + n.Name
		if !n.Declared || seen[key] {
			continue
		}
		seen[key] = true
		fmt.Fprintf(w, "%s declares less than its code reaches: its own nomi.toml does not allow %s, and nomi check refuses it until it does\n", n.Over, n.Name)
	}
}

// writeWritten prints what --write did: the tables it wrote and, when
// nomi.toml had a table, each name it added and removed.
func (rep capabilitiesReport) writeWritten(w io.Writer) {
	if rep.written == "" {
		fmt.Fprintln(w, "nomi.toml's [capabilities] table is current")
		return
	}
	fmt.Fprintf(w, "wrote nomi.toml's [capabilities] table:\n\n%s", rep.written)
	if rep.r.Lists != nil {
		fmt.Fprintln(w)
		rep.writeChanges(w, "added", "removed")
	}
	rep.writeDeclaredLess(w)
}

// capabilitiesJSON is `nomi capabilities --format json`.
type capabilitiesJSON struct {
	Project    string        `json:"project"`
	Std        []string      `json:"std"`
	StdInTests []string      `json:"std_in_tests"`
	Go         []string      `json:"go"`
	GoInTests  []string      `json:"go_in_tests"`
	Modules    []moduleReach `json:"modules"`
	// Table is how nomi.toml's table differs, nil when it has none.
	Table *capabilitiesTableJSON `json:"table"`
	// Written reports that --write wrote nomi.toml.
	Written bool `json:"written"`
	// DeclaredLess is each name a dependency's own nomi.toml does not
	// allow its own code.
	DeclaredLess []declaredLessJSON `json:"declared_less"`
}

type capabilitiesTableJSON struct {
	Changes []tableChange `json:"changes"`
	// Refused is how many calls `nomi check` refuses under the table.
	Refused int `json:"refused"`
}

type declaredLessJSON struct {
	Module string `json:"module"`
	Name   string `json:"name"`
}

func (rep capabilitiesReport) writeJSON(w io.Writer) error {
	orEmpty := func(s []string) []string {
		if s == nil {
			return []string{}
		}
		return s
	}
	out := capabilitiesJSON{
		Project: rep.r.Project, Std: orEmpty(rep.tight.Std), StdInTests: orEmpty(rep.tight.StdInTests),
		Go: orEmpty(rep.tight.Go), GoInTests: orEmpty(rep.tight.GoInTests),
		Modules: moduleReaches(rep.r), Written: rep.written != "", DeclaredLess: []declaredLessJSON{},
	}
	if rep.r.Lists != nil {
		out.Table = &capabilitiesTableJSON{Changes: rep.changes, Refused: len(rep.r.NotAllowed(rep.r.Lists))}
		if out.Table.Changes == nil {
			out.Table.Changes = []tableChange{}
		}
	}
	seen := map[declaredLessJSON]bool{}
	for _, n := range rep.r.NotAllowed(rep.tight) {
		d := declaredLessJSON{Module: n.Over, Name: n.Name}
		if n.Declared && !seen[d] {
			seen[d] = true
			out.DeclaredLess = append(out.DeclaredLess, d)
		}
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// whyCall is one call `--why` prints.
type whyCall struct {
	Name   string `json:"name"`
	Module string `json:"module"`
	Site   string `json:"site"`
	Callee string `json:"callee"`
	Tests  bool   `json:"tests"`
	Reach  string `json:"reach"`
}

// writeWhy prints each call that reaches name, a std name (`std::fs`) or a
// `go:` name, or that a module's files make, for a module path, with its
// site and how it is reached, in the words a refusal uses. A name nothing
// reaches is an error that lists what the programs reach.
func writeWhy(w io.Writer, r *vmhost.Reach, name string, asJSON bool) error {
	byName := strings.HasPrefix(name, "std::") || capability.BindsGo(name)
	var found []whyCall
	seen := map[string]bool{}
	var names []string
	for _, wk := range r.Walks {
		for _, c := range wk.Calls {
			if !slices.Contains(names, c.Name) {
				names = append(names, c.Name)
			}
			module := r.ModuleOf(c)
			if byName && c.Name != name || !byName && module != name {
				continue
			}
			key := c.Site + "\x00" + c.Name
			if seen[key] {
				continue
			}
			seen[key] = true
			found = append(found, whyCall{Name: c.Name, Module: module, Site: r.SiteOf(c), Callee: c.Callee, Tests: wk.Tests, Reach: c.ReachText()})
		}
	}
	if len(found) == 0 {
		slices.Sort(names)
		reaches := "nothing outside the machine"
		if len(names) > 0 {
			reaches = strings.Join(names, ", ")
		}
		return fmt.Errorf("nomi capabilities: nothing this program reaches is %s; it reaches %s, and its code comes from %s",
			name, reaches, strings.Join(r.Modules, ", "))
	}
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		return enc.Encode(struct {
			Name  string    `json:"name"`
			Calls []whyCall `json:"calls"`
		}{name, found})
	}
	verb := "reach"
	if len(found) == 1 {
		verb = "reaches"
	}
	switch {
	case byName:
		fmt.Fprintf(w, "%s %s %s:\n", calls(len(found)), verb, name)
	case name == r.Project:
		fmt.Fprintf(w, "%s in this project's own files %s outside the machine:\n", calls(len(found)), verb)
	default:
		fmt.Fprintf(w, "%s in %s %s outside the machine:\n", calls(len(found)), name, verb)
	}
	for _, c := range found {
		where := c.Site
		switch {
		case !byName:
			where += ": " + c.Name
		case c.Module != r.Project:
			where += " in " + c.Module
		}
		fmt.Fprintf(w, "  %s\n    %s\n", where, c.Reach)
	}
	return nil
}

// writeTightest writes tight into the project's nomi.toml as its
// [capabilities] and [capabilities.modules] tables (replaceCapabilityTables),
// unless nomi.toml's table already allows exactly what tight does, and
// answers the tables' text, or "" when it wrote nothing.
func writeTightest(r *vmhost.Reach, tight *vmhost.Capabilities, changes []tableChange) (string, error) {
	if r.Lists != nil && len(changes) == 0 {
		return "", nil
	}
	if r.Root == "" {
		return "", errors.New("nomi capabilities --write: this program has no nomi.toml; nomi init writes one")
	}
	path := filepath.Join(r.Root, "nomi.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("nomi capabilities --write: %w", err)
	}
	edited, tables, err := replaceCapabilityTables(r.Root, data, tight)
	if err != nil {
		return "", fmt.Errorf("nomi capabilities --write: %s: %w", vmhost.DisplayPath(path), err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, edited, info.Mode().Perm()); err != nil {
		return "", fmt.Errorf("nomi capabilities --write: %w", err)
	}
	return tables, nil
}

// replaceCapabilityTables is data, a nomi.toml, with its [capabilities]
// and [capabilities.modules] tables written as c (writeCapabilityTable),
// and the text of those two tables as written. Every line outside them is
// kept byte for byte. A table data lacks goes after the other, or at the
// end of the file. A file with CRLF line endings gets them on each line
// of the two tables. The edited text must parse back to c, or nothing is
// written.
func replaceCapabilityTables(dir string, data []byte, c *vmhost.Capabilities) ([]byte, string, error) {
	was, err := analysis.ParseManifestIn(dir, data)
	if err != nil {
		return nil, "", fmt.Errorf("the file does not parse (%v); edit its [capabilities] table by hand", err)
	}
	old := was.Capabilities
	if old == nil {
		old = &vmhost.Capabilities{}
	}
	text := string(data)
	cr := ""
	if strings.Contains(text, "\r\n") {
		cr = "\r"
	}
	lines := strings.Split(text, "\n")
	capsAt := analysis.ManifestTableLayout(data, "capabilities")
	modsAt := analysis.ManifestTableLayout(data, "capabilities", "modules")
	caps := writeCapabilityTable(lines, capsAt, "[capabilities]", capabilityKeys(c, old))
	var mods []string
	if len(c.Modules) > 0 || modsAt.Header > 0 {
		mods = writeCapabilityTable(lines, modsAt, "[capabilities.modules]", moduleKeys(c, old))
	}
	both := caps
	if mods != nil {
		both = slices.Concat(caps, []string{""}, mods)
	}
	ended := func(table []string) []string {
		out := make([]string, len(table))
		for i, line := range table {
			out[i] = line + cr
		}
		return out
	}
	replace := func(at analysis.ManifestTable, table []string) {
		lines = slices.Concat(lines[:at.Header-1], ended(table), lines[at.Last:])
	}
	switch {
	case capsAt.Header > 0 && modsAt.Header > 0:
		// The later table first, so the earlier one's lines stay put.
		if modsAt.Header > capsAt.Header {
			replace(modsAt, mods)
			replace(capsAt, caps)
		} else {
			replace(capsAt, caps)
			replace(modsAt, mods)
		}
	case capsAt.Header > 0:
		replace(capsAt, both)
	case modsAt.Header > 0:
		replace(modsAt, both)
	default:
		if text != "" && !strings.HasSuffix(text, "\n") {
			text += cr + "\n"
		}
		if text != "" {
			text += cr + "\n"
		}
		lines = strings.Split(text+strings.Join(ended(both), "\n")+cr+"\n", "\n")
	}
	edited := []byte(strings.Join(lines, "\n"))
	m, err := analysis.ParseManifestIn(dir, edited)
	if err != nil {
		return nil, "", fmt.Errorf("the edited file does not parse (%v); edit its [capabilities] table by hand", err)
	}
	if m.Capabilities == nil || len(tableChanges(m.Capabilities, c)) > 0 {
		return nil, "", errors.New("the edited file does not hold the table written; edit its [capabilities] table by hand")
	}
	return edited, strings.Join(both, "\n") + "\n", nil
}

// tableKey is a key --write writes in a table: its path as
// ManifestLines.Key holds it, its text when it is written new, the names
// its list holds and held, and the comment a new one ends with.
type tableKey struct {
	path, key  string
	names, was []string
	comment    string
	// kept is a key --write never adds and keeps as the file has it:
	// require_module_lists.
	kept bool
}

// capabilityKeys is what [capabilities] holds as c: std, the other lists
// when they hold a name, and require_module_lists as the file has it.
func capabilityKeys(c, old *vmhost.Capabilities) []tableKey {
	keys := []tableKey{{path: "std", names: c.Std, was: old.Std}}
	for _, list := range []tableKey{
		{path: "std_in_tests", names: c.StdInTests, was: old.StdInTests},
		{path: "go", names: c.Go, was: old.Go},
		{path: "go_in_tests", names: c.GoInTests, was: old.GoInTests},
	} {
		if len(list.names) > 0 {
			keys = append(keys, list)
		}
	}
	for i := range keys {
		keys[i].key = keys[i].path
	}
	return append(keys, tableKey{path: "require_module_lists", kept: true})
}

// moduleKeys is what [capabilities.modules] holds as c: an entry per
// module by path, one for a module that binds Go saying so.
func moduleKeys(c, old *vmhost.Capabilities) []tableKey {
	paths := make([]string, 0, len(c.Modules))
	for p := range c.Modules {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	keys := make([]tableKey, len(paths))
	for i, p := range paths {
		keys[i] = tableKey{path: p, key: fmt.Sprintf("%q", p), names: c.Modules[p], was: old.Modules[p]}
		switch {
		case slices.Contains(c.Go, p):
			keys[i].comment = "# binds Go"
		case slices.Contains(c.GoInTests, p):
			keys[i].comment = "# binds Go in tests"
		}
	}
	return keys
}

// writeCapabilityTable is the lines of one table, at in lines, as --write
// writes it with keys: its header as the file has it, then its body in the
// file's order (keptRuns). A key keys holds keeps the comment lines
// directly above it and the comment on its line; its lines are as they
// were when its list holds the same names, and its list is written again
// when not (writeListValue). A key keys does not hold goes, with its
// comments. Then each key the file lacks, in keys' order, with no comment
// but its own; after a blank line when a blank line separates the keys
// the table has.
func writeCapabilityTable(lines []string, at analysis.ManifestTable, header string, keys []tableKey) []string {
	out := []string{header}
	if at.Header > 0 {
		out[0] = strings.TrimSuffix(lines[at.Header-1], "\r")
	}
	written := map[string]bool{}
	spaced := false
	out = append(out, keptRuns(lines, at.Body, func(run analysis.ManifestLines) ([]string, bool) {
		i := slices.IndexFunc(keys, func(k tableKey) bool { return len(run.Key) == 1 && k.path == run.Key[0] })
		if i < 0 {
			return nil, false
		}
		k := keys[i]
		written[k.path] = true
		if k.kept || sameNames(k.was, k.names) {
			return runLines(lines, run.First, run.Last), true
		}
		return writeListValue(lines, run, k.was, k.names), true
	})...)
	for _, run := range at.Body {
		if run.Key == nil && slices.ContainsFunc(runLines(lines, run.First, run.Last), isBlank) {
			spaced = true
		}
	}
	var added []string
	for _, k := range keys {
		if written[k.path] || k.kept {
			continue
		}
		comment := ""
		if k.comment != "" {
			comment = "  " + k.comment
		}
		added = append(added, tomlList(k.key+" = ", k.names, comment)...)
	}
	if spaced && len(added) > 0 && len(out) > 1 {
		out = append(out, "")
	}
	return append(out, added...)
}

// keptRuns is runs, a table's body or a list's elements, as written: a key
// or an element is write's lines, or nothing when write drops it with its
// comments; a comment no key owns stays where it is; and a run of blank
// lines stays when a line of runs is written after it. Two runs of blank
// lines that dropping a key brings together are the first.
func keptRuns(lines []string, runs []analysis.ManifestLines, write func(analysis.ManifestLines) ([]string, bool)) []string {
	var out, blank []string
	blankEnds := 0
	text := func(more ...string) {
		out = append(slices.Concat(out, blank), more...)
		blank = nil
	}
	for _, run := range runs {
		if run.Key != nil {
			if more, ok := write(run); ok {
				text(more...)
			}
			continue
		}
		for line := run.First; line <= run.Last; line++ {
			l := strings.TrimSuffix(lines[line-1], "\r")
			switch {
			case !isBlank(l):
				text(l)
			case blank == nil || blankEnds == line-1:
				blank, blankEnds = append(blank, l), line
			}
		}
	}
	return out
}

// writeListValue is a key's lines, run in lines, with its list written as
// names, which held was: the comment lines above it and the text around
// the value stay. A list written one element per line keeps each element
// names holds, with its comments, and gets the others at its end, one per
// line. Any other list is written again whole, the names it keeps in its
// order and then the others, on one line when it fits; a comment inside
// it goes.
func writeListValue(lines []string, run analysis.ManifestLines, was, names []string) []string {
	out := runLines(lines, run.First, run.Line-1)
	var added []string
	for _, n := range names {
		if !slices.Contains(was, n) {
			added = append(added, n)
		}
	}
	first := strings.TrimSuffix(lines[run.Line-1], "\r")
	last := strings.TrimSuffix(lines[run.Last-1], "\r")
	if run.Elements != nil {
		indent := "    "
		out = append(out, first)
		out = append(out, keptRuns(lines, run.Elements, func(e analysis.ManifestLines) ([]string, bool) {
			if !slices.Contains(names, e.Key[0]) {
				return nil, false
			}
			indent = lines[e.Line-1][:e.ValueStart]
			return runLines(lines, e.First, e.Last), true
		})...)
		for _, n := range added {
			out = append(out, fmt.Sprintf("%s%q,", indent, n))
		}
		return append(out, last)
	}
	var order []string
	for _, n := range was {
		if slices.Contains(names, n) {
			order = append(order, n)
		}
	}
	return append(out, tomlList(first[:run.ValueStart], append(order, added...), last[run.ValueEnd:])...)
}

// runLines is lines first..last (1-based, inclusive), each without a
// carriage return.
func runLines(lines []string, first, last int) []string {
	out := make([]string, 0, max(0, last-first+1))
	for _, l := range lines[first-1 : max(first-1, last)] {
		out = append(out, strings.TrimSuffix(l, "\r"))
	}
	return out
}

func isBlank(line string) bool { return strings.TrimSpace(line) == "" }

// sameNames reports whether a and b hold the same names.
func sameNames(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for _, n := range a {
		if !slices.Contains(b, n) {
			return false
		}
	}
	return true
}

// tomlList is a list of names after prefix (`std = `) and before suffix,
// a comment: on one line when that fits in reportWidth, and with one
// name per line, the suffix on the first, when not.
func tomlList(prefix string, names []string, suffix string) []string {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = fmt.Sprintf("%q", n)
	}
	one := prefix + "[" + strings.Join(quoted, ", ") + "]" + suffix
	if len(one) <= reportWidth || len(names) < 2 {
		return []string{one}
	}
	out := []string{prefix + "[" + suffix}
	for _, q := range quoted {
		out = append(out, "    "+q+",")
	}
	return append(out, "]")
}
