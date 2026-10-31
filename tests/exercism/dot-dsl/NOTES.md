# DOT DSL

Subject: Parsing & Validation

Read a small part of Graphviz's DOT language into a graph of nodes, edges and attributes, refusing malformed graphs, edges and attributes.

Adapted from Exercism problem-specifications (MIT), exercises/dot-dsl.

## Choices

- Adapted: the description asks for an internal DSL, graph-building code written in the host language. The canonical data instead parses DOT text, and this port does that: `parse` reads the text into a `Graph` value. An internal DSL in Nomi would be plain constructor calls on immutable values, which leaves nothing for the canonical cases to check.
- A `Graph` is three maps: nodes by name, edges by their two node names in order (`#("a", "b")` for `b -- a`, so an edge given twice either way round is one edge), and the graph's own attributes. Attributes are a `Map<String, Value>`, written out: a `typealias` of a named type would only hide it.
- An attribute's `Value` is an enum of `Number`, `Flag` and `Text`, since the canonical attributes mix `1`, `true` and `"Testing Attrs"`. `value_of` tries them in order with one `case` over a tuple: quoted text, a whole number, `true` or `false`, then unquoted text (`color=green`).
- `parse` keeps the lines that are not empty or comments and takes them apart with `["graph {", ..body, "}"] = lines else { ... }`, so a text without both braces is `InvalidGraph`.
- Each line between the braces is one statement, read by `statement` into a private `Statement` enum: graph attributes, a node, or a chain of edges. The words of its subject decide which: none, one, or `a -- b -- ...`, checked by `edge_names` as names at even positions and `--` between them. `Result.collect` stops at the first bad line, and `add` folds the statements into the graph. Attributes given again for the same node, edge or graph are merged, the later value winning.
- The four canonical errors are an `Error` enum with an `impl Display` giving the canonical messages. The error cases assert the message, `"${error}"`, so the table reads as the canonical data does.
- The canonical inputs are lists of lines; each is a `"""` string here. "invalid edge type" ends its last line with a space, which would be invisible at the end of a `"""` line, so it is an ordinary string with `\n`.
- The cases are two `subtests` tables, the graphs and the errors, since they assert different things.
