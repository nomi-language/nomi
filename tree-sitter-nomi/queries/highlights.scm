; === Specific patterns (highest priority — listed first for tree-sitter) ===

[
  (attached_comment_test_prompt_marker)
  (attached_comment_test_line_prompt)
] @comment.documentation

; The `#!` interpreter line of an executable script.
(shebang) @comment

; Keyword declarations: `message cart_items(count: Int) `...``, whose
; keyword the grammar does not know and whose name is a function's. A
; macro's keyword is marked with `!` (`sqlkit.query! orders`) and
; reads as a macro, `!` included; its qualifier is the file it comes from,
; a module. Nomi's own `message` and `noun` take no `!` and are keywords.
(keyword_declaration
  module: (identifier) @module)
(keyword_declaration
  keyword: (identifier) @function.macro
  mark: (macro_mark) @function.macro)
(keyword_declaration
  keyword: (identifier) @keyword)
(keyword_declaration
  name: (identifier) @function.definition)
(keyword_declaration
  name: (type_identifier) @type.definition)

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

; Discard names (`_` and `_name`) bind nothing a program can read, so a name
; a pattern binds that way reads as a comment. The
; anonymous `_` token appears only where a pattern binds (and as a blank Go
; package name); a `_` read as a value (an error: a capture's parameter is `&1`)
; lexes as an identifier and keeps its variable colour, and so does a read of
; a top-level `fn _helper` or `once _cache`. The regex allows leading
; whitespace because some identifiers after a field name or `as` carry it.
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
; A destructure's nested pattern (`#(#(_a, b), c) = pair`,
; `#{x: Some(_n), y} = q else { ... }`), spelled as an expression.
(destructuring_binding
  pattern: [
    (call_expression (identifier) @comment)
    (tuple_expression (identifier) @comment)
    (list (identifier) @comment)
    (vector (identifier) @comment)
    (struct_construction ":" . (identifier) @comment)
    (struct_construction field_name: (identifier) @comment . ["," "}"])
    (anon_struct (anon_struct_field ":" . (identifier) @comment))
    (map_construction (map_entry (_) . (identifier) @comment))
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
; patterns at any depth (`Some(Ok(#(_a, b)))`, `[_x, .._rest]`) matches one.
(tuple_pattern
  (identifier) @comment
  (#match? @comment "^\\s*_($|[^_])"))
(list_pattern
  (identifier) @comment
  (#match? @comment "^\\s*_($|[^_])"))
(vector_pattern
  (identifier) @comment
  (#match? @comment "^\\s*_($|[^_])"))
; The rest of a vector or set pattern (`#[x, .._rest]`, `%["a", .._rest]`).
(pattern_spread
  (identifier) @comment
  (#match? @comment "^\\s*_($|[^_])"))
(set_pattern
  ".." . (identifier) @comment
  (#match? @comment "^\\s*_($|[^_])"))
(enum_pattern
  (identifier) @comment
  (#match? @comment "^\\s*_($|[^_])"))
(or_pattern
  (identifier) @comment
  (#match? @comment "^\\s*_($|[^_])"))
; A map pattern's value; its key (`key => v`) is an expression and is read.
(map_pattern
  "=>" . (identifier) @comment
  (#match? @comment "^\\s*_($|[^_])"))
; A punned field binds its name (`#{_w}`); a labeled one binds its pattern.
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
; binding with `else` or a pattern assertion (`Some(#(_n, o)) = opt`,
; `assert Err(_) = r`), the pattern before `as`, and a case, guard or else
; arm that took the expression reading (`Some(Ok((_g, n))) when n > 0 ->`).
; Those expressions also occur as values, where an identifier is read, so
; the match starts at the pattern itself: the first named child of any node,
; directly followed by `=`, `as`, `when` or `->`. The outer call must have a
; constructor head, since a guard arm may start with an ordinary call
; (`ready(_x) ->`); inside a pattern every call is a variant. A query has no
; descendant operator, so the alternation spells out each depth: a discard
; directly in a call, tuple, list, vector, struct construction, anonymous
; struct or map value, under at most two calls, tuples or lists
; (`Ok(Some(#(_a, b)))`).
; A deeper one keeps its colour. Each depth costs query compile time (the
; tour compiles this file once per page), which is what stops it at three.
((_ .
  [
    (call_expression function: [(type_identifier) (type_field_access) (dot_variant)] (identifier) @comment)
    (tuple_expression (identifier) @comment)
    (list (identifier) @comment)
    (vector (identifier) @comment)
    (struct_construction ":" . (identifier) @comment)
    (struct_construction field_name: (identifier) @comment . ["," "}"])
    (anon_struct (anon_struct_field ":" . (identifier) @comment))
    (map_construction (map_entry (_) . (identifier) @comment))
    (call_expression function: [(type_identifier) (type_field_access) (dot_variant)]
      [
        (call_expression (identifier) @comment)
        (tuple_expression (identifier) @comment)
        (list (identifier) @comment)
        (struct_construction ":" . (identifier) @comment)
        (struct_construction field_name: (identifier) @comment . ["," "}"])
        (anon_struct (anon_struct_field ":" . (identifier) @comment))
        (map_construction (map_entry (_) . (identifier) @comment))
        (call_expression
          [
            (call_expression (identifier) @comment)
            (tuple_expression (identifier) @comment)
            (list (identifier) @comment)
            (struct_construction ":" . (identifier) @comment)
            (struct_construction field_name: (identifier) @comment . ["," "}"])
            (anon_struct (anon_struct_field ":" . (identifier) @comment))
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
        (map_construction (map_entry (_) . (identifier) @comment))
        (call_expression
          [
            (call_expression (identifier) @comment)
            (tuple_expression (identifier) @comment)
            (list (identifier) @comment)
            (struct_construction ":" . (identifier) @comment)
            (struct_construction field_name: (identifier) @comment . ["," "}"])
            (anon_struct (anon_struct_field ":" . (identifier) @comment))
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
        (map_construction (map_entry (_) . (identifier) @comment))
        (call_expression
          [
            (call_expression (identifier) @comment)
            (tuple_expression (identifier) @comment)
            (list (identifier) @comment)
            (struct_construction ":" . (identifier) @comment)
            (struct_construction field_name: (identifier) @comment . ["," "}"])
            (anon_struct (anon_struct_field ":" . (identifier) @comment))
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
            (map_construction (map_entry (_) . (identifier) @comment))
          ])
      ])
  ] . ["=" "as" "when" "->"])
  (#match? @comment "^\\s*_($|[^_])"))

; Test declarations and assertions.
(test_declaration
  "test" @keyword)
(group_declaration
  "group" @keyword)
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

; Parameters
(parameter
  name: (identifier) @variable.parameter)

; A capture's parameter, `&1`, and the capture's `&`, which makes a
; function of the body it is written against, as `fn` does.
(placeholder) @variable.parameter
(capture "&" @keyword)

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

; `P as name`: the name binds the whole value P matched.
(as_pattern
  name: (identifier) @variable.parameter)

; String prefix patterns: `"prefix" + rest`
(enum_pattern
  (string)
  "+"
  (identifier) @comment
  (#match? @comment "^\\s*_($|[^_])"))
(enum_pattern
  (string)
  "+"
  (identifier) @variable.parameter)
(case_branch
  (string)
  "+"
  (identifier) @comment
  (#match? @comment "^\\s*_($|[^_])"))
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

; A bare enum variant as an `or` alternative (`None or Some(0)`)
(or_pattern
  (type_identifier) @constructor)

; Enum variant constructor in enum_pattern (e.g., Wrapped(v) in case branches)
(enum_pattern
  (type_identifier) @constructor)

; Dot-leading variant shorthand (`.Red`, `.Circle(1.0)`, `%.Obj{"k" => v}`)
; — variant identifier is highlighted as a constructor; the leading dot
; is a punctuation token (no separate tag needed).
(dot_variant
  (type_identifier) @constructor)

; An attached list's head (`Ids[1]`, `JV.Arr[x]`)
(typed_list
  (type_identifier) @type)

; The type name a literal or pattern writes after its shape's symbol
; (`#Point{..}`, `#geo.Point{..}`, `#Shape.Circle{..}`, `%Kvs{..}`,
; `#Path[..]`, `#Pair(..)`): a module name, then types. A `.Circle` head is
; a dot_variant, a constructor.
(_
  type: (identifier) @module)
(_
  type: (type_identifier) @type)

; A variant a pattern names through its file (`sql.Error.Closed`,
; `json.Json.Case.Snake`): the file is a module, the last name the variant,
; and the names between them its type.
(qualified_variant
  module: (identifier) @module)
(qualified_variant
  (type_identifier) @constructor .)
(qualified_variant
  (type_identifier) @type)

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
; `#{..base, x: 9}`, where `x` is a field name and nothing else. A punned
; `#{..base, x}` is deliberately left to the `(identifier) @variable` catch-all,
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
(export) @keyword
"opaque" @keyword
"once" @keyword
"fn" @keyword
"type" @keyword
"struct" @keyword
"enum" @keyword
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
"=>" @operator
".." @operator
"..=" @operator
"+" @operator
"-" @operator
"*" @operator
"/" @operator
; `%` is the remainder operator only between operands; a map or set
; literal starts with it too.
(binary_expression "%" @operator)
(lambda_body_binary_expression "%" @operator)
"==" @operator
"!=" @operator
"<" @operator
">" @operator
"<=" @operator
">=" @operator
"!" @operator
"=" @operator
"->" @operator

; Delimiters. The delimiters of data shape are @type, the colour of a struct
; name: a literal that builds a value (a struct's, map's or struct update's
; braces, a list's, vector's or set's brackets, a tuple's parentheses, each
; with the `#` or `%` that starts it: `#{`, `#Point{`, `%{`, `%Kvs{`, `#[`,
; `%[`, `#(`), a constructor call's parentheses, and every pattern's
; delimiters (tuple, list, vector, set, struct, map and variant-payload
; patterns, wherever they appear). Every other delimiter is program structure and is
; @punctuation.bracket: blocks, declaration bodies, function calls, grouping,
; parameter lists and types. A pattern spelled as the expression it looks like
; (`Some(#(n, [a])) = found else {...}`, `assert Err(_) = r`) parses as the
; literal and call nodes below, so it takes @type with no rule of its own.
(anon_struct ["#{" "}"] @type)
(struct_update ["#{" "}"] @type)
(struct_construction ["#" "{" "}"] @type)
(map_construction ["%{" "%" "{" "}"] @type)
(list ["[" "]"] @type)
(vector ["#[" "#" "[" "]"] @type)
(set ["%[" "%" "[" "]"] @type)
(tuple_expression ["#(" "#" "(" ")"] @type)
(struct_pattern ["#{" "#" "{" "}"] @type)
(map_pattern ["%{" "%" "{" "}"] @type)
(destructuring_binding ["#(" "#{" "%{" "#" "%" "(" ")" "{" "}"] @type)
(tuple_pattern ["#(" "#" "(" ")"] @type)
(list_pattern ["[" "]"] @type)
(vector_pattern ["#[" "#" "[" "]"] @type)
(set_pattern ["%[" "%" "[" "]"] @type)
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

"#{" @punctuation.bracket
"#(" @punctuation.bracket
"#[" @punctuation.bracket
"%{" @punctuation.bracket
"%[" @punctuation.bracket
"#" @punctuation.bracket
":" @punctuation.delimiter
"," @punctuation.delimiter
";" @punctuation.delimiter
"." @punctuation.delimiter
"(" @punctuation.bracket
")" @punctuation.bracket
"[" @punctuation.bracket
"]" @punctuation.bracket
"{" @punctuation.bracket
"}" @punctuation.bracket
