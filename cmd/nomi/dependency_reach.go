package main

// What `nomi add` and `nomi update` say about a dependency they change:
// the effects the code its [dependencies] entry covers has, its own files
// and those of every package the project reaches through it, in the words
// `nomi effects` prints under dependencies' entries
// (vmhost.WritePackageReach), and, when nomi.toml has [effects] tables,
// whether nomi check passes them. The effects are the check's (reachOf,
// vmhost.Reached), worked out over the project before and after the
// change, so an entry is charged for the effects it has in this program
// and nothing else.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/pkgcache"
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

// changedEntries is the key of each of the project's [dependencies]
// entries through which it reaches one of ids that is a Nomi package
// (pkgcache.Graph.Through), sorted: the entries whose `effects` keys hold
// the packages an update changed.
func changedEntries(root string, ids []pkgcache.ID) []string {
	g, err := pkgcache.Walk(root, pkgcache.CacheOnly)
	if err != nil || g == nil {
		return nil
	}
	var out []string
	for _, id := range ids {
		if d, ok := g.Find(id); ok && d.Nomi {
			for _, key := range g.Through(d.Name) {
				if !slices.Contains(out, key) {
					out = append(out, key)
				}
			}
		}
	}
	slices.Sort(out)
	return out
}

// write prints the effects the code each of entries covers has, under
// packages, and how the update changed it when the reach before is known:
// an entry through which the program loads no file is not imported yet.
// Then, when nomi.toml has tables, what they need and whether nomi check
// passes them (writeTableNote). A reach that could not be worked out is
// one line saying why.
func (d *dependencyReach) write(w io.Writer, entries []string, table func(*vmhost.Reach) []string) {
	if len(entries) == 0 {
		return
	}
	if d.failed != nil {
		what := entries[0]
		if len(entries) > 1 {
			what = "the updated packages"
		}
		why, _, _ := strings.Cut(strings.TrimSpace(d.failed.Error()), "\n")
		fmt.Fprintf(w, "could not work out the effects of %s: %s\n", what, why)
		return
	}
	now := reachesByKey(d.after)
	var was map[string]vmhost.PackageReach
	if d.before != nil {
		was = reachesByKey(d.before)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "packages")
	for _, m := range entries {
		fmt.Fprintln(w, "  "+m)
		u, loaded := now[m]
		if !loaded {
			fmt.Fprintln(w, "    not imported yet")
			continue
		}
		vmhost.WritePackageReach(w, "    ", u)
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

// reachesByKey is Reach.PackageReaches by the key of each [dependencies]
// entry.
func reachesByKey(r *vmhost.Reach) map[string]vmhost.PackageReach {
	out := map[string]vmhost.PackageReach{}
	for _, u := range r.PackageReaches() {
		out[u.Key] = u
	}
	return out
}

// writeReachChange prints how an entry's effects went from old to u: each
// std name its code now also has and no longer has, or that nothing
// changed.
func writeReachChange(w io.Writer, old, u vmhost.PackageReach) {
	changed := false
	for _, n := range u.All() {
		if !slices.Contains(old.All(), n) {
			fmt.Fprintf(w, "    now also has the effect %s\n", n)
			changed = true
		}
	}
	for _, n := range old.All() {
		if !slices.Contains(u.All(), n) {
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

// addToTable is what nomi add writes into nomi.toml for key, the
// [dependencies] entry it added, and the lines that say what it did or the
// user can do, when the project holds its dependencies
// ([effects.dependencies]) and the entry has no `effects` key: with
// entries_required, the entry's tightest `effects` (and `effects_in_tests`
// when the tests reach more), as --write writes them, when the program
// loads files through it; without, a line that offers them. It writes
// nothing when nomi.toml does not hold its dependencies. r.Lists is the
// tables it writes to, and is replaced by the tables written.
func addToTable(r *vmhost.Reach, key string) []string {
	lists := r.Lists
	deps := lists.Dependencies
	if deps == nil {
		return nil
	}
	if _, has := deps.Entries[key]; has {
		return nil
	}
	loaded := slices.Contains(r.Dependencies, key)
	if !loaded {
		if deps.EntriesRequired {
			return []string{"once a file imports it, entries_required needs an effects key on its [dependencies] entry; nomi effects --write writes it"}
		}
		return nil
	}
	tight := r.Tightest().Dependencies
	edited := lists.Clone()
	entry := tight.Entries[key]
	edited.SetEntry(analysis.ListEntryEffects, key, entry)
	text := fmt.Sprintf("effects = [%s]", quotedList(entry))
	if inTests := tight.EntriesInTests[key]; len(inTests) > 0 {
		edited.SetEntry(analysis.ListEntryEffectsInTests, key, inTests)
		text += fmt.Sprintf(" and effects_in_tests = [%s]", quotedList(inTests))
	}
	if !deps.EntriesRequired {
		return []string{fmt.Sprintf("to hold %s to what it has now, write %s on its [dependencies] entry; nomi effects --write writes it", key, text)}
	}
	if err := writeTable(r.Root, edited); err != nil {
		return []string{fmt.Sprintf("could not write nomi.toml's [effects] tables: %v", err)}
	}
	r.Lists = edited
	return []string{fmt.Sprintf("wrote %s on %s under [dependencies] in nomi.toml", text, key)}
}

// quotedList is names quoted and joined as a TOML list holds them.
func quotedList(names []string) string {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = fmt.Sprintf("%q", n)
	}
	return strings.Join(quoted, ", ")
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
