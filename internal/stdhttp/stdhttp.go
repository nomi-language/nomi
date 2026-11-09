package stdhttp

// LandedPhase is the last phase of std/http's order of work, as
// inventory.txt's rows number the phases, that std holds.
// TestGoInventoryIsComplete requires every covered row of a phase up to it
// to name a declaration std has.
const LandedPhase = 6
