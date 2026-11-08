package irbuild

// StdlibDeclines lowers the embedded stdlib module `module` afresh, as
// GenerateEditedStdlibTestIR lowers an edited one, and answers the decline of
// each of its bodies that did not lower, placed at the construct the body
// stopped at.
//
// The cached lowering records none: its census has no hooks, so a std
// function that declines is only marked unlowerable, and a call to it
// declines at the call (stdlib.go, "Refusals from inside the stdlib are
// SILENT"). That is right for a program, whose author did not write std. A
// stdlib module's own cases are std's author's code, and `nomi test` and
// `nomi check` on one follow a blocked case's call into std to the body that
// declined (vmhost), which this answers.
//
// A program-std module (programStdModules) is lowered with each program, so
// its declines already reach the program's hooks; it answers nil, as does a
// module the stdlib does not lower.
func StdlibDeclines(module string) []*Decline {
	ensureStdModule(module)
	if isProgramStd(module) {
		return nil
	}
	std := stdlibLowering()
	v := std.views[module]
	if v == nil {
		return nil
	}
	stdBodies(module)
	earlierMods := std.earlierMods[module]
	earlier := std.filtered(func(m string) bool { return earlierMods[m] })
	nv := stdModuleContext(module, v.path, v.pkg, v.nodes, v.fa, earlier)
	var declines []*Decline
	dc := newDeclineCensus(DeclineHooks{At: func(d *Decline) { declines = append(declines, d) }})
	lowerStdlibModuleIn(nv, earlier, dc)
	return declines
}
