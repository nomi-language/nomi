package irbuild

// A TEST CASE UNDER A `tests` GROUP, RETAINED FOR THE VM.
//
// A group contributes three things to each case in it:
//
//   - `boot server.boot(startup)`: a fresh app per case, built on the case's
//     own frame and torn down (supervisors drained, boot cleanups run) when
//     the case ends. The case records, in its `ir.TestGroup`, the entry
//     boot's symbol and, for a boot that takes a Startup, a zero-parameter
//     function that evaluates the `boot` line's argument or the parameter's
//     default; `ir.Module` records the boot through `AddTestBoot`, and the VM
//     runner calls the startup function and then the boot on the frame rt
//     hands the case.
//   - `clock Clock.Virtual`: the case, its boot and its drain run in a
//     virtual-time bubble. The VM runner supplies `rt/vclock.RunCase`, and
//     rt's runner opens it.
//   - `setup` and the case's pattern: the setup's statements run in the
//     case's own activation before the body, and the pattern binds its
//     value. See irtestsetup.go.
//
// A GROUPED CASE IS RETAINED FOR THE VM ONLY. The test-body reader does not
// stage a setup, and the body may read an app field, which the reader does
// not spell, so a grouped case is never read back.

import (
	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/ir"
)

// irTestGroup answers what the case's group asks a runner to arrange, or false
// when it is outside what the VM runner can arrange.
func (g *gen) irTestGroup(c testCaseDecl) (ir.TestGroup, bool) {
	group := ir.TestGroup{VirtualClock: c.clock == "Virtual"}
	for _, s := range c.setups {
		if s.boot == nil {
			continue
		}
		t, _ := s.group.(*ast.TestDecl)
		fd := analysis.TestGroupBoot(g.fa, t)
		owner := g.irEntryBootOwner(fd)
		if fd == nil || owner == nil {
			irDeclineNote("a test boot that names no entry boot this program lowers")
			return ir.TestGroup{}, false
		}
		startup, ok := g.irTestStartup(t, fd)
		if !ok {
			return ir.TestGroup{}, false
		}
		group.Boot = owner.irCalleeSym(fd, fd.Name)
		group.Startup = startup
		g.irRecordTestBoot(group.Boot)
	}
	return group, true
}

// irEntryBootOwner answers the unit that declares entry boot fd, whose symbol
// table names it, or nil.
func (g *gen) irEntryBootOwner(fd *ast.FuncDef) *gen {
	if fd == nil {
		return nil
	}
	if g.reg == nil {
		if declaresBoot(g.nodes) == fd {
			return g
		}
		return nil
	}
	for _, unit := range g.reg.gens {
		if unit != nil && declaresBoot(unit.nodes) == fd {
			return unit
		}
	}
	return nil
}

// irRecordTestBoot notes a boot this unit's cases start, recorded on the
// module with AddTestBoot once every unit is built.
func (g *gen) irRecordTestBoot(sym *ir.Symbol) {
	for _, seen := range g.irTestBoots {
		if seen == sym {
			return
		}
	}
	g.irTestBoots = append(g.irTestBoots, sym)
}

// irTestStartupToken is the declaration identity of a group's startup
// function: the group whose `boot` line it evaluates.
type irTestStartupToken struct{ group *ast.TestDecl }

// irTestStartup builds, once per group, the zero-parameter function that
// evaluates the Startup the entry boot fd receives: the argument of the
// group's `boot` line, or the parameter's declared default when the line
// passes none. It answers the function's symbol, or nil when fd takes no
// parameter and the runner calls it with no arguments.
//
// The line's argument is an expression of the test file, lowered here. The
// default is an expression of the entry file and may name what only that
// file declares, so the entry's unit lowers it, into its own module.
func (g *gen) irTestStartup(t *ast.TestDecl, fd *ast.FuncDef) (*ir.Symbol, bool) {
	call, isCall := t.Boot.(*ast.Call)
	if !isCall || len(call.Args) > 1 || len(fd.Params) > 1 {
		irDeclineNote("a test boot line that does not pass at most one Startup")
		return nil, false
	}
	if len(fd.Params) == 0 {
		return nil, true
	}
	if len(call.Args) == 1 {
		arg := call.Args[0]
		if named, isNamed := arg.(*ast.NamedArg); isNamed {
			arg = named.Value
		}
		return g.irStartupFunc(irTestStartupToken{t}, fd, arg)
	}
	owner := g.irEntryBootOwner(fd)
	if owner == nil || fd.Params[0].Default == nil {
		irDeclineNote("a test boot line that passes no Startup to a boot whose parameter has no default")
		return nil, false
	}
	return owner.irStartupFunc(irBootDefaultToken{fd}, fd, fd.Params[0].Default)
}

// irBootDefaultToken is the declaration identity of the function that
// evaluates entry boot fd's parameter default, shared by every group whose
// `boot` line relies on it.
type irBootDefaultToken struct{ boot *ast.FuncDef }

