package rt

// `std/tasks.Outcome<T>` and `std/tasks.Failure`, as Go types.
//
// # Why they are here
//
// prelude.go's identity argument, unchanged: a named Nomi type's identity in
// internal/irbuild is the `*typeDef` its declaration produced, and a std type
// needs one Go type that every module's code can name. rt is that one place.
//
// fragment.go's caveat applies too and for the same reason: "prelude" is
// internal/irbuild's table name, not a claim about scope. `Maybe` and `Result` are in
// every file's scope whether imported or not; `Outcome` is an ordinary
// `pub enum` in std/tasks that a file has to import, exactly as `Fragment` is.
//
// # Why the corpus needs them
//
// MEASURED at 18acb83d over the 43 refused corpus files:
// `14-modules-and-packaging/dotted_type_names/dotted_type_names_test.nomi` is
// the ONE file whose entire blocker set lies inside `qualified call` /
// `type-qualified member` / `type-qualified reference`, and all five of its
// residual sites are this type — `Outcome.Cancelled` under
// `type-qualified reference` and `Outcome.Completed` under `qualified call`,
// one absent representation split across two key names by nothing but whether
// the variant carries data.
//
// # WHAT IS DELIBERATELY NOT HERE: Task, and therefore Task.outcome
//
// `Task<T>` is a `pub host type` with no rt representation, so `Task.outcome`
// — the only std function that PRODUCES an Outcome — stays refused. Giving
// `Outcome<T>` a representation does not bring `Task` with it, and pretending
// otherwise would be a Go function whose answer nothing produced. What this
// buys is that the TYPE is nameable and its variants are constructible and
// matchable, which is the whole of what the corpus file writes.
//
// # A golden file cannot see a bug in here
//
// A wrong tag that is consistently wrong prints the same text, so the tags
// below are cross-checked against std/tasks.nomi's declaration order by
// internal/irbuild's TestOutcomeShapeMatchesStdSource, and against these structs
// by TestOutcomeLayoutMatchesRT.

// Tag values, written out for the reason prelude.go's and fragment.go's are:
// they are what a reader has to be able to check against std/tasks.nomi by eye.
//
// TagInvalid (prelude.go) is 0 here too: one reserved-invalid value for every
// tagged struct rt declares, not one per type.
const (
	// Failure's variants, in std/tasks.nomi declaration order.
	TagPanicked uint8 = 1
	TagErrored  uint8 = 2

	// Outcome's variants, in std/tasks.nomi declaration order.
	TagCompleted uint8 = 1
	TagCancelled uint8 = 2
	TagFailed    uint8 = 3
)

// Failure is Nomi's `std/tasks.Failure`: why a task broke.
//
//	pub enum Failure {
//	  Panicked String
//	  Errored String
//	}
//
// MONOMORPHIC, so the two String payloads share ONE field — the same slot
// dedup rt.CalendarError's five String variants use, and internal/irbuild's
// stdEnumDefs honours it from the spec's own `field` name. A generic struct
// could not do this (see Fragment), but this one is not generic.
//
// Cancellation is deliberately absent from this enum, and that is std's design
// rather than an omission here: being cancelled is not a failure, so it is
// Outcome's own third variant instead.
type Failure struct {
	// Tag is 1 for Panicked and 2 for Errored. 0 means never constructed.
	Tag uint8
	// Msg is the message either variant carries. Reading it without checking
	// Tag is the caller's bug; `case` lowering never does.
	Msg string
}

// Outcome is Nomi's `std/tasks.Outcome<T>`: how a task ended.
//
//	pub enum Outcome<T> {
//	  Completed T
//	  Cancelled
//	  Failed Failure
//	}
//
// Three variants, and it is the first type in rt to MIX a parametric payload
// with a payload that is another NOMINAL rt type. `Fragment` mixes parametric
// with a SCALAR (`string`), which internal/irbuild's `fixed` field already
// described; `Failed Failure` is the shape that needed a new one, because
// `Failure`'s kind is a named `*typeDef` rather than a scalar tag.
//
// `Cancelled` stores nothing, which is why it needs internal/irbuild's deferred
// bare-variant sentinel: `Outcome.Cancelled` names no `T` and the analyzer
// records `Outcome<T>` unsolved at that reference (measured), so the type
// argument arrives from the coercion target and not from the mention.
type Outcome[T any] struct {
	// Tag is 1 for Completed, 2 for Cancelled, 3 for Failed. 0 means never
	// constructed.
	Tag uint8
	// Completed is the value a task that ran to completion produced.
	//
	// Note what this does NOT mean: an `Err` a body returned deliberately is a
	// Completed task carrying an Err value, not a Failed one. std/tasks.nomi
	// says so in as many words, and the distinction is the point of the enum.
	Completed T
	// Failed is why an unplanned termination happened. Always present, like
	// every payload field of a tagged struct; only meaningful at Tag 3.
	Failed Failure
}

// Completed, Cancelled and Failed build the three variants.
//
// These exist for rt's OWN use, the way prelude.go's `Some`/`None`/`Ok`/`Err`
// and fragment.go's `Static`/`Dynamic` do: a hand-written host function that builds an Outcome
// must not spell the tag itself.
func Completed[T any](v T) Outcome[T] { return Outcome[T]{Tag: TagCompleted, Completed: v} }

// Cancelled builds the payload-less variant. The type argument is explicit at
// the call — `rt.Cancelled[int64]()` — because there is no value to infer it
// from, which is `rt.None`'s situation exactly.
func Cancelled[T any]() Outcome[T] { return Outcome[T]{Tag: TagCancelled} }

// Failed builds the failure variant.
func Failed[T any](f Failure) Outcome[T] { return Outcome[T]{Tag: TagFailed, Failed: f} }

// Panicked and Errored build Failure's two variants.
func Panicked(msg string) Failure { return Failure{Tag: TagPanicked, Msg: msg} }

// Errored builds the ordinary-error failure.
func Errored(msg string) Failure { return Failure{Tag: TagErrored, Msg: msg} }
