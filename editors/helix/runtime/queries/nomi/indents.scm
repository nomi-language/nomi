[
  (anon_struct)
  (anon_struct_type)
  (body)
  (case_expression)
  (concurrent_block)
  (else_arms)
  (enum_body)
  (enum_variant)
  (function_type)
  (if_expression)
  (impl_block)
  (import_block)
  (interface_definition)
  (lambda)
  (list)
  (list_pattern)
  (map_construction)
  (map_literal)
  (parameters)
  (paren_expression)
  (set)
  (struct_body)
  (struct_construction)
  (struct_pattern)
  (struct_update)
  (tests_body)
  (tuple_expression)
  (tuple_pattern)
  (tuple_type)
  (type_body)
  (type_parameters)
  (typed_list)
  (vector)
  (where_clause)
] @indent

[
  "}"
  "]"
  ")"
] @outdent

; Keep a continued case arm indented until the next arm starts.
(case_branch) @extend
(else_arm) @extend
