package main

// `nomi effects` reports the effects a project's programs have, from
// the roots `nomi check` holds to nomi.toml's [effects.self] and
// [effects.deps] tables, and with --write writes the tightest tables
// that pass. The facts are vmhost's (vmhost.Reached, Reach.Tightest, which
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
	"github.com/nomi-language/nomi/internal/effects"
	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/vmhost"
)

const effectsUsage = "usage: nomi effects [--write | --check | --why <name> | --tree [--tests]] [--format text|json|mermaid] [path]"

// effectsArgs is `nomi effects`' parsed command line.
type effectsArgs struct {
	path  string
	write bool
	// check fails the command when nomi.toml's tables differ from the
	// tightest ones.
	check bool
	json  bool
	why   string
	// tree prints the calls from each root to each effect call, tests
	// each test case's (writeTree), and mermaid them as a flowchart
	// (writeMermaid).
	tree, tests, mermaid bool
}

func parseEffectsArgs(args []string) (effectsArgs, error) {
	parsed := effectsArgs{path: "."}
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
			return "", false, fmt.Errorf("nomi effects: %s needs a value\n%s", flag, effectsUsage)
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
		if arg == "--tree" {
			parsed.tree = true
			continue
		}
		if arg == "--tests" {
			parsed.tests = true
			continue
		}
		if v, ok, err := value(&i, arg, "--format"); err != nil {
			return parsed, err
		} else if ok {
			switch v {
			case "text":
			case "json":
				parsed.json = true
			case "mermaid":
				parsed.mermaid = true
			default:
				return parsed, fmt.Errorf("nomi effects: unknown format %q; use text, json or mermaid\n%s", v, effectsUsage)
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
			return parsed, unknownFlag("nomi effects", arg, effectsUsage)
		}
		if pathSet {
			return parsed, fmt.Errorf("nomi effects: unexpected argument %q\n%s", arg, effectsUsage)
		}
		parsed.path, pathSet = arg, true
	}
	var modes []string
	for flag, on := range map[string]bool{"--check": parsed.check, "--tree": parsed.tree, "--why": parsed.why != "", "--write": parsed.write} {
		if on {
			modes = append(modes, flag)
		}
	}
	if len(modes) > 1 {
		slices.Sort(modes)
		return parsed, fmt.Errorf("nomi effects: %s do not go together\n%s", strings.Join(modes, " and "), effectsUsage)
	}
	switch {
	case parsed.tests && !parsed.tree:
		return parsed, fmt.Errorf("nomi effects: --tests goes with --tree\n%s", effectsUsage)
	case parsed.mermaid && !parsed.tree:
		return parsed, fmt.Errorf("nomi effects: --format mermaid goes with --tree\n%s", effectsUsage)
	case parsed.json && parsed.tree:
		return parsed, fmt.Errorf("nomi effects: --tree prints text or mermaid\n%s", effectsUsage)
	}
	return parsed, nil
}

// runEffects is `nomi effects`, writing its report to w. Each package has
// its own [effects] tables, so at a workspace's root it reports on each
// member in turn (frontend.WorkspaceMembers), each report as `nomi effects`
// in the member gives it, and fails when one of them does.
func runEffects(w io.Writer, args []string) error {
	parsed, err := parseEffectsArgs(args)
	if err != nil {
		return err
	}
	members, err := frontend.WorkspaceMembers(parsed.path)
	if err != nil {
		return fmt.Errorf("nomi effects: %w", err)
	}
	if members == nil {
		return effectsOf(w, parsed)
	}
	var failed error
	for i, m := range members {
		if !parsed.json {
			// A JSON report names its project; a text one is headed by
			// the member it is about.
			if i > 0 {
				fmt.Fprintln(w)
			}
			fmt.Fprintf(w, "%s:\n", vmDisplay(m))
		}
		one := parsed
		one.path = m
		if err := effectsOf(w, one); errors.Is(err, errCheckFailed) {
			failed = err
		} else if err != nil {
			return err
		}
	}
	return failed
}

