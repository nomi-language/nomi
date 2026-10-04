package irbuild

// The interface-dispatch turbofish, from both sides: the shape it claims and the
// shape it must keep refusing.
//
// A UNIT test over the refusal set rather than a differential fixture, and that
// is deliberate rather than cheaper. The corpus already runs the behaviour end
// to end — `18-ffi-and-dynamic/json_derive_test.nomi` lowers, runs and matches
// its golden record. What no differential fixture can assert is the
// BOUNDARY: that the generic target is still refused, and refused under the key
// that names it. A fixture can only say a program works; only the refusal set
// can say a program is declined for the right reason.
