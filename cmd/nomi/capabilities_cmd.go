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
	"github.com/nomi-language/nomi/internal/frontend"
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
		report.changes = vmhost.HeldChanges(report.changes, r.Lists)
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
	files, _, singleFile, err := frontend.CheckedFiles(path)
	if err != nil {
		return nil, fmt.Errorf("nomi capabilities: %w", pathError(path, err))
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("nomi capabilities: no Nomi files found in %s", path)
	}
	var first error
	reported := map[string]bool{}
	warnings := report
	if report == nil {
		warnings = io.Discard
	}
	r := vmhost.ReachedAll(files, func(file string, err error) {
		switch {
		case report == nil && first == nil:
			first = fmt.Errorf("%s does not check: %w", vmhost.DisplayPath(file), err)
		case report == nil:
		case singleFile && ownLines(err):
			vmhost.WriteFailure(report, err)
		default:
			writeCheckFailure(report, file, err, reported)
		}
	}, vmhost.WithWarnings(warnings))
	switch {
	case first != nil:
		return nil, first
	case r == nil:
		return nil, errCheckFailed
	}
	return r, nil
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

// writeText prints what the programs reach: the tightest tables, self's
// lists for the project's own files and deps' lists with what each
// dependency's own files reach, then how nomi.toml's tables differ from
// them.
func (rep capabilitiesReport) writeText(w io.Writer) {
	t := rep.tight
	flag := func(on bool) []string { return []string{fmt.Sprint(on)} }
	fmt.Fprintln(w, "self: "+rep.r.Project)
	fmt.Fprintln(w, capability.Wrapped("  std            ", t.Self.Std))
	fmt.Fprintln(w, capability.Wrapped("  std_in_tests   ", t.Self.StdInTests))
	fmt.Fprintln(w, capability.Wrapped("  go             ", flag(t.Self.Go)))
	fmt.Fprintln(w, capability.Wrapped("  go_in_tests    ", flag(t.Self.GoInTests)))
	fmt.Fprintln(w)
	fmt.Fprintln(w, "deps")
	fmt.Fprintln(w, capability.Wrapped("  std            ", t.Deps.Std))
	fmt.Fprintln(w, capability.Wrapped("  std_in_tests   ", t.Deps.StdInTests))
	fmt.Fprintln(w, capability.Wrapped("  go             ", t.Deps.Go))
	fmt.Fprintln(w, capability.Wrapped("  go_in_tests    ", t.Deps.GoInTests))
	modules := rep.r.ModuleReaches()
	if len(modules) == 0 {
		fmt.Fprintln(w, "  modules        (none)")
	} else {
		fmt.Fprintln(w, "  modules")
	}
	for _, u := range modules {
		fmt.Fprintln(w, "    "+u.Path)
		vmhost.WriteModuleReach(w, "      ", u)
	}
	fmt.Fprintln(w)
	rep.writeTable(w, false)
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
	Std        []string             `json:"std"`
	StdInTests []string             `json:"std_in_tests"`
	Go         []string             `json:"go"`
	GoInTests  []string             `json:"go_in_tests"`
	Modules    []vmhost.ModuleReach `json:"modules"`
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
			Modules: rep.r.ModuleReaches()},
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

// writeWhy prints each call that reaches name, a std name (`std::fs`) or a
// `go:` name, or that a module's files make, for a module path, with its
// site and how it is reached, in the words a refusal uses (Reach.Why,
// Reach.WriteWhy). A name nothing reaches is an error that lists what the
// programs reach.
func writeWhy(w io.Writer, r *vmhost.Reach, name string, asJSON bool) error {
	found, err := r.Why(name)
	if err != nil {
		return fmt.Errorf("nomi capabilities: %w", err)
	}
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		return enc.Encode(struct {
			Name  string           `json:"name"`
			Calls []vmhost.WhyCall `json:"calls"`
		}{name, found})
	}
	r.WriteWhy(w, name, found, 0)
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
