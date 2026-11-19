package main

// What `nomi add` and `nomi update` say about a dependency they change:
// what its own files reach outside the machine, in the words `nomi
// capabilities` prints under deps' modules (writeModuleReach), and, when
// nomi.toml has [capabilities] tables, whether nomi check passes them. The
// reach is the check's (reachOf, vmhost.Reached), worked out over the
// project before and after the change, so a module is charged for what it
// reaches in this program and nothing else.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/modcache"
	"github.com/nomi-language/nomi/vmhost"
)

// dependencyReach is the project's reach after nomi add or nomi update,
// and before it for an update.
type dependencyReach struct {
	root          string
	before, after *vmhost.Reach
	// failed is why the reach after could not be worked out, or nil.
	failed error
}

// reachAfter works out the project's reach as it is now.
func (d *dependencyReach) reachAfter() {
	d.after, d.failed = reachOf(d.root, nil)
}

// nomiModules is which of paths are Nomi modules (a nomi.toml at their
// root), whose own files can reach anything; a Go module's code is only
// what a binding reaches, charged to the module that binds it.
func nomiModules(root string, paths []string) []string {
	g, err := modcache.Walk(root, cachedModule)
	if err != nil || g == nil {
		return nil
	}
	var out []string
	for _, p := range paths {
		if d, ok := g.Find(p); ok && d.Nomi {
			out = append(out, p)
		}
	}
	return out
}

// write prints what each of modules reaches, under modules, and how the
// update changed it when the reach before is known: a module the program
// does not load is not imported yet. Then, when nomi.toml has tables,
// what they need and whether nomi check passes them (writeTableNote).
// A reach that could not be worked out is one line saying why.
func (d *dependencyReach) write(w io.Writer, modules []string, table func(*vmhost.Reach) []string) {
	if len(modules) == 0 {
		return
	}
	if d.failed != nil {
		what := modules[0]
		if len(modules) > 1 {
			what = "the updated modules"
		}
		why, _, _ := strings.Cut(strings.TrimSpace(d.failed.Error()), "\n")
		fmt.Fprintf(w, "could not work out what %s reaches: %s\n", what, why)
		return
	}
	now := reachesByPath(d.after)
	var was map[string]moduleReach
	if d.before != nil {
		was = reachesByPath(d.before)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "modules")
	for _, m := range modules {
		fmt.Fprintln(w, "  "+m)
		u, loaded := now[m]
		if !loaded {
			fmt.Fprintln(w, "    not imported yet")
			continue
		}
		writeModuleReach(w, "    ", u)
		if old, ok := was[m]; ok {
			writeReachChange(w, old, u)
		}
	}
	if d.after.Lists == nil {
		return
	}
	fmt.Fprintln(w)
	if table != nil {
		for _, line := range table(d.after) {
			fmt.Fprintln(w, line)
		}
	}
	writeTableNote(w, d.after, d.after.Lists)
}

// reachesByPath is moduleReaches by module path.
func reachesByPath(r *vmhost.Reach) map[string]moduleReach {
	out := map[string]moduleReach{}
	for _, u := range moduleReaches(r) {
		out[u.Path] = u
	}
	return out
}

// writeReachChange prints how a module's reach went from old to u: each
// std name it now also reaches and no longer reaches, and whether it now
// binds Go or no longer does, or that nothing changed.
func writeReachChange(w io.Writer, old, u moduleReach) {
	changed := false
	for _, n := range u.Std {
		if !slices.Contains(old.Std, n) {
			fmt.Fprintf(w, "    now also reaches %s\n", n)
			changed = true
		}
	}
	for _, n := range old.Std {
		if !slices.Contains(u.Std, n) {
			fmt.Fprintf(w, "    no longer reaches %s\n", n)
			changed = true
		}
	}
	switch {
	case u.BindsGo && !old.BindsGo:
		fmt.Fprintln(w, "    now binds Go")
	case old.BindsGo && !u.BindsGo:
		fmt.Fprintln(w, "    no longer binds Go")
	case !changed:
		fmt.Fprintln(w, "    no change")
	}
}

