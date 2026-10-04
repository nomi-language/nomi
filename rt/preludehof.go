package rt

// The higher-order prelude driver rt keeps: `Result.map_err`, which the VM
// calls with a callback over its own function values.
//
// # Why the callback takes a frame
//
// A Nomi callback over Go types is `func(fr *Frame, x T) U`, and a bare
// function reference has the same Go type. So the frame is part of the
// callback's type and not an extra this file invents. seq.go's SeqMap takes
// the identical shape and does not take a frame of its own because the
// consumer driving the Seq supplies one; this driver is EAGER, so it is passed
// the caller's frame directly.
//
// # The absent arm PRESERVES the tag rather than naming a variant
//
// It returns `{Tag: r.Tag}` on the non-transforming arm rather than
// `{Tag: TagOk}`. std's body names the variant because Nomi has no zero
// values, so `case` is exhaustive over the two variants. A Go `Result[T, E]`
// has a third state: `TagInvalid`, which prelude.go's header calls a
// deliberately-detectable never-constructed value. Writing `TagOk` on the
// fall-through would silently CONVERT that detectable invalid into a
// legitimate Ok. Copying the tag maps Ok->Ok and Invalid->Invalid, agrees with
// std on the reachable arm, and costs the same one field write.
//
// The payload copy alongside it is the same rule: an Ok's payload crosses
// unchanged, because `T` is not touched by the mapping.

// ResultMapErr is std/results' `map_err`. The callback transforms the ERROR
// and an Ok passes through with its payload intact.
func ResultMapErr[T, E, F any](fr *Frame, r Result[T, E], f func(fr *Frame, e E) F) Result[T, F] {
	if r.Tag == TagErr {
		return Result[T, F]{Tag: TagErr, Err: f(fr, r.Err)}
	}
	return Result[T, F]{Tag: r.Tag, Ok: r.Ok}
}
