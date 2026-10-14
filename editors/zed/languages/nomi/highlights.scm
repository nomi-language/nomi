; === Catch-all patterns (lowest priority — put first) ===

; Identifiers (default)
(identifier) @variable
(type_identifier) @type

; === Operators and punctuation ===

"|>" @operator
"|" @operator
"=>" @operator
".." @operator
"..=" @operator
"+" @operator
"-" @operator
"*" @operator
"/" @operator
"%" @operator
"==" @operator
"!=" @operator
"<" @operator
">" @operator
"<=" @operator
">=" @operator
"!" @operator
"=" @operator
"->" @operator
"#[" @punctuation.bracket
"#{" @punctuation.bracket
":" @punctuation.delimiter
"," @punctuation.delimiter
"." @punctuation.delimiter
"(" @punctuation.bracket
")" @punctuation.bracket
"[" @punctuation.bracket
"]" @punctuation.bracket
"{" @punctuation.bracket
"}" @punctuation.bracket

; === Keywords ===

"fn" @keyword
"type" @keyword
"struct" @keyword
"enum" @keyword
"typealias" @keyword
"interface" @keyword
"import" @keyword
"as" @keyword
"if" @keyword
"else" @keyword
"case" @keyword
"when" @keyword
"break" @keyword
(continue_statement) @keyword
"return" @keyword
"and" @keyword
"or" @keyword
(pub) @keyword
(export) @keyword
"opaque" @keyword
"once" @keyword
"extern" @keyword
"embeds" @keyword
"extends" @keyword
"where" @keyword
"with" @keyword
"defer" @keyword
"test" @keyword
"assert" @keyword
"refute" @keyword
"subtests" @keyword
"dbg" @keyword
"todo" @keyword
"concurrent" @keyword
"try" @keyword
"then" @keyword
"tap" @keyword
"impl" @keyword
"for" @keyword
(self_type) @keyword
(self_expression) @keyword
(import_self) @keyword

; Built-in constants
"true" @constant.builtin
"false" @constant.builtin

; === Literals ===

(doc_comment) @comment.documentation
(line_comment) @comment
; The `#!` interpreter line of an executable script.
(shebang) @comment
(string) @string
(triple_string) @string
(raw_string) @string
(tagged_type_string) @string
(tagged_type_triple_string) @string
(raw_tagged_string) @string
; A codepoint literal, `'a'`. Before escape_sequence, so the `\n` in `'\n'`
; keeps the escape colour under Zed's last-wins priority.
(codepoint) @string.special
(string_content) @string
(triple_string_content) @string
(raw_string_content) @string
(escape_sequence) @string.escape
(string_interpolation
  "${" @punctuation.special
  "}" @punctuation.special)

(integer) @number
(float) @number
(decimal) @number

; === Specific patterns (highest priority — put last) ===

; Once bindings (the only "set once, never changes" declaration)
(once_binding
  name: (identifier) @constant)
(once_binding
  name: (type_identifier) @constant)

; Parameters
(parameter
  name: (identifier) @variable.parameter)

; Lambda parameters
(lambda_parameter
  name: (identifier) @variable.parameter)
(lambda_parameter
  (identifier) @variable.parameter)

; Destructuring bindings
(destructuring_binding
  binding: (identifier) @variable.parameter)
(destructuring_binding
  field: (identifier) @property)

; `P as name`: the name binds the whole value P matched.
(as_pattern
  name: (identifier) @variable.parameter)

; Module path in imports
(module_path
  "/" @module.path_separator)
(module_path
  (relative_import_parent) @module)
(module_path
  (identifier) @module)
(module_path
  (type_identifier) @module)

