package main

// What `nomi add` and `nomi update` say about a dependency they change:
// the effects its own files have, in the words `nomi effects` prints
// under deps' modules (vmhost.WriteModuleReach), and, when nomi.toml has
// [effects] tables, whether nomi check passes them. The effects are
// the check's (reachOf, vmhost.Reached), worked out over the project
// before and after the change, so a module is charged for the effects it
// has in this program and nothing else.

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

// nomiModules is the name in the program (modcache.Dep.Name) of each of
// paths, module paths, that is a Nomi module the project needs, the name
// the effects lists hold it by.
func nomiModules(root string, paths []string) []string {
	g, err := modcache.Walk(root, modcache.CacheOnly)
	if err != nil || g == nil {
		return nil
	}
	var out []string
	for _, p := range paths {
		if d, ok := g.Find(p); ok && d.Nomi {
			out = append(out, d.Name)
		}
	}
	return out
}

// write prints the effects each of modules has, under modules, and how the
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
		fmt.Fprintf(w, "could not work out the effects of %s: %s\n", what, why)
		return
	}
	now := reachesByPath(d.after)
	var was map[string]vmhost.ModuleReach
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
		vmhost.WriteModuleReach(w, "    ", u)
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

// reachesByPath is moduleReaches by the name each module has in the
// program.
func reachesByPath(r *vmhost.Reach) map[string]vmhost.ModuleReach {
	out := map[string]vmhost.ModuleReach{}
	for _, u := range r.ModuleReaches() {
		out[u.Path] = u
	}
	return out
}

// writeReachChange prints how a module's effects went from old to u: each
// std name it now also has and no longer has, or that nothing changed.
func writeReachChange(w io.Writer, old, u vmhost.ModuleReach) {
	changed := false
	for _, n := range u.Std {
		if !slices.Contains(old.Std, n) {
			fmt.Fprintf(w, "    now also has the effect %s\n", n)
			changed = true
		}
	}
	for _, n := range old.Std {
		if !slices.Contains(u.Std, n) {
			fmt.Fprintf(w, "    no longer has the effect %s\n", n)
			changed = true
		}
	}
	if !changed {
		fmt.Fprintln(w, "    no change")
	}
}

// writeTableNote prints whether lists, nomi.toml's tables, allow every
// effect r has, or how many calls nomi check refuses. A list entry the check
// refuses on its own, such as a dependency entries_required finds with no
// entry, is the check's to report.
func writeTableNote(w io.Writer, r *vmhost.Reach, lists *vmhost.Effects) {
	if refused := len(r.NotAllowed(lists)); refused > 0 {
		fmt.Fprintf(w, "nomi check refuses %s under nomi.toml's [effects] tables; nomi effects says how they differ\n", calls(refused))
		return
	}
	fmt.Fprintln(w, "nomi.toml's [effects] tables allow every effect the program has")
}

// addToTable is what nomi add writes into nomi.toml's [effects.deps]
// table for module, which r reaches, and the lines that say what it did or
// the user must do: with entries_required, the module's tightest entry
// under [effects.deps.modules] when the program loads it and the
// table has none. It writes nothing when the table needs nothing, or when
// nomi.toml has no [effects.deps], which holds no dependency. r.Lists
// is the tables it writes to, and is replaced by the tables written.
func addToTable(r *vmhost.Reach, module string) []string {
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
			lines = append(lines, fmt.Sprintf("wrote %q = [%s] under [effects.deps.modules] in nomi.toml", module, strings.Join(quoted, ", ")))
		} else {
			lines = append(lines, "once a file imports it, entries_required needs its entry under [effects.deps.modules]; nomi effects --write writes it")
		}
	}
	if len(analysis.EffectChanges(lists, edited)) == 0 {
		return lines
	}
	if err := writeTable(r.Root, edited); err != nil {
		return []string{fmt.Sprintf("could not write nomi.toml's [effects] tables: %v", err)}
	}
	r.Lists = edited
	return lines
}

// writeTable writes c as the [effects] tables of the nomi.toml in
// root, with the writer --write uses (analysis.WriteEffectTables).
func writeTable(root string, c *vmhost.Effects) error {
	path := filepath.Join(root, "nomi.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	edited, _, err := analysis.WriteEffectTables(root, data, c)
	if err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	return os.WriteFile(path, edited, info.Mode().Perm())
}