// writeTableNote prints whether lists, nomi.toml's tables, allow every
// call r reaches, or how many nomi check refuses. A list entry the check
// refuses on its own, such as a dependency entries_required finds with no
// entry, is the check's to report.
func writeTableNote(w io.Writer, r *vmhost.Reach, lists *vmhost.Capabilities) {
	if refused := len(r.NotAllowed(lists)); refused > 0 {
		fmt.Fprintf(w, "nomi check refuses %s under nomi.toml's [capabilities] tables; nomi capabilities says how they differ\n", calls(refused))
		return
	}
	fmt.Fprintln(w, "nomi.toml's [capabilities] tables allow every call the program reaches")
}

// addToTable is what nomi add writes into nomi.toml's [capabilities.deps]
// table for module, which r reaches, and the lines that say what it did or
// the user must do: with entries_required, the module's tightest entry
// under [capabilities.deps.modules] when the program loads it and the
// table has none; and when it binds Go and the table's go (or go_in_tests,
// when only the tests reach its bindings) does not list it, the module in
// that list when allowGo is set, else a line saying it binds Go and what
// to add. It writes nothing when the table needs nothing, or when
// nomi.toml has no [capabilities.deps], which holds no dependency. r.Lists
// is the tables it writes to, and is replaced by the tables written.
func addToTable(r *vmhost.Reach, module string, allowGo bool) []string {
	lists := r.Lists
	if lists.Deps == nil {
		return nil
	}
	tight := r.Tightest().Deps
	edited := lists.Clone()
	deps := edited.Deps
	var lines []string
	loaded := slices.Contains(r.Modules, module)
	if _, has := deps.Modules[module]; deps.EntriesRequired && !has {
		if loaded {
			entry := tight.Modules[module]
			deps.Modules[module] = entry
			quoted := make([]string, len(entry))
			for i, n := range entry {
				quoted[i] = fmt.Sprintf("%q", n)
			}
			lines = append(lines, fmt.Sprintf("wrote %q = [%s] under [capabilities.deps.modules] in nomi.toml", module, strings.Join(quoted, ", ")))
		} else {
			lines = append(lines, "once a file imports it, entries_required needs its entry under [capabilities.deps.modules]; nomi capabilities --write writes it")
		}
	}
	goList, goKey := &deps.Go, "go"
	if !slices.Contains(tight.Go, module) {
		goList, goKey = &deps.GoInTests, "go_in_tests"
	}
	bindsGo := slices.Contains(tight.Go, module) || slices.Contains(tight.GoInTests, module)
	if bindsGo && !slices.Contains(*goList, module) && !slices.Contains(lists.Deps.Go, module) {
		if allowGo {
			*goList = append(*goList, module)
			lines = append(lines, fmt.Sprintf("added %q to %s under [capabilities.deps] in nomi.toml, so its Go may run", module, goKey))
		} else {
			lines = append(lines, fmt.Sprintf("%s binds Go, which nomi.toml's [capabilities.deps] does not allow; add %q to %s under [capabilities.deps] in nomi.toml, or pass --allow-go to nomi add", module, module, goKey))
		}
	}
	if len(analysis.CapabilityChanges(lists, edited)) == 0 {
		return lines
	}
	if err := writeTable(r.Root, edited); err != nil {
		return []string{fmt.Sprintf("could not write nomi.toml's [capabilities] tables: %v", err)}
	}
	r.Lists = edited
	return lines
}

// writeTable writes c as the [capabilities] tables of the nomi.toml in
// root, with the writer --write uses (analysis.WriteCapabilityTables).
func writeTable(root string, c *vmhost.Capabilities) error {
	path := filepath.Join(root, "nomi.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	edited, _, err := analysis.WriteCapabilityTables(root, data, c)
	if err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	return os.WriteFile(path, edited, info.Mode().Perm())
}
