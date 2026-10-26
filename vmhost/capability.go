package vmhost

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/stdlibbindings"
	"github.com/nomi-language/nomi/internal/vm"
)

// Capability classes and the targets that lack them.
//
// Every std host function that acts outside the machine in a way some
// target cannot carries a class on its binding row (stdlibbindings.Classes):
// `files` today, `processes` and `network` later. `nomi build` walks what
// the program reaches, as it does to prune the image, and refuses a target
// that lacks a class a reached call needs, naming each call. The walk
// over-approximates (a call in a branch the program never takes counts), so
// `--allow-unsupported <class>` builds anyway; such a call then answers
// `Err(.Unsupported{...})` when it runs.
//
// Every target has files today: a js/wasm module's are in memory, empty
// under Go's Node loader and seeded by `run`'s `files` option in the
// browser (internal/stdio's default_js.go), so `nomi build --web` notes that
// a program uses files rather than refusing it (WebFilesNote).

// ClassFiles is the class of a function that reads or writes files.
const ClassFiles = stdlibbindings.ClassFiles

// TargetLacks is the capability classes a build for goos/goarch cannot
// provide, sorted. None lacks one yet: `processes` will be the first,
// which js/wasm and wasip1 lack.
func TargetLacks(goos, goarch string) []string {
	return testTargets[goos+"/"+goarch]
}

// testTargets are targets only tests build for, each with the classes it
// lacks (LackingTargetForTest), so the refusal is tested through
// CheckTarget before a real target lacks a class.
var testTargets map[string][]string

// WebFilesNote is the line `nomi build --web` prints when the program
// reaches the `files` class.
const WebFilesNote = `nomi build --web: this program uses files; in the browser it sees only the files run's "files" option gives it`

// Uses reports whether the program's reachable code calls a host function
// of class, counting the exported functions when withExports is set.
func (p *Program) Uses(class string, withExports bool) bool {
	return slices.ContainsFunc(p.CapabilityUses(withExports), func(u CapabilityUse) bool { return u.Class == class })
}

// CapabilityUse is one call a program's reachable code makes to a host
// function of a capability class, reported at the program's own call that
// reaches it: a crossing inside the standard library is reported at the
// call of the std function that leads to it (`fs.read_text(path)` in
// `main`), not inside std.
type CapabilityUse struct {
	// Class is the function's class (ClassFiles).
	Class string
	// Crossing is the host function's key (`fs_host.read_text`, the
	// crossing std/fs's `read_text` makes).
	Crossing string
	// Callee is what the program's own call at Site calls: a std
	// function (`fs.read_text`), or Crossing itself when the program's
	// code makes the crossing.
	Callee string
	// Site is that call's position, `file:line:col`, the file named as a
	// built image names it.
	Site string
	// Chain is the functions from a root to the one that holds the call
	// at Site, outermost first.
	Chain []string
}

// CapabilityUses is every call to a host function with a capability class
// that the program reaches from main, the boot and, when withExports is
// set, each exported function: what BuildImage or BuildExportsImage would
// keep. Call it on a program that builds.
func (p *Program) CapabilityUses(withExports bool) []CapabilityUse {
	classes := stdlibbindings.Classes()
	var roots []*ir.Func
	if p.HasMain() {
		if mainFn := p.entryFunc("main"); mainFn != nil {
			roots = vm.MainRoots(p.entry, mainFn)
		}
	}
	if withExports {
		for _, e := range p.res.Exports {
			if e.Func != nil {
				roots = append(roots, e.Func)
			}
		}
	}
	var boots []*ir.Symbol
	if p.entry != nil {
		if boot := p.entry.Boot(); boot != nil {
			boots = append(boots, boot)
		}
	}
	sites := vm.ReachedCrossingSites(p.entry, p.res.IRModules(), roots, boots, func(name string) bool {
		return classes[name] != ""
	})
	out := make([]CapabilityUse, len(sites))
	for i, s := range sites {
		out[i] = CapabilityUse{
			Class:    classes[s.Name],
			Crossing: s.Name,
			Callee:   s.Via,
			Site:     fmt.Sprintf("%s:%d:%d", p.imageName(s.At.File()), s.At.Line(), s.At.Col()),
			Chain:    s.Chain,
		}
	}
	return out
}

// Unsupported is a build refused because the program reaches calls its
// target cannot make: Command is the command as the user wrote it (`nomi
// build --target js/wasm`), Target the target's name, and Uses the calls,
// each of a class in Lacking.
type Unsupported struct {
	Command string
	Target  string
	Lacking []string
	Uses    []CapabilityUse
}

// CheckTarget is the refusal of a build for goos/goarch whose program
// reaches a class the target lacks and allowed does not name, or nil.
// command names the build in the report.
func (p *Program) CheckTarget(command, goos, goarch string, withExports bool, allowed []string) *Unsupported {
	var lacking []string
	for _, class := range TargetLacks(goos, goarch) {
		if !slices.Contains(allowed, class) {
			lacking = append(lacking, class)
		}
	}
	return p.Refuse(command, goos+"/"+goarch, lacking, withExports)
}

// Refuse is the refusal of a build for target, which lacks the classes in
// lacking, or nil when the program reaches none of them.
func (p *Program) Refuse(command, target string, lacking []string, withExports bool) *Unsupported {
	if len(lacking) == 0 {
		return nil
	}
	var uses []CapabilityUse
	for _, u := range p.CapabilityUses(withExports) {
		if slices.Contains(lacking, u.Class) {
			uses = append(uses, u)
		}
	}
	if len(uses) == 0 {
		return nil
	}
	return &Unsupported{Command: command, Target: target, Lacking: lacking, Uses: uses}
}

// Error is the report's first line.
func (u *Unsupported) Error() string {
	var what []string
	for _, class := range u.Lacking {
		if slices.ContainsFunc(u.Uses, func(c CapabilityUse) bool { return c.Class == class }) {
			what = append(what, classDoing(class))
		}
	}
	return fmt.Sprintf("%s: this program %s, which a %s module cannot do", u.Command, strings.Join(what, " and "), u.Target)
}

// Write prints the report: the first line, each call with where it is
// reached from, and how to build anyway.
func (u *Unsupported) Write(w io.Writer) {
	fmt.Fprintf(w, "%s:\n", u.Error())
	var classes []string
	for _, c := range u.Uses {
		from := "fn " + c.Chain[0]
		if len(c.Chain) > 1 {
			from += " through " + strings.Join(c.Chain[1:], ", ")
		}
		what := "`" + c.Callee + "`"
		if c.Callee != c.Crossing {
			what += " calls `" + c.Crossing + "`"
		}
		fmt.Fprintf(w, "  %s: %s (reached from %s)\n", c.Site, what, from)
		if !slices.Contains(classes, c.Class) {
			classes = append(classes, c.Class)
		}
	}
	allow := make([]string, len(classes))
	for i, class := range classes {
		allow[i] = "--allow-unsupported " + class
	}
	if strings.Contains(u.Command, "--web") {
		fmt.Fprintf(w, "hint: pass %s to build anyway; the calls then answer Err(.Unsupported{...})\n", strings.Join(allow, " "))
		return
	}
	fmt.Fprintf(w, "hint: build for your platform without --target, or pass %s\n", strings.Join(allow, " "))
	fmt.Fprintf(w, "      to build anyway; the calls then answer Err(.Unsupported{...})\n")
}

// classDoing is what a program reaching a class does, for the report.
func classDoing(class string) string {
	switch class {
	case ClassFiles:
		return "reads or writes files"
	}
	return "uses " + class
}
