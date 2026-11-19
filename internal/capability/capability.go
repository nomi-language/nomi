package capability

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/nomi-language/nomi/internal/suggest"
)

// Name is the name of an outside call whose code called the std module std
// (as an import names it after `std::`, "" for none), whose crossing binds
// the Go package goPath ("" when unknown or none), and whose crossing's key
// is key, as a reader knows it (hostadapt.DisplayName).
func Name(std, goPath, key string) string {
	if std != "" {
		return "std::" + std
	}
	if goPath != "" {
		return "go:" + goPath
	}
	return "host:" + key
}

// BindsGo reports whether name, an outside call's Name, is a `go` binding's.
func BindsGo(name string) bool {
	return strings.HasPrefix(name, "go:")
}

// NameProblem is what is wrong with the form of name as an entry in a list
// of std names, or "": `std::` and a module. goFix, which ends the problem
// with a `go:` name, says how the list's holder allows a Go binding
// instead.
func NameProblem(name, goFix string) string {
	switch {
	case strings.HasPrefix(name, "host:"):
		return "names a function an embedding Go program supplies, which only that program can allow"
	case BindsGo(name):
		return "names a Go package; " + goFix
	case strings.HasPrefix(name, "std::"):
		if rest := strings.TrimPrefix(name, "std::"); rest == "" || strings.ContainsAny(rest, " /.") {
			return "is not a std module; write one as an import does, such as \"std::fs\""
		}
		return ""
	}
	return "is not a std module; write one as an import does, such as \"std::fs\""
}

// UnknownStdName is what is wrong with name, an entry of a list of std
// names of the right form (NameProblem), when it names no std module a
// program can import, as the end of a message that names it, with a hint
// naming the nearest module; both are "" when it names one. The modules
// are stdNames, which `go generate ./internal/stdlibadapters` writes from
// std, so a runner that links no front end checks a name as nomi.toml's
// lists are checked. A module none of whose functions reaches outside is
// a name: allowing it allows nothing.
func UnknownStdName(name string) (problem, hint string) {
	if _, found := slices.BinarySearch(stdNames, name); found {
		return "", ""
	}
	// The nearest module by its own name, so the shared `std::` does not
	// make a far one look close.
	modules := make([]string, len(stdNames))
	for i, v := range stdNames {
		modules[i] = strings.TrimPrefix(v, "std::")
	}
	hint = "name a std module as an import writes it, such as std::fs"
	if near := suggest.Closest(strings.TrimPrefix(name, "std::"), modules); near != "" {
		hint = fmt.Sprintf("did you mean %q?", "std::"+near)
	}
	return "which is not a std module", hint
}

// StdNames is every std name a list may hold, sorted: the names
// UnknownStdName accepts, which the language server completes in a list.
func StdNames() []string {
	return slices.Clone(stdNames)
}

// ModuleProblem is what is wrong with the form of path as an entry in a
// list of modules whose code may bind Go, or "": a module's path as go.mod
// writes it.
func ModuleProblem(path string) string {
	switch {
	case strings.HasPrefix(path, "std::"):
		return "is a std module, which a list of std names holds; this list names modules, such as \"example.com/pg\""
	case BindsGo(path):
		return "names a Go package; name the module that binds it, as go.mod writes it, such as \"example.com/pg\""
	case strings.HasPrefix(path, "host:"):
		return "names a function an embedding Go program supplies; this list names modules, such as \"example.com/pg\""
	case strings.TrimSpace(path) == "" || strings.ContainsAny(path, " \t\\"):
		return "is not a module path; write one as go.mod writes it, such as \"example.com/pg\""
	}
	return ""
}

// Allows reports whether an outside call named name, written in module's
// files, is allowed by std, a list of std names, and goModules, the
// modules whose code may bind Go: a Go binding when goModules holds its
// module, any other call when std holds its name.
func Allows(std, goModules []string, name, module string) bool {
	if BindsGo(name) {
		return slices.Contains(goModules, module)
	}
	return slices.Contains(std, name)
}

