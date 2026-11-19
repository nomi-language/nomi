package capability

import (
	"fmt"
	"slices"
	"sort"
	"strings"
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
// NameProblem's form, with goFix for a `go:` name, and then unknown, when
// it is not nil, which answers what is wrong with a name of the right form
// that names nothing a list can allow ("" when it names something), with
// a hint.
func CheckHostList(api, tables, goFix string, names []string, keys func() (map[string]bool, error), unknown func(name string) (problem, hint string)) error {
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
		if unknown == nil {
			continue
		}
		if problem, hint := unknown(name); problem != "" {
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

// GoMessage is the headline of a refused Go binding: module binds Go
// through name (`go:` and the package), and holder's list ("nomi.toml",
// "its own nomi.toml", "the host") lets only the modules in allowed bind
// Go, or none.
func GoMessage(module, name, holder string, allowed []string) string {
	lets := fmt.Sprintf("%s lets only %s bind Go", holder, strings.Join(allowed, ", "))
	if len(allowed) == 0 {
		lets = fmt.Sprintf("%s lets no module bind Go", holder)
	}
	return fmt.Sprintf("%s binds Go through %s, and %s", module, name, lets)
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
