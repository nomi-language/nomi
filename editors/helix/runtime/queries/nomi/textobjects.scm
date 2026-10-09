(function_definition
  (body)? @function.inside) @function.around

(interface_method
  (body)? @function.inside) @function.around

(lambda
  body: (_) @function.inside) @function.around

(test_declaration
  (body)? @test.inside) @test.around

(tests_declaration
  (tests_body)? @test.inside) @test.around

(struct_definition
  (struct_body)? @class.inside) @class.around

(enum_definition
  (enum_body)? @class.inside) @class.around

(type_definition
  (type_body)? @class.inside) @class.around

(interface_definition
  (interface_method)* @class.inside) @class.around

(impl_block
  [
    (decorated_impl_item)
    (function_definition)
    (extern_func_definition)
    (once_binding)
  ]* @class.inside) @class.around

(parameters
  ((parameter) @parameter.inside . ","? @parameter.around) @parameter.around)

(type_parameters
  ((type_parameter) @parameter.inside . ","? @parameter.around) @parameter.around)

(call_expression
  ((_) @parameter.inside . ","? @parameter.around) @parameter.around)

(struct_construction
  ((_) @entry.inside . ","? @entry.around) @entry.around)

; A struct update's entries are its spread and its fields, so `]e` walks
; `{..base, x: 9}` the same way it walks `Point{x: 1, y: 2}` above.
(struct_update
  ((_) @entry.inside . ","? @entry.around) @entry.around)

(map_literal
  ((map_entry) @entry.inside . ","? @entry.around) @entry.around)

(list
  ((_) @entry.inside . ","? @entry.around) @entry.around)

(vector
  ((_) @entry.inside . ","? @entry.around) @entry.around)

(tuple_expression
  ((_) @entry.inside . ","? @entry.around) @entry.around)

(enum_variant) @entry.around

(field_definition
  (_) @entry.inside) @entry.around

[
  (line_comment)
  (doc_comment)
] @comment.inside

[
  (line_comment)
  (doc_comment)
]+ @comment.around
