(((function_definition
  name: (_) @run)
  (#eq? @run "main"))
  (#set! tag nomi-run))

((test_declaration
  name: (string) @run)
  (#set! tag nomi-test))

((tests_declaration
  name: (string) @run)
  (#set! tag nomi-test))

((attached_test_prompt
  (attached_comment_test_prompt_marker) @name) @run
  (#set! tag nomi-test))
