//go:build !js

package stdwasm

// refusedGrowthTrap is the trap of a call that asked memory to grow past
// the limit and then trapped: wazero's allocator sees the refusal.
var refusedGrowthTrap = []string{"memory_limit"}