; `internal` path segment in imports — the packaging convention
; for sub-tree access barriers. Using @attribute for visual distinctness;
; semantically honest as access-scope metadata. Listed AFTER the generic
; @module rule per the file's last-wins convention.
;
; Companion required: lsp/semantic_tokens.go must skip
; emitting a module token for `internal` in ImportStmt context.
; Without that skip the LSP overlay overrides this tree-sitter
; capture and `internal` renders as module inline — though the
; hover pipeline (which doesn't apply LSP tokens) shows the
; @attribute color correctly. The skip mirrors the existing one for
; `self` in the same file.
(module_path
  (identifier) @attribute
  (#eq? @attribute "internal"))

; Imported names in selective imports: import models.{User, Point}
(import_entry
  (type_identifier) @type)
(import_entry
  (identifier) @function)
(import_selector_path
  (type_identifier) @type)
(import_selector_path
  (identifier) @function)
(import_value_selector
  (identifier) @function)

; Type-parameter binders and where-clause variables.
(type_parameter
  name: (type_parameter_identifier) @variable)
(type_parameter_identifier) @variable

; Generic types
(generic_type
  (type_identifier) @type)

; Enum variants — only the name, not type args
(enum_variant
  name: (type_identifier) @constructor)

; Enum variant in case patterns (first child = variant name)
(case_branch
  . (type_identifier) @constructor)

; Enum variant in a binding's else arms
(else_arm
  . (type_identifier) @constructor)

; Enum variant constructor in enum_pattern (e.g., Wrapped(v) in case branches)
(enum_pattern
  (type_identifier) @constructor)

; Type definitions
(type_definition
  name: (qualified_type_identifier) @type)

(type_alias
  name: (qualified_type_identifier) @type)

(interface_definition
  name: (type_identifier) @type)

(gopkg_declaration
  "gopkg" @keyword)
(gopkg_declaration
  alias: (identifier) @module)
(go_selector_binding
  "go" @keyword)
(go_selector_binding
  package: (identifier) @module)
(go_selector_binding
  symbol: (identifier) @function)
(go_selector_binding
  symbol: (type_identifier) @function)

(extern_func_definition
  (host_keyword) @keyword)
(extern_type_definition
  (host_keyword) @keyword)

; Test declarations and assertions.
(test_declaration
  "test" @keyword)
(tests_declaration
  "tests" @keyword)
(clock_block
  "clock" @keyword)
(boot_block
  "boot" @keyword)
(setup_block
  "setup" @keyword)
(assertion
  "assert" @keyword)
(assertion
  "refute" @keyword)
(bare_assertion
  "assert" @keyword)
(bare_assertion
  "refute" @keyword)
(subtests_statement
  "subtests" @keyword)
; Recovery fallback for list-led assertions that can be confused with
; `assert <list-pattern> = ...` while editing/highlighting.
(assertion
  value: (identifier) @keyword
  (#match? @keyword "^(assert|refute)$"))
(ERROR
  "assert" @keyword)
(ERROR
  "refute" @keyword)
(dbg_expression
  "dbg" @keyword)
(todo_expression
  "todo" @keyword)

; Struct construction
(struct_construction
  (type_identifier) @type)

(struct_construction
  (type_field_access
    (type_identifier) @type))

; Map construction (TypeName{key => value, ...})
(map_construction
  (type_identifier) @type)

(map_construction
  (type_field_access
    (type_identifier) @type))

; Type field access
; Module name (lowercase first position)
(type_field_access
  (identifier) @module)
; Enum name (uppercase first position)
(type_field_access
  . (type_identifier) @type)
; Variant name (last position) is a constructor
(type_field_access
  (type_identifier) @constructor .)

; Struct construction field names
(struct_construction
  field_name: (identifier) @property)

; Struct pattern field names
(pattern_field
  name: (identifier) @property)

; String prefix patterns: `"prefix" + rest`
(enum_pattern
  (string)
  "+"
  (identifier) @variable.parameter)
(enum_pattern
  (string)
  "+"
  (identifier) @comment
  (#match? @comment "^\\s*_($|[^_])"))
(case_branch
  (string)
  "+"
  (identifier) @variable.parameter)
(case_branch
  (string)
  "+"
  (identifier) @comment
  (#match? @comment "^\\s*_($|[^_])"))

; Struct/enum field definitions
(field_definition
  (identifier) @property)

; Anonymous struct fields
(anon_struct_field
  (identifier) @property)

; Struct update fields. The `:` in the pattern restricts this to the labeled
; `{..base, x: 9}`, where `x` is a field name and nothing else. A punned
; `{..base, x}` is deliberately left to the `(identifier) @variable` catch-all
; at the top of this file, because that `x` is a variable read: it must be in
; scope or the program does not compile, and that is the part a reader can get
; wrong. It also keeps the captures agreeing with the semantic tokens editors
; layer on top, which classify the punned identifier as a variable — otherwise
; the same field reads as a property before the LSP answers and as a variable
; after. It sits after the catch-all because Zed's query priority is last-wins.
; The spread's `..` needs no rule: `".." @operator` above already covers it.
(struct_update_field
  field_name: (identifier) @property
  ":")

; Field access — property (anchor: last named child only)
(field_access
  (identifier) @property .)

; Field accessor shorthand (`.name`, `.address.city`) — every segment is a
; property.
(field_accessor
  (identifier) @property)

; Function calls — anchor: first named child is the function
(call_expression
  . (identifier) @function)

(call_expression
  . (type_identifier) @constructor)

(call_expression
  . (field_access
    (identifier) @function .))

; Function definitions
(function_definition
  impl_interface: (type_identifier) @type)
(function_definition
  name: (identifier) @function)
(function_definition
  name: (type_identifier) @function)

; Host function definitions
(extern_func_definition
  impl_interface: (type_identifier) @type)
(extern_func_definition
  name: (identifier) @function)
(extern_func_definition
  name: (type_identifier) @function)

; Host type definitions
(extern_type_definition
  name: (qualified_type_identifier) @type)

; Interface methods — name is a function definition. The optional `open`
; modifier (Nomi marker for "default is overridable") is CONTEXTUAL: it
; only carries meaning inside an interface body. By capturing the literal
; via the parent rule we scope the keyword highlight to interface bodies
; only — outside, a binding like `open = 42` keeps its plain-identifier
; colouring.
(interface_method
  name: (identifier) @function)
(interface_method
  name: (type_identifier) @function)
(interface_method
  "open" @keyword)

; Top-level derive declarations. The bare `"impl" @keyword` and `"for" @keyword`
; captures in the keywords section match `impl Iface for Type` blocks; the
; interface / receiver types keep the catch-all `(type_identifier) @type`.
; `derive` is contextual.
(derive_conformance
  "derive" @keyword)

; Tag identifiers on tagged literals are PascalCase type names
; (`Date"..."`, `Sql"..."`), coloured by the general type_identifier
; rule — the tag resolves to the type's own symbol.

; Dot-leading variant shorthand (`.Red`, `.Circle(1.0)`, `.Obj{"k" => v}`).
; Listed last so this specific match overrides the catch-all
; `(type_identifier) @type` at the top — the inner type_identifier reads
; as a constructor, not a type name. Mirrors the tree-sitter-nomi
; highlight query, reversed for Zed's last-wins priority.
(dot_variant
  (type_identifier) @constructor)

; Discard names (`_` and `_name`) bind nothing a program can read, so a name
; a pattern binds that way reads as a comment. The
; anonymous `_` token appears only where a pattern binds (and as a blank Go
; package name); a `_` that stands for a missing call argument (`add(1, _)`)
; lexes as an identifier and keeps its variable colour, and so does a read of
; a top-level `fn _helper` or `once _cache`. The regex allows leading
; whitespace because some identifiers after a field name or `as` carry it.
; Zed's query priority is last-wins, so this block comes after every
; ordinary binding, parameter, property and constructor capture, and before
; the attached-test dim and the `//!` marker rules, which must win over it.
"_" @comment
(binding
  . (identifier) @comment
  (#match? @comment "^\\s*_($|[^_])"))
(parameter
  name: (identifier) @comment
  (#match? @comment "^\\s*_($|[^_])"))
(lambda_parameter
  name: (identifier) @comment
  (#match? @comment "^\\s*_($|[^_])"))
(destructuring_binding
  binding: (identifier) @comment
  (#match? @comment "^\\s*_($|[^_])"))
; A destructure's nested pattern (`((_a, b), c) = pair`,
; `{x: Some(_n), y} = q else { ... }`), spelled as an expression.
(destructuring_binding
  pattern: [
    (call_expression (identifier) @comment)
    (tuple_expression (identifier) @comment)
    (list (identifier) @comment)
    (struct_construction ":" . (identifier) @comment)
    (struct_construction field_name: (identifier) @comment . ["," "}"])
    (anon_struct (anon_struct_field ":" . (identifier) @comment))
    (map_literal (map_entry (_) . (identifier) @comment))
  ]
  (#match? @comment "^\\s*_($|[^_])"))
; A test's setup binding, an `if` pattern condition, `x as y`.
(_
  pattern: (identifier) @comment
  (#match? @comment "^\\s*_($|[^_])"))
(case_branch
  . (identifier) @comment
  (#match? @comment "^\\s*_($|[^_])"))
(else_arm
  . (identifier) @comment
  (#match? @comment "^\\s*_($|[^_])"))
; A subject-less `case`'s catch-all. Any other leading name there is a guard.
(guard_branch
  . (identifier) @comment
  (#eq? @comment "_"))
; Pattern nodes. Each rule names a direct parent, so a discard nested in
; patterns at any depth (`Some(Ok((_a, b)))`, `[_x, .._rest]`) matches one.
(tuple_pattern
  (identifier) @comment
  (#match? @comment "^\\s*_($|[^_])"))
(list_pattern
  (identifier) @comment
  (#match? @comment "^\\s*_($|[^_])"))
(enum_pattern
  (identifier) @comment
  (#match? @comment "^\\s*_($|[^_])"))
; A map pattern's value; its key (`key => v`) is an expression and is read.
(struct_pattern
  "=>" . (identifier) @comment
  (#match? @comment "^\\s*_($|[^_])"))
; A punned field binds its name (`{_w}`); a labeled one binds its pattern.
(pattern_field
  . name: (identifier) @comment .
  (#match? @comment "^\\s*_($|[^_])"))
(pattern_field
  ":" . (identifier) @comment
  (#match? @comment "^\\s*_($|[^_])"))
(as_pattern
  name: (identifier) @comment
  (#match? @comment "^\\s*_($|[^_])"))
(as_pattern
  pattern: (body
    (identifier) @comment)
  (#match? @comment "^\\s*_($|[^_])"))
; A pattern spelled as the expression it looks like: the left side of a
; binding with `else` or a pattern assertion (`Some((_n, o)) = opt`,
; `assert Err(_) = r`), the pattern before `as`, and a case, guard or else
; arm that took the expression reading (`Some(Ok((_g, n))) when n > 0 ->`).
; Those expressions also occur as values, where an identifier is read, so
; the match starts at the pattern itself: the first named child of any node,
; directly followed by `=`, `as`, `when` or `->`. The outer call must have a
; constructor head, since a guard arm may start with an ordinary call
; (`ready(_x) ->`); inside a pattern every call is a variant. A query has no
; descendant operator, so the alternation spells out each depth: a discard
; directly in a call, tuple, list, struct construction, anonymous struct or
; map value, under at most two calls, tuples or lists (`Ok(Some((_a, b)))`).
; A deeper one keeps its colour. Each depth costs query compile time (the
; tour compiles this file once per page), which is what stops it at three.
((_ .
  [
    (call_expression function: [(type_identifier) (type_field_access) (dot_variant)] (identifier) @comment)
    (tuple_expression (identifier) @comment)
    (list (identifier) @comment)
    (struct_construction ":" . (identifier) @comment)
    (struct_construction field_name: (identifier) @comment . ["," "}"])
    (anon_struct (anon_struct_field ":" . (identifier) @comment))
    (map_literal (map_entry (_) . (identifier) @comment))
    (map_construction (map_entry (_) . (identifier) @comment))
    (call_expression function: [(type_identifier) (type_field_access) (dot_variant)]
      [
        (call_expression (identifier) @comment)
        (tuple_expression (identifier) @comment)
        (list (identifier) @comment)
        (struct_construction ":" . (identifier) @comment)
        (struct_construction field_name: (identifier) @comment . ["," "}"])
        (anon_struct (anon_struct_field ":" . (identifier) @comment))
        (map_literal (map_entry (_) . (identifier) @comment))
        (map_construction (map_entry (_) . (identifier) @comment))
        (call_expression
          [
            (call_expression (identifier) @comment)
            (tuple_expression (identifier) @comment)
            (list (identifier) @comment)
            (struct_construction ":" . (identifier) @comment)
            (struct_construction field_name: (identifier) @comment . ["," "}"])
            (anon_struct (anon_struct_field ":" . (identifier) @comment))
            (map_literal (map_entry (_) . (identifier) @comment))
            (map_construction (map_entry (_) . (identifier) @comment))
          ])
        (tuple_expression
          [
            (call_expression (identifier) @comment)
            (tuple_expression (identifier) @comment)
            (list (identifier) @comment)
            (struct_construction ":" . (identifier) @comment)
            (struct_construction field_name: (identifier) @comment . ["," "}"])
            (anon_struct (anon_struct_field ":" . (identifier) @comment))
            (map_literal (map_entry (_) . (identifier) @comment))
            (map_construction (map_entry (_) . (identifier) @comment))
          ])
        (list
          [
            (call_expression (identifier) @comment)
            (tuple_expression (identifier) @comment)
            (list (identifier) @comment)
            (struct_construction ":" . (identifier) @comment)
            (struct_construction field_name: (identifier) @comment . ["," "}"])
            (anon_struct (anon_struct_field ":" . (identifier) @comment))
            (map_literal (map_entry (_) . (identifier) @comment))
            (map_construction (map_entry (_) . (identifier) @comment))
          ])
      ])
    (tuple_expression
      [
        (call_expression (identifier) @comment)
        (tuple_expression (identifier) @comment)
        (list (identifier) @comment)
        (struct_construction ":" . (identifier) @comment)
        (struct_construction field_name: (identifier) @comment . ["," "}"])
        (anon_struct (anon_struct_field ":" . (identifier) @comment))
        (map_literal (map_entry (_) . (identifier) @comment))
        (map_construction (map_entry (_) . (identifier) @comment))
        (call_expression
          [
            (call_expression (identifier) @comment)
            (tuple_expression (identifier) @comment)
            (list (identifier) @comment)
            (struct_construction ":" . (identifier) @comment)
            (struct_construction field_name: (identifier) @comment . ["," "}"])
            (anon_struct (anon_struct_field ":" . (identifier) @comment))
            (map_literal (map_entry (_) . (identifier) @comment))
            (map_construction (map_entry (_) . (identifier) @comment))
          ])
        (tuple_expression
          [
            (call_expression (identifier) @comment)
            (tuple_expression (identifier) @comment)
            (list (identifier) @comment)
            (struct_construction ":" . (identifier) @comment)
            (struct_construction field_name: (identifier) @comment . ["," "}"])
            (anon_struct (anon_struct_field ":" . (identifier) @comment))
            (map_literal (map_entry (_) . (identifier) @comment))
            (map_construction (map_entry (_) . (identifier) @comment))
          ])
        (list
          [
            (call_expression (identifier) @comment)
            (tuple_expression (identifier) @comment)
            (list (identifier) @comment)
            (struct_construction ":" . (identifier) @comment)
            (struct_construction field_name: (identifier) @comment . ["," "}"])
            (anon_struct (anon_struct_field ":" . (identifier) @comment))
            (map_literal (map_entry (_) . (identifier) @comment))
            (map_construction (map_entry (_) . (identifier) @comment))
          ])
      ])
    (list
      [
        (call_expression (identifier) @comment)
        (tuple_expression (identifier) @comment)
        (list (identifier) @comment)
        (struct_construction ":" . (identifier) @comment)
        (struct_construction field_name: (identifier) @comment . ["," "}"])
        (anon_struct (anon_struct_field ":" . (identifier) @comment))
        (map_literal (map_entry (_) . (identifier) @comment))
        (map_construction (map_entry (_) . (identifier) @comment))
        (call_expression
          [
            (call_expression (identifier) @comment)
            (tuple_expression (identifier) @comment)
            (list (identifier) @comment)
            (struct_construction ":" . (identifier) @comment)
            (struct_construction field_name: (identifier) @comment . ["," "}"])
            (anon_struct (anon_struct_field ":" . (identifier) @comment))
            (map_literal (map_entry (_) . (identifier) @comment))
            (map_construction (map_entry (_) . (identifier) @comment))
          ])
        (tuple_expression
          [
            (call_expression (identifier) @comment)
            (tuple_expression (identifier) @comment)
            (list (identifier) @comment)
            (struct_construction ":" . (identifier) @comment)
            (struct_construction field_name: (identifier) @comment . ["," "}"])
            (anon_struct (anon_struct_field ":" . (identifier) @comment))
            (map_literal (map_entry (_) . (identifier) @comment))
            (map_construction (map_entry (_) . (identifier) @comment))
          ])
        (list
          [
            (call_expression (identifier) @comment)
            (tuple_expression (identifier) @comment)
            (list (identifier) @comment)
            (struct_construction ":" . (identifier) @comment)
            (struct_construction field_name: (identifier) @comment . ["," "}"])
            (anon_struct (anon_struct_field ":" . (identifier) @comment))
            (map_literal (map_entry (_) . (identifier) @comment))
            (map_construction (map_entry (_) . (identifier) @comment))
          ])
      ])
  ] . ["=" "as" "when" "->"])
  (#match? @comment "^\\s*_($|[^_])"))

; Delimiters. The delimiters of data shape are @type, the colour of a struct
; name: a literal that builds a value (the braces of a struct, anonymous
; struct, map, set, struct update or map update, the brackets of a list or
; vector, a tuple's parentheses), a constructor call's parentheses, and every
; pattern's delimiters (tuple, list, struct, map and variant-payload patterns,
; wherever they appear). Every other delimiter is program structure and keeps
; the @punctuation.bracket catch-all at the top: blocks, declaration bodies,
; function calls, grouping, parameter lists and types. A pattern spelled as
; the expression it looks like (`Some((n, [a])) = found else {...}`,
; `assert Err(_) = r`) parses as the literal and call nodes below, so it
; takes @type with no rule of its own.
;
; An anonymous struct or map literal, a struct or map pattern and a destructure
; are captured whole, because the scanner may lex the opening brace as a hidden
; token that no query can name; every other token inside has a capture of its
; own, and Zed draws the innermost capture, so each visible delimiter is also
; captured directly.
[
  (anon_struct)
  (map_literal)
  (struct_update)
  (map_update)
  (struct_pattern)
  (destructuring_binding)
] @type
(anon_struct ["{" "}"] @type)
(map_literal ["{" "}"] @type)
(struct_update ["{" "}"] @type)
(map_update ["{" "}"] @type)
(struct_construction ["{" "}"] @type)
(map_construction ["{" "}"] @type)
(set ["#{" "}"] @type)
(list ["[" "]"] @type)
(vector ["#[" "]"] @type)
(tuple_expression ["(" ")"] @type)
(struct_pattern ["{" "}"] @type)
(destructuring_binding ["(" ")" "{" "}"] @type)
(tuple_pattern ["(" ")"] @type)
(list_pattern ["[" "]"] @type)
(enum_pattern ["(" ")"] @type)

; A constructor call: a call whose head is a PascalCase name, a variant
; (`Some(1)`, `Shape.Circle(r)`, `shapes.Shape.Circle(r)`, `.Circle(r)`) or a
; distinct type (`Balance(5)`). A call whose head ends in a lowercase name
; (`Int.to_string(n)`, `io.print(x)`) is a function call and keeps
; @punctuation.bracket. A partial application of a constructor
; (`Two.Both(_, "c")`) is @type as well: it parses exactly as the pattern
; `Two.Both(_, "c")` does, and it names the data shape it will build.
(call_expression
  function: [(type_identifier) (type_field_access) (dot_variant)]
  ["(" ")"] @type)

; BEGIN attached-test dim (Zed and Helix only; scripts/sync-nvim-runtime.sh
; drops this block, since the Neovim plugin dims attached tests from Lua.)
;
; `//!` attached tests read as comments: everything inside an
; attached_test_prompt is captured @comment. A capture on an outer node
; cannot do it, because Zed and Helix draw the innermost capture over an
; outer one, and neither supports an ancestor predicate (Zed implements only
; has-parent?/not-has-parent?; Helix rejects unknown predicates). Query
; syntax has no descendant operator either, so one pattern per depth
; captures every node, named or anonymous, at that depth below the prompt.
; Each pattern comes after every other capture, so on any node it captures
; it is the last capture and wins. Depth 16 is the bound: the deepest node in
; any attached test in std, examples and tests is at depth 9. A node deeper
; than 16 keeps its ordinary highlight.
(attached_test_prompt _ @comment)
(attached_test_prompt (_ _ @comment))
(attached_test_prompt (_ (_ _ @comment)))
(attached_test_prompt (_ (_ (_ _ @comment))))
(attached_test_prompt (_ (_ (_ (_ _ @comment)))))
(attached_test_prompt (_ (_ (_ (_ (_ _ @comment))))))
(attached_test_prompt (_ (_ (_ (_ (_ (_ _ @comment)))))))
(attached_test_prompt (_ (_ (_ (_ (_ (_ (_ _ @comment))))))))
(attached_test_prompt (_ (_ (_ (_ (_ (_ (_ (_ _ @comment)))))))))
(attached_test_prompt (_ (_ (_ (_ (_ (_ (_ (_ (_ _ @comment))))))))))
(attached_test_prompt (_ (_ (_ (_ (_ (_ (_ (_ (_ (_ _ @comment)))))))))))
(attached_test_prompt (_ (_ (_ (_ (_ (_ (_ (_ (_ (_ (_ _ @comment))))))))))))
(attached_test_prompt (_ (_ (_ (_ (_ (_ (_ (_ (_ (_ (_ (_ _ @comment)))))))))))))
(attached_test_prompt (_ (_ (_ (_ (_ (_ (_ (_ (_ (_ (_ (_ (_ _ @comment))))))))))))))
(attached_test_prompt (_ (_ (_ (_ (_ (_ (_ (_ (_ (_ (_ (_ (_ (_ _ @comment)))))))))))))))
(attached_test_prompt (_ (_ (_ (_ (_ (_ (_ (_ (_ (_ (_ (_ (_ (_ (_ _ @comment))))))))))))))))
; END attached-test dim

; The `//!` markers stay documentation comments. Last, so it wins over any
; capture above that reaches a marker (the attached-test dim, where present).
[
  (attached_comment_test_prompt_marker)
  (attached_comment_test_line_prompt)
] @comment.documentation
