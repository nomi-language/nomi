package analysis

import (
	"maps"
	"slices"
)

// ProgramReads is a program's project files as what a check of each one
// reads of the others is worked out from: each file's imports and the
// headers of its interface impls, by module key. Every decision of which
// files an edit reaches is made through it: a recheck from source
// (recheckDependents), an edit of a program opened from its record
// (advanceStored), a check from an installed check
// (ProgramInterfaces.CheckAfter), and vmhost's lowerings per file and
// relinks from file records.
type ProgramReads struct {
	// Keys are the module keys of the program's project files.
	Keys []string
	// Imports answers the module keys of the project files the file with
	// key imports, and Headers its impl headers, whose ordinals no
	// decision here reads.
	Imports func(key string) []string
	Headers func(key string) []ImplHeader
}

// ImportClosure answers the module keys of the files the file with key
// imports, directly or through others, with key itself.
func (r ProgramReads) ImportClosure(key string) map[string]bool {
	seen := map[string]bool{key: true}
	queue := slices.Clone(r.Imports(key))
	for len(queue) > 0 {
		k := queue[0]
		queue = queue[1:]
		if !seen[k] {
			seen[k] = true
			queue = append(queue, r.Imports(k)...)
		}
	}
	return seen
}

// Closure answers the module keys of the files a check of the file with
// key reads whole, with key itself: its import closure, and for each impl
// it sees in a file outside that (ImplHeader.RelevantTo), the files the
// impl's header names, each with its import closure. Such an impl names
// its interface, which may be declared in a file key never imports.
func (r ProgramReads) Closure(key string) map[string]bool {
	visible := r.ImportClosure(key)
	out := maps.Clone(visible)
	for _, k := range r.Keys {
		if visible[k] {
			continue
		}
		for _, h := range r.Headers(k) {
			if !h.RelevantTo(visible) {
				continue
			}
			for _, f := range h.Files {
				if !out[f] {
					maps.Copy(out, r.ImportClosure(f))
				}
			}
		}
	}
	return out
}

// Readers answers the module keys of the files whose checks read what an
// edit changed (Reads). A file of changed is among them.
func (r ProgramReads) Readers(changed map[string]bool, moved []ImplHeader) map[string]bool {
	out := map[string]bool{}
	for _, key := range r.Keys {
		if r.Reads(key, changed, moved) {
			out[key] = true
		}
	}
	return out
}

// Reads reports whether a check of the file with key reads what an edit
// changed: its Closure holds one of changed, the files whose interfaces
// changed, or it sees one of moved, the impls that came, went or changed
// (ImplHeader.RelevantTo over its import closure).
func (r ProgramReads) Reads(key string, changed map[string]bool, moved []ImplHeader) bool {
	visible := r.ImportClosure(key)
	return holdsAny(visible, changed) || slices.ContainsFunc(moved, func(h ImplHeader) bool { return h.RelevantTo(visible) }) ||
		len(changed) > 0 && holdsAny(r.Closure(key), changed)
}

// holdsAny reports whether set holds a key of keys.
func holdsAny(set, keys map[string]bool) bool {
	for k := range keys {
		if set[k] {
			return true
		}
	}
	return false
}

// programReads answers pi's files as ProgramReads, the file with key
// importing what its source imports ("" for none).
func (pi *ProgramInterfaces) programReads(key string) ProgramReads {
	var own []string
	if key != "" {
		own = pi.projectKeys(key, importsOf(pi.source(key)))
	}
	return ProgramReads{
		Keys: pi.Keys(),
		Imports: func(k string) []string {
			if k == key {
				return own
			}
			return pi.imports[k]
		},
		Headers: pi.implHeaders,
	}
}

// Readers answers the module keys of pi's files whose checks read what an
// edit changed: the files with the keys of changed, whose interfaces
// changed, and moved, the impls that came, went or changed
// (ProgramReads.Readers).
func (pi *ProgramInterfaces) Readers(changed []string, moved []ImplHeader) map[string]bool {
	set := map[string]bool{}
	for _, k := range changed {
		set[k] = true
	}
	return pi.programReads("").Readers(set, moved)
}
