package irbuild

// stdInstanceModule is the stdlib module the instance-driver tests read.
//
// std/json holds generic `impl FromJson` blocks for three containers: one whose
// body lowers at a concrete instantiation on its own (`Maybe`), one that needs a
// generic sibling instantiated too (`List` via `decode_list`), and `Map`.
const stdInstanceModule = "json"
