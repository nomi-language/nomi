;; A body is written below its header line and closed by `end`, so the
;; node that owns the header indents the lines below it, and `end` and
;; `else` return to the header's level. A body node itself starts on its
;; first statement's line, so capturing it as well would indent twice.
[
  (anon_struct)
  (anon_struct_type)
  (begin_block)
  (case_branch)
  (case_expression)
  (concurrent_block)
  (else_arm)
  (else_binding)
  (enum_definition)
  (enum_variant)
  (function_definition)
  (function_type)
  (group_declaration)
  (group_subtests)
  (guard_branch)
  (if_expression)
  (impl_block)
  (import_block)
  (interface_definition)
  (interface_method)
  (lambda)
  (list)
  (list_pattern)
  (map_construction)
  (map_pattern)
  (parameters)
  (paren_expression)
  (pipe_if_expression)
  (struct_construction)
  (struct_definition)
  (struct_pattern)
  (struct_update)
  (subtests_statement)
  (test_declaration)
  (tuple_expression)
  (tuple_pattern)
  (tuple_type)
  (type_parameters)
  (typed_list)
  (where_clause)
] @indent

[
  "end"
  "else"
  "}"
  "]"
  ")"
] @outdent

; Keep a continued case arm indented until the next arm starts.
(case_branch) @extend
(else_arm) @extend