// irStartupFunc builds, once per token, the zero-parameter function that
// lowers arg as the Startup entry boot fd receives, into this unit's module.
func (g *gen) irStartupFunc(token any, fd *ast.FuncDef, arg ast.Node) (*ir.Symbol, bool) {
	sym := g.irCalleeSym(token, "boot startup")
	if g.irModule().FuncFor(sym) != nil {
		return sym, true
	}
	k := g.irEntryBootStartupKind(fd)
	// kindInvalid: lookup — the checker typed no Startup parameter for the boot.
	if k == kindInvalid || !irRetainedValueKind(k) {
		irDeclineNote("a test boot whose Startup parameter has no retained kind")
		return nil, false
	}
	at := g.irNodePos(arg)
	f := ir.NewFuncFor(at, sym)
	sh := &irFuncShell{fn: f, syms: map[string]*ir.Symbol{}, params: map[string]ir.Temp{}, patternOK: true}
	sh.frame = newIRFuncFrame(f)
	sh.entry = f.NewBlock(at, "entry")
	sh.result = f.NewTemp()
	sh.slot = ir.NewSlot(at, sh.result, g.irTypeOf(k))
	sh.entry.Append(sh.slot)
	sh.prologue = 1
	bl := &irScalarBuilder{g: g, sh: sh, f: f, b: sh.entry, returnKind: k, bound: map[string]ir.Temp{}, boundK: map[string]kind{}}
	val, got, _, ok := bl.lowerWant(arg, k)
	if !ok || got != k {
		irDeclineNote("a test boot's Startup argument outside the retained shape")
		return nil, false
	}
	bl.resultCopy(arg, val)
	bl.side(sh.result, irScalarSide{k: k})
	bl.b.SetTerm(ir.NewReturn(at, sh.result))
	if err := ir.Lint(f); err != nil {
		irDeclineNote("a test boot startup graph that does not lint: " + err.Error())
		return nil, false
	}
	g.irModule().AddFunc(f)
	return sym, true
}

// irEntryBootStartupKind is the kind of entry boot fd's Startup parameter, as
// the checker typed it in the file that declares the boot.
func (g *gen) irEntryBootStartupKind(fd *ast.FuncDef) kind {
	if g.fa == nil || fd == nil {
		return kindInvalid
	}
	owner := g.fa.ScopedFunctionFiles[fd]
	if owner == nil {
		owner = g.fa
	}
	sym := owner.Definitions[analysis.Pos{Line: fd.Line, Col: fd.Col}]
	if sym == nil {
		return kindInvalid
	}
	ft, isFunc := sym.Type.(*analysis.FuncType)
	if !isFunc || len(ft.Params) != 1 {
		return kindInvalid
	}
	return g.project(ft.Params[0])
}

// irTestGrouped reports whether a case inherits anything from a group, which
// is what makes it VM-only.
func irTestGrouped(c testCaseDecl) bool {
	return len(c.setups) != 0 || c.ctxPat != nil || c.clock != ""
}

// recordedQualCall lowers a qualified call inside a grouped case's assertion
// subject and then records its `values:` rows after the call's operands are
// evaluated: after every row the operands themselves recorded, with the
// literal rule decided by the call's result.
//
// A path that does not lower its operands through irQualLowerArgs leaves no
// operands to record, and a report missing a row is a wrong failure message,
// so such a call declines. A piped call records nothing.
func (bl *irScalarBuilder) recordedQualCall(t *ast.Call, lower func() (ir.Temp, kind, bool, bool)) (ir.Temp, kind, bool, bool) {
	prevCall, prevArgs, prevSeen := bl.qualRecord, bl.qualRecordArgs, bl.qualRecordSeen
	bl.qualRecord, bl.qualRecordArgs, bl.qualRecordSeen = t, irQualArgs{}, false
	v, k, mobile, ok := lower()
	args, seen := bl.qualRecordArgs, bl.qualRecordSeen
	bl.qualRecord, bl.qualRecordArgs, bl.qualRecordSeen = prevCall, prevArgs, prevSeen
	if !ok {
		return ir.NoTemp, kindInvalid, false, false
	}
	if t == bl.pipedCall {
		return v, k, mobile, true
	}
	if !seen || !args.ok || len(args.temps) != len(t.Args) {
		irDeclineNote("a qualified call in an assertion subject whose operands no row can name")
		return ir.NoTemp, kindInvalid, false, false
	}
	bl.recordCallArgs(t.Args, args.temps, args.kinds, k)
	return v, k, mobile, true
}

// rowsUnrecorded reports whether lowering call t inside an assertion subject
// would leave its `values:` rows unrecorded: true unless recordedQualCall is
// lowering t and records them after it.
func (bl *irScalarBuilder) rowsUnrecorded(t *ast.Call) bool {
	return bl.recording > 0 && bl.qualRecord != t
}
