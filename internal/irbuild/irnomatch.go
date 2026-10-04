package irbuild

// The no-match trap, `ir.NoMatch`. `internal/ir/nomatch.go` carries the
// argument for the node.
//
// The line the trap prints is the node's own position, the `case`'s and not
// the last arm's (see `ir.NewNoMatch`). A consumer reading a retained graph
// has no AST node, so the instruction is the one place the line comes from.
//
// The message is not built here. Its text lives in rt (rt.NoCaseMatch), so
// the builder names the position and formats nothing.
