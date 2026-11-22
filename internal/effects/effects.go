package effects

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/nomi-language/nomi/internal/suggest"
)

// Name is the name of an effect whose code called the std module std
// (as an import names it after `std::`, "" for none), and whose crossing's
// key is key, as a reader knows it (hostadapt.DisplayName).
func Name(std, key string) string {
	if std != "" {
		return "std::" + std
	}
	return "host:" + key
}

// Listed reports whether a list names name, an effect's name: every effect
// but an unlisted one, a `dbg`, which prints and which no built program
// holds. An unlisted effect is named by its crossing's key alone (`dbg`),
// of neither of the forms `std::` and `host:` that Name gives the others,
// and every list allows it (Allows).
func Listed(name string) bool {
	return strings.HasPrefix(name, "std::") || strings.HasPrefix(name, "host:")
}

// NameProblem is what is wrong with the form of name as an entry in a list
// of std names, or "": `std::` and a module.
func NameProblem(name string) string {
	switch {
	case strings.HasPrefix(name, "host:"):
		return "names a function an embedding Go program supplies, which only that program can allow"
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
// lists are checked. A module none of whose functions has an effect is a
// name: allowing it allows nothing.
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

// Allows reports whether std, a list of names, allows an effect named
// name: an unlisted effect always (Listed), any other when std holds its
// name.
func Allows(std []string, name string) bool {
	return !Listed(name) || slices.Contains(std, name)
}

// CheckHostList is what is wrong with names, the list a host passes as api
// (`vmhost.WithEffects`), or nil. A name listed twice is wrong; a
// `host:` name is right when a host table the host passes with tables
// (`vmhost.WithHosts`) answers its key, and keys answers those keys; any
// other name is held to what an entry of nomi.toml's std lists is held to:
// NameProblem's form, and then UnknownStdName.
func CheckHostList(api, tables string, names []string, keys func() (map[string]bool, error)) error {
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
		if problem := NameProblem(name); problem != "" {
			return fmt.Errorf("%s: %q %s", api, name, problem)
		}
		if problem, hint := UnknownStdName(name); problem != "" {
			return fmt.Errorf("%s names %s, %s (%s)", api, name, problem, hint)
		}
	}
	return nil
}

// Message is the headline of a refusal: who has the effect name (who is
// the subject and its verb, "this program has", "its tests have"), and
// what holder's list ("the host", "[effects.self]") allows, them
// naming who it allows it to (" it", " them", or ""): only the names in
// allowed, or no effects.
func Message(who, name, holder, them string, allowed []string) string {
	allows := fmt.Sprintf("%s allows%s only %s", holder, them, strings.Join(allowed, ", "))
	if len(allowed) == 0 {
		allows = fmt.Sprintf("%s allows%s no effects", holder, them)
	}
	return fmt.Sprintf("%s %s, and %s", who, EffectList([]string{name}), allows)
}

// EffectList is names, each an effect's Name, as a sentence says them
// after "has": "the effect std::fs", "the effects std::fs and std::io",
// "the effects std::env, std::fs and std::io"; "no effects" for none.
func EffectList(names []string) string {
	switch len(names) {
	case 0:
		return "no effects"
	case 1:
		return "the effect " + names[0]
	}
	return "the effects " + strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// Reached is one effect code has, as an editor's hover says it
// (ReachedLine): Name, the effect's name, and whether
// nomi.toml's effects tables allow it. In is where nomi.toml says so,
// each as a phrase that follows "in", a table's name in backticks or "the
// entry for `M`": every place that allows the call, none when no table
// holds it, or the one list that refuses it. Own is the module whose own
// nomi.toml refuses the call, "" when the refusal is the project's
// nomi.toml's; the refusal's diagnostic at the call says the rest. Through
// is, for a function that can reach the call, the functions on the way
// from it to the one whose body makes the call, none when its own body
// makes it.
type Reached struct {
	Name    string
	Through []string
	Allowed bool
	In      []string
	Own     string
}

// ReachedLine is the one line of Markdown an editor's hover shows for r:
// the effect it has, through which functions, and, when the program has
// effects tables (listed) and a list names the effect (Listed), whether
// they allow it.
func ReachedLine(r Reached, listed bool) string {
	line := fmt.Sprintf("Has effects: `%s`", r.Name)
	if len(r.Through) > 0 {
		line += " through `" + strings.Join(r.Through, "`, `") + "`"
	}
	switch {
	case !listed || !Listed(r.Name):
	case r.Allowed && len(r.In) == 0:
		line += ", which no table in `nomi.toml` holds"
	case r.Allowed:
		line += ", which `nomi.toml` allows in " + strings.Join(r.In, " and in ")
	case r.Own != "":
		line += fmt.Sprintf(", which the `nomi.toml` of `%s` does not allow", r.Own)
	case len(r.In) > 0:
		line += ", which `nomi.toml` does not allow in " + r.In[0]
	default:
		line += ", which `nomi.toml` does not allow"
	}
	return line + "."
}

// functionReachShown is how many refused names FunctionReach gives a line
// of their own.
const functionReachShown = 4

// FunctionReach is the Markdown a hover on a function, or on a call of
// one, ends with, for the names in reached, the effects the function can
// have.
// One name is said as ReachedLine says it. Several are kept short: each
// name the lists refuse gets ReachedLine's sentence, since it says where
// the call is and which list refuses it, the first four in name order and
// then one line naming the rest; the names the lists allow, or no list
// holds, follow in one line by name alone. `nomi effects --why` has
// the way to each. listed is ReachedLine's.
func FunctionReach(reached []Reached, listed bool) string {
	if len(reached) == 1 {
		return ReachedLine(reached[0], listed)
	}
	sorted := slices.Clone(reached)
	slices.SortStableFunc(sorted, func(a, b Reached) int { return strings.Compare(a.Name, b.Name) })
	var lines, refusedRest, allowed []string
	for _, r := range sorted {
		switch {
		case r.Allowed || !listed:
			allowed = append(allowed, "`"+r.Name+"`")
		case len(lines) < functionReachShown:
			lines = append(lines, ReachedLine(r, listed))
		default:
			refusedRest = append(refusedRest, "`"+r.Name+"`")
		}
	}
	if len(refusedRest) > 0 {
		lines = append(lines, fmt.Sprintf("And %d more `nomi.toml` does not allow: %s.", len(refusedRest), strings.Join(refusedRest, ", ")))
	}
	if len(allowed) > 0 {
		lead := "Has effects: "
		if len(lines) > 0 {
			lead = "Also has effects: "
		}
		lines = append(lines, lead+strings.Join(allowed, ", ")+".")
	}
	return strings.Join(lines, "\n\n")
}

// HeldLine is the line of Markdown a hover on a call, or on a function or
// a call of one, shows when it can act and reaches no effect a list names:
// it acts only through a handle or a function it is given (a Writer, a
// channel end, a database), whose effect is named where that was made.
const HeldLine = "Has effects on what it is passed."

// MayLine is the line of Markdown a hover on a call through an interface
// shows when only some of the implementations it can reach can act: name
// is the interface function as the call writes it (`UserStore.find`).
func MayLine(name string) string {
	return fmt.Sprintf("May have effects: `%s` is not pure in every implementation.", name)
}

// usesShown is how many app fields UsesLine names before it counts the
// rest.
const usesShown = 5

// UsesLine is the line of Markdown a hover on a function, or on a call of
// one, shows for the app fields it reads or replaces, fields, each as the
// program writes it (`App.limit`), sorted: the first five by name, then a
// count of the rest. It informs: an app field is an input, not an effect,
// and no effects list names one.
func UsesLine(fields []string) string {
	shown := fields[:min(len(fields), usesShown)]
	line := "Uses `" + strings.Join(shown, "`, `") + "`"
	if rest := len(fields) - len(shown); rest > 0 {
		line += fmt.Sprintf(" and %d more", rest)
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

// ReportWidth is the width `nomi effects` wraps a list of names at.
const ReportWidth = 80

// Wrapped is names after a first line's prefix, joined with ", " and
// wrapped at ReportWidth, each later line indented to the prefix's width;
// "(none)" for no names.
func Wrapped(prefix string, names []string) string {
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
		case len(line)+1+len(item) > ReportWidth:
			b.WriteString(line + "\n")
			line = indent + item
		default:
			line += " " + item
		}
	}
	b.WriteString(line)
	return b.String()
}
