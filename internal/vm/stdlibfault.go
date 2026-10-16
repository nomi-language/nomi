package vm

// WHERE A TRAP INSIDE THE STANDARD LIBRARY IS REPORTED.
//
// A trap names the line of the operation that raised it (rt/arith.go's
// rule). Inside the standard library that line is one of std's own source,
// which means nothing to the program's author: `Int.abs(Int.min_value)`
// would report a line of std/int.nomi. So a trap that leaves stdlib code
// for the program's own code is reported at the program's line that called
// into the stdlib, with the trap's own words after it:
//
//	line 3: integer overflow: 0 - -9223372036854775808
//
// THE RULE, as the fault unwinds (Machine.fault, at each activation it
// leaves through):
//
//   - The first site it passes is where it arose. At a program site the
//     trap is placed and keeps its line; one that names no line (a host
//     function's own trap, a once cycle) is given that site's. At a stdlib
//     site it is not placed yet.
//   - The first program site an unplaced trap reaches is the program's call
//     into the stdlib that led to it, two stdlib calls deep or ten. The trap
//     is reported there (rt.Error.At) and placed.
//   - A placed trap is never moved again. So a program lambda that traps
//     while `Iter.map` drives it keeps the lambda's own line: the trap was
//     placed in the lambda, and passing back through `Iter.map` and its
//     call site changes nothing.
//
// A STDLIB SITE IS A STDLIB POSITION (ir.Pos.Stdlib), set by the builder that
// lowers a stdlib module, so a built image and an in-process program answer
// alike and the machine reads no path. A program's tail call into the
// stdlib replaces the program's activation, and with it the site the trap
// would be placed at, so the call's line is kept on the frame
// (frame.stdEntry) and the trap is placed there as it leaves.
//
// A trap that never reaches program code (a stdlib test, a stdlib `once`)
// keeps the line it arose at. A task's failure re-raised where it is
// awaited (rt.Error.Awaited) was placed in the task and is not moved.
//
// THE DEBUGGER STILL STOPS WHERE THE TRAP AROSE: Machine.fault asks it
// before placing, so an exception stop inside the stdlib shows that frame,
// with the program's frames below it in the call stack. Its exception text
// is the placed one the program prints (reportedText).

import (
	"errors"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
)

// place records that the fault err carries has passed the site at, and
// moves it to at's line when at is the first program site a trap from the
// stdlib reaches. arose says at may be the instruction that raised it (the
// first site the fault passes); todo says that instruction is a `todo`,
// whose text names its own file and line and takes no prefix. An error
// that is not a Nomi trap passes through.
func place(err error, at ir.Pos, todo bool) error {
	f, isFault := asFault(err)
	if !isFault {
		var e *rt.Error
		if !errors.As(err, &e) {
			return err
		}
		f = &Fault{err: err}
		err = f
	}
	if f.placed {
		return err
	}
	var e *rt.Error
	if !errors.As(f.err, &e) || e.Awaited {
		f.seen, f.placed = true, true
		return err
	}
	if !f.seen {
		f.seen = true
		if at.Stdlib() {
			return err
		}
		f.placed = true
		if e.Line == 0 && !todo {
			f.at = e.At(at.Line())
		}
		return err
	}
	if at.Stdlib() {
		return err
	}
	f.placed = true
	f.at = e.At(at.Line())
	return err
}

// tailInto notes a tail call at pc into fn, which replaces fr's function: a
// call from the program into the stdlib keeps its line for place, and a
// call into the program's own code forgets it, since a trap from there on
// arises at a program site.
func (fr *frame) tailInto(fn *ir.Func, pc int) {
	if !fn.Pos().Stdlib() {
		fr.stdEntry = 0
		return
	}
	if fr.fn.Pos().Stdlib() {
		return
	}
	if st := fr.c.siteAt(pc); st != nil && !st.pos.Stdlib() {
		fr.stdEntry = st.pos.Line()
	}
}

// leaving places a fault that leaves fr at the program call whose tail call
// into the stdlib replaced fr's function, if one did.
func (fr *frame) leaving(err error) error {
	if fr.stdEntry == 0 {
		return err
	}
	f, isFault := asFault(err)
	if !isFault || f.placed {
		return err
	}
	var e *rt.Error
	f.seen, f.placed = true, true
	if errors.As(f.err, &e) && !e.Awaited {
		f.at = e.At(fr.stdEntry)
	}
	return err
}

// isTodoAt reports whether the instruction at pc is a `todo`.
func isTodoAt(c *code, pc int) bool {
	if pc >= len(c.code) || opcode(c.code[pc]&0xff) != opIR {
		return false
	}
	_, is := c.irs[c.code[pc]>>8].(*ir.Todo)
	return is
}

// reportedText is the text the trap err, stopped for at the site st (the
// instruction at pc) in fr, is reported with once it unwinds: placed as
// Machine.fault places it, at the first program site among fr's and its
// callers'. A debugger stops where the trap arose, before it unwinds, and
// shows this text, the one the program prints, so a stop inside the stdlib
// names the program's line. It places a copy and leaves err as it was.
func reportedText(fr *frame, pc int, st *site, err error) string {
	probe := err
	if f, isFault := asFault(err); isFault {
		cp := *f
		probe = &cp
	}
	probe = fr.leaving(place(probe, st.pos, isTodoAt(fr.c, pc)))
	for p := fr.parent; p != nil; p = p.parent {
		pos, _ := callerAt(p)
		probe = p.leaving(place(probe, pos, false))
	}
	return probe.Error()
}
