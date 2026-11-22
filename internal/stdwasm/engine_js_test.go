//go:build js

package stdwasm

// refusedGrowthTrap is the trap of a call that asked memory to grow past
// the limit and then trapped: the host engine does not see the refusal, so
// the trap is the module's own `unreachable`. It is the one way the
// engines differ.
var refusedGrowthTrap = []string{"unreachable"}
