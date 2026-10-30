# Forth

Subject: Parsing & Validation

Evaluate a small subset of Forth: integer arithmetic, `DUP`, `DROP`, `SWAP` and `OVER`, and words the program defines with `: name ... ;`.

Adapted from Exercism problem-specifications (MIT), exercises/forth.

## Choices

- The program is one list of lower-cased tokens, every line's words in order, so a definition is read the same wherever it falls and case never matters again.
- A word means a list of `Op` steps (`Push(n)`, `Add`, `Dup`, ...), and the dictionary is a `Map<String, List<Op>>` that starts as `builtins`, each built-in word one step. A definition resolves its body against the dictionary as it stands and stores the steps, so a later redefinition of a word it uses changes nothing, and `: foo foo 1 + ;` reads the old `foo`. Built-in words are entries like any other, so overriding `swap` or `+` needs no special case.
- `run` walks the tokens with list patterns, `[":", name, ..rest]` for a definition and `[word, ..rest]` for anything else, and threads the stack and the dictionary through its own recursion. Each step can fail, so `execute` is a recursive function with `?` rather than an `Iter.reduce`, whose state would have to be a `Result`.
- The stack is a `List<Int>` with its top first, so every built-in is one list pattern. `apply` matches the step and the stack as a tuple, `#(op, stack)`: an empty stack and a stack of one are an arm each before the arms that need two values, and the compiler checks the `case` covers every step and stack.
- Errors are an `Error` enum, one variant per canonical message.
- A number cannot be redefined: a definition whose name `String.to_int` reads is `IllegalOperation`. `String.to_int` reads `-1` too, so negative numbers push themselves, as the canonical data asks, though its prose says a number is digits only.
- Each canonical group is a test named by the group's description, holding one `subtests` table; several groups share row descriptions such as "errors if there is nothing on the stack". "only defines locally" runs two programs, so it is a test of its own.

## Friction

None.
