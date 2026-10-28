package hostadapt

import "strings"

// A project's own host functions and std's share no crossing name.
//
// A host table answers each function by the key its declaration registers
// under: the bare name in a program's entry file and `<module>.<name>` in a
// sibling (frontend.HostKey), and std's rows are keyed the same way by the
// std file (`regex.compile`). So a project's own regex.nomi binding a Go
// function named compile would give its binding std's key, and a machine
// that binds both tables could tell neither apart. The VM therefore names a
// project's crossing ProjectName(key), which no std row can be: a project
// table's keys are put under that name when a host binds it (ProjectTable),
// and a host showing a crossing to a reader shows the key (DisplayName).
// Embedders keep writing tables by the declaration's key.

const projectPrefix = "project:"

// ProjectName is the crossing name of the project host function registered
// under key.
func ProjectName(key string) string { return projectPrefix + key }

// DisplayName is the name a reader knows a crossing by: a project host
// function's key, or a std row's name as it is.
func DisplayName(name string) string {
	return strings.TrimPrefix(name, projectPrefix)
}

// ProjectTable is table with each key put under its ProjectName, as a
// machine binds a project's or an embedder's host table beside std's.
func ProjectTable(table func(*Env) (map[string]Func, error)) func(*Env) (map[string]Func, error) {
	if table == nil {
		return nil
	}
	return func(env *Env) (map[string]Func, error) {
		funcs, err := table(env)
		if err != nil {
			return nil, err
		}
		out := make(map[string]Func, len(funcs))
		for k, f := range funcs {
			out[ProjectName(k)] = f
		}
		return out, nil
	}
}

// ProjectLookup is lookup asked by key for a ProjectName, and nil for any
// other name, as a machine asks a REPL session for its host functions.
func ProjectLookup(lookup func(name string) Func) func(name string) Func {
	return func(name string) Func {
		key, ok := strings.CutPrefix(name, projectPrefix)
		if !ok {
			return nil
		}
		return lookup(key)
	}
}
