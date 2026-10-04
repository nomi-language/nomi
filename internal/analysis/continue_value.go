package analysis

// continueValueMessage rejects `continue <value>`. `continue` takes no value
// (spec, "`break` and `continue` in Callbacks"): it moves on with the state or
// accumulator unchanged. Inside `Iter.loop` a written value read as the next
// state, but it was ignored and the loop spun forever on the old one. The
// value belongs in the callback's result.
const continueValueMessage = "continue takes no value; it goes to the next iteration with the loop state or accumulator unchanged. " +
	"To carry a new value forward, make it the callback's result: write it as the last expression, without continue " +
	"(in Iter.loop that result is the next state)"
