package rt

// An assertable assertion subject: a user type whose own `Assertable.failure`
// decides whether the assertion holds.
//
// std/assertions declares the interface as
//
//	pub interface Assertable {
//	  fn failure(value: self): Maybe<AssertionDetails>
//	}
//
// so a subject answers its own verdict, and the answer's polarity is the
// opposite of a `Maybe` subject's: `assert` holds on `None` and fails on
// `Some(details)`, `refute` is the mirror, and either way the assertion's value
// is the original subject. Getting that inversion backwards is invisible to any
// output comparison over this repository's corpus, because a passing
// assertion prints nothing either way and `tests/` contains no program
// that fails one — see internal/irbuild/testdata/assertable_subject_report.nomi
// for the deliberately red fixture that does compare it.
//
// # Which half is here and which half cannot be
//
// The same split JudgeBool and ShapeFailure already document. Dispatch is not
// here: the IR builder selects the `failure` implementation of the subject's
// type and attaches its answer to the assertion (ir.Assert.WithAnswer), so
// there is no parameter here that would pretend otherwise.
//
// What is here is everything after the answer arrives: which reason a failure
// carries, whether `actual` is present, whether the answered rows are read at
// all, and which of `assert`/`refute` is satisfied by which answer.

// AssertableDetails is the `AssertionDetails` an `Assertable` subject answered,
// in the report's shapes rather than Nomi's.
//
// Two shapes exist for one type on purpose and nomiassertion.go's header says
// why: `NomiAssertionDetails` is what a Nomi program holds (a `Maybe[string]`
// and a `*List`), and a report wants a `string` and a slice. This is the report
// side, so the conversion happens once, at the boundary, rather than inside the
// judgement.
//
// A nil *AssertableDetails is `None` — the subject reported no failure. That is
// the JudgeBool convention (nil means nothing to report) rather than a second
// one.
type AssertableDetails struct {
	// Reason is the text the Assertable authored. Empty means it declined to
	// author one, which is a different thing from std's declared default: the
	// default is a non-empty string written at a construction site that
	// omits the field, so "" reaches here only
	// when a program wrote `reason: ""` on purpose.
	Reason string
	// Actual is the Assertable's own string for the subject, empty when absent.
	// Not a rendering of the subject: a shape subject's `actual:` row is
	// the value's Debug text, and this one is whatever the `failure`
	// implementation put in the field.
	Actual  string
	Details []AssertionDetailContext
}

// AssertableDetailsOf reads the `Maybe<AssertionDetails>` an `Assertable`
// answered into the report-shaped ingredients the judgement takes.
//
// `TagSome` rather than a comparison against 0: a zero `Maybe` is neither Some
// nor None, so a tag test written the other way round would treat an
// uninitialised value as a failure report.
func AssertableDetailsOf(answer Maybe[NomiAssertionDetails]) *AssertableDetails {
	if answer.Tag != TagSome {
		return nil
	}
	return &AssertableDetails{
		Reason:  answer.Some.Reason,
		Actual:  reportMaybeString(answer.Some.Actual),
		Details: reportDetails(answer.Some.Details),
	}
}

// JudgeAssertable is the rule for an Assertable subject, and the counterpart of
// JudgeBool: nil means the assertion held.
//
// `assert` holds when the subject answered nothing and fails on the details it
// answered; `refute` is the mirror. A failing `refute` therefore carries no
// details and cannot — the answer that made it fail was `None`, so no details
// exist — which is why it says so in its own words instead.
//
// The authored reason wins over the site's, including for a `check`: a `reason`
// of "assertion failed" reported through `testing.check` is std's declared field
// default arriving intact and not the site's `check failed`. Only an explicitly
// empty reason falls through to FailedReason, and that is the one case where the
// keyword decides.
func (s AssertionSite) JudgeAssertable(
	answer *AssertableDetails,
	binding *AssertionBindingContext,
	values []AssertionValueContext,
) *AssertionFailure {
	if s.Holds(answer == nil) {
		return nil
	}
	if s.Refute {
		return s.Failure("refute failed", "", binding, values)
	}
	reason := answer.Reason
	if reason == "" {
		reason = s.FailedReason()
	}
	failure := s.Failure(reason, answer.Actual, binding, values)
	failure.Details = answer.Details
	return failure
}
