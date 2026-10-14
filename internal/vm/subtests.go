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
// only for a row that failed.

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
	var failed []rt.SubtestFailure
	total := 0
	var stop error
	seq.Run(fr.runtime, func(runtime *rt.Frame, item any) bool {
		total++
		_, rowErr := run.activateSlot(row.fnSlot(run), []any{item}, row.captures, runtime, true, run.depth, nil)
		if rowErr == nil {
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
		text, textErr := run.rowText(fr, item)
		if textErr != nil {
			stop = textErr
			return false
		}
		failed = append(failed, rt.SubtestFailure{Row: text, Failure: failure})
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

// rowVerdict is the AssertionFailure a `try` whose boundary is a test body
// carried out of it: `try testing.check(e)`, or a case's or a row's final
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