// CheckHostList is what is wrong with names, the list a host passes as api
// (`vmhost.WithCapabilities`), or nil. A name listed twice is wrong; a
// `host:` name is right when a host table the host passes with tables
// (`vmhost.WithHosts`) answers its key, and keys answers those keys; any
// other name is held to what an entry of nomi.toml's std lists is held to:
// NameProblem's form, with goFix for a `go:` name, and then
// UnknownStdName.
func CheckHostList(api, tables, goFix string, names []string, keys func() (map[string]bool, error)) error {
	var answered []string
	keysRead := false
	seen := map[string]bool{}
	for _, name := range names {
		if seen[name] {
			return fmt.Errorf("%s lists %s twice", api, name)
		}
		seen[name] = true
		if strings.HasPrefix(name, "host:") {
			if !keysRead {
				got, err := keys()
				if err != nil {
					return err
				}
				for k := range got {
					answered = append(answered, "host:"+k)
				}
				sort.Strings(answered)
				keysRead = true
			}
			if slices.Contains(answered, name) {
				continue
			}
			hint := "no table is passed with " + tables
			if len(answered) > 0 {
				hint = "its tables answer " + strings.Join(answered, ", ")
			}
			return fmt.Errorf("%s names %s, which no table passed with %s answers (%s)", api, name, tables, hint)
		}
		if problem := NameProblem(name, goFix); problem != "" {
			return fmt.Errorf("%s: %q %s", api, name, problem)
		}
		if problem, hint := UnknownStdName(name); problem != "" {
			return fmt.Errorf("%s names %s, %s (%s)", api, name, problem, hint)
		}
	}
	return nil
}

// Message is the headline of a refusal: who reaches name ("this program
// reaches"), and what holder's list ("the host", "nomi.toml") allows,
// them naming who it allows it to (" it", " them", or ""): only the names
// in allowed, or nothing outside the program.
func Message(who, name, holder, them string, allowed []string) string {
	allows := fmt.Sprintf("%s allows%s only %s", holder, them, strings.Join(allowed, ", "))
	if len(allowed) == 0 {
		allows = fmt.Sprintf("%s allows%s nothing outside the program", holder, them)
	}
	return fmt.Sprintf("%s %s, and %s", who, name, allows)
}

// GoMessage is the headline of a refused Go binding: who ("lib.example/pg")
// binds Go through name (`go:` and the package), and holder's list
// ("[capabilities.deps]", "the host") lets only the modules in allowed bind
// Go, or no whom ("dependency", "module").
func GoMessage(who, name, holder, whom string, allowed []string) string {
	lets := fmt.Sprintf("%s lets only %s bind Go", holder, strings.Join(allowed, ", "))
	if len(allowed) == 0 {
		lets = fmt.Sprintf("%s lets no %s bind Go", holder, whom)
	}
	return fmt.Sprintf("%s binds Go through %s, and %s", who, name, lets)
}

// GoFlagMessage is the headline of a Go binding refused by a table whose go
// is a flag for the code it holds, which is false: who binds Go through
// name, and holder ("[capabilities.self]") does not let it.
func GoFlagMessage(who, name, holder string) string {
	return fmt.Sprintf("%s binds Go through %s, and %s does not let it bind Go", who, name, holder)
}

// ReachedLine is the one line of Markdown an editor's hover shows on an
// outside call named name: what it reaches, or for a Go binding that it
// binds Go, and, when the program has capability tables (listed), whether
// they allow it. in is where nomi.toml says so, each as a phrase that
// follows "in", a table's name in backticks or "the entry for `M`": every
// place that allows the call, none when no table holds it, or the one
// list that refuses it. own is the module whose own nomi.toml refuses the
// call, "" when the refusal is the project's nomi.toml's; the refusal's
// diagnostic at the call says the rest.
func ReachedLine(name string, listed, allowed bool, in []string, own string) string {
	line := fmt.Sprintf("Reaches `%s`", name)
	if BindsGo(name) {
		line = fmt.Sprintf("Binds Go: `%s`", name)
	}
	switch {
	case !listed:
	case allowed && len(in) == 0:
		line += ", which no table in `nomi.toml` holds"
	case allowed:
		line += ", which `nomi.toml` allows in " + strings.Join(in, " and in ")
	case own != "":
		line += fmt.Sprintf(", which the `nomi.toml` of `%s` does not allow", own)
	case len(in) > 0:
		line += ", which `nomi.toml` does not allow in " + in[0]
	default:
		line += ", which `nomi.toml` does not allow"
	}
	return line + "."
}

// HostAddHint is the help for a call over a host's list: add name to the
// names the host passes to api.
func HostAddHint(name, api string) string {
	return fmt.Sprintf("add %q to the names the host passes to %s", name, api)
}

// RemoveHint is the other way out of a refusal: remove the call, or for a
// boot's Startup (boot), have the boot take none.
func RemoveHint(boot bool) string {
	if boot {
		return "have the boot take no Startup"
	}
	return "remove the call"
}