// effectsOf is `nomi effects` on one package's, or one file's, programs.
func effectsOf(w io.Writer, parsed effectsArgs) error {
	r, err := reachOf(parsed.path, os.Stderr)
	if err != nil {
		return err
	}
	if parsed.why != "" {
		return writeWhy(w, r, parsed.why, parsed.json)
	}
	if parsed.tree {
		// A file's own tests are expanded, a project's counted by file.
		info, err := os.Stat(parsed.path)
		tests := parsed.tests || err == nil && !info.IsDir()
		if parsed.mermaid {
			writeMermaid(w, r.Trees(), tests)
		} else {
			writeTree(w, r.Trees(), tests)
		}
		return nil
	}
	tight := r.Tightest()
	report := effectsReport{r: r, tight: tight, changes: analysis.EffectChanges(r.Lists, tight)}
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
	if err == nil && parsed.check && r.Lists != nil && (len(report.changes) > 0 || len(r.ListProblems)+len(r.Undetermined) > 0) {
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
		return nil, fmt.Errorf("nomi effects: %w", pathError(path, err))
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("nomi effects: no Nomi files found in %s", path)
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

// effectsReport is one run's report.
type effectsReport struct {
	r       *vmhost.Reach
	tight   *vmhost.Effects
	changes []analysis.EffectChange
	// written is the text of the tables --write wrote, "" when it wrote
	// nothing, the tables being current.
	written string
}

// writeText prints the effects the programs have: the tightest tables,
// self's lists for the project's own files and deps' lists with the
// effects each dependency's own files have, then how nomi.toml's tables
// differ from them.
func (rep effectsReport) writeText(w io.Writer) {
	t := rep.tight
	fmt.Fprintln(w, "self: "+rep.r.Project)
	fmt.Fprintln(w, effects.Wrapped("  std            ", t.Self.Std))
	fmt.Fprintln(w, effects.Wrapped("  std_in_tests   ", t.Self.StdInTests))
	fmt.Fprintln(w)
	fmt.Fprintln(w, "deps")
	fmt.Fprintln(w, effects.Wrapped("  std            ", t.Deps.Std))
	fmt.Fprintln(w, effects.Wrapped("  std_in_tests   ", t.Deps.StdInTests))
	modules := rep.r.PackageReaches()
	if len(modules) == 0 {
		fmt.Fprintln(w, "  packages       (none)")
	} else {
		fmt.Fprintln(w, "  packages")
	}
	for _, u := range modules {
		fmt.Fprintln(w, "    "+u.Path)
		vmhost.WritePackageReach(w, "      ", u)
	}
	fmt.Fprintln(w)
	rep.writeTable(w, false)
}

// writeTable prints how nomi.toml's tables differ from the tightest ones:
// one line for no tables or exact ones, else each name the table lacks
// (missing) and each it holds that nothing here has (unused), and whether
// nomi check passes it. The problems nomi check finds in the lists
// themselves come after (writeListProblems), and then the check fails on
// them whatever the calls. check is --check's report, which prints no
// table above it.
func (rep effectsReport) writeTable(w io.Writer, check bool) {
	switch {
	case rep.r.Lists == nil && check:
		fmt.Fprintln(w, "nomi.toml has no [effects.self] or [effects.deps] table, so there is nothing to hold; nomi effects --write writes them")
	case rep.r.Lists == nil:
		fmt.Fprintln(w, "nomi.toml has no [effects.self] or [effects.deps] table; nomi effects --write writes these")
	case len(rep.changes) == 0 && check:
		fmt.Fprintln(w, "nomi.toml's [effects] tables allow exactly the effects the program has")
	case len(rep.changes) == 0:
		fmt.Fprintln(w, "nomi.toml's [effects] tables allow exactly this")
	default:
		fmt.Fprintln(w, "nomi.toml's [effects] tables differ:")
		rep.writeChanges(w, "missing", "unused")
		written := "the tables above"
		if check {
			written = "the tightest tables"
		}
		switch refused := len(rep.r.NotAllowed(rep.r.Lists)); {
		case len(rep.r.ListProblems)+len(rep.r.Undetermined) > 0:
		case refused > 0:
			fmt.Fprintf(w, "nomi check refuses %s; nomi effects --write writes %s\n", calls(refused), written)
		default:
			fmt.Fprintln(w, "nomi check passes; an unused name allows an effect nothing here has")
		}
	}
	rep.writeListProblems(w)
	rep.writeDeclaredLess(w)
}

// writeListProblems prints each problem nomi check finds in the lists
// themselves (Reach.ListProblems), and each public function whose effects
// they cannot hold (Reach.Undetermined), as nomi check prints it, and that
// the check fails on them; nothing when there are none.
func (rep effectsReport) writeListProblems(w io.Writer) {
	ps := append(slices.Clip(rep.r.ListProblems), rep.r.Undetermined...)
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
func (rep effectsReport) writeChanges(w io.Writer, add, remove string) {
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
		case slices.Contains(analysis.EntryLists, c.List):
			name = "(an empty entry)"
		case c.List == analysis.ListSelf || c.List == analysis.ListDeps:
			name = "(the table)"
		default:
			name = "(an empty list)"
		}
		fmt.Fprintf(w, "  %-8s %-*s  %s\n", word, width, c.Label(), name)
	}
}

// writeDeclaredLess prints each call the [effects.self] in a
// dependency's own nomi.toml does not allow, which no project table can:
// `nomi check` refuses it until that module's own table does.
func (rep effectsReport) writeDeclaredLess(w io.Writer) {
	seen := map[string]bool{}
	for _, n := range rep.r.NotAllowed(rep.tight) {
		key := n.Package + "\x00" + n.Name
		if n.List != vmhost.OverDeclared || seen[key] {
			continue
		}
		seen[key] = true
		fmt.Fprintf(w, "%s declares fewer effects than its code has: its own [effects.self] does not allow %s, and nomi check refuses it until it does\n", n.Package, n.Name)
	}
}

// writeWritten prints what --write did: the tables it wrote and, when
// nomi.toml had a table, each name it added and removed.
func (rep effectsReport) writeWritten(w io.Writer) {
	if rep.written == "" {
		fmt.Fprintln(w, "nomi.toml's [effects] tables are current")
		return
	}
	fmt.Fprintf(w, "wrote nomi.toml's [effects] tables:\n\n%s", rep.written)
	if rep.r.Lists != nil {
		fmt.Fprintln(w)
		rep.writeChanges(w, "added", "removed")
	}
	rep.writeDeclaredLess(w)
}

// effectsJSON is `nomi effects --format json`.
type effectsJSON struct {
	Project string   `json:"project"`
	Self    selfJSON `json:"self"`
	Deps    depsJSON `json:"deps"`
	// Table is how nomi.toml's tables differ, nil when it has neither.
	Table *effectsTableJSON `json:"table"`
	// Written reports that --write wrote nomi.toml.
	Written bool `json:"written"`
	// DeclaredLess is each name a dependency's own [effects.self]
	// does not allow its own code.
	DeclaredLess []declaredLessJSON `json:"declared_less"`
}

// selfJSON is the tightest [effects.self].
type selfJSON struct {
	Std        []string `json:"std"`
	StdInTests []string `json:"std_in_tests"`
}

// depsJSON is the tightest [effects.deps], with the effects each
// dependency has, from which its entry under [effects.deps.packages] is
// written.
type depsJSON struct {
	Std        []string              `json:"std"`
	StdInTests []string              `json:"std_in_tests"`
	Packages   []vmhost.PackageReach `json:"packages"`
}

type effectsTableJSON struct {
	Changes []analysis.EffectChange `json:"changes"`
	// Refused is how many calls `nomi check` refuses under the table.
	Refused int `json:"refused"`
	// Problems is each problem `nomi check` finds in the lists themselves,
	// in the short form it prints, on which it fails whatever the calls.
	Problems []string `json:"problems"`
}

type declaredLessJSON struct {
	Package string `json:"package"`
	Name    string `json:"name"`
}

func (rep effectsReport) writeJSON(w io.Writer) error {
	orEmpty := func(s []string) []string {
		if s == nil {
			return []string{}
		}
		return s
	}
	t := rep.tight
	out := effectsJSON{
		Project: rep.r.Project,
		Self:    selfJSON{Std: orEmpty(t.Self.Std), StdInTests: orEmpty(t.Self.StdInTests)},
		Deps:    depsJSON{Std: orEmpty(t.Deps.Std), StdInTests: orEmpty(t.Deps.StdInTests), Packages: rep.r.PackageReaches()},
		Written: rep.written != "", DeclaredLess: []declaredLessJSON{},
	}
	if rep.r.Lists != nil {
		out.Table = &effectsTableJSON{Changes: rep.changes, Refused: len(rep.r.NotAllowed(rep.r.Lists)), Problems: []string{}}
		for _, d := range rep.r.ListProblems {
			out.Table.Problems = append(out.Table.Problems, d.String())
		}
		if out.Table.Changes == nil {
			out.Table.Changes = []analysis.EffectChange{}
		}
	}
	seen := map[declaredLessJSON]bool{}
	for _, n := range rep.r.NotAllowed(rep.tight) {
		d := declaredLessJSON{Package: n.Package, Name: n.Name}
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

// writeWhy prints each call that has the effect name, a std name
// (`std::fs`), or that a module's files make, for a module path, with its
// site and how it is reached, in the words a refusal uses (Reach.Why,
// Reach.WriteWhy). A name nothing has is an error that lists the effects
// the programs have.
func writeWhy(w io.Writer, r *vmhost.Reach, name string, asJSON bool) error {
	found, err := r.Why(name)
	if err != nil {
		return fmt.Errorf("nomi effects: %w", err)
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
// [effects.self], [effects.self.modules], [effects.deps]
// and [effects.deps.packages] tables (analysis.WriteEffectTables),
// unless nomi.toml's tables already allow exactly what tight does, and
// answers the tables' text, or "" when it wrote nothing. Tight has
// [effects.self.modules] only when nomi.toml has it or asks for it
// (vmhost.Reach.Tightest).
func writeTightest(r *vmhost.Reach, tight *vmhost.Effects, changes []analysis.EffectChange) (string, error) {
	if r.Lists != nil && len(changes) == 0 {
		return "", nil
	}
	if r.Root == "" {
		return "", errors.New("nomi effects --write: this program has no nomi.toml; nomi init writes one")
	}
	path := filepath.Join(r.Root, "nomi.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("nomi effects --write: %w", err)
	}
	edited, tables, err := analysis.WriteEffectTables(r.Root, data, tight)
	if err != nil {
		return "", fmt.Errorf("nomi effects --write: %s: %w", vmhost.DisplayPath(path), err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, edited, info.Mode().Perm()); err != nil {
		return "", fmt.Errorf("nomi effects --write: %w", err)
	}
	return tables, nil
}
