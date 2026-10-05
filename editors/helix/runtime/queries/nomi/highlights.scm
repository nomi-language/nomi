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
"opaque" @keyword
"export" @keyword
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
"dbg" @keyword
"todo" @keyword
"concurrent" @keyword
"try" @keyword
"then" @keyword
"impl" @keyword
"for" @keyword
(self_type) @keyword
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
; keeps the escape colour under Helix's last-wins priority.
(codepoint) @constant.character
(string_content) @string
(triple_string_content) @string
(raw_string_content) @string
(escape_sequence) @string.escape
(string_interpolation
  "${" @punctuation.special
  "}" @punctuation.special)

(integer) @constant.numeric
(float) @constant.numeric
(decimal) @constant.numeric

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
  field: (identifier) @variable.other.member)

; Module path in imports
(module_path
  "/" @namespace.path_separator)
(module_path
  (relative_import_parent) @namespace)
(module_path
  (identifier) @namespace)
(module_path
  (type_identifier) @namespace)

; `internal` path segment in imports — the packaging convention
; for sub-tree access barriers. Using @attribute for visual distinctness;
; semantically honest as access-scope metadata. Listed AFTER the generic
; @namespace rule per the file's last-wins convention.
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
  alias: (identifier) @namespace)
(go_selector_binding
  "go" @keyword)
(go_selector_binding
  package: (identifier) @namespace)
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
  (identifier) @namespace)
; Enum name (uppercase first position)
(type_field_access
  . (type_identifier) @type)
; Variant name (last position) is a constructor
(type_field_access
  (type_identifier) @constructor .)

; Struct construction field names
(struct_construction
  field_name: (identifier) @variable.other.member)

; Struct pattern field names
(pattern_field
  name: (identifier) @variable.other.member)

; String prefix patterns: `"prefix" + rest`
(enum_pattern
  (string)
  "+"
  (identifier) @variable.parameter)
(enum_pattern
  (string)
  "+"
  (identifier) @comment
  (#match? @comment "^[[:space:]]*_($|[^_])"))
(case_branch
  (string)
  "+"
  (identifier) @variable.parameter)
(case_branch
  (string)
  "+"
  (identifier) @comment
  (#match? @comment "^[[:space:]]*_($|[^_])"))

; Struct/enum field definitions
(field_definition
  (identifier) @variable.other.member)

; Anonymous struct fields
(anon_struct_field
  (identifier) @variable.other.member)

; Struct update fields. The `:` in the pattern restricts this to the labeled
; `{..base, x: 9}`, where `x` is a field name and nothing else. A punned
; `{..base, x}` is deliberately left to the `(identifier) @variable` catch-all
; at the top of this file, because that `x` is a variable read: it must be in
; scope or the program does not compile, and that is the part a reader can get
; wrong. It also keeps the captures agreeing with the semantic tokens editors
; layer on top, which classify the punned identifier as a variable — otherwise
; the same field reads as a property before the LSP answers and as a variable
; after. It sits after the catch-all because Helix's query priority is last-wins.
; The spread's `..` needs no rule: `".." @operator` above already covers it.
(struct_update_field
  field_name: (identifier) @variable.other.member
  ":")

; Field access — property (anchor: last named child only)
(field_access
  (identifier) @variable.other.member .)

; Field accessor shorthand (`.name`, `.address.city`) — every segment is a
; property.
(field_accessor
  (identifier) @variable.other.member)

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
(go_block
  "go" @keyword)
(go_inline_body
  "go" @keyword)

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
; modifier (Nomi marker for "default is overridable") and the `field`
; keyword in `interface_field` are CONTEXTUAL: they only carry meaning
; inside an interface body. By capturing the literals via the parent
; rule we scope the keyword highlight to interface bodies only — outside,
; a binding like `open = 42` keeps its plain-identifier colouring.
(interface_method
  name: (identifier) @function)
(interface_method
  name: (type_identifier) @function)
(interface_method
  "open" @keyword)

; Interface field requirements — `field name: T` inside interface bodies.
(interface_field
  "field" @keyword)
(interface_field
  name: (identifier) @variable.other.member)

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
; highlight query, reversed for Helix's last-wins priority.
(dot_variant
  (type_identifier) @constructor)

; Discard bindings/patterns (`_` / `_name`) are intentionally not readable.
; Helix's query priority is last-wins, so this stays last to override ordinary
; binding, parameter, property, and constructor captures.
(binding
  . (identifier) @comment
  (#match? @comment "^_($|[^_])"))
(parameter
  name: (identifier) @comment
  (#match? @comment "^_($|[^_])"))
(lambda_parameter
  name: (identifier) @comment
  (#match? @comment "^_($|[^_])"))
(destructuring_binding
  binding: (identifier) @comment
  (#match? @comment "^_($|[^_])"))
(destructuring_binding
  binding: "_" @comment)
(case_branch
  . (identifier) @comment
  (#match? @comment "^_($|[^_])"))
(else_arm
  . (identifier) @comment
  (#match? @comment "^_($|[^_])"))
(pattern_field
  name: (identifier) @comment
  (#match? @comment "^_($|[^_])"))
(enum_pattern
  (identifier) @comment
  (#match? @comment "^_($|[^_])"))

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
