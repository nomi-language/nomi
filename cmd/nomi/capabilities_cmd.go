package main

// `nomi capabilities` reports what a project's programs reach outside the
// machine, from the roots `nomi check` holds to nomi.toml's
// [capabilities.self] and [capabilities.deps] tables, and with --write
// writes the tightest tables that pass. The facts are vmhost's (vmhost.Reached, Reach.Tightest, which
// the same rule as the check derives), so the tables it writes are ones
// the check passes; this file only prints them and edits nomi.toml.

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

const capabilitiesUsage = "usage: nomi capabilities [--write | --check | --why <name>] [--format text|json] [path]"

// capabilitiesArgs is `nomi capabilities`' parsed command line.
type capabilitiesArgs struct {
	path  string
	write bool
	// check fails the command when nomi.toml's tables differ from the
	// tightest ones.
	check bool
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
		if arg == "--check" {
			parsed.check = true
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
	var modes []string
	for flag, on := range map[string]bool{"--check": parsed.check, "--why": parsed.why != "", "--write": parsed.write} {
		if on {
			modes = append(modes, flag)
		}
	}
	if len(modes) > 1 {
		slices.Sort(modes)
		return parsed, fmt.Errorf("nomi capabilities: %s do not go together\n%s", strings.Join(modes, " and "), capabilitiesUsage)
	}
	return parsed, nil
}

// runCapabilities is `nomi capabilities`, writing its report to w.
func runCapabilities(w io.Writer, args []string) error {
	parsed, err := parseCapabilitiesArgs(args)
	if err != nil {
		return err
	}
	r, err := reachOf(parsed.path, os.Stderr)
	if err != nil {
		return err
	}
	if parsed.why != "" {
		return writeWhy(w, r, parsed.why, parsed.json)
	}
	tight := r.Tightest()
	report := capabilitiesReport{r: r, tight: tight, changes: analysis.CapabilityChanges(r.Lists, tight)}
	if parsed.check {
		report.changes = heldChanges(report.changes, r.Lists)
	}
	if parsed.write {
		if report.written, err = writeTightest(r, tight, report.changes); err != nil {
			return err
		}
	}
	switch {
	case parsed.json:
		err = report.writeJSON(w)
	case parsed.write:
		report.writeWritten(w)
	case parsed.check:
		report.writeTable(w, true)
	default:
		report.writeText(w)
	}
	if err == nil && parsed.check && r.Lists != nil && (len(report.changes) > 0 || len(r.ListProblems) > 0) {
		// --check holds the table to the tightest one, in both directions,
		// and fails where nomi check would on the lists themselves.
		return errCheckFailed
	}
	return err
}

// reachOf is what the programs `nomi check path` checks reach, together.
// A program that does not check is reported to report as `nomi check`
// reports it, with the warnings, and the answer is errCheckFailed. With
// report nil nothing is printed, and the answer is the first program's
// error, naming its file.
func reachOf(path string, report io.Writer) (*vmhost.Reach, error) {
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
	warnings := report
	if report == nil {
		warnings = io.Discard
	}
	for _, file := range files {
		one, err := vmhost.Reached(file, vmhost.WithWarnings(warnings))
		if err != nil {
			if report == nil {
				return nil, fmt.Errorf("%s does not check: %w", vmhost.DisplayPath(file), err)
			}
			failed = true
			if singleFile && ownLines(err) {
				vmhost.WriteFailure(report, err)
			} else {
				writeCheckFailure(report, file, err, reported)
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

// heldChanges is changes without the ones --check does not hold the
// tables to, since leaving them out is a choice and not a difference: a
// table the file leaves out, [capabilities.deps]' ceiling when the table
// leaves out its std, and the entry of a dependency the table gives none,
// which only the ceiling and the dependency's own table hold, unless
// entries_required asks every dependency for one. A dependency the table
// does give an entry is held to the tightest one, like every list.
func heldChanges(changes []analysis.CapabilityChange, lists *vmhost.Capabilities) []analysis.CapabilityChange {
	if lists == nil {
		return changes
	}
	var out []analysis.CapabilityChange
	for _, c := range changes {
		self := c.List == analysis.ListSelf || strings.HasPrefix(c.List, analysis.ListSelf+".")
		switch {
		case self && lists.Self == nil:
		case !self && lists.Deps == nil:
		case self:
			out = append(out, c)
		case (c.List == analysis.ListDepsStd || c.List == analysis.ListDepsStdInTests) && lists.Deps.Std == nil:
		case c.List == analysis.ListDepsModules && c.Change == "add" && !lists.Deps.EntriesRequired && !hasEntry(lists, c.Module):
		default:
			out = append(out, c)
		}
	}
	return out
}

// hasEntry reports whether lists give module an entry under
// [capabilities.deps.modules].
func hasEntry(lists *vmhost.Capabilities, module string) bool {
	_, has := lists.Deps.Modules[module]
	return has
}

// moduleReach is what one dependency's own files reach, as the report
// prints it.
type moduleReach struct {
	Path string `json:"path"`
	// Std is each std name the module's files reach; StdTestsOnly those
	// of them only the tests reach.
	Std          []string `json:"std"`
	StdTestsOnly []string `json:"std_tests_only"`
	// BindsGo reports that the module's files bind Go in calls the walks
	// reach, and GoTestsOnly that only the tests reach them.
	BindsGo     bool `json:"binds_go"`
	GoTestsOnly bool `json:"go_tests_only,omitempty"`
}

// moduleReaches is what each dependency the programs' code comes from
// reaches, by path.
func moduleReaches(r *vmhost.Reach) []moduleReach {
	byPath := map[string]*moduleReach{}
	use := func(path string) *moduleReach {
		if u, ok := byPath[path]; ok {
			return u
		}
		u := &moduleReach{Path: path, Std: []string{}, StdTestsOnly: []string{}}
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
			if c.Module == "" {
				continue
			}
			u := use(c.Module)
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
	out := []moduleReach{}
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
	slices.SortFunc(out, func(a, b moduleReach) int { return strings.Compare(a.Path, b.Path) })
	return out
}

// capabilitiesReport is one run's report.
type capabilitiesReport struct {
	r       *vmhost.Reach
	tight   *vmhost.Capabilities
	changes []analysis.CapabilityChange
	// written is the text of the tables --write wrote, "" when it wrote
	// nothing, the tables being current.
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

// writeText prints what the programs reach: the tightest tables, self's
// lists for the project's own files and deps' lists with what each
// dependency's own files reach, then how nomi.toml's tables differ from
// them.
func (rep capabilitiesReport) writeText(w io.Writer) {
	t := rep.tight
	flag := func(on bool) []string { return []string{fmt.Sprint(on)} }
	fmt.Fprintln(w, "self: "+rep.r.Project)
	fmt.Fprintln(w, wrapped("  std            ", t.Self.Std))
	fmt.Fprintln(w, wrapped("  std_in_tests   ", t.Self.StdInTests))
	fmt.Fprintln(w, wrapped("  go             ", flag(t.Self.Go)))
	fmt.Fprintln(w, wrapped("  go_in_tests    ", flag(t.Self.GoInTests)))
	fmt.Fprintln(w)
	fmt.Fprintln(w, "deps")
	fmt.Fprintln(w, wrapped("  std            ", t.Deps.Std))
	fmt.Fprintln(w, wrapped("  std_in_tests   ", t.Deps.StdInTests))
	fmt.Fprintln(w, wrapped("  go             ", t.Deps.Go))
	fmt.Fprintln(w, wrapped("  go_in_tests    ", t.Deps.GoInTests))
	modules := moduleReaches(rep.r)
	if len(modules) == 0 {
		fmt.Fprintln(w, "  modules        (none)")
	} else {
		fmt.Fprintln(w, "  modules")
	}
	for _, u := range modules {
		fmt.Fprintln(w, "    "+u.Path)
		writeModuleReach(w, "      ", u)
	}
	fmt.Fprintln(w)
	rep.writeTable(w, false)
}

// writeModuleReach prints, under a module's path, indented by indent, what
// its own files reach: its std names, with (tests only) on those only the
// tests reach, and whether it binds Go, or nothing.
func writeModuleReach(w io.Writer, indent string, u moduleReach) {
	var names []string
	for _, n := range u.Std {
		if slices.Contains(u.StdTestsOnly, n) {
			n += " (tests only)"
		}
		names = append(names, n)
	}
	if len(names) > 0 {
		fmt.Fprintln(w, wrapped(indent, names))
	}
	switch {
	case u.BindsGo && u.GoTestsOnly:
		fmt.Fprintln(w, indent+"binds Go (tests only)")
	case u.BindsGo:
		fmt.Fprintln(w, indent+"binds Go")
	case len(names) == 0:
		fmt.Fprintln(w, indent+"nothing")
	}
}

// writeTable prints how nomi.toml's tables differ from the tightest ones:
// one line for no tables or exact ones, else each name the table lacks
// (missing) and each it holds that nothing reaches (unused), and whether
// nomi check passes it. The problems nomi check finds in the lists
// themselves come after (writeListProblems), and then the check fails on
// them whatever the calls. check is --check's report, which prints no
// table above it.
func (rep capabilitiesReport) writeTable(w io.Writer, check bool) {
	switch {
	case rep.r.Lists == nil && check:
		fmt.Fprintln(w, "nomi.toml has no [capabilities.self] or [capabilities.deps] table, so there is nothing to hold; nomi capabilities --write writes them")
	case rep.r.Lists == nil:
		fmt.Fprintln(w, "nomi.toml has no [capabilities.self] or [capabilities.deps] table; nomi capabilities --write writes these")
	case len(rep.changes) == 0 && check:
		fmt.Fprintln(w, "nomi.toml's [capabilities] tables allow exactly what the program reaches")
	case len(rep.changes) == 0:
		fmt.Fprintln(w, "nomi.toml's [capabilities] tables allow exactly this")
	default:
		fmt.Fprintln(w, "nomi.toml's [capabilities] tables differ:")
		rep.writeChanges(w, "missing", "unused")
		written := "the tables above"
		if check {
			written = "the tightest tables"
		}
		switch refused := len(rep.r.NotAllowed(rep.r.Lists)); {
		case len(rep.r.ListProblems) > 0:
		case refused > 0:
			fmt.Fprintf(w, "nomi check refuses %s; nomi capabilities --write writes %s\n", calls(refused), written)
		default:
			fmt.Fprintln(w, "nomi check passes; an unused name allows what nothing here reaches")
		}
	}
	rep.writeListProblems(w)
	rep.writeDeclaredLess(w)
}

// writeListProblems prints each problem nomi check finds in the lists
// themselves (Reach.ListProblems) as nomi check prints it, and that the
// check fails on them; nothing when there are none.
func (rep capabilitiesReport) writeListProblems(w io.Writer) {
	ps := rep.r.ListProblems
	if len(ps) == 0 {
		return
	}
	for _, d := range ps {
		fmt.Fprintln(w, d.String())
	}
	if len(ps) == 1 {
		fmt.Fprintln(w, "nomi check fails on the problem above")
	} else {
		fmt.Fprintf(w, "nomi check fails on the %d problems above\n", len(ps))
	}
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
		width = max(width, len(c.Label()))
	}
	for _, c := range rep.changes {
		word := add
		if c.Change == "remove" {
			word = remove
		}
		name := c.Name
		switch {
		case name != "":
		case c.List == analysis.ListDepsModules:
			name = "(an empty entry)"
		case c.List == analysis.ListSelf || c.List == analysis.ListDeps:
			name = "(the table)"
		default:
			name = "(an empty list)"
		}
		fmt.Fprintf(w, "  %-8s %-*s  %s\n", word, width, c.Label(), name)
	}
}

// writeDeclaredLess prints each call the [capabilities.self] in a
// dependency's own nomi.toml does not allow, which no project table can:
// `nomi check` refuses it until that module's own table does.
func (rep capabilitiesReport) writeDeclaredLess(w io.Writer) {
	seen := map[string]bool{}
	for _, n := range rep.r.NotAllowed(rep.tight) {
		key := n.Module + "\x00" + n.Name
		if n.List != vmhost.OverDeclared || seen[key] {
			continue
		}
		seen[key] = true
		fmt.Fprintf(w, "%s declares less than its code reaches: its own [capabilities.self] does not allow %s, and nomi check refuses it until it does\n", n.Module, n.Name)
	}
}

// writeWritten prints what --write did: the tables it wrote and, when
// nomi.toml had a table, each name it added and removed.
func (rep capabilitiesReport) writeWritten(w io.Writer) {
	if rep.written == "" {
		fmt.Fprintln(w, "nomi.toml's [capabilities] tables are current")
		return
	}
	fmt.Fprintf(w, "wrote nomi.toml's [capabilities] tables:\n\n%s", rep.written)
	if rep.r.Lists != nil {
		fmt.Fprintln(w)
		rep.writeChanges(w, "added", "removed")
	}
	rep.writeDeclaredLess(w)
}

// capabilitiesJSON is `nomi capabilities --format json`.
type capabilitiesJSON struct {
	Project string   `json:"project"`
	Self    selfJSON `json:"self"`
	Deps    depsJSON `json:"deps"`
	// Table is how nomi.toml's tables differ, nil when it has neither.
	Table *capabilitiesTableJSON `json:"table"`
	// Written reports that --write wrote nomi.toml.
	Written bool `json:"written"`
	// DeclaredLess is each name a dependency's own [capabilities.self]
	// does not allow its own code.
	DeclaredLess []declaredLessJSON `json:"declared_less"`
}

// selfJSON is the tightest [capabilities.self].
type selfJSON struct {
	Std        []string `json:"std"`
	StdInTests []string `json:"std_in_tests"`
	Go         bool     `json:"go"`
	GoInTests  bool     `json:"go_in_tests"`
}

// depsJSON is the tightest [capabilities.deps], with what each dependency
// reaches, from which its entry under [capabilities.deps.modules] is
// written.
type depsJSON struct {
	Std        []string      `json:"std"`
	StdInTests []string      `json:"std_in_tests"`
	Go         []string      `json:"go"`
	GoInTests  []string      `json:"go_in_tests"`
	Modules    []moduleReach `json:"modules"`
}

type capabilitiesTableJSON struct {
	Changes []analysis.CapabilityChange `json:"changes"`
	// Refused is how many calls `nomi check` refuses under the table.
	Refused int `json:"refused"`
	// Problems is each problem `nomi check` finds in the lists themselves,
	// in the short form it prints, on which it fails whatever the calls.
	Problems []string `json:"problems"`
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
	t := rep.tight
	out := capabilitiesJSON{
		Project: rep.r.Project,
		Self:    selfJSON{Std: orEmpty(t.Self.Std), StdInTests: orEmpty(t.Self.StdInTests), Go: t.Self.Go, GoInTests: t.Self.GoInTests},
		Deps: depsJSON{Std: orEmpty(t.Deps.Std), StdInTests: orEmpty(t.Deps.StdInTests), Go: orEmpty(t.Deps.Go), GoInTests: orEmpty(t.Deps.GoInTests),
			Modules: moduleReaches(rep.r)},
		Written: rep.written != "", DeclaredLess: []declaredLessJSON{},
	}
	if rep.r.Lists != nil {
		out.Table = &capabilitiesTableJSON{Changes: rep.changes, Refused: len(rep.r.NotAllowed(rep.r.Lists)), Problems: []string{}}
		for _, d := range rep.r.ListProblems {
			out.Table.Problems = append(out.Table.Problems, d.String())
		}
		if out.Table.Changes == nil {
			out.Table.Changes = []analysis.CapabilityChange{}
		}
	}
	seen := map[declaredLessJSON]bool{}
	for _, n := range rep.r.NotAllowed(rep.tight) {
		d := declaredLessJSON{Module: n.Module, Name: n.Name}
		if n.List == vmhost.OverDeclared && !seen[d] {
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
			name, reaches, strings.Join(append([]string{r.Project}, r.Modules...), ", "))
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
// [capabilities.self], [capabilities.deps] and [capabilities.deps.modules]
// tables (analysis.WriteCapabilityTables), unless nomi.toml's tables
// already allow exactly what tight does, and
// answers the tables' text, or "" when it wrote nothing.
func writeTightest(r *vmhost.Reach, tight *vmhost.Capabilities, changes []analysis.CapabilityChange) (string, error) {
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
	edited, tables, err := analysis.WriteCapabilityTables(r.Root, data, tight)
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
