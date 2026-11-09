package vmhost

import (
	"errors"
	"maps"
	"path/filepath"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/irbuild"
	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
)

// Lowering the files an edit reaches against a kept lowering, as a relink
// from file records (filerecord.go) and the language server's lowering of
// a program it opened from its record (checklowering_stored.go) do.

// relowerOne lowers the file with module key key at path from check, its
// check against the others' interfaces, and links it with the objects
// kept holds (irbuild.LowerFileAmong), and answers the lowering, kept with
// the file's new object (Rekeep) and the check it lowered from. moved says
// the file's declarations moved, relowering names the paths of the files
// this edit lowers again that are not lowered yet, and files the module
// keys of the program's files. A lowering that names an instance of
// another file's generic declaration no kept object holds is made again
// from a check of the file that reads that file with its bodies (recheck),
// up to four such files; bodies, when set, are files read with their
// bodies from the start, for which check is a check that does. asked are
// the linkage names a file lowered later asks for again
// (irbuild.LowerFileAmong). why says why it could not lower the file as a
// whole lowering would, which is then the answer; dropped, when set with
// it, is irbuild.FileMiss.Dropped.
func relowerOne(check *analysis.FileCheck, key, path, root string, hasTests bool, kept *irbuild.KeptLowering, moved bool, relowering, asked, files, bodies map[string]bool,
	recheck func(bodies map[string]bool) *analysis.FileCheck, logf func(string, ...any)) (res *irbuild.Result, next *irbuild.KeptLowering, used *analysis.FileCheck, dropped map[string][]string, why string) {
	bodies = maps.Clone(bodies)
	if bodies == nil {
		bodies = map[string]bool{}
	}
	for {
		var err error
		res, err = irbuild.LowerFileAmong(irbuild.FileProgram(check, key, path, root, hasTests), kept, moved, relowering, asked)
		var fm *irbuild.FileMiss
		if errors.As(err, &fm) {
			if t := templateFile(fm.Undefined, files); t != "" && t != key && !bodies[t] && len(bodies) < 4 && recheck != nil {
				bodies[t] = true
				logf("lowering %s again with %s's bodies, for %s", key, t, fm.Undefined)
				check = recheck(bodies)
				continue
			}
			return nil, nil, nil, fm.Dropped, fm.Why
		}
		if err != nil {
			return nil, nil, nil, nil, err.Error()
		}
		break
	}
	next, err := res.Rekeep(kept)
	if err != nil {
		return nil, nil, nil, nil, err.Error()
	}
	return res, next, check, nil, ""
}

// relinkedProgram is the program res, the last file's lowering linked with
// the others' kept objects, makes for the entry at path whose text is src,
// as a whole lowering's would run.
func relinkedProgram(res *irbuild.Result, path, src, root string, hasTests bool, cfg config) (*Program, bool) {
	prog := &irbuild.Program{
		Modules:  []irbuild.Module{{Path: path, Name: strings.TrimSuffix(filepath.Base(path), ".nomi")}},
		HasTests: hasTests,
		Root:     root,
	}
	if hasTests {
		nodes, err := parser.Parse(lexer.Lex(src))
		if err != nil {
			return nil, false
		}
		prog.Modules[0].Nodes = nodes
	}
	return newProgram(prog, res, map[string]string{}, cfg), true
}
