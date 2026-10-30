package vm

// RUNNING A `subtests` STATEMENT (ir.Subtests).
//
// Each row is one activation of the row function, opened as a test body's
// is (testBody), so a failed assertion in it leaves the activation as an
// *assertFailure, which this catches and records under the row, and the next
// row runs. Anything else that leaves a row (a Nomi fault, a machine limit)
// leaves the statement too, as it would leave a call. After the last row the
// combined failure, when there is one, is delivered as one assertion's is
// (assertExit): out of a test body as the case's report, out of an ordinary
// `fn` as its `Err(AssertionFailure)`.
//
// The row is rendered by rowText, as a `values:` row renders a value, and
// only for a row that failed. A statement with a name calls the name
// function once per row, before the row, and records a failed row under
// the name rendered as a String; a name that faults fails its row without
// running it, recorded under the rendered row (rt.SubtestNameTrapFailure).
//
// Each row that runs to its end, passed or failed, is a case of the running
// test: the statement reports how many to the case's test capture
// (rt.EnterSubtests), and the report counts them (rt.CountCases).

import (
	"errors"
	"fmt"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
)

func (m *Machine) subtestsInstr(fr *frame, n *ir.Subtests) (err error) {
	defer func() {
		if p := recover(); p != nil {
			if failure, ok := p.(iterationFailure); ok {
				err = failure.err
				return
			}
			if failure, ok := p.(keyFailure); ok {
				err = failure.err
				return
			}
			if fault, ok := p.(*rt.Error); ok {
				err = &Fault{err: fault}
				return
			}
			panic(p)
		}
	}()
	src, err := fr.read(n.Rows())
	if err != nil {
		return err
	}
	seq, ok := src.(rt.Seq[any])
	if !ok {
		return fmt.Errorf("vm: subtests reads %T, not a sequence", src)
	}
	fv, err := fr.read(n.Row())
	if err != nil {
		return err
	}
	row, ok := fv.(*functionValue)
	if !ok || row == nil || row.arity != 1 || row.host != nil {
		return fmt.Errorf("vm: subtests requires a row function of one parameter")
	}
	run := m
	if m.dbg != nil {
		view := *m
		view.dvia = "subtests"
		run = &view
	}
	var name *functionValue
	if n.Name() != ir.NoTemp {
		nv, err := fr.read(n.Name())
		if err != nil {
			return err
		}
		name, ok = nv.(*functionValue)
		if !ok || name == nil || name.arity != 1 || name.host != nil {
			return fmt.Errorf("vm: subtests requires a name function of one parameter")
		}
	}
	var failed []rt.SubtestFailure
	total := 0
	// ran counts the rows that ran to their end, passed or failed: the
	// running test case's cases (rt.EnterSubtests, rt.CountCases).
	ran := 0
	done := rt.EnterSubtests(fr.runtime)
	defer func() { done(ran) }()
	var stop error
	seq.Run(fr.runtime, func(runtime *rt.Frame, item any) bool {
		total++
		label := ""
		if name != nil {
			text, trap, nameErr := run.rowName(name, item, runtime)
			if nameErr != nil {
				stop = nameErr
				return false
			}
			if trap != nil {
				// The name faulted: the row fails without running its body,
				// under the rendered row, with the fault's text.
				rowText, textErr := run.rowText(fr, item)
				if textErr != nil {
					stop = textErr
					return false
				}
				failed = append(failed, rt.SubtestFailure{Row: rowText,
					Failure: rt.SubtestNameTrapFailure(n.NameLine(), n.NameText(), trap.Error())})
				ran++
				return true
			}
			label = rt.RowText(text)
		}
		_, rowErr := run.activateSlot(row.fnSlot(run), []any{item}, row.captures, runtime, true, run.depth, nil)
		if rowErr == nil {
			ran++
			return true
		}
		failure, isAssertion := asAssertFailure(rowErr)
		if !isAssertion {
			failure, isAssertion = rowVerdict(rowErr)
		}
		if !isAssertion {
			stop = rowErr
			return false
		}
		text := label
		if name == nil {
			var textErr error
			if text, textErr = run.rowText(fr, item); textErr != nil {
				stop = textErr
				return false
			}
		}
		failed = append(failed, rt.SubtestFailure{Row: text, Failure: failure})
		ran++
		return true
	})
	if stop != nil {
		return stop
	}
	combined := rt.SubtestsFailure(n.Pos().Line(), n.Text(), total, failed)
	if combined == nil {
		return nil
	}
	return assertExit(fr, combined)
}

// rowName runs a row's name function on item. It answers the name, or trap
// when the name faulted (a Nomi trap, which fails the row), or err for
// anything else that leaves it (a machine limit), which leaves the
// statement.
func (m *Machine) rowName(name *functionValue, item any, runtime *rt.Frame) (text string, trap error, err error) {
	defer func() {
		if p := recover(); p != nil {
			if fault, ok := p.(*rt.Error); ok {
				text, trap, err = "", fault, nil
				return
			}
			panic(p)
		}
	}()
	out, callErr := m.activateSlot(name.fnSlot(m), []any{item}, name.captures, runtime, false, m.depth, nil)
	if callErr != nil {
		if fault, ok := asFault(callErr); ok {
			return "", fault, nil
		}
		var fault *rt.Error
		if errors.As(callErr, &fault) {
			return "", fault, nil
		}
		return "", nil, callErr
	}
	s, ok := out.(string)
	if !ok {
		return "", nil, fmt.Errorf("vm: a subtests name answered %T, not a String", out)
	}
	return s, nil, nil
}

// rowVerdict is the AssertionFailure a `?` whose boundary is a test body
// carried out of it: `testing.check(e)?`, or a case's or a row's final
// `Result<_, AssertionFailure>` statement (irbuild's testVerdict). The case
// runner (testFn) and a `subtests` row read it back the same way.
func rowVerdict(err error) (*rt.AssertionFailure, bool) {
	var early *tryReturn
	if !errors.As(err, &early) || early.at == nil {
		return nil, false
	}
	e, isRec := enumRecord(early.value)
	if !isRec || !isEnum(e, "results.Result") || variantName(e) != "Err" {
		return nil, false
	}
	payload, has := payloadOf(e)
	if !has {
		return nil, false
	}
	f, isFailure := nomiFailureOf(payload)
	if !isFailure {
		return nil, false
	}
	return f.Report(), true
}
