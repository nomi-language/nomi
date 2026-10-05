; === Specific patterns (highest priority — listed first for tree-sitter) ===

[
  (attached_comment_test_prompt_marker)
  (attached_comment_test_line_prompt)
] @comment.documentation

; The `#!` interpreter line of an executable script.
(shebang) @comment

; Function definitions
(function_definition
  impl_interface: (type_identifier) @type)
(function_definition
  name: (identifier) @function.definition)
(function_definition
  name: (type_identifier) @function.definition)
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
(go_block
  "go" @keyword)
(go_inline_body
  "go" @keyword)

; Host function definitions
(extern_func_definition
  (host_keyword) @keyword)
(extern_func_definition
  impl_interface: (type_identifier) @type)
(extern_func_definition
  name: (identifier) @function.definition)
(extern_func_definition
  name: (type_identifier) @function.definition)

; Host type definitions
(extern_type_definition
  (host_keyword) @keyword)
(extern_type_definition
  name: (qualified_type_identifier) @type.definition)

; Interface methods — `open` modifier and the `fn` keyword.
; The literal `open` only appears inside `interface_method` (contextual
; keyword), so capturing it via the parent rule keeps highlighting from
; firing on user identifiers named `open` outside interface bodies.
(interface_method
  name: (identifier) @function.definition)
(interface_method
  name: (type_identifier) @function.definition)
(interface_method
  "open" @keyword)

; Interface field requirements — `field name: T` inside interface bodies.
; Same contextual rule as `open` above: matching the literal via the parent
; rule scopes the keyword highlight to interface bodies only.
(interface_field
  "field" @keyword)
(interface_field
  name: (identifier) @variable.other.member)

; Top-level derive declarations (`derive Iface for Type`) and implementation
; blocks (`impl Iface for Type { ... }`). The bare `"impl" @keyword` and
; `"for" @keyword` captures below match impl block literals, and the interface
; / implementing types fall through to `(type_identifier) @type`. `derive` is
; contextual.
(derive_conformance
  "derive" @keyword)

; Function calls
(call_expression
  function: (identifier) @function.call)

(call_expression
  function: (type_identifier) @constructor)

(call_expression
  function: (field_access
    (identifier) @function.call .))

; Once bindings (the only "set once, never changes" declaration)
(once_binding
  name: (identifier) @constant)
(once_binding
  name: (type_identifier) @constant)

; Discard bindings/patterns (`_` / `_name`) are intentionally not readable.
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

; String prefix patterns: `"prefix" + rest`
(enum_pattern
  (string)
  "+"
  (identifier) @comment
  (#match? @comment "^[[:space:]]*_($|[^_])"))
(enum_pattern
  (string)
  "+"
  (identifier) @variable.parameter)
(case_branch
  (string)
  "+"
  (identifier) @comment
  (#match? @comment "^[[:space:]]*_($|[^_])"))
(case_branch
  (string)
  "+"
  (identifier) @variable.parameter)

; Type definitions
(type_definition
  name: (qualified_type_identifier) @type.definition)

(interface_definition
  name: (type_identifier) @type.definition)

(type_alias
  name: (qualified_type_identifier) @type.definition)

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

; Enum variant in case patterns
(case_branch
  . (type_identifier) @constructor)

; Enum variant in a binding's else arms
(else_arm
  . (type_identifier) @constructor)

; Enum variant constructor in enum_pattern (e.g., Wrapped(v) in case branches)
(enum_pattern
  (type_identifier) @constructor)

; Dot-leading variant shorthand (`.Red`, `.Circle(1.0)`, `.Obj{"k" => v}`)
; — variant identifier is highlighted as a constructor; the leading dot
; is a punctuation token (no separate tag needed).
(dot_variant
  (type_identifier) @constructor)

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

; `internal` path segment in imports — the packaging convention
; for sub-tree access barriers. Using @attribute for visual distinctness;
; semantically honest as access-scope metadata. Listed BEFORE the generic
; @module rules below per tree-sitter's first-match-wins (specific FIRST).
(module_path
  (identifier) @attribute
  (#eq? @attribute "internal"))

; Module path in imports
(module_path
  "/" @module.path_separator)
(module_path
  (relative_import_parent) @module)
(module_path
  (identifier) @module)
(module_path
  (type_identifier) @module)

; Imported names in selective imports: import models.{User, Point}
(import_selector_path
  (type_identifier) @type)
(import_selector_path
  (identifier) @function)
(import_value_selector
  (identifier) @function)
(import_entry
  (type_identifier) @type)
(import_entry
  (identifier) @function)

; Struct construction field names
(struct_construction
  field_name: (identifier) @variable.other.member)

; Struct pattern field names
(pattern_field
  name: (identifier) @variable.other.member)

; Struct/enum field definitions
(field_definition
  (identifier) @variable.other.member)

; Anonymous struct fields
(anon_struct_field
  (identifier) @variable.other.member)

; Struct update fields. The `:` in the pattern restricts this to the labeled
; `{..base, x: 9}`, where `x` is a field name and nothing else. A punned
; `{..base, x}` is deliberately left to the `(identifier) @variable` catch-all,
; because that `x` is a variable read: it must be in scope or the program does
; not compile, and that is the part a reader can get wrong. It also keeps the
; captures agreeing with the analyzer's semantic tokens, which classify the
; punned identifier as a variable — otherwise the same field reads as a member
; wherever analysis has not run and as a variable wherever it has. The spread's
; own `..` needs nothing here: the global `".." @operator` below already covers
; it.
(struct_update_field
  field_name: (identifier) @variable.other.member
  ":")

; Field access — member is the last named child.
(field_access
  (identifier) @variable.other.member .)

; Field accessor shorthand (`.name`, `.address.city`) — every segment is a
; field name.
(field_accessor
  (identifier) @variable.other.member)

; === Keywords ===

(pub) @keyword
"opaque" @keyword
"export" @keyword
"once" @keyword
"fn" @keyword
"type" @keyword
"struct" @keyword
"enum" @keyword
"field" @keyword
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
"typealias" @keyword
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
(string) @string
(triple_string) @string
(raw_string) @string
(tagged_type_string) @string
(tagged_type_triple_string) @string
(raw_tagged_string) @string
(codepoint) @character
(string_content) @string
(triple_string_content) @string
(raw_string_content) @string
(escape_sequence) @string.escape
(string_interpolation
  "${" @punctuation.special
  "}" @punctuation.special)

; Tag identifiers on tagged literals are PascalCase type names
; (`Date"..."`, `Sql"..."`), coloured by the general type_identifier
; rule — the tag resolves to the type's own symbol.

(integer) @number
(float) @number
(decimal) @number

; === Catch-all patterns (lowest priority — listed last for tree-sitter) ===

(type_identifier) @type
(identifier) @variable

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
