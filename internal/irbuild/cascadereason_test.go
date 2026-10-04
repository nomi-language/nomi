package irbuild

// A type refused at its declaration must report that reason everywhere it is
// used, never a second key naming the position the use appeared in.
//
// `unloweredReason` and `rejectUnlowered` read a declaration's refusal list
// rather than synthesizing a placeholder from the type's name. So
// `struct Config { context: Context, … }` reports
// `non-scalar field type | Config.context: Context` at its declaration and at
// every use site, instead of `unlowered type | Config` at each use. The
// blocker tally then names the root cause (a generic type, a stdlib value
// type, a payload shape) rather than the position a use of it appeared in.
//
// registerImpl follows the same rule for an unlowerable interface
// (`d.iface.why`), and sigreason.go's rejectTypeAnnotation is the shared
// helper each arm calls.
